package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// MaxRetries is the number of times SendWithRetry re-attempts the connection +
// header phase after the initial try (so up to MaxRetries+1 total attempts).
const MaxRetries = 10

// DefaultDialTimeout is the hard-coded dial timeout the three HTTP providers
// (anthropic/openai/responses) use when no experimental retry budget is in
// effect — task 1056 only changes this number behind its lab switch, so the
// off-state keeps today's exact 30s behaviour.
const DefaultDialTimeout = 30 * time.Second

const maxBackoff = 15 * time.Second

// maxRetryAfter bounds a server-supplied Retry-After. Rate-limit windows are
// routinely longer than our own backoff cap, and clamping to it just spends
// attempts re-hitting the same closed window; the sleep is cancellable, so a
// longer honest wait costs nothing the user can't interrupt.
const maxRetryAfter = 60 * time.Second

// errorBodyReadTimeout bounds how long draining a non-OK response body may
// block. Proxies and gateways under load (502/524 storms) can send headers and
// then stall the body on a half-open connection; http.Client has no Timeout
// and ResponseHeaderTimeout no longer applies once headers arrive, so without
// this deadline the retry loop blocks in io.ReadAll indefinitely with no
// user-visible progress — the turn looks frozen until the process is killed
// (#6607). A var, not a const, so tests can shrink it.
var errorBodyReadTimeout = 10 * time.Second

// maxAuthRetries bounds how many times a 401/403 is retried for a key that has
// authenticated before: a transient server-side rejection (quota/gateway/rate)
// usually clears in a couple of attempts, whereas a key that never worked is a
// real config error and fails fast.
const maxAuthRetries = 2

// SendOptions carries the per-request context SendWithRetry needs to label
// errors and decide whether a 401 is worth retrying.
type SendOptions struct {
	Provider            string // stable provider instance id
	ProviderDisplayName string // user-editable display label
	Protocol            string // configured wire adapter id
	KeyEnv              string // api_key_env the key is read from, when known
	KeySource           string // human-readable source of KeyEnv, when known
	KeyPresent          bool   // a non-empty key is being sent — separates "rejected" from "missing"
	RetryAuth           bool   // the key has authenticated before — retry transient 401s instead of failing fast
	// RefreshCredential re-reads the credential from its source and swaps the
	// request key when the source has changed, reporting whether it changed.
	// 任务1308: a long-lived runtime caches its key at build time while
	// token-plan credentials keep rotating behind it — the cached state goes
	// stale and every request 401s until a runtime rebuild re-reads the
	// credential. force=true is the 401 re-auth path (one forced re-read per
	// SendWithRetry); force=false is the throttled proactive check SendWithRetry
	// runs once per call before the first attempt. Nil = no live re-read wired
	// (custom assemblers) — behaviour stays exactly as before. Key material
	// never crosses this boundary: the callback owns the swap and only the
	// changed-bool comes back.
	RefreshCredential func(force bool) bool
}

// CredentialRefreshInterval is how long a client trusts its cached credential
// before SendWithRetry's proactive check re-reads the source (任务1308 ③). A
// var so tests can shrink it; the value only bounds staleness of the cached
// copy — a stale window left by a shorter upstream TTL is still caught by the
// 401 re-auth path.
var CredentialRefreshInterval = 10 * time.Minute

// RetryBudget (task 1056 方案 B) is the lab-switch-gated retry tuning layer.
// It rides a context value so SendWithRetry — the single choke point every HTTP
// provider shares — can read it without touching any provider signature or the
// wire bytes, exactly like WithManagedRecovery's ctx-value precedent.
//
// Off-state contract: a zero Budget (no WithRetryBudget on the context) keeps
// every field at its current hard-coded behaviour, byte-for-byte — this is
// pinned by TestSendWithRetryDefaultBudgetKeepsBaselineSemantics.
type RetryBudget struct {
	// MaxRetries replaces the package const MaxRetries as the header-phase
	// retry cap. 0 means "use the const" — there is no way to configure
	// "retry zero times" through the budget (ManagedRecovery already covers
	// the retry-ownership case), so an absent field never silently disables
	// retries.
	MaxRetries int
	// DialTimeout is the per-attempt dial deadline, advertised on
	// DialTimeoutError so the provider's transport options can pick it up —
	// SendWithRetry itself does not dial, it only classifies and enforces
	// the budget.
	DialTimeout time.Duration
	// FastBackoff caps the exponential backoff ramp for a DialTimeoutError,
	// so a flapping dial path (the 2026-10-21 incident: 8.5 minutes of 11 ×
	// 30s dials) burns through attempts in seconds instead of riding the
	// 0.5s→15s ramp. Unset (0) keeps today's curve.
	FastBackoff time.Duration
	// WallClock bounds the whole retry loop (all attempts + backoff sleeps,
	// from the first attempt to exhaustion) so a bad network fails the turn
	// with a clear error instead of hanging the session indefinitely.
	WallClock time.Duration
	// WrapExhausted, when true, wraps the terminal error of a budget-gated
	// retry loop in a RetryBudgetExhaustedError ("network retries exhausted
	// after N attempt(s) / duration (budget X)") instead of returning the bare
	// last error. Without it the wire bytes of a given provider request stay
	// unchanged even while the budget's timing behaviour is on — a
	// conservative default for a switch that ships off: timing and wrapping
	// are separately gated so a user can tighten their retry timing without
	// also opting into the error-shape change.
	WrapExhausted bool
}

type retryBudgetKey struct{}

// WithRetryBudget attaches a non-nil budget to ctx for SendWithRetry to read.
func WithRetryBudget(ctx context.Context, b RetryBudget) context.Context {
	return context.WithValue(ctx, retryBudgetKey{}, b)
}

// RetryBudgetFromContext returns the attached budget, or the zero Budget when
// none is attached (the off-state: every field falls back to its hard-coded
// default).
func RetryBudgetFromContext(ctx context.Context) RetryBudget {
	if ctx == nil {
		return RetryBudget{}
	}
	b, _ := ctx.Value(retryBudgetKey{}).(RetryBudget)
	return b
}

// DialTimeoutError is returned when a single attempt's dial (TCP handshake)
// exceeded RetryBudget.DialTimeout. It carries the configured dial deadline so
// an outer classification site can distinguish "the network path is dead right
// now" (fast-backoff candidate) from "the server refused us immediately"
// (keep the existing ramp).
type DialTimeoutError struct {
	Timeout time.Duration
	Err     error
}

func (e *DialTimeoutError) Error() string {
	return fmt.Sprintf("dial timeout after %s: %v", e.Timeout.Round(time.Millisecond), e.Err)
}

func (e *DialTimeoutError) Unwrap() error { return e.Err }

// RetryBudgetExhaustedError wraps the last error of a budget-gated retry loop
// once either the attempt cap or the wall-clock budget is spent. It is the
// terminal face of 方案 B's wall-clock cap: the turn fails here with a clear
// "network retries exhausted" instead of hanging.
type RetryBudgetExhaustedError struct {
	Attempts  int
	Elapsed   time.Duration
	WallClock time.Duration
	Cause     error
}

func (e *RetryBudgetExhaustedError) Error() string {
	return fmt.Sprintf("network retries exhausted after %d attempt(s) / %s (budget %s)", e.Attempts, e.Elapsed.Round(time.Second), e.WallClock.Round(time.Second))
}

func (e *RetryBudgetExhaustedError) Unwrap() error { return e.Cause }

// AsRetryBudgetExhausted unwraps err to the terminal RetryBudgetExhaustedError
// when present, or nil.
func AsRetryBudgetExhausted(err error) *RetryBudgetExhaustedError {
	var exhausted *RetryBudgetExhaustedError
	if errors.As(err, &exhausted) {
		return exhausted
	}
	return nil
}

// IsDialTimeout reports whether err (or anything it wraps) is the
// task-1056-B dial-timeout marker — the shape a fast-backoff lane keys on.
func IsDialTimeout(err error) bool {
	var dialErr *DialTimeoutError
	return errors.As(err, &dialErr)
}

// TransportTuning reads the task-1056-B "retry_budget" transport knob out of a
// provider.Config.Extra map: when the lab switch is on, boot stamps a
// provider.TransportBudget there (see boot/model_provider.go) so the concrete
// dialer can shorten DialTimeout and force a fresh DNS lookup per attempt.
// Off-state contract: no key, or a zero-valued budget, returns
// (DefaultDialTimeout, false) — exactly today's hard-coded 30s behaviour with
// no DNS-refresh override.
func TransportTuning(extra map[string]any) (dialTimeout time.Duration, forceDNSRefresh bool) {
	if extra == nil {
		return DefaultDialTimeout, false
	}
	b, ok := extra["retry_budget"].(TransportBudget)
	if !ok {
		return DefaultDialTimeout, false
	}
	if b.DialTimeout <= 0 {
		return DefaultDialTimeout, false
	}
	return b.DialTimeout, b.ForceDNSRefresh
}

// TransportBudget is the transport-knob half of the task-1056-B retry budget
// — the part a provider's newHTTPClient needs to pick up a lab-switched dial
// deadline without dragging the whole RetryBudget type (backoff/wall-clock are
// retry-loop concerns, not transport concerns) into every constructor.
type TransportBudget struct {
	DialTimeout     time.Duration
	ForceDNSRefresh bool
}

// RetryInfo describes a backoff about to happen: Attempt is the 1-based retry
// number (of Max) and Delay is how long SendWithRetry will wait before it.
type RetryInfo struct {
	Attempt int
	Max     int
	Delay   time.Duration
	Err     error
}

type RetryNotify func(RetryInfo)

type retryNotifyKey struct{}

// ConnectNotify fires when an HTTP attempt STARTS — before the dial, not
// after a failure (task 1055-A1). The RetryInfo carries Attempt = the 1-based
// request ordinal from ctx's request-attempt counter (so agent-level sampling
// retries and provider-level header retries share one increasing number) and
// Max = the provider-layer attempt cap. Receivers must never block the
// request goroutine: buffered channel plus a non-blocking send.
type ConnectNotify func(RetryInfo)

type connectNotifyKey struct{}

type requestAttemptCounterKey struct{}

type requestAttemptCounter struct {
	count atomic.Int64
}

// WithRetryNotify attaches a callback that SendWithRetry invokes before each
// backoff sleep, so the agent can surface a transient "retrying (n/m)" status.
func WithRetryNotify(ctx context.Context, fn RetryNotify) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, retryNotifyKey{}, fn)
}

func retryNotifyFromContext(ctx context.Context) RetryNotify {
	fn, _ := ctx.Value(retryNotifyKey{}).(RetryNotify)
	return fn
}

// WithConnectNotify attaches a callback that SendWithRetry invokes the moment
// each HTTP attempt starts, so the agent can surface an immediate "connecting
// (attempt N)" status instead of waiting for the first failure (task 1055-A1).
func WithConnectNotify(ctx context.Context, fn ConnectNotify) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, connectNotifyKey{}, fn)
}

func connectNotifyFromContext(ctx context.Context) ConnectNotify {
	fn, _ := ctx.Value(connectNotifyKey{}).(ConnectNotify)
	return fn
}

// WithRequestAttemptCounter returns a context that counts every HTTP request
// SendWithRetry starts. An existing counter is reused so a caller can observe
// attempts even when the provider returns before producing a Usage chunk.
// Provider implementations use one counter for a logical stream (including
// header retries and safe reconnects), then attach the final count to the
// stream's Usage record.
func WithRequestAttemptCounter(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if counter, _ := ctx.Value(requestAttemptCounterKey{}).(*requestAttemptCounter); counter != nil {
		return ctx
	}
	return context.WithValue(ctx, requestAttemptCounterKey{}, &requestAttemptCounter{})
}

// WithIndependentRequestAttemptCounter gives an auxiliary call its own usage
// count while preserving cancellation and other context values from its parent.
func WithIndependentRequestAttemptCounter(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestAttemptCounterKey{}, &requestAttemptCounter{})
}

// RequestAttemptCount returns the number of HTTP requests started through
// SendWithRetry for the counter attached to ctx.
func RequestAttemptCount(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	counter, _ := ctx.Value(requestAttemptCounterKey{}).(*requestAttemptCounter)
	if counter == nil {
		return 0
	}
	return int(counter.count.Load())
}

// ApplyRequestAttemptCount copies the stream's exact HTTP request count into a
// Usage record. Contexts without a counter leave the record unchanged so custom
// providers keep the zero-means-one compatibility contract.
func ApplyRequestAttemptCount(ctx context.Context, usage *Usage) {
	if usage == nil {
		return
	}
	if count := RequestAttemptCount(ctx); count > 0 {
		usage.RequestCount = count
	}
}

// UsageWithRequestAttemptCount returns a copy of usage carrying the exact
// number of HTTP requests observed through ctx. When a provider request fails
// before producing token usage, it returns a request-only Usage record so
// callers can still account for the API calls. If neither usage nor attempts
// exist, it returns nil.
func UsageWithRequestAttemptCount(ctx context.Context, usage *Usage) *Usage {
	count := RequestAttemptCount(ctx)
	if usage == nil {
		if count <= 0 {
			return nil
		}
		return &Usage{RequestCount: count, Unknown: true}
	}
	result := *usage
	if count > 0 {
		result.RequestCount = count
	}
	return &result
}

func recordRequestAttempt(ctx context.Context) {
	if ctx == nil {
		return
	}
	counter, _ := ctx.Value(requestAttemptCounterKey{}).(*requestAttemptCounter)
	if counter != nil {
		counter.count.Add(1)
	}
}

// APIError reports a non-OK HTTP status that isn't an auth failure. Status
// carries the code so the display layer can map it to an actionable, localized
// message; Body is a trimmed snippet of the response.
type APIError struct {
	RetryAfter          time.Duration // uncapped server delay for managed recovery
	ShouldRetry         string        // explicit provider retry hint
	Provider            string        // stable provider instance id
	ProviderDisplayName string
	Protocol            string
	Status              int
	Body                string
	TraceID             string // provider trace identifier from the response headers, when present
	RequestPath         string // path only; query and URL userinfo are never retained
	ToolContext         string // resolved Reasonix/MCP identity for provider-indexed tool schema errors
	Hint                string // actionable next step derived from request-side facts (e.g. task 470 inline-image rejection)
}

func (e *APIError) Error() string {
	label := ProviderDisplayLabel(e.Provider, e.ProviderDisplayName, e.Protocol)
	var base string
	if e.Body == "" {
		base = fmt.Sprintf("%s: status %d", label, e.Status)
	} else {
		base = fmt.Sprintf("%s: status %d: %s", label, e.Status, e.Body)
	}
	if e.ToolContext != "" {
		base += "\n" + e.ToolContext
	}
	if e.Hint != "" {
		base += "\n" + e.Hint
	}
	return base
}

// RetryableStatus reports whether a backoff can plausibly recover from status s:
// 408 (request timeout), 429 (rate limit) and 5xx (incl. Anthropic's 529). Other
// 4xx (400/401/402/422, …) are caller/config problems retrying can't fix.
func RetryableStatus(s int) bool {
	return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || (s >= 500 && s <= 599)
}

func transientErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// IsConnReset reports whether err is a connection-level drop (peer reset,
// truncated body, closed socket) as opposed to a protocol or caller error. A
// stream cut this way mid-body can be replayed from scratch, unlike a decode or
// 4xx error. The common trigger is a local proxy (v2rayN/sing-box) idle-closing
// the long-lived SSE connection during a reasoner's first-token gap.
func IsConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// responseHeaderTimeoutMarker is the phrase both net/http transports share
// when the client-side ResponseHeaderTimeout guard fires: HTTP/1.1 reports
// "net/http: timeout awaiting response headers" and the bundled HTTP/2
// transport reports "http2: timeout awaiting response headers" — the gateway
// accepted the request but never sent response headers (task 927: the 2026-10-16
// anthropic-endpoint incident burned 300s × 4 attempts this way). Shape match,
// not a closed enumeration (retrybudget rule #2407).
const responseHeaderTimeoutMarker = "timeout awaiting response headers"

// IsResponseHeaderTimeout reports whether err is the client-side
// ResponseHeaderTimeout guard firing — the request went out but no response
// headers arrived before the transport's deadline. A context deadline the
// CALLER set is not this shape: the caller owns that cancellation and must
// keep its own semantics, so context.DeadlineExceeded is excluded even though
// both surface as net.Error timeouts.
func IsResponseHeaderTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), responseHeaderTimeoutMarker)
}

func backoffDelay(attempt int, retryAfter time.Duration, budget RetryBudget, lastErr error) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return retryAfter
	}
	d := min(time.Duration(1<<(attempt-1))*500*time.Millisecond, maxBackoff)
	// 任务1056-B: a dial-timeout (the path itself is dead, not the server)
	// fast-backs-off instead of riding the exponential ramp — the incident
	// this budget was built for was 8.5 minutes of the latter.
	if budget.FastBackoff > 0 && IsDialTimeout(lastErr) && d > budget.FastBackoff {
		d = budget.FastBackoff
	}
	return d + time.Duration(rand.Intn(250))*time.Millisecond
}

func parseRetryAfter(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	// RFC 9110 also allows an HTTP-date; gateways in front of rate-limited
	// backends use it more often than the delta-seconds form.
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// readErrorBody drains a non-OK response body under a hard deadline and
// returns up to the first 4 KiB for the error message. Context cancellation
// already unblocks the read (the transport aborts body reads when the request
// context is canceled); the timer covers the case nobody cancels — a half-open
// upstream that sent headers and then went silent. Closing the body from the
// timer goroutine is the documented way to unblock an in-flight Read; it
// tears down the connection, which is the right call for a stalled peer.
func readErrorBody(resp *http.Response) []byte {
	timer := time.AfterFunc(errorBodyReadTimeout, func() { resp.Body.Close() })
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	// Drain the rest so a healthy connection can be reused; the timer still
	// arms this read, so a body that stalls after the first 4 KiB cannot
	// wedge the retry loop either.
	_, _ = io.Copy(io.Discard, resp.Body)
	timer.Stop()
	resp.Body.Close()
	return msg
}

// SendWithRetry POSTs a streaming request built by newReq and returns the OK
// response. It retries the connection+header phase up to MaxRetries times on
// transient network errors and retryable statuses with capped exponential
// backoff + jitter, honoring Retry-After. A 401/403 becomes *AuthError: it
// fails fast for a key that has never authenticated (opts.RetryAuth false), but
// for a previously-good key it backs off and retries up to maxAuthRetries —
// MiMo and similar gateways return a transient 401 under load. 任务1308: within
// that same budget the first 401 also forces ONE RefreshCredential re-read, so
// a stale cached credential self-heals on the next attempt instead of 401-
// looping until a runtime rebuild (AuthError.Reauthed marks the terminal face).
// Other non-OK statuses become *APIError. A RetryNotify in ctx fires before
// each sleep. Retries cover only the header phase — once the body streams,
// mid-stream failures are not retried (the model has already emitted tokens).
//
// Task 1056 方案 B: when a RetryBudget is attached to ctx (lab switch, default
// off) this loop additionally enforces a wall-clock cap (RetryBudget.WallClock)
// over the whole retry sequence and can fast-back-off a dial-timeout-class
// failure (RetryBudget.FastBackoff + DialTimeoutError). Off-state (no budget on
// ctx) is byte-for-byte identical to the pre-1056 loop.
func SendWithRetry(ctx context.Context, httpClient *http.Client, opts SendOptions, newReq func(context.Context) (*http.Request, error)) (*http.Response, error) {
	notify := retryNotifyFromContext(ctx)
	connect := connectNotifyFromContext(ctx)
	identity := RequestIdentity{Provider: opts.Provider, DisplayName: opts.ProviderDisplayName, Protocol: opts.Protocol}
	budget := RetryBudgetFromContext(ctx)
	var lastErr error
	var retryAfter time.Duration
	authRetries := 0

	limit := MaxRetries
	if budget.MaxRetries > 0 {
		limit = budget.MaxRetries
	}
	if ManagedRecovery(ctx) {
		limit = 0
	}
	var deadline time.Time
	if budget.WallClock > 0 {
		deadline = time.Now().Add(budget.WallClock)
	}
	exhausted := func(attempts int, started time.Time) error {
		if !budget.WrapExhausted {
			return lastErr
		}
		return &RetryBudgetExhaustedError{Attempts: attempts, Elapsed: time.Since(started), WallClock: budget.WallClock, Cause: lastErr}
	}
	started := time.Now()
	// 任务1308 ③: one throttled proactive credential check per SendWithRetry —
	// a long-lived session picks up an externally rotated credential on its
	// next request instead of waiting for a 401. The callback throttles itself
	// (CredentialRefreshInterval); nil keeps today's behaviour byte-for-byte.
	if opts.RefreshCredential != nil {
		opts.RefreshCredential(false)
	}
	reauthed := false
	for attempt := 0; attempt <= limit; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt, retryAfter, budget, lastErr)
			if !deadline.IsZero() && time.Now().Add(delay).After(deadline) {
				// The backoff itself would overrun the wall-clock budget:
				// cut it to the remaining time (a still-positive remainder
				// is honored below; a zero/negative one fails immediately).
				delay = time.Until(deadline)
				if delay <= 0 {
					return nil, exhausted(attempt, started)
				}
			}
			if notify != nil {
				notify(RetryInfo{Attempt: attempt, Max: limit, Delay: delay, Err: lastErr})
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		retryAfter = 0

		req, err := newReq(ctx)
		if err != nil {
			return nil, &RequestFailure{Identity: identity, Operation: "build request", Err: err}
		}
		recordRequestAttempt(ctx)
		if connect != nil {
			// 任务1055-A1: fired BEFORE the dial — the "connecting" status must
			// be visible during the handshake silence, not after a failure.
			// The ordinal comes from the request counter so the number keeps
			// increasing across both retry layers.
			connect(RetryInfo{Attempt: RequestAttemptCount(ctx), Max: limit + 1})
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			if !transientErr(err) {
				return nil, &RequestFailure{Identity: identity, Operation: "request failed", Err: err}
			}
			lastErr = &RequestFailure{Identity: identity, Operation: "request failed", Err: err}
			if !deadline.IsZero() && time.Now().After(deadline) {
				return nil, exhausted(attempt+1, started)
			}
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		msg := readErrorBody(resp)
		retryAfter = parseRetryAfter(resp)
		// Task 845: carry the provider's Retry-After into the quota error so
		// the agent's quota lane can honor a stated reset time instead of
		// probing blindly.
		if quota := quotaErrorFromResponse(opts.Provider, opts.ProviderDisplayName, opts.Protocol, resp.StatusCode, string(msg), retryAfter); quota != nil {
			return nil, quota
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			authErr := &AuthError{Provider: opts.Provider, ProviderDisplayName: opts.ProviderDisplayName, Protocol: opts.Protocol, KeyEnv: opts.KeyEnv, KeySource: opts.KeySource, Status: resp.StatusCode, HasKey: opts.KeyPresent, Body: strings.TrimSpace(string(msg)), Reauthed: reauthed}
			if !ManagedRecovery(ctx) && opts.RetryAuth && authRetries < maxAuthRetries {
				authRetries++
				// 任务1308 ①: the cached key may be stale while the credential
				// source has moved on (token-plan TTL / external rotation).
				// Force ONE re-read inside the existing auth budget; a changed
				// key rides this same retry attempt via newReq. Replay safety:
				// a header-phase 401 means the request was rejected before any
				// generation, and the changed-key guard means an unchanged
				// credential never adds an attempt beyond today's budget.
				if !reauthed && opts.RefreshCredential != nil {
					reauthed = true
					opts.RefreshCredential(true)
				}
				lastErr = authErr
				continue
			}
			return nil, authErr
		}
		apiErr := &APIError{
			RetryAfter:          retryAfter,
			ShouldRetry:         resp.Header.Get("x-should-retry"),
			Provider:            opts.Provider,
			ProviderDisplayName: opts.ProviderDisplayName,
			Protocol:            opts.Protocol,
			Status:              resp.StatusCode,
			Body:                strings.TrimSpace(string(msg)),
			TraceID:             responseTraceID(resp.Header),
			RequestPath:         responseRequestPath(resp),
		}
		if !RetryableStatus(resp.StatusCode) {
			if limitErr := ParseOutputLimitError(apiErr); limitErr != nil {
				return nil, limitErr
			}
			if limitErr := ParseContextLimitError(apiErr); limitErr != nil {
				return nil, limitErr
			}
			if replayErr := ParseReasoningReplayError(apiErr); replayErr != nil {
				return nil, replayErr
			}
			return nil, apiErr
		}
		lastErr = apiErr
	}
	if !deadline.IsZero() && time.Now().After(deadline) {
		// Wall-clock budget spent across attempts that each individually
		// returned a retryable failure in time — fail clearly instead of
		// handing back the bare last error with no explanation.
		return nil, exhausted(limit+1, started)
	}
	return nil, lastErr
}

func responseRequestPath(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	path := resp.Request.URL.EscapedPath()
	if len(path) > 512 {
		return path[:512]
	}
	return path
}

func responseTraceID(header http.Header) string {
	for _, name := range []string{"trace_id", "trace-id", "x-trace-id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
