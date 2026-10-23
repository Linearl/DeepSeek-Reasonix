package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func statusResp(status int, hdr map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("body")), Header: h}
}

func newDummyReq(ctx context.Context) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodPost, "http://x/y", nil)
}

func TestRetryableStatus(t *testing.T) {
	for _, s := range []int{408, 429, 500, 502, 503, 504, 529, 599} {
		if !RetryableStatus(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	for _, s := range []int{200, 400, 401, 402, 403, 404, 422} {
		if RetryableStatus(s) {
			t.Errorf("status %d should not be retryable", s)
		}
	}
}

func TestSendWithRetryCarriesDisplayIdentityAndSanitizedRequestPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, err := SendWithRetry(context.Background(), server.Client(), SendOptions{
		Provider: "deepseek-anthropic", ProviderDisplayName: "Deepseek2", Protocol: "openai",
	}, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/anthropic/v1/chat/completions?token=secret", nil)
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	if apiErr.Provider != "deepseek-anthropic" || apiErr.ProviderDisplayName != "Deepseek2" || apiErr.Protocol != "openai" || apiErr.RequestPath != "/anthropic/v1/chat/completions" {
		t.Fatalf("API error identity = %+v", apiErr)
	}
	if strings.Contains(apiErr.RequestPath, "secret") {
		t.Fatalf("query leaked into request path: %q", apiErr.RequestPath)
	}
}

func TestTransientErr(t *testing.T) {
	if transientErr(nil) {
		t.Error("nil should not be transient")
	}
	if transientErr(context.Canceled) || transientErr(context.DeadlineExceeded) {
		t.Error("ctx cancel/deadline should not be transient")
	}
	if !transientErr(errors.New("connection reset")) {
		t.Error("network-ish error should be transient")
	}
}

func TestIsConnReset(t *testing.T) {
	if IsConnReset(nil) {
		t.Error("nil is not a conn reset")
	}
	if IsConnReset(context.Canceled) || IsConnReset(context.DeadlineExceeded) {
		t.Error("ctx cancel/deadline must not look like a recoverable reset")
	}
	if IsConnReset(errors.New("decode stream: invalid character")) {
		t.Error("a plain protocol error must not be treated as a conn reset")
	}
	for _, err := range []error{
		io.ErrUnexpectedEOF,
		&net.OpError{Op: "read", Err: syscall.ECONNRESET},
		fmt.Errorf("read stream: %w", &net.OpError{Op: "read", Err: errors.New("wsarecv: forcibly closed")}),
	} {
		if !IsConnReset(err) {
			t.Errorf("want conn reset for %v", err)
		}
	}
}

// headerTimeoutErr is the production shape of a ResponseHeaderTimeout fire:
// a net.Error whose message carries the transport phrase (HTTP/1.1 and HTTP/2
// share it modulo prefix), reachable through RequestFailure → url.Error wraps
// exactly like SendWithRetry hands the error to the agent.
type headerTimeoutErr struct{ msg string }

func (e *headerTimeoutErr) Error() string   { return e.msg }
func (e *headerTimeoutErr) Timeout() bool   { return true }
func (e *headerTimeoutErr) Temporary() bool { return true }

func TestIsResponseHeaderTimeout(t *testing.T) {
	if IsResponseHeaderTimeout(nil) {
		t.Error("nil is not a header timeout")
	}
	if IsResponseHeaderTimeout(context.Canceled) {
		t.Error("caller cancel must not read as a gateway header timeout")
	}
	// A deadline the CALLER set carries DeadlineExceeded through the wrap; the
	// marker may even appear in an outer message — the ctx identity wins.
	ctxShaped := fmt.Errorf("post request: %w", context.DeadlineExceeded)
	if IsResponseHeaderTimeout(ctxShaped) {
		t.Error("caller-set deadline must be excluded")
	}
	if IsResponseHeaderTimeout(errors.New("dial tcp: i/o timeout")) {
		t.Error("a dial timeout is not a header wait")
	}
	if IsResponseHeaderTimeout(errors.New("net/http: timeout awaiting response headers")) {
		t.Error("plain text without a net.Error face must not match: transport guards surface as net.Error")
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"h2 direct", &headerTimeoutErr{msg: "http2: timeout awaiting response headers"}},
		{"h1 wrapped in url.Error", &url.Error{Op: "Post", URL: "https://gw/anthropic", Err: &headerTimeoutErr{msg: "net/http: timeout awaiting response headers"}}},
		{"RequestFailure chain", &RequestFailure{Identity: RequestIdentity{Provider: "mimo"}, Operation: "request failed", Err: &url.Error{Op: "Post", URL: "https://gw/anthropic", Err: &headerTimeoutErr{msg: "http2: timeout awaiting response headers"}}}},
	} {
		if !IsResponseHeaderTimeout(tc.err) {
			t.Errorf("%s: want response-header timeout match, got false (%v)", tc.name, tc.err)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	// 任务1056: the signature grew a (budget, lastErr) pair; the off-state
	// (zero budget) must produce the exact pre-1056 curve, byte-for-byte.
	if d := backoffDelay(1, 0, RetryBudget{}, nil); d < 500*time.Millisecond || d >= 750*time.Millisecond {
		t.Errorf("attempt 1 base delay = %v, want [500ms,750ms)", d)
	}
	if d := backoffDelay(20, 0, RetryBudget{}, nil); d > maxBackoff+250*time.Millisecond {
		t.Errorf("delay %v exceeds cap+jitter", d)
	}
	if d := backoffDelay(5, 3*time.Second, RetryBudget{}, nil); d != 3*time.Second {
		t.Errorf("Retry-After should win: %v", d)
	}
	if d := backoffDelay(1, 45*time.Second, RetryBudget{}, nil); d != 45*time.Second {
		t.Errorf("Retry-After beyond the backoff cap should still be honored: %v", d)
	}
	if d := backoffDelay(1, time.Hour, RetryBudget{}, nil); d != maxRetryAfter {
		t.Errorf("Retry-After should be capped to %v, got %v", maxRetryAfter, d)
	}
	// Fast-backoff only engages on a dial-timeout error — a generic transient
	// error (or a non-dial-timeout error with the budget set) keeps the ramp.
	dialErr := &DialTimeoutError{Timeout: 30 * time.Second, Err: errors.New("dial tcp: i/o timeout")}
	if d := backoffDelay(5, 0, RetryBudget{FastBackoff: 500 * time.Millisecond}, dialErr); d > 500*time.Millisecond+250*time.Millisecond {
		t.Errorf("dial-timeout fast backoff exceeded its cap: %v", d)
	}
	if d := backoffDelay(5, 0, RetryBudget{FastBackoff: 500 * time.Millisecond}, errors.New("connection reset")); d < time.Second {
		t.Errorf("generic transient error must not fast-back-off (got %v)", d)
	}
}

// B2: a dial-timeout failure must burn its budget fast — with the budget's
// fast-backoff set, 3 dial timeouts + sleeps must complete in well under the
// ~7 minutes the incident log showed (30s × 11 + exponential ramp). This test
// is the executable form of "单次 dial 失败耗时 ≤10s" at the retry-loop level.
func TestSendWithRetryFastBackoffOnDialTimeout(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, &DialTimeoutError{Timeout: 8 * time.Second, Err: errors.New("dial tcp: i/o timeout")}
	})}
	ctx := WithRetryBudget(context.Background(), RetryBudget{
		MaxRetries:  3,
		DialTimeout: 8 * time.Second,
		FastBackoff: 50 * time.Millisecond,
	})
	start := time.Now()
	_, err := SendWithRetry(ctx, cl, SendOptions{Provider: "p"}, newDummyReq)
	elapsed := time.Since(start)
	if calls != 4 {
		t.Fatalf("calls = %d, want 4 (1 initial + 3 retries)", calls)
	}
	// Fast-backoff (not wrap-exhausted) must preserve the pre-1056 terminal
	// error shape — the RequestFailure carrying the dial timeout — while
	// burning its budget in well under the 8.5-minute incident total.
	var reqErr *RequestFailure
	if !errors.As(err, &reqErr) {
		t.Fatalf("want the pre-1056 terminal shape (*RequestFailure), got %T: %v", err, err)
	}
	if !IsDialTimeout(reqErr.Err) {
		t.Fatalf("terminal error must still carry the dial-timeout shape: %v", reqErr.Err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("fast-backoff retry loop took %v; budget promised a sub-10s burn", elapsed)
	}
}

// B3: the wall-clock cap must terminate a retry loop whose every attempt
// individually fails "in time" (transient, not a dial timeout) but which keeps
// failing — without it this loop would ride the exponential ramp to maxBackoff.
func TestSendWithRetryWallClockCap(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("dial tcp: i/o timeout")
	})}
	ctx := WithRetryBudget(context.Background(), RetryBudget{
		MaxRetries:    MaxRetries, // a high cap the wall-clock must cut short
		WallClock:      300 * time.Millisecond,
		WrapExhausted: true,
	})
	start := time.Now()
	_, err := SendWithRetry(ctx, cl, SendOptions{Provider: "p"}, newDummyReq)
	elapsed := time.Since(start)
	var exhaustedErr *RetryBudgetExhaustedError
	if !errors.As(err, &exhaustedErr) {
		t.Fatalf("want *RetryBudgetExhaustedError when the wall-clock cap fires, got %T: %v", err, err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("wall-clock cap let the loop run for %v (budget 300ms)", elapsed)
	}
	if calls >= MaxRetries {
		t.Fatalf("wall-clock cap should have cut the loop short, but it made %d calls", calls)
	}
}

// Off-state contract: no budget on the context must reproduce today's
// semantics exactly — MaxRetries=10 retries, the pre-1056 exponential ramp,
// and no RetryBudgetExhaustedError wrap on exhaustion.
func TestSendWithRetryDefaultBudgetKeepsBaselineSemantics(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return statusResp(http.StatusServiceUnavailable, nil), nil
	})}
	start := time.Now()
	_, err := SendWithRetry(context.Background(), cl, SendOptions{Provider: "p"}, newDummyReq)
	elapsed := time.Since(start)
	if calls != MaxRetries+1 {
		t.Fatalf("calls = %d, want %d (initial + MaxRetries)", calls, MaxRetries+1)
	}
	var exhaustedErr *RetryBudgetExhaustedError
	if errors.As(err, &exhaustedErr) {
		t.Fatalf("off-state must not wrap the terminal error in RetryBudgetExhaustedError: %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("want bare *APIError(503), got %T: %v", err, err)
	}
	// The pre-1056 ramp's last backoff is 15s+jitter; the loop must have
	// waited on that order, not the sub-second fast-backoff lane.
	if elapsed < 10*time.Second {
		t.Fatalf("off-state elapsed %v; expected the pre-1056 exponential ramp (~30s), not a fast lane", elapsed)
	}
}

// TransportTuning: off-state (nil Extra, or Extra without the key) must return
// the hard-coded 30s default and no DNS-refresh override.
func TestTransportTuningDefaults(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"nil":       nil,
		"empty":     {},
		"unrelated": {"proxy_spec": struct{}{}},
	} {
		dialTimeout, forceDNS := TransportTuning(extra)
		if dialTimeout != DefaultDialTimeout || forceDNS {
			t.Errorf("%s: got (%v, %v), want (%v, false)", name, dialTimeout, forceDNS, DefaultDialTimeout)
		}
	}
	// On-state: a positive budget shortens the dial deadline and forces the
	// DNS re-resolve.
	dialTimeout, forceDNS := TransportTuning(map[string]any{
		"retry_budget": TransportBudget{DialTimeout: 8 * time.Second, ForceDNSRefresh: true},
	})
	if dialTimeout != 8*time.Second || !forceDNS {
		t.Fatalf("on-state tuning = (%v, %v), want (8s, true)", dialTimeout, forceDNS)
	}
	// A zero-valued budget (switch configured but knob unset) collapses to
	// the default — the "0 keeps the hard-coded default" contract.
	dialTimeout, forceDNS = TransportTuning(map[string]any{
		"retry_budget": TransportBudget{},
	})
	if dialTimeout != DefaultDialTimeout || forceDNS {
		t.Fatalf("zero budget = (%v, %v), want (%v, false)", dialTimeout, forceDNS, DefaultDialTimeout)
	}
}

func TestParseRetryAfterAcceptsHTTPDate(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat))
	if d := parseRetryAfter(resp); d < 25*time.Second || d > 31*time.Second {
		t.Errorf("http-date Retry-After = %v, want ~30s", d)
	}

	resp.Header.Set("Retry-After", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat))
	if d := parseRetryAfter(resp); d != 0 {
		t.Errorf("elapsed http-date Retry-After = %v, want 0", d)
	}
}

func TestSendWithRetryFailsFastOnClientErrors(t *testing.T) {
	for _, status := range []int{400, 402, 422} {
		calls := 0
		cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return statusResp(status, nil), nil
		})}
		_, err := SendWithRetry(context.Background(), cl, SendOptions{Provider: "p", KeyEnv: "KEY"}, newDummyReq)
		if calls != 1 {
			t.Errorf("status %d retried (%d calls), should fail fast", status, calls)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != status {
			t.Errorf("status %d: want *APIError with Status=%d, got %v", status, status, err)
		}
	}
}

func TestSendWithRetryPreservesProviderTraceID(t *testing.T) {
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return statusResp(422, map[string]string{"trace_id": "minimax-trace-123"}), nil
	})}
	_, err := SendWithRetry(context.Background(), cl, SendOptions{Provider: "minimax-cn-api"}, newDummyReq)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.TraceID != "minimax-trace-123" {
		t.Fatalf("TraceID = %q, want minimax-trace-123", apiErr.TraceID)
	}
}

func TestSendWithRetryAuthError(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return statusResp(401, nil), nil
	})}
	_, err := SendWithRetry(context.Background(), cl, SendOptions{Provider: "deepseek", KeyEnv: "DEEPSEEK_API_KEY", KeyPresent: true}, newDummyReq)
	if calls != 1 {
		t.Errorf("401 retried (%d calls), should fail fast for a never-authed key", calls)
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.KeyEnv != "DEEPSEEK_API_KEY" {
		t.Errorf("want *AuthError naming the key env, got %v", err)
	}
	if authErr != nil && authErr.Body != "body" {
		t.Errorf("AuthError should carry the response body, got %q", authErr.Body)
	}
}

func TestSendWithRetryRetriesTransientAuthForKnownKey(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls <= 2 {
			return statusResp(401, nil), nil
		}
		return statusResp(200, nil), nil
	})}
	resp, err := SendWithRetry(context.Background(), cl,
		SendOptions{Provider: "mimo", KeyEnv: "MIMO_API_KEY", KeyPresent: true, RetryAuth: true}, newDummyReq)
	if err != nil {
		t.Fatalf("a previously-good key should recover from a transient 401: %v", err)
	}
	if resp.StatusCode != http.StatusOK || calls != 3 {
		t.Fatalf("status=%d calls=%d, want 200 after 3 calls", resp.StatusCode, calls)
	}
}

func TestSendWithRetryAuthGivesUpAfterMaxAuthRetries(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return statusResp(401, nil), nil
	})}
	_, err := SendWithRetry(context.Background(), cl,
		SendOptions{Provider: "mimo", KeyEnv: "MIMO_API_KEY", KeyPresent: true, RetryAuth: true}, newDummyReq)
	if calls != 1+maxAuthRetries {
		t.Errorf("persistent 401 made %d calls, want %d (initial + maxAuthRetries)", calls, 1+maxAuthRetries)
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || !authErr.HasKey {
		t.Fatalf("want *AuthError with HasKey=true, got %v", err)
	}
}

// stallingBody sends headers' worth of promise and then never delivers: Read
// blocks until Close, mimicking a half-open 502/524 gateway that stalls after
// the status line. Close is what the errorBodyReadTimeout timer fires.
type stallingBody struct {
	closeOnce sync.Once
	closed    chan struct{}
}

func newStallingBody() *stallingBody { return &stallingBody{closed: make(chan struct{})} }

func (b *stallingBody) Read(p []byte) (int, error) {
	<-b.closed
	return 0, errors.New("body closed")
}

func (b *stallingBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

// TestSendWithRetryUnblocksStalledErrorBody locks in the #6607 freeze fix: a
// retryable status whose body never arrives must not wedge the retry loop —
// the deadline closes the body, the attempt is retried, and the eventual OK
// response is returned. Without the timer in readErrorBody this test hangs on
// the first 502 body and fails via the watchdog below.
func TestSendWithRetryUnblocksStalledErrorBody(t *testing.T) {
	prev := errorBodyReadTimeout
	errorBodyReadTimeout = 50 * time.Millisecond
	defer func() { errorBodyReadTimeout = prev }()

	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: newStallingBody(), Header: http.Header{}}, nil
		}
		return statusResp(200, nil), nil
	})}

	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := SendWithRetry(context.Background(), cl, SendOptions{Provider: "p", KeyEnv: "KEY"}, newDummyReq)
		done <- result{resp, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("should recover after the stalled 502: %v", r.err)
		}
		if r.resp.StatusCode != http.StatusOK || calls != 2 {
			t.Fatalf("status=%d calls=%d, want 200 after 2 calls", r.resp.StatusCode, calls)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendWithRetry wedged on a stalled error body — read deadline did not fire")
	}
}

func TestSendWithRetryRecoversAndNotifies(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return statusResp(503, nil), nil
		}
		return statusResp(200, nil), nil
	})}
	var infos []RetryInfo
	ctx := WithRequestAttemptCounter(context.Background())
	ctx = WithRetryNotify(ctx, func(i RetryInfo) { infos = append(infos, i) })

	resp, err := SendWithRetry(ctx, cl, SendOptions{Provider: "p", KeyEnv: "KEY"}, newDummyReq)
	if err != nil {
		t.Fatalf("should recover after one retry: %v", err)
	}
	if resp.StatusCode != http.StatusOK || calls != 2 {
		t.Fatalf("status=%d calls=%d, want 200 after 2 calls", resp.StatusCode, calls)
	}
	if len(infos) != 1 || infos[0].Attempt != 1 || infos[0].Max != MaxRetries {
		t.Fatalf("retry notify = %#v, want one Attempt 1/%d", infos, MaxRetries)
	}
	if got := RequestAttemptCount(ctx); got != 2 {
		t.Fatalf("request attempt count = %d, want 2", got)
	}
}

func TestRequestAttemptCountSurvivesRetriesThenTerminalFailure(t *testing.T) {
	calls := 0
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls < 3 {
			return statusResp(http.StatusServiceUnavailable, nil), nil
		}
		return statusResp(http.StatusBadRequest, nil), nil
	})}
	ctx := WithRequestAttemptCounter(context.Background())
	providerCtx := WithRequestAttemptCounter(ctx)

	if _, err := SendWithRetry(providerCtx, cl, SendOptions{Provider: "p"}, newDummyReq); err == nil {
		t.Fatal("expected terminal provider error")
	}
	if got := RequestAttemptCount(ctx); got != 3 {
		t.Fatalf("request attempt count = %d, want 3", got)
	}
	usage := UsageWithRequestAttemptCount(ctx, nil)
	if usage == nil || usage.TotalTokens != 0 || usage.RequestCount != 3 {
		t.Fatalf("failed request usage = %+v, want tokens=0 requests=3", usage)
	}
}

func TestIndependentRequestAttemptCounter(t *testing.T) {
	parent := WithRequestAttemptCounter(context.Background())
	recordRequestAttempt(parent)
	child := WithIndependentRequestAttemptCounter(parent)
	recordRequestAttempt(child)
	recordRequestAttempt(child)
	if RequestAttemptCount(parent) != 1 || RequestAttemptCount(child) != 2 {
		t.Fatalf("auxiliary and main request counts leaked: parent=%d child=%d", RequestAttemptCount(parent), RequestAttemptCount(child))
	}
}
