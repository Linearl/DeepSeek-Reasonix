package serve

import (
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tl1854 R5 (S1–S4): the heartbeat lease narrows from device scope to session
// scope. A device beating for one conversation must not lock the instance's
// OTHER sessions; releasing/expiring is judged per session; the device record
// dies only with its last session; the lease frees only when the released or
// expired session is the one this runtime guards.

// hookEvent captures one remote-write-authority notification for assertions.
type hookEvent struct {
	device  string
	held    bool
	session string
}

func captureHookEvents(t *testing.T) func() []hookEvent {
	t.Helper()
	var notified []hookEvent
	prev := remoteWriteAuthorityHook
	remoteWriteAuthorityHook = func(deviceID string, held bool, session string) {
		notified = append(notified, hookEvent{device: deviceID, held: held, session: session})
	}
	t.Cleanup(func() { remoteWriteAuthorityHook = prev })
	return func() []hookEvent { return notified }
}

// TestSubmitGuardIsSessionScoped pins S1: the write guard keys on the
// foreground session, not on "some device is alive".
func TestSubmitGuardIsSessionScoped(t *testing.T) {
	f := newHeartbeatFixture(t) // foreground session: held

	// A device beating for ANOTHER session must not hold this foreground.
	f.postHeartbeat(t, "gc-a", "some-other-session")
	if id, held := f.server.remoteDeviceHoldsWrite(); held {
		t.Fatalf("remoteDeviceHoldsWrite = (%q,%v) for an unrelated session's heartbeat, want not held", id, held)
	}

	// The same device beating for the FOREGROUND session does hold it.
	f.postHeartbeat(t, "gc-a", f.name)
	if id, held := f.server.remoteDeviceHoldsWrite(); !held || id != "gc-a" {
		t.Fatalf("remoteDeviceHoldsWrite = (%q,%v), want (gc-a,true) while the foreground is held", id, held)
	}

	// Foreign submits are refused; the holder's own identity is exempt.
	req := httptest.NewRequest("POST", "/submit", strings.NewReader(`{"input":"x"}`))
	rec := httptest.NewRecorder()
	f.server.submit(rec, req)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "session-level exclusion") {
		t.Fatalf("foreign submit = %d %q, want 409 session-level exclusion", rec.Code, rec.Body.String())
	}
}

// TestHeartbeatReleaseWithSessionNameIsPerSession pins S2+S4: releasing one
// named session keeps the device record and any OTHER held session's lease;
// releasing the current session frees the lease and drops the record with it.
func TestHeartbeatReleaseWithSessionNameIsPerSession(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", f.name) // current foreground
	f.postHeartbeat(t, "gc-a", "other-session")
	events := captureHookEvents(t)

	// Release the UNRELATED session: record survives, lease untouched.
	f.postRelease(t, "gc-a", "other-session")
	f.server.heartbeatMu.Lock()
	_, alive := f.server.heartbeats["gc-a"]
	f.server.heartbeatMu.Unlock()
	if !alive {
		t.Fatal("device record died after releasing one of two held sessions")
	}
	if f.leases.Lease() == nil {
		t.Fatal("lease freed by an unrelated session's release")
	}
	if evs := events(); len(evs) != 1 || filepath.Base(evs[0].session) != "other-session.jsonl" {
		t.Fatalf("hook events = %+v, want exactly the unrelated session cleared", evs)
	}

	// Release the CURRENT session: lease freed, record dies with it.
	f.postRelease(t, "gc-a", f.name)
	f.server.heartbeatMu.Lock()
	_, alive = f.server.heartbeats["gc-a"]
	f.server.heartbeatMu.Unlock()
	if alive {
		t.Fatal("device record survived its last session's release")
	}
	if f.leases.Lease() != nil {
		t.Fatal("current session's lease not freed by its named release")
	}
	if evs := events(); len(evs) != 2 || filepath.Base(evs[1].session) != f.name+".jsonl" {
		t.Fatalf("hook events = %+v, want the current session cleared second", evs)
	}
}

// TestSessionExpiryReleasesOnlyExpiredSession pins S3: per-session timestamps
// drive expiry; the record dies only when its LAST session expires.
func TestSessionExpiryReleasesOnlyExpiredSession(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", f.name)
	f.postHeartbeat(t, "gc-a", "other-session")
	events := captureHookEvents(t)

	// Age ONLY the unrelated session: it expires and frees nothing; the
	// device record survives because "held" is still fresh.
	f.server.heartbeatMu.Lock()
	if entry := f.server.heartbeats["gc-a"]; entry != nil {
		entry.sessions["other-session"] = time.Now().Add(-2 * heartbeatExpiry)
	}
	f.server.heartbeatMu.Unlock()
	f.server.releaseExpiredHeartbeats()

	f.server.heartbeatMu.Lock()
	_, alive := f.server.heartbeats["gc-a"]
	f.server.heartbeatMu.Unlock()
	if !alive {
		t.Fatal("device record dropped while a fresh session remained")
	}
	if f.leases.Lease() == nil {
		t.Fatal("current lease freed by an unrelated session's expiry")
	}
	if evs := events(); len(evs) != 1 || filepath.Base(evs[0].session) != "other-session.jsonl" {
		t.Fatalf("hook events = %+v, want exactly the expired session", evs)
	}

	// Age the current session too: record dies, lease frees.
	f.ageDevice("gc-a")
	f.server.heartbeatMu.Lock()
	_, alive = f.server.heartbeats["gc-a"]
	f.server.heartbeatMu.Unlock()
	if alive {
		t.Fatal("device record survived full expiry")
	}
	if f.leases.Lease() != nil {
		t.Fatal("current lease not freed by its own expiry")
	}
	if evs := events(); len(evs) != 2 || filepath.Base(evs[1].session) != f.name+".jsonl" {
		t.Fatalf("hook events = %+v, want the current session's expiry second", evs)
	}
}

func (f *heartbeatFixture) postRelease(t *testing.T, deviceID, session string) {
	t.Helper()
	body := `{"name":` + strconv.Quote(session) + `,"device_id":` + strconv.Quote(deviceID) + `,"release":true}`
	req := httptest.NewRequest("POST", "/heartbeat", strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.server.heartbeat(rec, req)
	if rec.Code != 204 {
		t.Fatalf("release heartbeat status = %d body=%q, want 204", rec.Code, rec.Body.String())
	}
}
