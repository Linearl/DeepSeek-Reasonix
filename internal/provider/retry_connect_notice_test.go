package provider

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// 任务1055-A1: the connect notify fires BEFORE each dial — the "connecting
// (attempt N)" status must be visible during the handshake silence, not only
// after the first failure — and its ordinal rides the request-attempt counter
// so the number increases across retries. The timeline also pins the ordering:
// connect → (attempt fails) → retry → connect, never retry before connect.
func TestSendWithRetryNotifiesConnectBeforeEachDial(t *testing.T) {
	calls := 0
	timeline := []string{}
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		timeline = append(timeline, "dial")
		// A brief block so a naive "notify after Do returns" implementation
		// would order the connect notice after the failure instead of before.
		time.Sleep(10 * time.Millisecond)
		if calls == 1 {
			timeline = append(timeline, "fail")
			return statusResp(503, nil), nil
		}
		timeline = append(timeline, "ok")
		return statusResp(200, nil), nil
	})}
	ctx := WithRequestAttemptCounter(context.Background())
	var connectAttempts, retryAttempts []int
	ctx = WithConnectNotify(ctx, func(i RetryInfo) {
		timeline = append(timeline, "connect")
		connectAttempts = append(connectAttempts, i.Attempt)
	})
	ctx = WithRetryNotify(ctx, func(i RetryInfo) {
		timeline = append(timeline, "retry")
		retryAttempts = append(retryAttempts, i.Attempt)
	})

	resp, err := SendWithRetry(ctx, cl, SendOptions{Provider: "p", KeyEnv: "KEY"}, newDummyReq)
	if err != nil {
		t.Fatalf("should recover after one retry: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	// One connect notice per dial, ordinal = the request counter (1, 2).
	if len(connectAttempts) != 2 || connectAttempts[0] != 1 || connectAttempts[1] != 2 {
		t.Fatalf("connect notices = %v, want ordinals [1 2]", connectAttempts)
	}
	// The retry notice still fires after the failure only.
	if len(retryAttempts) != 1 || retryAttempts[0] != 1 {
		t.Fatalf("retry notices = %v, want [1]", retryAttempts)
	}
	// Ordering: every "connect" precedes the dial it belongs to, and the first
	// connect precedes the first failure (发起即见，不等失败).
	want := []string{"connect", "dial", "fail", "retry", "connect", "dial", "ok"}
	if len(timeline) != len(want) {
		t.Fatalf("timeline = %v, want %v", timeline, want)
	}
	for i, step := range want {
		if timeline[i] != step {
			t.Fatalf("timeline = %v, want %v (diverges at %d: %s != %s)", timeline, want, i, timeline[i], step)
		}
	}
}

// 任务1055: the connecting and retry callbacks are independent context values
// — attaching one must not shadow the other (the agent wires two channels).
func TestConnectAndRetryNotifyAreIndependent(t *testing.T) {
	cl := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return statusResp(200, nil), nil
	})}
	var connects, retries int
	ctx := WithRequestAttemptCounter(context.Background())
	ctx = WithConnectNotify(ctx, func(RetryInfo) { connects++ })
	ctx = WithRetryNotify(ctx, func(RetryInfo) { retries++ })
	if _, err := SendWithRetry(ctx, cl, SendOptions{Provider: "p", KeyEnv: "KEY"}, newDummyReq); err != nil {
		t.Fatalf("send: %v", err)
	}
	if connects != 1 || retries != 0 {
		t.Fatalf("connects=%d retries=%d, want 1 connect notice and 0 retry notices on a clean attempt", connects, retries)
	}
}
