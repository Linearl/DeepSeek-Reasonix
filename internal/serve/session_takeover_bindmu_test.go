package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 747: takeoverSession (and the 539 background poll that completes the
// same takeover) swap the lease keeper's binding — a session switch — so both
// must hold bindMu like /resume, /new, /fork and /release-session already do.
// These tests pin the lock behavior deterministically: with bindMu held by the
// test, neither path may complete its rebind; after the unlock both finish
// and the keeper lands on the taken-over session.

// publishTestYield makes the desktop half of a takeover: it holds target's
// lease, publishes a handoff reservation for this process's writer with the
// given handoff id, and (when marker is non-empty) writes the yielded marker
// so the poll's reservation-consume branch fires.
func publishTestYield(t *testing.T, target, handoffID string, marker string) {
	t.Helper()
	desktopLease, err := agent.TryAcquireSessionLease(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := desktopLease.ReleaseForHandoff(agent.SessionWriterID(), handoffID); err != nil {
		t.Fatal(err)
	}
	if marker != "" {
		if err := os.WriteFile(marker, []byte(agent.FormatTakeoverMarkerYielded(agent.SessionWriterID(), handoffID)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTakeoverPollRebindWaitsForBindMu(t *testing.T) {
	server, _, target := newTakeoverPendingFixture(t)
	marker := agent.TakeoverRequestMarkerPath(target)

	publishTestYield(t, target, "test-poll-bindmu", marker)

	// Hold bindMu: the poll consumes the reservation (marker removed, done
	// recorded) but its rebind must wait, not swap the keeper underneath the
	// other session-switch endpoints.
	server.bindMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.pollTakeoverYield(target, marker, "gc-1")
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("poll never consumed the reservation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if lease := server.leases.Lease(); lease != nil {
		t.Fatalf("keeper rebound to %q while bindMu was held — the poll rebind escaped the lock (task 747)", lease.Path())
	}

	server.bindMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not finish after bindMu was released")
	}
	lease := server.leases.Lease()
	if lease == nil || agent.CanonicalSessionPath(lease.Path()) != agent.CanonicalSessionPath(target) {
		t.Fatalf("keeper lease = %v, want rebind onto target after unlock", lease)
	}
}

func TestTakeoverSessionRebindWaitsForBindMu(t *testing.T) {
	server, _, target := newTakeoverPendingFixture(t)

	// Override the handoff-id seam so a pre-published reservation matches the
	// synchronous probe: the acquire succeeds and the handler reaches its
	// bindMu-guarded rebind (the only part of the endpoint the lock must
	// cover; the acquire itself stays outside by design, matching the
	// handoffLocked pattern).
	orig := newTakeoverHandoffID
	newTakeoverHandoffID = func() string { return "test-sync-bindmu" }
	t.Cleanup(func() { newTakeoverHandoffID = orig })
	publishTestYield(t, target, "test-sync-bindmu", "")

	server.bindMu.Lock()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.takeoverSession(rec, httptest.NewRequest(http.MethodPost, "/takeover-session",
			strings.NewReader(`{"name":"target","from":"`+agent.SessionWriterID()+`"}`)))
	}()

	// While bindMu is held the handler cannot complete — mutual exclusion is
	// unconditional, so any completion inside this window means the rebind
	// ran without the lock (pre-fix behavior). The recorder starts at Code
	// 200, and every terminal path of takeoverSession either WriteHeaders a
	// non-200 code or http.Errors, so "200 with an empty body" is exactly the
	// not-finished state.
	time.Sleep(300 * time.Millisecond)
	if rec.Code != http.StatusOK || rec.Body.Len() > 0 {
		t.Fatalf("takeoverSession completed (%d, body %q) while bindMu was held — the rebind escaped the lock (task 747)", rec.Code, rec.Body.String())
	}

	server.bindMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("takeoverSession did not finish after bindMu was released")
	}
}
