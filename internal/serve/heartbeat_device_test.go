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

// Task 36 Phase 2: the heartbeat is a device lease — deviceID-grouped
// entries with a session set, per-device expiry, explicit release, and the
// desktop write-authority hook. These pin the batch-2 semantics on top of
// batch 1's per-device keying.

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

func (f *heartbeatFixture) ageDevice(deviceID string) {
	f.server.heartbeatMu.Lock()
	if entry := f.server.heartbeats[deviceID]; entry != nil {
		entry.lastBeat = time.Now().Add(-2 * heartbeatExpiry)
	}
	f.server.heartbeatMu.Unlock()
	f.server.releaseExpiredHeartbeats()
}

func TestDeviceLeaseGroupedAndIndependent(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", f.name)
	f.postHeartbeat(t, "gc-b", f.name)

	f.server.heartbeatMu.Lock()
	entries := len(f.server.heartbeats)
	sessionsA := len(f.server.heartbeats["gc-a"].sessions)
	f.server.heartbeatMu.Unlock()
	if entries != 2 || sessionsA != 1 {
		t.Fatalf("entries=%d sessions[gc-a]=%d, want 2 devices / 1 session", entries, sessionsA)
	}

	// gc-a (which holds the current session) goes silent: its lease entry
	// expires, the held lease frees, and the hook is notified. gc-b's entry
	// must remain (devices expire independently).
	f.ageDevice("gc-a")
	f.server.heartbeatMu.Lock()
	_, aAlive := f.server.heartbeats["gc-a"]
	_, bAlive := f.server.heartbeats["gc-b"]
	f.server.heartbeatMu.Unlock()
	if aAlive || !bAlive {
		t.Fatalf("after gc-a expiry: aAlive=%v bAlive=%v, want false/true", aAlive, bAlive)
	}
	if f.leases.Lease() != nil {
		t.Fatal("lease survived although the holding device expired")
	}
}

func TestDeviceLeaseExpiryLeavesUnrelatedSessionLease(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", "some-other-session") // not the current one
	f.ageDevice("gc-a")
	if f.leases.Lease() == nil {
		t.Fatal("expiry of a device that never held the current session must not free this lease")
	}
}

func TestExplicitReleaseEndpointFreesImmediately(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", f.name)

	var notified []string
	prev := remoteWriteAuthorityHook
	remoteWriteAuthorityHook = func(deviceID string, held bool) {
		notified = append(notified, deviceID+":"+strconv.FormatBool(held))
	}
	t.Cleanup(func() { remoteWriteAuthorityHook = prev })

	req := httptest.NewRequest("POST", "/release-device", strings.NewReader(`{"device_id":"gc-a"}`))
	rec := httptest.NewRecorder()
	f.server.releaseDevice(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if f.leases.Lease() != nil {
		t.Fatal("explicit device release must free the held lease immediately")
	}
	if len(notified) != 1 || notified[0] != "gc-a:false" {
		t.Fatalf("hook notifications = %v, want [gc-a:false]", notified)
	}
}

func TestRemoteDeviceHoldsWriteBlocksSubmit(t *testing.T) {
	f := newHeartbeatFixture(t)
	f.postHeartbeat(t, "gc-a", f.name)
	if id, held := f.server.remoteDeviceHoldsWrite(); !held || id != "gc-a" {
		t.Fatalf("remoteDeviceHoldsWrite = (%q,%v), want (gc-a,true)", id, held)
	}
	// The submit handler must refuse before parsing anything.
	req := httptest.NewRequest("POST", "/submit", strings.NewReader(`{"input":"hello"}`))
	rec := httptest.NewRecorder()
	f.server.submit(rec, req)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "device-level exclusion") {
		t.Fatalf("submit = %d %q, want 409 device-level exclusion", rec.Code, rec.Body.String())
	}
	// After release the same submit is no longer blocked by device exclusion
	// (it may still fail later checks — we only assert the exclusion gate).
	f.server.releaseDeviceLease("gc-a")
	if _, held := f.server.remoteDeviceHoldsWrite(); held {
		t.Fatal("lease must be gone after release")
	}
}

// tl1532: device identity exemption — the live lease holder's own submit
// carries its device identity and must not be refused by its own heartbeat,
// while every other (or absent) identity keeps the task 36 Phase 2 409 and
// the exemption dies with the heartbeat that justified it.

func TestSubmitDeviceIdentityExemption(t *testing.T) {
	bc := NewBroadcaster()
	// serve_lock_test's blockingRunner keeps the admitted turn Running until
	// cancelled, so every later submit deterministically reaches the
	// device-exclusion guard (the busy-session 409 lives behind it and must
	// never be the asserted code).
	ctrl := control.New(control.Options{Runner: blockingRunner{}, Sink: bc})
	s := New(ctrl, bc, config.ServeConfig{})
	t.Cleanup(func() { ctrl.Cancel(); ctrl.Close() })

	post := func(body, deviceID string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/submit", strings.NewReader(body))
		if deviceID != "" {
			req.Header.Set(deviceIDHeader, deviceID)
		}
		rec := httptest.NewRecorder()
		s.submit(rec, req)
		return rec
	}
	beat := func(t *testing.T, deviceID string) {
		t.Helper()
		req := httptest.NewRequest("POST", "/heartbeat", strings.NewReader(`{"name":"held","device_id":`+strconv.Quote(deviceID)+`}`))
		rec := httptest.NewRecorder()
		s.heartbeat(rec, req)
		if rec.Code != 204 {
			t.Fatalf("heartbeat status = %d, want 204", rec.Code)
		}
	}
	beat(t, "gc-a")

	// ① The holder's own submit (same identity as its live heartbeat) is
	// exempt: 202, the turn is admitted (held open by blockingRunner).
	if rec := post(`{"input":"hello"}`, "gc-a"); rec.Code != 202 {
		t.Fatalf("holder's own submit = %d %q, want 202 (self exemption)", rec.Code, rec.Body.String())
	}
	// ② A foreign device identity (never beat) keeps the task 36 Phase 2
	// exclusion.
	if rec := post(`{"input":"x"}`, "gc-zz"); rec.Code != 409 || !strings.Contains(rec.Body.String(), "device-level exclusion") {
		t.Fatalf("foreign-device submit = %d %q, want 409 device-level exclusion", rec.Code, rec.Body.String())
	}
	// ③ No identity header (legacy client) keeps the exclusion.
	if rec := post(`{"input":"x"}`, ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "device-level exclusion") {
		t.Fatalf("legacy no-header submit = %d %q, want 409 device-level exclusion", rec.Code, rec.Body.String())
	}
	// ④ The exemption dies with the heartbeat: gc-b joins and holds, then
	// gc-a ages out — gc-a's identity no longer exempts its submits.
	beat(t, "gc-b")
	s.heartbeatMu.Lock()
	if entry := s.heartbeats["gc-a"]; entry != nil {
		entry.lastBeat = time.Now().Add(-2 * heartbeatExpiry)
	}
	s.heartbeatMu.Unlock()
	s.releaseExpiredHeartbeats()
	if rec := post(`{"input":"x"}`, "gc-a"); rec.Code != 409 || !strings.Contains(rec.Body.String(), "device-level exclusion") {
		t.Fatalf("expired-holder submit = %d %q, want 409 device-level exclusion", rec.Code, rec.Body.String())
	}
}
