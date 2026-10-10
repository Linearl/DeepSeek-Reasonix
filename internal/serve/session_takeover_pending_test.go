package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// Task 539 route A: a takeover against a desktop-held session answers 202
// immediately and finishes in the background once the desktop yields (marker
// three-state protocol). The terminal state is queryable through the
// ?session= status view.

func newTakeoverPendingFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	active := filepath.Join(dir, "active.jsonl")
	target := filepath.Join(dir, "target.jsonl")
	for _, p := range []string{active, target} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: active})
	server := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	server.leases = control.NewSessionLeaseKeeper()
	t.Cleanup(server.Close)
	takeoverPollInterval = 20 * time.Millisecond
	takeoverPollWindow = 2 * time.Second
	t.Cleanup(func() {
		takeoverPollInterval = 700 * time.Millisecond
		takeoverPollWindow = agent.SessionTakeoverYieldWindow
	})
	return server, dir, target
}

func TestTakeoverSessionPendingUntilDesktopYields(t *testing.T) {
	server, _, target := newTakeoverPendingFixture(t)
	marker := agent.TakeoverRequestMarkerPath(target)

	// The desktop holds the lease.
	desktopLease, err := agent.TryAcquireSessionLease(target)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	server.takeoverSession(rec, httptest.NewRequest(http.MethodPost, "/takeover-session",
		strings.NewReader(`{"name":"target","from":"gc-1"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("takeover status = %d, want 202 pending (body %q)", rec.Code, rec.Body.String())
	}
	var body struct {
		Status  string `json:"status"`
		Session string `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Status != "pending" || body.Session != "target" {
		t.Fatalf("202 body = %q (err %v), want pending/target", rec.Body.String(), err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("takeover marker not written: %v", err)
	}
	if state := agent.ParseTakeoverMarker(string(raw)); state.Kind != agent.TakeoverMarkerKindRequest {
		t.Fatalf("fresh marker state = %+v, want plain request", state)
	}
	if pending, _, _ := server.takeoverStatusFor(target); !pending {
		t.Fatal("takeover attempt not recorded as pending")
	}

	// The desktop accepts (marker → pending) and later finishes its turn:
	// reservation published, yield-ack written.
	if err := os.WriteFile(marker, []byte(agent.FormatTakeoverMarkerPending(agent.SessionWriterID())), 0o600); err != nil {
		t.Fatal(err)
	}
	handoffID := "test-takeover-1"
	if err := desktopLease.ReleaseForHandoff(agent.SessionWriterID(), handoffID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(agent.FormatTakeoverMarkerYielded(agent.SessionWriterID(), handoffID)), 0o600); err != nil {
		t.Fatal(err)
	}

	// The background poll consumes the reservation and completes. Marker
	// removal precedes the keeper rebind inside the poll, so "marker gone"
	// alone is not readiness — wait for the rebind to land too, or this test
	// flakes on the observation window (observed ~30% on Windows even before
	// task 747's lock widened it by an uncontended mutex).
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, statErr := os.Stat(marker)
		lease := server.leases.Lease()
		if os.IsNotExist(statErr) && lease != nil &&
			agent.CanonicalSessionPath(lease.Path()) == agent.CanonicalSessionPath(target) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background takeover poll never completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending, failed, message := server.takeoverStatusFor(target); pending || failed || message != "" {
		t.Fatalf("terminal state = (pending %v, failed %v, %q), want done", pending, failed, message)
	}
	// The keeper rebind onto the taken-over session.
	lease := server.leases.Lease()
	if lease == nil || agent.CanonicalSessionPath(lease.Path()) != agent.CanonicalSessionPath(target) {
		t.Fatalf("keeper lease = %v, want rebind onto target", lease)
	}
}

func TestTakeoverSessionPendingFailsOnMarkerWithdrawal(t *testing.T) {
	server, _, target := newTakeoverPendingFixture(t)
	marker := agent.TakeoverRequestMarkerPath(target)

	desktopLease, err := agent.TryAcquireSessionLease(target)
	if err != nil {
		t.Fatal(err)
	}
	defer desktopLease.Release()

	rec := httptest.NewRecorder()
	server.takeoverSession(rec, httptest.NewRequest(http.MethodPost, "/takeover-session",
		strings.NewReader(`{"name":"target","from":"gc-1"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("takeover status = %d, want 202", rec.Code)
	}
	// The desktop rejects: the watcher removes the marker.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, failed, message := server.takeoverStatusFor(target)
		if failed {
			if !strings.Contains(message, takeoverWithdrawnMessage) {
				t.Fatalf("failure message = %q, want the withdrawal wording", message)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("withdrawn takeover never recorded a failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTakeoverSessionPendingTimesOutWithApprovedWording(t *testing.T) {
	server, _, target := newTakeoverPendingFixture(t)
	marker := agent.TakeoverRequestMarkerPath(target)

	desktopLease, err := agent.TryAcquireSessionLease(target)
	if err != nil {
		t.Fatal(err)
	}
	defer desktopLease.Release()

	rec := httptest.NewRecorder()
	server.takeoverSession(rec, httptest.NewRequest(http.MethodPost, "/takeover-session",
		strings.NewReader(`{"name":"target","from":"gc-1"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("takeover status = %d, want 202", rec.Code)
	}
	// Nobody yields; the marker stays in the accepted-pending state until T2.
	if err := os.WriteFile(marker, []byte(agent.FormatTakeoverMarkerPending(agent.SessionWriterID())), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, failed, message := server.takeoverStatusFor(target)
		if failed {
			if message != takeoverTimeoutMessage {
				t.Fatalf("timeout message = %q, want the approved wording %q", message, takeoverTimeoutMessage)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("timed-out marker not cleaned up")
			}
			// The desktop keeps its lease after the failure.
			if desktopLease.Released() {
				t.Fatal("desktop lease released despite the timeout")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("pending takeover never timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStatusViewForPathDecoratesTakeoverFields(t *testing.T) {
	server, _, target := newTakeoverPendingFixture(t)

	view := server.statusViewForPath(target, true)
	if pending, ok := view["takeoverPending"].(bool); !ok || pending {
		t.Fatalf("takeoverPending without an attempt = %v (present %v)", view["takeoverPending"], ok)
	}

	server.recordTakeoverState(target, takeoverStatePending, "")
	view = server.statusViewForPath(target, true)
	if pending, _ := view["takeoverPending"].(bool); !pending {
		t.Fatalf("takeoverPending = %v, want true while pending", view["takeoverPending"])
	}

	server.recordTakeoverState(target, takeoverStateFailed, takeoverTimeoutMessage)
	view = server.statusViewForPath(target, false)
	if pending, _ := view["takeoverPending"].(bool); pending {
		t.Fatal("takeoverPending true after failure")
	}
	if view["takeoverStatus"] != takeoverStateFailed || view["takeoverMessage"] != takeoverTimeoutMessage {
		t.Fatalf("failure decoration = %v/%v", view["takeoverStatus"], view["takeoverMessage"])
	}
}
