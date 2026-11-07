package main

import (
	"testing"

	"reasonix/internal/checkpoint"
)

// tl1476: the desktop view must carry the per-file preflight classification
// through to the frontend unchanged, and omit it when the plan has none.
func TestRewindPlanToViewCarriesFileSafety(t *testing.T) {
	plan := checkpoint.RewindPlan{
		PlanID:   "plan-1",
		Turn:     3,
		CanFiles: true,
		FileSafety: []checkpoint.RewindFileSafety{
			{Path: "a.txt", Safe: true},
			{Path: "b.txt", Safe: false, Reason: checkpoint.ConflictPermissionDenied},
			{Path: "c.txt", Safe: false, Reason: checkpoint.ConflictPathUnsafe},
			{Path: "d.txt", Safe: false, Reason: checkpoint.ConflictMissingPayload},
		},
	}
	view := rewindPlanToView(plan, "code")
	if len(view.FileSafety) != 4 {
		t.Fatalf("expected 4 file safety entries, got %d", len(view.FileSafety))
	}
	for i, want := range plan.FileSafety {
		got := view.FileSafety[i]
		if got.Path != want.Path || got.Safe != want.Safe || got.Reason != want.Reason {
			t.Fatalf("entry %d: got %+v, want %+v", i, got, want)
		}
	}

	empty := rewindPlanToView(checkpoint.RewindPlan{PlanID: "plan-2"}, "conversation")
	if empty.FileSafety != nil {
		t.Fatalf("plans without file targets must not expose fileSafety, got %v", empty.FileSafety)
	}
}
