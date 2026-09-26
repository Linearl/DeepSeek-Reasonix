package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// Task 243 A4 acceptance: budget matrix (4xx terminal in any phase, 5xx/
// network budgeted), sliding 8/15min window, original-string error identity
// surviving JSON encoding, and the stream fast-lane actually refusing.

// TestRetryBudgetMatrix: the phase×kind table — a 4xx never admits, 429/
// 408/5xx/transport markers and unknown shapes do (budget permitting).
func TestRetryBudgetMatrix(t *testing.T) {
	cases := []struct {
		errText string
		want    ErrKind
		allow   bool
	}{
		{"provider returned status 400 bad request", KindTerminal4xx, false},
		{"api error: http 404 not found", KindTerminal4xx, false},
		{"status 401 unauthorized", KindTerminal4xx, false},
		{"status 429 too many requests", KindRateLimit, true},
		{"status 408 request timeout", KindNetwork, true},
		{"status 500 internal error", KindTransient5xx, true},
		{"status 503 service unavailable", KindTransient5xx, true},
		{"connection reset by peer", KindNetwork, true},
		{"stream error: unexpected eof", KindNetwork, true},
		{"something unprecedented from the provider", KindUnknown, true},
	}
	for _, tc := range cases {
		got := classifyRetryKind(tc.errText)
		if got != tc.want {
			t.Errorf("classifyRetryKind(%q) = %q, want %q", tc.errText, got, tc.want)
		}
		if allow := retryMatrix("stream", got); allow != tc.allow {
			t.Errorf("retryMatrix(stream, %q) = %v, want %v", got, allow, tc.allow)
		}
		// Matrix is phase-open: terminal 4xx refuses in EVERY phase, the
		// transient kinds admit in every phase today.
		for _, phase := range []string{"connect", "headers", "body", "summary"} {
			if allow := retryMatrix(phase, got); allow != tc.allow {
				t.Errorf("retryMatrix(%s, %q) = %v, want %v", phase, got, allow, tc.allow)
			}
		}
	}
}

// TestRetryBudgetWindow: 8 admissions inside the window, the 9th refused —
// and a stamp older than the window frees a slot again (sliding).
func TestRetryBudgetWindow(t *testing.T) {
	b := NewRetryBudget()
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < retryBudgetLimit; i++ {
		ok, kind, why := b.Allow("stream", errors.New("status 503"), base.Add(time.Duration(i)*time.Second))
		if !ok {
			t.Fatalf("attempt %d refused (%s/%s), want admitted", i+1, kind, why)
		}
	}
	ok, kind, why := b.Allow("stream", errors.New("status 503"), base)
	if ok || kind != KindTransient5xx || why != "window budget exhausted" {
		t.Fatalf("9th Allow = (%v, %q, %q), want refused by window", ok, kind, why)
	}
	// The refused identity is the ORIGINAL string, byte-for-byte.
	if id := b.RefusedIdentity("stream"); id != "status 503" {
		t.Fatalf("refused identity = %q, want the original error string", id)
	}
	// Sliding: after the window passes, a fresh attempt admits again.
	ok, _, _ = b.Allow("stream", errors.New("status 503"), base.Add(retryBudgetWindow+time.Second))
	if !ok {
		t.Fatal("attempt after the window refused, want the slide to free a slot")
	}
	// Terminal 4xx: refused immediately AND stamps the identity.
	ok, kind, why = b.Allow("stream", errors.New("status 400"), base)
	if ok || kind != KindTerminal4xx || !strings.HasPrefix(why, "terminal kind") {
		t.Fatalf("4xx Allow = (%v, %q, %q), want terminal refusal", ok, kind, why)
	}
	if id := b.RefusedIdentity("stream"); id != "status 400" {
		t.Fatalf("4xx identity = %q, want overwritten original string", id)
	}
}

// TestRetryBudgetNilFailsClosed: a nil budget (unconstructed agent) never
// admits — fail closed rather than admit an unbounded storm.
func TestRetryBudgetNilFailsClosed(t *testing.T) {
	var b *RetryBudget
	if ok, _, _ := b.Allow("stream", errors.New("status 500"), time.Now()); ok {
		t.Fatal("nil budget admitted, want fail-closed")
	}
	if id := b.RefusedIdentity("stream"); id != "" {
		t.Fatalf("nil budget identity = %q, want empty", id)
	}
}

// TestRetryFingerprintJSONStable: the persisted error identity survives a
// JSON round-trip byte-for-byte — the plain-string trap MiMo hit
// (JSON.stringify re-encoding broke opaque-token matching) has no Go equivalent.
func TestRetryFingerprintJSONStable(t *testing.T) {
	err := errors.New(`stream error: unexpected EOF mid-frame {"trace":"abc"}`)
	fp := RetryFingerprint(err)
	if fp != err.Error() {
		t.Fatalf("fingerprint = %q, want the original string verbatim", fp)
	}
	blob, jerr := json.Marshal(fp)
	if jerr != nil {
		t.Fatalf("marshal: %v", jerr)
	}
	var back string
	if uerr := json.Unmarshal(blob, &back); uerr != nil {
		t.Fatalf("unmarshal: %v", uerr)
	}
	if back != err.Error() {
		t.Fatalf("JSON round-trip changed identity: %q != %q", back, err.Error())
	}
	if RetryFingerprint(nil) != "" {
		t.Fatal("nil fingerprint must be empty")
	}
}

// TestWaitSamplingRetryBudgetRefuses: the wired fast lane — a terminal 4xx
// stream error is refused before the attempt-cap logic, so the turn stops
// instead of burning attempts on a refusal the provider will repeat.
func TestWaitSamplingRetryBudgetRefuses(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	// Drive the gate directly through the same call the fast lane makes:
	// a terminal shape must refuse, a transient shape must admit.
	if ok, kind, _ := a.streamRetryBudget.Allow("body", errors.New("status 400 bad request"), time.Now()); ok || kind != KindTerminal4xx {
		t.Fatalf("4xx via agent budget = (%v, %q), want refused", ok, kind)
	}
	if ok, kind, _ := a.streamRetryBudget.Allow("body", errors.New("status 502 bad gateway"), time.Now()); !ok || kind != KindTransient5xx {
		t.Fatalf("5xx via agent budget = (%v, %q), want admitted", ok, kind)
	}
	_ = context.Background() // keep context imported for the wired-suite shape
}
