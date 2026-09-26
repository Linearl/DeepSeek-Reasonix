package agent

import (
	"strings"
	"sync"
	"time"
)

// Task 243 A4 (sub-report 02-④C, MiMo #2407/#2450 borrowed as rules):
// a kind-aware, bounded retry budget for provider stream errors.
//
// Three rules absorbed from MiMo's mistakes:
//  1. #2407: closed enums of retryable codes always lag the provider —
//     classify by SHAPE (HTTP status class, phase, transport markers) through
//     an OPEN matcher list, never a fixed error-message enumeration.
//  2. #2450: a bounded window budget (8 attempts / 15 minutes), not one
//     failure = terminal and not an unbounded attempt loop either.
//  3. the persisted error identity keeps the ORIGINAL error string — MiMo's
//     "JSON.stringify plain string errors broke Desktop opaque-token
//     matching" is the encoding trap: fingerprint must survive a JSON
//     round-trip byte-for-byte.

// ErrKind is the shape class of a failed sampling attempt. It is a closed
// SET of kinds but an open CLASSIFIER: unknown shapes map to kindUnknown,
// which the matrix treats as budgeted-transient (never silently terminal).
type ErrKind string

const (
	KindTerminal4xx  ErrKind = "terminal_4xx"  // a 4xx the provider will refuse again (except 408/429)
	KindRateLimit    ErrKind = "rate_limit_429" // 429: its own lane — fast-retried, never waited out here
	KindTransient5xx ErrKind = "transient_5xx"  // 5xx: budgeted transient
	KindNetwork      ErrKind = "network"        // connect/reset/EOF/deadline: budgeted transient
	KindUnknown      ErrKind = "unknown"        // unclassifiable: budgeted transient, never silently terminal
)

// httpStatusOf extracts an HTTP status from free-form provider error text
// (the shape face — the provider layer does not expose a typed status on
// every failure). Open-ended: it matches where a status token appears, not a
// closed list of message formats.
func httpStatusOf(errText string) int {
	low := strings.ToLower(errText)
	// The marker stops at the space: "status 4" would eat the class digit and
	// parse "400" as "00" → 0. Take all THREE digits after the space instead.
	for _, marker := range []string{"status ", "http ", "code "} {
		if i := strings.Index(low, marker); i >= 0 && i+len(marker)+3 <= len(low) {
			digits := low[i+len(marker) : i+len(marker)+3]
			n := 0
			ok := true
			for _, c := range digits {
				if c < '0' || c > '9' {
					ok = false
					break
				}
				n = n*10 + int(c-'0')
			}
			if ok && n >= 400 && n < 600 {
				return n
			}
		}
	}
	return 0
}

// classifyRetryKind maps a shape to a kind: 4xx terminal EXCEPT 408/429
// (both are "retry, the condition is timing"), 5xx and transport markers as
// transient, everything else unknown-transient.
func classifyRetryKind(errText string) ErrKind {
	low := strings.ToLower(errText)
	if status := httpStatusOf(errText); status != 0 {
		switch {
		case status == 429:
			return KindRateLimit
		case status == 408:
			return KindNetwork
		case status >= 500:
			return KindTransient5xx
		case status >= 400:
			return KindTerminal4xx
		}
	}
	for _, marker := range []string{
		"connection reset", "broken pipe", "unexpected eof", "server disconnected",
		"deadline exceeded", "i/o timeout", "dial tcp", "tls handshake",
		"stream error", "internal_error", "read stream",
	} {
		if strings.Contains(low, marker) {
			return KindNetwork
		}
	}
	return KindUnknown
}

// retryMatrix is the phase×kind admission table (the rule surface — the
// phase argument lets a stricter lane opt in later without touching the
// classifier). 4xx never retries in ANY phase; rate limit and transient
// kinds stay budgeted.
func retryMatrix(phase string, kind ErrKind) bool {
	if kind == KindTerminal4xx {
		return false
	}
	return true
}

// RetryBudgetBounds borrows MiMo #2450's window: 8 attempts per 15 minutes.
const (
	retryBudgetLimit  = 8
	retryBudgetWindow = 15 * time.Minute
)

// RetryBudget counts admitted retry attempts per phase inside a sliding
// window. Zero value is NOT ready — construct with NewRetryBudget. The
// fingerprint map records the identity of the last refused error (original
// string, unmodified) so a persisted record survives JSON encoding.
type RetryBudget struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	stamps  map[string][]time.Time // phase -> attempt times inside the window
	refused map[string]string      // phase -> original error string of the refusal
}

// NewRetryBudget constructs a budget with the borrowed bounds.
func NewRetryBudget() *RetryBudget {
	return &RetryBudget{
		limit:   retryBudgetLimit,
		window:  retryBudgetWindow,
		stamps:  make(map[string][]time.Time),
		refused: make(map[string]string),
	}
}

// RetryFingerprint is the persisted error identity: the ORIGINAL error
// string, byte-for-byte. No re-encoding — a plain string that goes through
// another marshal layer is exactly the trap that broke identity matching.
func RetryFingerprint(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Allow reports whether another retry may run in this phase right now: the
// kind must be admitted by the matrix, and the phase's sliding-window count
// must stay under the limit. On refusal the original error string is kept as
// the phase's refused identity. now is injectable for tests.
func (b *RetryBudget) Allow(phase string, err error, now time.Time) (ok bool, kind ErrKind, reason string) {
	if b == nil {
		return false, "", "no budget" // nil budget never admits: fail closed
	}
	text := ""
	if err != nil {
		text = err.Error()
	}
	kind = classifyRetryKind(text)
	if !retryMatrix(phase, kind) {
		b.mu.Lock()
		b.refused[phase] = RetryFingerprint(err)
		b.mu.Unlock()
		return false, kind, "terminal kind: " + string(kind)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cut := now.Add(-b.window)
	kept := b.stamps[phase][:0:0]
	for _, t := range b.stamps[phase] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= b.limit {
		b.stamps[phase] = kept
		b.refused[phase] = RetryFingerprint(err)
		return false, kind, "window budget exhausted"
	}
	kept = append(kept, now)
	b.stamps[phase] = kept
	return true, kind, "admitted"
}

// RefusedIdentity returns the original error string of the last refusal in
// this phase ("" when none) — the persisted identity face.
func (b *RetryBudget) RefusedIdentity(phase string) string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refused[phase]
}
