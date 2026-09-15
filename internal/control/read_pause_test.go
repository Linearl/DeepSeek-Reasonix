package control

import (
	"fmt"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

func TestReadPauseIsTerminalForAutomaticGoalContinuation(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &agent.IncompleteReadError{Reason: "page budget"})
	if turnOutcome(err) != event.TurnOutcomeIncompleteRead {
		t.Fatal("read pause lost its outcome")
	}
	if goalTurnErrorAbsorbable(err, false) {
		t.Fatal("Goal would absorb a read pause and retry automatically")
	}
	if goalTurnErrorAbsorbable(err, true) {
		t.Fatal("an unattended Goal would absorb a read pause and retry automatically")
	}
}
