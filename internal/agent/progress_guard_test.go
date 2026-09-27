package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	_ "reasonix/internal/tool/builtin"
)

// runRound executes one read-only batch and returns its result texts.
func runRound(t *testing.T, a *Agent, path string) []string {
	t.Helper()
	batch := a.executeBatch(context.Background(), &a.turn, []provider.ToolCall{
		{ID: "c", Name: "read_probe", Arguments: `{"path":"` + path + `"}`},
	})
	return batch.results
}

// TestReadOnlyLongZeroGainRoundsNeverStopTheTurn is the task 329 regression:
// the two upstream no-progress guards (the zero-gain progress ladder and the
// storm breaker, retired in #10223 / upstream #9766) used to inject guidance
// at rounds 2/4/6 and stop the turn at the stop tier. A run of more than 21
// consecutive same-read rounds — the upstream reproduction window — must now
// pass through untouched: no guard text, no loop-guard arm, results flowing
// every round.
func TestReadOnlyLongZeroGainRoundsNeverStopTheTurn(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_probe", readOnly: true})
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)
	a.resetTurnEvidence()

	const rounds = 25
	for i := 1; i <= rounds; i++ {
		got := runRound(t, a, "same.go")
		if len(got) == 0 || got[0] == "" {
			t.Fatalf("round %d produced no result — the turn would stall here", i)
		}
		if strings.Contains(got[0], "[progress guard]") {
			t.Fatalf("round %d injected retired progress-guard guidance: %q", i, got[0])
		}
		if strings.Contains(got[0], "[loop guard]") {
			t.Fatalf("round %d injected retired storm-breaker guidance: %q", i, got[0])
		}
	}
	if a.turn.loopGuardArmed {
		t.Fatal("read-only zero-gain rounds must not arm the final-readiness pass")
	}
	if a.turn.blockedTurnStreak != 0 {
		t.Fatalf("blocked streak = %d, want 0 for successful read-only rounds", a.turn.blockedTurnStreak)
	}
}

type outcomeSampleSink struct {
	samples []evidence.OutcomeSample
}

func (s *outcomeSampleSink) Emit(event.Event) {}
func (s *outcomeSampleSink) RecordOutcomeProgress(sample evidence.OutcomeSample) {
	s.samples = append(s.samples, sample)
}

func TestOutcomeShadowRecordsEveryRoundWithoutTouchingGuardText(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_probe", readOnly: true})
	sink := &outcomeSampleSink{}
	a := New(nil, reg, NewSession(""), Options{}, sink)
	a.resetTurnEvidence()

	first := runRound(t, a, "a.go")
	second := runRound(t, a, "a.go")
	if len(sink.samples) != 2 {
		t.Fatalf("got %d shadow samples, want one per round", len(sink.samples))
	}
	if s := sink.samples[0]; s.Round != 1 || s.Exploration != 1 || s.Objective != 0 {
		t.Fatalf("round 1 sample = %+v, want exploration 1 objective 0", s)
	} else if s.Runway != 23 || s.RunwayDry != 0 || s.RunwayIdle != 1 || s.RunwaySpent {
		t.Fatalf("round 1 runway = %+v, want balance 23, idle 1", s)
	}
	if s := sink.samples[1]; s.Round != 2 || s.Exploration != 0 || s.LegacyGain != 0 {
		t.Fatalf("round 2 repeat sample = %+v, want all-zero with legacy gain 0", s)
	} else if s.Runway != 19 || s.RunwayDry != 1 || s.RunwayIdle != 2 || s.RunwaySpent {
		t.Fatalf("round 2 runway = %+v, want balance 19, dry 1, idle 2", s)
	}
	// The shadow observes; retired guards never write into round texts.
	if strings.Contains(first[0], "[progress guard]") || strings.Contains(second[0], "[progress guard]") {
		t.Fatalf("shadow must not resurrect guard text: %q / %q", first[0], second[0])
	}
}
