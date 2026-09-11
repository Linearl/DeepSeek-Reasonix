package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Task 49 A2: a fresh controller may only adopt a goal that was still running
// unattended. Everything else is either history or a deliberate wait for a human.
func writeGoalStateFile(t *testing.T, sessionPath string, state goalState) {
	t.Helper()
	path := goalStatePath(sessionPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestResumableAutopilotGoalAdoptsARunningUnattendedRun(t *testing.T) {
	session := filepath.Join(t.TempDir(), "s.jsonl")
	writeGoalStateFile(t, session, goalState{
		Goal:      "ship the thing",
		Status:    GoalStatusRunning,
		Autopilot: true,
	})
	goal, ok := resumableAutopilotGoal(session)
	if !ok || goal != "ship the thing" {
		t.Fatalf("running autopilot goal = (%q, %v), want it adopted", goal, ok)
	}
}

func TestResumableAutopilotGoalIgnoresInteractiveRuns(t *testing.T) {
	session := filepath.Join(t.TempDir(), "s.jsonl")
	writeGoalStateFile(t, session, goalState{
		Goal:   "ship the thing",
		Status: GoalStatusRunning,
	})
	if goal, ok := resumableAutopilotGoal(session); ok {
		t.Fatalf("an interactive goal must not be adopted unattended, got %q", goal)
	}
}

func TestResumableAutopilotGoalIgnoresFinishedAndWaitingGoals(t *testing.T) {
	for _, status := range []string{
		GoalStatusComplete, GoalStatusBlocked, GoalStatusStopped,
		GoalStatusBudgetExhausted, GoalStatusTimeLimitReached,
	} {
		session := filepath.Join(t.TempDir(), "s.jsonl")
		writeGoalStateFile(t, session, goalState{
			Goal: "ship the thing", Status: status, Autopilot: true,
		})
		if goal, ok := resumableAutopilotGoal(session); ok {
			t.Fatalf("status %q must not be resumed, got %q", status, goal)
		}
	}
}

func TestResumableAutopilotGoalNeedsAGoalTextAndAFile(t *testing.T) {
	session := filepath.Join(t.TempDir(), "s.jsonl")
	writeGoalStateFile(t, session, goalState{Status: GoalStatusRunning, Autopilot: true})
	if goal, ok := resumableAutopilotGoal(session); ok {
		t.Fatalf("an autopilot goal without text must not be resumed, got %q", goal)
	}
	if goal, ok := resumableAutopilotGoal(""); ok {
		t.Fatalf("an empty session path must not be resumed, got %q", goal)
	}
	if goal, ok := resumableAutopilotGoal(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Fatalf("a missing state file must not be resumed, got %q", goal)
	}
}
