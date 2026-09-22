package serve

import (
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// Task 36 (acceptance: heartbeats are keyed per device). Two devices holding
// the same-named session renew two independent entries: one device's silence
// expires its own lease-hold without the other's liveness keeping it alive —
// the pre-36 bug where every client renewed the shared session-name key.

type heartbeatFixture struct {
	server *Server
	leases *control.SessionLeaseKeeper
	name   string
}

func newHeartbeatFixture(t *testing.T) *heartbeatFixture {
	t.Helper()
	dir := t.TempDir()
	active := filepath.Join(dir, "held.jsonl")
	saveServeTestSession(t, active)

	bc := NewBroadcaster()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, bc)
	ctrl := control.New(control.Options{Executor: exec, Sink: bc, SessionDir: dir, SessionPath: active})
	server := New(ctrl, bc, config.ServeConfig{})
	leases := control.NewSessionLeaseKeeper()
	if err := leases.Rebind(active); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	server.SetSessionLeases(leases)
	t.Cleanup(func() { leases.Release(); ctrl.Close() })
	return &heartbeatFixture{server: server, leases: leases, name: "held"}
}

func (f *heartbeatFixture) postHeartbeat(t *testing.T, deviceID, session string) {
	t.Helper()
	body := `{"name":` + strconv.Quote(session)
	if deviceID != "" {
		body += `,"device_id":` + strconv.Quote(deviceID)
	}
	body += `}`
	req := httptest.NewRequest("POST", "/heartbeat", strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.server.heartbeat(rec, req)
	if rec.Code != 204 {
		t.Fatalf("heartbeat status = %d body=%q, want 204", rec.Code, rec.Body.String())
	}
}

// expireAllBut ages every entry except keepKeys into the past and runs the
// expiry sweep — simulating "these devices went silent".
func (f *heartbeatFixture) expireAllBut(t *testing.T, keepKeys ...string) {
	t.Helper()
	f.server.heartbeatMu.Lock()
	for key := range f.server.heartbeats {
		kept := false
		for _, k := range keepKeys {
			if key == k {
				kept = true
				break
			}
		}
		if !kept {
			f.server.heartbeats[key] = time.Now().Add(-2 * heartbeatExpiry)
		}
	}
	f.server.heartbeatMu.Unlock()
	f.server.releaseExpiredHeartbeats()
}

func TestHeartbeatKeyedPerDevice(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", f.name)
	f.postHeartbeat(t, "gc-b", f.name)

	if got := len(f.server.heartbeats); got != 2 {
		t.Fatalf("entries = %d, want one per device (2)", got)
	}

	// gc-a goes silent; gc-b keeps beating.
	f.expireAllBut(t, heartbeatKey("gc-b", f.name))

	if f.leases.Lease() == nil {
		t.Fatal("lease was released while device gc-b is still beating — devices must not share a key")
	}
	if _, alive := f.server.heartbeats[heartbeatKey("gc-b", f.name)]; !alive {
		t.Fatal("gc-b heartbeat was dropped by gc-a's expiry")
	}
}

func TestHeartbeatSilentDeviceReleasesLease(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", f.name)
	f.expireAllBut(t) // no keepers: the only device went silent

	if f.leases.Lease() != nil {
		t.Fatal("lease survived after its only device stopped beating")
	}
}

func TestHeartbeatLegacyClientKeepsStableKey(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "", f.name)

	want := "\x1f" + f.name
	if _, ok := f.server.heartbeats[want]; !ok {
		keys := make([]string, 0, len(f.server.heartbeats))
		for k := range f.server.heartbeats {
			keys = append(keys, strconv.Quote(k))
		}
		t.Fatalf("legacy heartbeat missing key %q (keys: %s)", want, strings.Join(keys, ", "))
	}
	device, session := splitHeartbeatKey(want)
	if device != "" || session != f.name {
		t.Fatalf("splitHeartbeatKey(%q) = (%q,%q), want empty device + session", want, device, session)
	}
}
