package control

import (
	"errors"
	"testing"

	"reasonix/internal/sessioninbox"
)

// fakeGuidanceInbox satisfies Inbox via embedding and records the one method
// the G3 entry drives, so the wrapper's grammar is verified against the port
// without a full controller.
type fakeGuidanceInbox struct {
	Inbox
	got InboxRequest
	rec sessioninbox.InboxReceipt
	err error
}

func (f *fakeGuidanceInbox) TryEnqueueAndSteer(req InboxRequest) (sessioninbox.InboxReceipt, error) {
	f.got = req
	return f.rec, f.err
}

func TestInjectGuidanceGrammar(t *testing.T) {
	fake := &fakeGuidanceInbox{rec: sessioninbox.InboxReceipt{ItemID: "item-1"}}
	rec, err := InjectGuidance(fake, GuidanceRequest{
		Source:              "collab",
		Priority:            GuidancePriorityUrgent,
		Text:                "  please re-run the failing check  ",
		Idempotency:         "msg-42",
		ExpectedSessionPath: "/sessions/a.jsonl",
		Extra:               map[string]string{"thread": "t1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ItemID != "item-1" {
		t.Fatalf("receipt = %+v", rec)
	}
	got := fake.got
	if got.Intent != sessioninbox.IntentSteer {
		t.Fatalf("intent = %v, want steer", got.Intent)
	}
	if got.Source != "collab" || got.Idempotency != "msg-42" || got.ExpectedSessionPath != "/sessions/a.jsonl" {
		t.Fatalf("passthrough mismatch: %+v", got)
	}
	if got.Display != "please re-run the failing check" || got.Raw != got.Display || got.Submit != got.Display {
		t.Fatalf("text fields mismatch: %+v", got)
	}
	if got.Extra[GuidancePriorityExtraKey] != string(GuidancePriorityUrgent) {
		t.Fatalf("priority not recorded: %v", got.Extra)
	}
	if got.Extra["thread"] != "t1" {
		t.Fatalf("caller extra lost: %v", got.Extra)
	}
}

func TestInjectGuidanceDefaults(t *testing.T) {
	fake := &fakeGuidanceInbox{}
	if _, err := InjectGuidance(fake, GuidanceRequest{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if fake.got.Source != "guidance" {
		t.Fatalf("source default = %q", fake.got.Source)
	}
	if fake.got.Extra[GuidancePriorityExtraKey] != string(GuidancePriorityNormal) {
		t.Fatalf("priority default = %v", fake.got.Extra)
	}
	// nil inbox / empty text are rejected before any enqueue.
	if _, err := InjectGuidance(nil, GuidanceRequest{Text: "hi"}); err == nil {
		t.Fatal("nil inbox must error")
	}
	if _, err := InjectGuidance(fake, GuidanceRequest{Text: "   "}); !errors.Is(err, sessioninbox.ErrEmpty) {
		t.Fatalf("empty text err = %v, want ErrEmpty", err)
	}
}

func TestNormalizeGuidancePriorityFailsOpen(t *testing.T) {
	if NormalizeGuidancePriority(GuidancePriorityLow) != GuidancePriorityLow {
		t.Fatal("low must survive")
	}
	if NormalizeGuidancePriority(GuidancePriorityUrgent) != GuidancePriorityUrgent {
		t.Fatal("urgent must survive")
	}
	if NormalizeGuidancePriority("asap") != GuidancePriorityNormal {
		t.Fatal("unknown priority must degrade to normal, never drop the guidance")
	}
	if NormalizeGuidancePriority("") != GuidancePriorityNormal {
		t.Fatal("empty priority must degrade to normal")
	}
}
