package main

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// TestSessionCollabRefusedHopTextReportsConfiguredLimit: the refusal a sender sees must
// name the ceiling actually in force (task 204), not the built-in default.
func TestSessionCollabRefusedHopTextReportsConfiguredLimit(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	if err := cfg.SetSessionCollabHopLimit(7); err != nil {
		t.Fatal(err)
	}
	cfg.Agent.ExperimentalSessionCollab = true
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	text := sessionCollabRefusedHopText(sessioncollab.MailMessage{ID: "msg_test"})
	if !strings.Contains(text, "7") {
		t.Fatalf("refusal must report the configured ceiling: %q", text)
	}
	if !strings.Contains(text, "msg_test") {
		t.Fatalf("refusal must keep the message id: %q", text)
	}
}

// Task 213: the pump's own derivation path must land in the hop refusal, not
// the provenance catch-all. A reply whose parent already sits at the ceiling
// derives past it and is refused as an exhausted chain — told to start a new
// chain, not sent hunting for a threadId bug that does not exist.
func TestPumpDerivedHopExhaustionIsAHopRefusal(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	if err := cfg.SetSessionCollabHopLimit(1); err != nil {
		t.Fatal(err)
	}
	cfg.Agent.ExperimentalSessionCollab = true
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	// The pump's verifyHop resolves the mail root from the live config, so the
	// fixture mailbox must sit on that exact path, not in a scratch TempDir.
	target := "sc_target"
	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	pump := &sessionCollabPump{}

	// Leg 1: the first message lands at hop 0.
	start, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_from", To: target, Body: "chain start"})
	if err != nil {
		t.Fatal(err)
	}
	deliver := func(contactID string) (string, string, int, int) {
		var kind, text string
		d := collabDelivery{
			enqueue:   func(msg sessioncollab.MailMessage, body string) (bool, error) { return false, nil },
			notify:    func(msg sessioncollab.MailMessage, k, note string) { kind, text = k, note },
			deriveHop: pump.verifyHop,
			render:    sessionCollabDeliveryText,
		}
		delivered, refused, err := runCollabDelivery(mail, contactID, d)
		if err != nil {
			t.Fatal(err)
		}
		return kind, text, delivered, refused
	}
	kind, text, delivered, refused := deliver(target)
	if delivered != 1 || refused != 0 || kind != "" {
		t.Fatalf("leg 1 must deliver quietly: %d/%d %q %q", delivered, refused, kind, text)
	}

	// Leg 2: the sender replies on that thread — derived = 0+1 = 1, exactly
	// at the ceiling, so the reply itself still delivers. The sender fills the
	// derived hop into the record, the way the tool side does before Deliver.
	reply, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: target, To: "sc_from", Body: "at the ceiling", ThreadID: start.ID, Hop: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	kind, text, delivered, refused = deliver("sc_from")
	if delivered != 1 || refused != 0 || kind != "" {
		t.Fatalf("leg 2 must deliver at the ceiling: %d/%d %q %q", delivered, refused, kind, text)
	}

	// Leg 3: relaying onward derives 1+1 = 2, still inside the ceiling
	// (MinHop clamps the configured 1 up to 3), so it delivers too.
	relay, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_from", To: target, Body: "still inside", ThreadID: reply.ID, Hop: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	kind, text, delivered, refused = deliver(target)
	if delivered != 1 || refused != 0 || kind != "" {
		t.Fatalf("leg 3 must deliver inside the ceiling: %d/%d %q %q", delivered, refused, kind, text)
	}

	// Leg 4: relaying once more derives 2+1 = 3... the ceiling itself, so one
	// further relay is where the chain exhausts: 3+1 = 4 > 3. The refusal must
	// be the hop text with the configured ceiling — never the provenance text.
	deep, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: target, To: "sc_from", Body: "at the ceiling", ThreadID: relay.ID, Hop: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	kind, text, delivered, refused = deliver("sc_from")
	if delivered != 1 || refused != 0 || kind != "" {
		t.Fatalf("leg 4 must still deliver at the ceiling: %d/%d %q %q", delivered, refused, kind, text)
	}

	deep2, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_from", To: target, Body: "one too deep", ThreadID: deep.ID, Hop: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	kind, text, delivered, refused = deliver(target)
	if delivered != 0 || refused != 1 {
		t.Fatalf("leg 5 must refuse: %d/%d", delivered, refused)
	}
	if kind != "refused_hop" {
		t.Fatalf("a derived hop exhaustion is a hop refusal, got %q: %s", kind, text)
	}
	if !strings.Contains(text, "已达 hop 上限（3）") {
		t.Fatalf("refusal must report the configured ceiling: %s", text)
	}
	if strings.Contains(text, "无法核实") {
		t.Fatalf("refusal must not dress a hop exhaustion as provenance: %s", text)
	}
	if !strings.Contains(text, deep2.ID) {
		t.Fatalf("refusal must keep the message id: %s", text)
	}
}
