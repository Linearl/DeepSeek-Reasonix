package control

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/event"
)

// Task 991 acceptance ②④: the background-tab toast must not count a hardcoded
// copy of the auto-decide window. The AskRequest wire event carries the same
// askWaitFor() value that arms the backend timer, stamped only when a timer is
// actually armed — attended asks carry 0 so the toast shows without a deadline.
// The hover factor pin guards the 45s→150s foreground ceiling change.

// TestAskAutoDecideHoverFactorIsTen pins the task-991 foreground knob: the
// hover-reset budget covers 10 ask windows (150s at the default 15s wait),
// raised from 3 (45s) because a present-but-slow reader kept hitting the old
// ceiling and watching the run decide mid-read.
func TestAskAutoDecideHoverFactorIsTen(t *testing.T) {
	if askAutoDecideHoverFactor != 10 {
		t.Fatalf("askAutoDecideHoverFactor = %d, want 10 (task 991: 45s → 150s at the default wait)", askAutoDecideHoverFactor)
	}
	if got := time.Duration(askAutoDecideHoverFactor) * DefaultAutopilotAskTimeoutWait; got != 150*time.Second {
		t.Fatalf("hover budget at the default wait = %v, want 150s", got)
	}
}

// lastAskAutoDecideWaitMs reads the stamped auto-decide window off the most
// recent AskRequest, or -1 when no panel was emitted.
func lastAskAutoDecideWaitMs(s *askTimeoutSink) int64 {
	found := int64(-1)
	for _, e := range s.all() {
		if e.Kind == event.AskRequest {
			found = e.Ask.AutoDecideWaitMs
		}
	}
	return found
}

// 验收② (same-source): a reversible ask under autopilot carries the armed
// window on the wire — the exact value the timer block below uses, not a
// frontend copy. This is what lets the background toast expire with the
// decision.
func TestReversibleAskCarriesAutoDecideWaitOnWire(t *testing.T) {
	const askWait = 77 * time.Millisecond
	c, sink := askActivityController(t, askWait)
	go func() {
		_, _ = c.Ask(context.Background(), askActivityQuestion())
	}()
	askID := waitPanel(t, sink)

	got := lastAskAutoDecideWaitMs(sink)
	if got != askWait.Milliseconds() {
		t.Fatalf("AskRequest AutoDecideWaitMs = %d, want %d (the same value that arms the backend timer)", got, askWait.Milliseconds())
	}
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}})
}

// 验收② (attended = no deadline): an ask with no auto-decide timer carries 0,
// so a background toast for it reminds without ever expiring into a decision
// the backend would never make.
func TestAttendedAskCarriesNoAutoDecideWaitOnWire(t *testing.T) {
	c, sink := askActivityController(t, 50*time.Millisecond)
	c.SetExplicitAskGranted(true) // 806b: the grant revokes the timer on a reversible ask
	go func() {
		_, _ = c.Ask(context.Background(), askActivityQuestion())
	}()
	askID := waitPanel(t, sink)

	if got := lastAskAutoDecideWaitMs(sink); got != 0 {
		t.Fatalf("AskRequest AutoDecideWaitMs = %d, want 0 (explicit grant → attended wait, no timer to mirror)", got)
	}
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}})
}
