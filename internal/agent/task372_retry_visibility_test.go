package agent

import (
	"errors"
	"testing"
	"time"

	"reasonix/internal/event"
)

// Task 372 — visibility faces around the task-243 A4 budget. The admission
// semantics themselves stay covered by retrybudget_test.go (5 tests, run
// alongside); these tests pin the read-only exposure only.

func TestRetryBudgetCountVisibility(t *testing.T) {
	b := NewRetryBudget()
	now := time.Now()
	// Two admissions in this phase.
	for i := 0; i < 2; i++ {
		if ok, _, _ := b.Allow("stream", errors.New("stream error"), now); !ok {
			t.Fatalf("admission %d refused unexpectedly", i+1)
		}
	}
	if got := b.Count("stream", now); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	if got := b.Limit(); got != retryBudgetLimit {
		t.Fatalf("Limit = %d, want %d", got, retryBudgetLimit)
	}
	// Count is read-only: calling it must not consume the window.
	if ok, _, _ := b.Allow("stream", errors.New("stream error"), now); !ok {
		t.Fatal("third admission should still fit under the limit")
	}
	if got := b.Count("stream", now); got != 3 {
		t.Fatalf("Count after read = %d, want 3 (read-only observation)", got)
	}
	// A nil budget reports 0/0 instead of panicking (visibility callers run
	// even when the budget failed closed).
	var nilBudget *RetryBudget
	if got := nilBudget.Count("stream", now); got != 0 {
		t.Fatalf("nil Count = %d, want 0", got)
	}
	if got := nilBudget.Limit(); got != 0 {
		t.Fatalf("nil Limit = %d, want 0", got)
	}
}

func TestRecoveryStatusCarriesBudgetFields(t *testing.T) {
	// The wire shape must expose the three JSON fields the UI reads.
	status := &event.RecoveryStatus{
		Phase:           "stream",
		BudgetUsed:      3,
		BudgetLimit:     retryBudgetLimit,
		BudgetExhausted: true,
	}
	if status.BudgetUsed != 3 || status.BudgetLimit != retryBudgetLimit || !status.BudgetExhausted {
		t.Fatalf("budget fields not carried: %+v", status)
	}
}

// runtimeActivity lives in internal/control and is covered by
// internal/control/task372_runtime_activity_test.go — this file stays scoped
// to the budget's read-only exposure.
