package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// rateLimitMarkers is the OPEN shape matcher for a rate-limit answer
// (retrybudget rule #2407: classify by shape, never by a closed message
// enumeration). Gateways report a limit two ways: an HTTP 429 body — caught by
// the *APIError branch below — or, inside a streaming error frame, a plain
// wrapped error carrying no status at all ("mimo-token-plan: Request rate
// limited", openai.go's `fmt.Errorf("%s: %s", name, sr.Error.Message)`).
// Matching text keeps both faces one class instead of leaving the second one
// at Phase "unknown"/Retryable=false.
var rateLimitMarkers = []string{
	"rate limit", "rate-limit", "rate_limit",
	"too many requests", "too_many_requests",
}

// RateLimitText reports whether free-form provider error text carries the
// rate-limit shape. Exported so the agent's summary and retry-budget
// classifiers read the same markers as this package's recovery classifier —
// the three judgments must never disagree about what a rate limit is.
func RateLimitText(text string) bool {
	low := strings.ToLower(text)
	for _, marker := range rateLimitMarkers {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

// RateLimited reports the rate-limit shape of an error: a typed 429 or the
// text markers above (see RateLimitText).
func RateLimited(err error) bool {
	if err == nil {
		return false
	}
	var api *APIError
	if errors.As(err, &api) && api.Status == 429 {
		return true
	}
	return RateLimitText(err.Error())
}

type managedRecoveryKey struct{}

// WithManagedRecovery gives the caller sole ownership of retries. It does not
// change wire bytes and is intentionally opt-in for standalone provider users.
func WithManagedRecovery(ctx context.Context) context.Context {
	return context.WithValue(ctx, managedRecoveryKey{}, true)
}
func ManagedRecovery(ctx context.Context) bool {
	v, _ := ctx.Value(managedRecoveryKey{}).(bool)
	return v
}

// RecoveryFailure is local diagnostic state, never part of a model request.
type RecoveryFailure struct {
	Phase      string
	Status     int
	Code       string
	RetryAfter time.Duration
	Retryable  bool
}

func ClassifyRecovery(err error) RecoveryFailure {
	f := RecoveryFailure{Phase: "unknown"}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return f
	}
	if exhausted := AsRecoveryWaitExhausted(err); exhausted != nil {
		f.Phase, f.Status, f.Code = exhausted.Phase, exhausted.Status, exhausted.Code
		return f
	}
	if q := AsQuotaError(err); q != nil {
		// Task 845: carry the provider-stated Retry-After so the agent's
		// quota wait lane can schedule its next probe from it.
		f.Phase, f.Status, f.Code, f.RetryAfter = "quota", q.Status, q.Code, q.RetryAfter
		return f
	}
	var auth *AuthError
	if errors.As(err, &auth) {
		f.Phase = "auth"
		return f
	}
	if AsContextLimitError(err) != nil || AsOutputLimitError(err) != nil {
		f.Phase = "limit"
		return f
	}
	if AsReasoningReplayError(err) != nil {
		f.Phase = "protocol"
		return f
	}
	var api *APIError
	if errors.As(err, &api) {
		f.Phase, f.Status, f.RetryAfter = "headers", api.Status, api.RetryAfter
		var body struct {
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(api.Body), &body) == nil {
			f.Code = body.Error.Code
			if f.Code == "" {
				f.Code = body.Error.Type
			}
		}
		f.Retryable = RetryableStatus(api.Status) || api.Status == 409
		if api.ShouldRetry == "false" {
			f.Retryable = false
		}
		return f
	}
	// 任务912 P0-b: a rate limit that arrives as plain text (streaming error
	// frame, no HTTP status) used to fall through to Phase "unknown",
	// Retryable=false — the sampling log's `retryable=false` on
	// "mimo-token-plan: Request rate limited" killed the turn on its first
	// response. The shape says "headers, 429": that is the lane the wait/queue
	// logic and the budget exemption already read, so the typed-429 face and
	// the text face now behave identically.
	if RateLimited(err) {
		f.Phase, f.Status, f.Retryable = "headers", 429, true
		return f
	}
	if IsStreamInterrupted(err) {
		f.Phase, f.Retryable = "stream", true
		return f
	}
	if errors.Is(err, ErrEmptyResponse) {
		f.Phase, f.Retryable = "empty", true
		return f
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || IsConnReset(err) {
		f.Phase, f.Retryable = "connect", true
	}
	return f
}
