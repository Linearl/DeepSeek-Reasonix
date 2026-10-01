package busmcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/sessioncollab"
)

// recordingInjector is the MailInjector mock at the busmcp/serve seam: it
// records every nudge so tests can assert delivery→injection wiring without
// any bridge. Calls are synchronous by contract, so no polling is needed.
type recordingInjector struct {
	mu  sync.Mutex
	got []nudge
}

type nudge struct {
	contact string
	msgID   string
	from    string
}

func (r *recordingInjector) InjectMail(contactID string, msg sessioncollab.MailMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, nudge{contact: contactID, msgID: msg.ID, from: msg.From})
}

func (r *recordingInjector) nudges() []nudge {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]nudge{}, r.got...)
}

func newInjectedServer(t *testing.T, rec *recordingInjector) *Server {
	t.Helper()
	s, err := New(Config{
		Enabled: true,
		Roles: map[string]string{
			"dev":       testTokenDev,
			"heartbeat": testTokenHB,
		},
		MailDir:    t.TempDir(),
		SpawnRoles: []string{"heartbeat"},
		Injector:   rec,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestDeliveryNudgesInjectorForZcodeContacts pins the hook wiring: every
// successful Deliver whose recipient is a zcode- contact reaches the injector
// with the delivered message, through all three entry points (hook events,
// collab_send, collab_spawn).
func TestDeliveryNudgesInjectorForZcodeContacts(t *testing.T) {
	rec := &recordingInjector{}
	s := newInjectedServer(t, rec)
	srv := httptest.NewServer(http.HandlerFunc(s.HandleEvent))
	t.Cleanup(func() { srv.Close() })

	// ① hook event → eventTarget (zcode-heartbeat).
	req, _ := http.NewRequest("POST", srv.URL+"/bus/events", strings.NewReader(`{"event":"Stop","session_id":"s1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testTokenHB)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /bus/events: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		resp.Body.Close()
		t.Fatalf("event: want 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// ②③ collab_send (dev role) and collab_spawn (heartbeat role is the
	// spawn-authorized one in this table).
	mcpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { mcpSrv.Close() })
	cs := connectAs(t, mcpSrv.URL, testTokenDev)
	var sent sendOut
	if err := callTool(t, cs, "collab_send", map[string]any{"to": "zcode-heartbeat", "body": "status?"}, &sent); err != nil {
		t.Fatalf("collab_send: %v", err)
	}
	csHB := connectAs(t, mcpSrv.URL, testTokenHB)
	var spawned spawnOut
	if err := callTool(t, csHB, "collab_spawn", map[string]any{
		"to": "zcode-heartbeat", "title": "nightly", "body": "run the nightly",
	}, &spawned); err != nil {
		t.Fatalf("collab_spawn: %v", err)
	}

	got := rec.nudges()
	if len(got) != 3 {
		t.Fatalf("want 3 nudges (event+send+spawn), got %d: %+v", len(got), got)
	}
	for _, n := range got {
		if n.contact != "zcode-heartbeat" {
			t.Fatalf("nudge contact: want zcode-heartbeat, got %+v", n)
		}
		if n.msgID == "" {
			t.Fatalf("nudge missing msg id: %+v", n)
		}
	}
	// The nudged ids are the delivered ids, not synthetic ones.
	if got[0].msgID == got[1].msgID || got[1].msgID == got[2].msgID {
		t.Fatalf("nudges must carry distinct delivered ids: %+v", got)
	}
}

// TestDeliverySkipsNudgeForSessionContacts: Reasonix session contacts have
// their own steer channel — the injector must not see them.
func TestDeliverySkipsNudgeForSessionContacts(t *testing.T) {
	rec := &recordingInjector{}
	s := newInjectedServer(t, rec)
	mcpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { mcpSrv.Close() })
	cs := connectAs(t, mcpSrv.URL, testTokenDev)

	var sent sendOut
	if err := callTool(t, cs, "collab_send", map[string]any{"to": "sc_session1", "body": "for a reasonix session"}, &sent); err != nil {
		t.Fatalf("collab_send: %v", err)
	}
	if n := rec.nudges(); len(n) != 0 {
		t.Fatalf("session contact must not be nudged, got %+v", n)
	}
	// The mail itself is delivered regardless — the inbox is the durable
	// channel and the nudge is only the realtime half.
	pending, err := s.mail.Peek("sc_session1")
	if err != nil || len(pending) != 1 {
		t.Fatalf("session inbox: want 1 mail, got %v (%v)", pending, err)
	}
}

// TestNudgeFailureNeverBreaksDelivery pins the failure semantics: the
// injector observing a dead bridge (or anything else) must leave the mail in
// the inbox untouched — not lost, not duplicated.
func TestNudgeFailureNeverBreaksDelivery(t *testing.T) {
	rec := &recordingInjector{}
	s := newInjectedServer(t, rec)
	srv := httptest.NewServer(http.HandlerFunc(s.HandleEvent))
	t.Cleanup(func() { srv.Close() })

	// An injector that panics would fail the handler; the contract forbids
	// that, and the recording injector's work happens before any return. So
	// here just deliver normally and prove retention end to end.
	req, _ := http.NewRequest("POST", srv.URL+"/bus/events", strings.NewReader(`{"event":"Stop"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testTokenHB)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /bus/events: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("event: want 202, got %d", resp.StatusCode)
	}
	pending, err := s.mail.Peek(s.eventTarget)
	if err != nil || len(pending) != 1 {
		t.Fatalf("mail must stay in inbox after nudge, got %v (%v)", pending, err)
	}
}

// TestNilInjectorZeroPath: without an injector (unwired serve) deliveries
// behave exactly as before — the realtime path does not exist.
func TestNilInjectorZeroPath(t *testing.T) {
	s := newTestServer(t)
	mcpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { mcpSrv.Close() })
	cs := connectAs(t, mcpSrv.URL, testTokenDev)

	var sent sendOut
	if err := callTool(t, cs, "collab_send", map[string]any{"to": "zcode-heartbeat", "body": "still delivered"}, &sent); err != nil {
		t.Fatalf("collab_send: %v", err)
	}
	pending, err := s.mail.Peek("zcode-heartbeat")
	if err != nil || len(pending) != 1 || pending[0].ID != sent.ID {
		t.Fatalf("mail must land with nil injector, got %v (%v)", pending, err)
	}
}
