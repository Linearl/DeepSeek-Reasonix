package control

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"

	"reasonix/internal/agent"
)

// 任务178（打开后才计时）tests: NotifyAskReattached — the session-reopen signal
// the desktop attach-replay hook delivers after the panel frames are out —
// re-arms a fresh auto-decide window, so a tab closed into the background
// never spends an answer window the user never saw. Attended asks (no timer)
// consume the signal as a no-op; the 806/844/849 acceptance files keep their
// own scenarios unchanged.

// Reopening mid-window must defeat the clock the backgrounded tab spent: the
// original expiry does not decide, and the fresh window decides afterwards
// with the unchanged 806 decision trail (marker + notice + decide-for-you
// answer shape).
func TestNotifyAskReattachedRearmsReversibleWindow(t *testing.T) {
	const askWait = 600 * time.Millisecond
	c, sink := askActivityController(t, askWait)
	done := make(chan struct{})
	var answers []event.AskAnswer
	var err error
	go func() {
		answers, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)
	start := time.Now()

	// Reopen mid-window: the backgrounded tab has spent half of it.
	time.Sleep(askWait / 2)
	c.NotifyAskReattached(askID)

	// Past the original expiry (~600ms from the panel) but still inside the
	// fresh window (re-presented at ~300ms → fresh deadline ~900ms): if the
	// reopen did not re-arm, the ask self-answers here and the select trips.
	select {
	case <-done:
		t.Fatalf("ask decided at %v although the reopen re-armed the window (%v); the backgrounded clock kept running", time.Since(start).Round(time.Millisecond), askWait)
	case <-time.After(400 * time.Millisecond): // ~700ms: 100ms past original, ~200ms before fresh
	}

	select {
	case <-done:
		elapsed := time.Since(start)
		if elapsed < askWait+askWait/4 {
			t.Fatalf("ask decided at %v, before the fresh window re-armed at ~%v could expire; the re-arm ran early", elapsed.Round(time.Millisecond), askWait/2)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ask never returned; the re-armed window never expired")
	}
	if err != nil {
		t.Fatalf("ask err = %v, want the auto-decide answers with no error", err)
	}
	if len(answers) != 1 || answers[0].QuestionID != "q1" || answers[0].Selected[0] != autopilotNoHumanAnswer {
		t.Fatalf("answers = %+v, want the decide-for-yourself answer for q1", answers)
	}
	if c.consumeTurnAskAnswered() == "" {
		t.Fatal("turn-ask-answered marker empty; the re-armed decide must keep the 806 decision trail")
	}
	notices := sink.notices()
	if len(notices) != 1 || notices[0].Code != "autopilot_ask_auto_decide" {
		t.Fatalf("notices = %+v, want exactly the autopilot_ask_auto_decide anchor", notices)
	}
}

// The signal is inert on an attended ask (no timer to re-arm): the ask keeps
// waiting for the user's own answer, exactly as before the signal existed.
func TestAttendedAskIgnoresReattachSignal(t *testing.T) {
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	sink := &askTimeoutSink{}
	c := New(Options{
		Runner:              ag,
		Executor:            ag,
		Sink:                sink,
		AutopilotMaxRuntime: time.Minute,
	})
	done := make(chan struct{})
	var err error
	go func() {
		_, err = c.Ask(context.Background(), askActivityQuestion())
		close(done)
	}()
	askID := waitPanel(t, sink)
	for i := 0; i < 20; i++ {
		c.NotifyAskReattached(askID)
	}
	select {
	case <-done:
		t.Fatal("attended ask returned from the reopen signal alone; there is no timer to re-arm and no answer to invent")
	case <-time.After(150 * time.Millisecond):
	}
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}})
	select {
	case <-done:
		if err != nil {
			t.Fatalf("ask err = %v, want a clean user answer", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return after the user answered")
	}
}

// Unknown/expired ids are ignored (same contract as the notify* family), and
// the one-shot buffered slot collapses repeated attaches without blocking.
func TestNotifyAskReattachedIgnoresUnknownID(t *testing.T) {
	c := New(Options{Label: "reattach-noop"})
	defer c.Close()
	c.NotifyAskReattached("")   // empty id
	c.NotifyAskReattached("99") // never registered
}
