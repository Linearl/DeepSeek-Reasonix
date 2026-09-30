package serve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/zcodebridge"
)

// fakeInjectBridge is the mock at the bridge seam: production passes a live
// *zcodebridge.Bridge, tests record every SendText against canned sessions.
type fakeInjectBridge struct {
	sessions []zcodebridge.Session
	listErr  error
	sendErr  error
	sendAck  zcodebridge.Ack

	sent []fakeSend
}

type fakeSend struct {
	sessionID string
	text      string
	delivery  zcodebridge.Delivery
}

func (f *fakeInjectBridge) List(ctx context.Context) ([]zcodebridge.Session, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.sessions, nil
}

func (f *fakeInjectBridge) SendText(ctx context.Context, sessionID, text string, d zcodebridge.Delivery) (zcodebridge.Ack, error) {
	f.sent = append(f.sent, fakeSend{sessionID: sessionID, text: text, delivery: d})
	if f.sendErr != nil {
		return zcodebridge.Ack{}, f.sendErr
	}
	if f.sendAck.CommandID == "" {
		f.sendAck = zcodebridge.Ack{CommandID: "cmd-test", Status: "accepted"}
	}
	return f.sendAck, nil
}

func testMail(id, delivery string) sessioncollab.MailMessage {
	body := "please run the bus acceptance task\nsecond line\n" + strings.Repeat("filler ", 60) + "TAIL_MARKER_BEYOND_EXCERPT"
	return sessioncollab.MailMessage{
		ID: id, From: "sc_sender", To: "zcode-dev",
		Body: body, Delivery: delivery,
	}
}

// TestInjectBusMailNudgesMostRecentSession: a delivered mail becomes exactly
// one queued sendText into the live session, carrying a pointer to the inbox
// — never the full body twice.
func TestInjectBusMailNudgesMostRecentSession(t *testing.T) {
	fb := &fakeInjectBridge{sessions: []zcodebridge.Session{
		{SessionID: "old", WorkspacePath: `C:\ws`, UpdatedAtMs: 100},
		{SessionID: "live", WorkspacePath: `C:\ws`, UpdatedAtMs: 200},
	}}
	injectBusMailVia(fb, `C:\ws`, "zcode-dev", testMail("msg_1", string(sessioncollab.DeliverySteer)))
	if len(fb.sent) != 1 {
		t.Fatalf("want exactly one nudge, got %+v", fb.sent)
	}
	send := fb.sent[0]
	if send.sessionID != "live" {
		t.Fatalf("nudge must target the most recent session, got %+v", send)
	}
	if send.delivery != zcodebridge.DeliveryQueue {
		t.Fatalf("mail nudge must be queue semantics, got %q", send.delivery)
	}
	for _, want := range []string{"zcode-dev", "msg_1", "collab_inbox_read", "sc_sender"} {
		if !strings.Contains(send.text, want) {
			t.Fatalf("nudge text missing %q: %q", want, send.text)
		}
	}
	if strings.Contains(send.text, "TAIL_MARKER_BEYOND_EXCERPT") {
		t.Fatalf("nudge must clip the body to an excerpt: %q", send.text)
	}
}

// TestInjectBusMailPrefersBridgeWorkspace: when the bridge child has a
// workspace, its sessions win over a more recently updated foreign one.
func TestInjectBusMailPrefersBridgeWorkspace(t *testing.T) {
	fb := &fakeInjectBridge{sessions: []zcodebridge.Session{
		{SessionID: "foreign-newest", WorkspacePath: `D:\other`, UpdatedAtMs: 500},
		{SessionID: "ours-older", WorkspacePath: `C:\ws`, UpdatedAtMs: 100},
	}}
	injectBusMailVia(fb, `C:\ws`, "zcode-dev", testMail("msg_2", ""))
	if len(fb.sent) != 1 || fb.sent[0].sessionID != "ours-older" {
		t.Fatalf("want the bridge-workspace session, got %+v", fb.sent)
	}
}

// TestInjectBusMailWorkspaceCaseFold: Windows paths are case-insensitive; the
// CLI may report the workspace in different casing than the child cwd.
func TestInjectBusMailWorkspaceCaseFold(t *testing.T) {
	fb := &fakeInjectBridge{sessions: []zcodebridge.Session{
		{SessionID: "ws-session", WorkspacePath: `c:\WS`, UpdatedAtMs: 1},
	}}
	injectBusMailVia(fb, `C:\ws`, "zcode-dev", testMail("msg_3", ""))
	if len(fb.sent) != 1 || fb.sent[0].sessionID != "ws-session" {
		t.Fatalf("case-folded workspace match failed, got %+v", fb.sent)
	}
}

// TestInjectBusMailWithoutWorkspaceFallsBackToMostRecent: no workspace
// knowledge (inherit failed, empty list fields) degrades to recency, not to
// silence.
func TestInjectBusMailWithoutWorkspaceFallsBackToMostRecent(t *testing.T) {
	fb := &fakeInjectBridge{sessions: []zcodebridge.Session{
		{SessionID: "a", UpdatedAtMs: 10},
		{SessionID: "b", UpdatedAtMs: 20},
	}}
	injectBusMailVia(fb, "", "zcode-dev", testMail("msg_4", ""))
	if len(fb.sent) != 1 || fb.sent[0].sessionID != "b" {
		t.Fatalf("want most recent session b, got %+v", fb.sent)
	}
}

// Failure semantics (bus dev item #3): every failure downgrades to the
// polling fallback — no send, no retry storm, mail stays in the inbox.
func TestInjectBusMailFailuresKeepMailInInbox(t *testing.T) {
	t.Run("list_failed", func(t *testing.T) {
		fb := &fakeInjectBridge{listErr: errors.New("bridge dead")}
		injectBusMailVia(fb, `C:\ws`, "zcode-dev", testMail("msg_5", ""))
		if len(fb.sent) != 0 {
			t.Fatalf("dead bridge must not nudge, got %+v", fb.sent)
		}
	})
	t.Run("no_live_session", func(t *testing.T) {
		fb := &fakeInjectBridge{}
		injectBusMailVia(fb, `C:\ws`, "zcode-dev", testMail("msg_6", ""))
		if len(fb.sent) != 0 {
			t.Fatalf("no live session must not nudge, got %+v", fb.sent)
		}
	})
	t.Run("ack_refused_no_retry", func(t *testing.T) {
		fb := &fakeInjectBridge{sessions: []zcodebridge.Session{{SessionID: "live", UpdatedAtMs: 1}},
			sendErr: &zcodebridge.AckError{Ack: zcodebridge.Ack{
				CommandID: "cmd-x", Status: "rejected", ReasonCode: "sessionInactive",
			}}}
		injectBusMailVia(fb, `C:\ws`, "zcode-dev", testMail("msg_7", ""))
		if len(fb.sent) != 1 {
			t.Fatalf("refused nudge must not retry, attempts %+v", fb.sent)
		}
	})
	t.Run("transport_error_no_retry", func(t *testing.T) {
		fb := &fakeInjectBridge{sessions: []zcodebridge.Session{{SessionID: "live", UpdatedAtMs: 1}},
			sendErr: zcodebridge.ErrClosed}
		injectBusMailVia(fb, `C:\ws`, "zcode-dev", testMail("msg_8", ""))
		if len(fb.sent) != 1 {
			t.Fatalf("closed bridge must not retry, attempts %+v", fb.sent)
		}
	})
}

// TestInjectDeliveryForMapping: mail is queue semantics; only an explicit
// mark upgrades to startNow (no such mark exists in the mail vocabulary yet —
// Deliver rejects unknown delivery values — so today everything is queue).
func TestInjectDeliveryForMapping(t *testing.T) {
	cases := map[string]zcodebridge.Delivery{
		"":                                     zcodebridge.DeliveryQueue,
		string(sessioncollab.DeliverySteer):    zcodebridge.DeliveryQueue,
		string(sessioncollab.DeliveryFollowup): zcodebridge.DeliveryQueue,
		mailStartNowMark:                       zcodebridge.DeliveryStartNow,
	}
	for in, want := range cases {
		if got := injectDeliveryFor(testMail("m", in)); got != want {
			t.Fatalf("delivery %q: want %s, got %s", in, want, got)
		}
	}
}

// TestZcodeMailInjectorGateClosedZeroPath: the env gate is the bridge itself.
// With no connected child (gate off, or the window between child death and
// rebuild) the injector returns before any goroutine or IO — mail stays in
// the inbox for the role's own polling.
func TestZcodeMailInjectorGateClosedZeroPath(t *testing.T) {
	s := &Server{}
	if s.ZcodeBridge() != nil {
		t.Fatal("fresh server must have no bridge")
	}
	zcodeMailInjector{s: s}.InjectMail("zcode-dev", testMail("msg_9", ""))
	// Nothing to observe but the absence of a panic: the zero path is the
	// early return. Workspace state must stay untouched too.
	if p := s.zcodeBridgeWorkspace.Load(); p != nil {
		t.Fatalf("gate-closed injector must not write workspace state, got %q", *p)
	}
}

// TestServeNewWiresBusInjector pins the serve-side wiring: an enabled bus_mcp
// table constructs the bus with the mail injector attached, so deliveries
// inside a real server reach the bridge path.
func TestServeNewWiresBusInjector(t *testing.T) {
	s := New(control.New(control.Options{}), NewBroadcaster(), config.ServeConfig{
		BusMCP: config.BusMCPConfig{
			Enabled: true,
			Roles:   map[string]string{"dev": "tok-wiring-0000000000000000"},
			MailDir: t.TempDir(),
		},
	})
	if s.bus == nil {
		t.Fatal("enabled bus_mcp must mount the bus server")
	}
	if s.bus.Injector() == nil {
		t.Fatal("bus server must carry the mail injector")
	}
	// And the injector is the bridge-backed one: on a server without a live
	// bridge it must take the zero path (no panic, no state) — the same
	// contract as TestZcodeMailInjectorGateClosedZeroPath, now via the wired
	// instance.
	s.bus.Injector().InjectMail("zcode-dev", testMail("msg_10", ""))
}
