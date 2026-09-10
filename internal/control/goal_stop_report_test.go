package control

import (
	"strings"
	"testing"
)

// Task 49 step 4: a terminal notice has to carry the run's spend. An unattended
// run stops when nobody is watching, so this report is the only account of what
// the run cost and how far it got.
func TestAppendGoalStopReportAddsSpendToTerminalNotices(t *testing.T) {
	g := &goalMachine{
		status:         GoalStatusTimeLimitReached,
		turnsUsed:      12,
		tokensUsed:     34000,
		workDurationMs: 8 * 60 * 60 * 1000,
	}
	got := appendGoalStopReport("goal stopped: autopilot time limit reached", g)
	for _, want := range []string{"time limit reached", "turns 12", "tokens 34000", "work 8h0m0s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stop report %q is missing %q", got, want)
		}
	}
}

func TestAppendGoalStopReportLeavesRunningGoalsAlone(t *testing.T) {
	g := &goalMachine{status: GoalStatusRunning, turnsUsed: 3, tokensUsed: 10, workDurationMs: 1000}
	const notice = "goal continues"
	if got := appendGoalStopReport(notice, g); got != notice {
		t.Fatalf("a running goal should keep its notice unchanged, got %q", got)
	}
}

func TestAppendGoalStopReportHandlesEmptyAndNil(t *testing.T) {
	if got := appendGoalStopReport("", &goalMachine{status: GoalStatusComplete}); got != "" {
		t.Fatalf("an empty notice should stay empty, got %q", got)
	}
	if got := appendGoalStopReport("keep me", nil); got != "keep me" {
		t.Fatalf("a nil machine should be a no-op, got %q", got)
	}
}

func TestAppendGoalStopReportShortRunReadsInSeconds(t *testing.T) {
	g := &goalMachine{status: GoalStatusComplete, turnsUsed: 1, workDurationMs: 4200}
	got := appendGoalStopReport("goal complete", g)
	if !strings.Contains(got, "work 4s") {
		t.Fatalf("a short run should read in seconds, got %q", got)
	}
}

func TestAppendGoalStopReportSkipsZeroSpend(t *testing.T) {
	// A goal that ended before spending anything should not print "tokens 0".
	g := &goalMachine{status: GoalStatusBlocked, turnsUsed: 2}
	got := appendGoalStopReport("goal blocked: needs a decision", g)
	if strings.Contains(got, "tokens") || strings.Contains(got, "work") {
		t.Fatalf("zero spend should be omitted, got %q", got)
	}
	if !strings.Contains(got, "turns 2") {
		t.Fatalf("the turn count should always be reported, got %q", got)
	}
}
