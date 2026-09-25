package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Task 244 B2 (experimental_loop_streak_note). The repeat guard's detect side
// is task 110's; these tests pin the two states of the second-strike decision:
// off = the historical nudge-then-pause contract, on = bounded neutral
// "Continue." notes before the pause, with no repetition wording written back
// into the context.

const b2RepeatPayload = "same sentence over and over again and again "

func b2Run(t *testing.T, opts Options, turns int) (*Agent, *testutil.MockProvider, *recordSink, error) {
	t.Helper()
	script := make([]testutil.Turn, 0, turns)
	for i := 0; i < turns; i++ {
		script = append(script, testutil.Turn{Text: strings.Repeat(b2RepeatPayload, 40)})
	}
	mp := testutil.NewMock("m", script...)
	sink := &recordSink{}
	a := New(mp, echoRegistry(), NewSession(""), opts, sink)
	err := a.Run(withNoClosedLoop(context.Background()), "loop")
	return a, mp, sink, err
}

func isTextRepeatPause(err error) bool {
	var rp *RecoveryPauseError
	return errors.As(err, &rp) && rp.StopReason == "text_repeat"
}

func b2ContinueNotes(a *Agent) int {
	n := 0
	for _, m := range a.Session().Messages {
		if m.Role == provider.RoleUser && m.Content == "Continue." {
			n++
		}
	}
	return n
}

// Off (default): first strike nudges, second strike pauses — the historical
// contract must not move when the experiment is off.
func TestLoopStreakNoteOffKeepsNudgeThenPause(t *testing.T) {
	a, mp, _, err := b2Run(t, Options{}, 4)
	if !isTextRepeatPause(err) {
		t.Fatalf("off state must pause on the second strike, got: %v", err)
	}
	if mp.CallCount() != 2 {
		t.Fatalf("calls=%d, want exactly 2 (nudge then pause)", mp.CallCount())
	}
	if n := b2ContinueNotes(a); n != 0 {
		t.Fatalf("off state must inject no neutral notes, got %d", n)
	}
}

// On: strikes two and three become bounded neutral notes; the fourth strike
// pauses (maxLoopStreakNotes=2), and the notes carry no repetition wording.
func TestLoopStreakNoteOnInjectsBoundedNeutralNotes(t *testing.T) {
	a, mp, sink, err := b2Run(t, Options{LoopStreakNote: true}, 6)
	if !isTextRepeatPause(err) {
		t.Fatalf("on state must still pause after the bounded notes, got: %v", err)
	}
	if want := maxLoopStreakNotes + 2; mp.CallCount() != want {
		t.Fatalf("calls=%d, want %d (1 nudge + %d notes + 1 pause)", mp.CallCount(), want, maxLoopStreakNotes)
	}
	if n := b2ContinueNotes(a); n != maxLoopStreakNotes {
		t.Fatalf("neutral notes=%d, want %d", n, maxLoopStreakNotes)
	}
	notices := 0
	for _, e := range sink.kinds(event.Notice) {
		if strings.Contains(e.Text, "Loop-streak note") {
			notices++
			low := strings.ToLower(e.Text)
			if strings.Contains(low, "repeated the same") || strings.Contains(low, "looping") {
				t.Fatalf("loop-streak notice must stay neutral: %q", e.Text)
			}
		}
	}
	if notices != maxLoopStreakNotes {
		t.Fatalf("loop-streak notices=%d, want %d", notices, maxLoopStreakNotes)
	}
}
