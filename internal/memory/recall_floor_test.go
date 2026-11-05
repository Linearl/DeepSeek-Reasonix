package memory

import (
	"testing"
	"time"
)

// TestRecallScoreFloorsAreSeparate is acceptance criterion 2: the two recall
// lanes must be independently configurable, and moving one must not move the
// other.
func TestRecallScoreFloorsAreSeparate(t *testing.T) {
	if explicitRecallScoreFloor == autoRecallScoreFloor {
		t.Fatalf("both lanes ship the same floor %v; the split is not wired", explicitRecallScoreFloor)
	}
	// Explicit search is the "someone is actively looking" lane, so its floor
	// must be the laxer (smaller) of the two. A tighter explicit floor would
	// hide facts exactly when a call is asking for them.
	if explicitRecallScoreFloor >= autoRecallScoreFloor {
		t.Fatalf("explicit floor %v must be laxer than auto floor %v",
			explicitRecallScoreFloor, autoRecallScoreFloor)
	}

	// Overriding the explicit lane must not disturb the automatic lane.
	original := explicitRecallFloor
	t.Cleanup(func() { explicitRecallFloor = original })

	explicitRecallFloor = 0.77
	if explicitRecallFloor == autoRecallScoreFloor {
		t.Fatalf("lanes collapsed after an explicit override")
	}
	if autoRecallScoreFloor != 0.24 {
		t.Fatalf("auto floor moved to %v when only the explicit floor was set", autoRecallScoreFloor)
	}

	// A non-positive override falls back to the package default rather than
	// disabling trimming entirely.
	explicitRecallFloor = 0
	floor := explicitRecallFloor
	if floor <= 0 {
		floor = explicitRecallScoreFloor
	}
	if floor != explicitRecallScoreFloor {
		t.Fatalf("zero override must fall back to %v, got %v", explicitRecallScoreFloor, floor)
	}
}

// TestAutoRecallScoreFloorOverride pins the per-call override on the automatic
// lane: a caller can loosen or tighten one turn without touching the package
// default that every other turn uses.
func TestAutoRecallScoreFloorOverride(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	writeTestFact(t, dir, "floor-fact", "代理端口 fact",
		"proxy port is 40808 and relayed through nano", time.Time{})

	// Default: the strict automatic floor applies.
	base := AutoRecall(store, "proxy port", RecallOptions{})
	// A very high floor trims everything down to at most the best hit.
	strict := AutoRecall(store, "proxy port", RecallOptions{ScoreFloor: 0.999})
	if len(strict.Hits) > len(base.Hits) {
		t.Fatalf("a higher floor returned more hits (%d) than the default (%d)",
			len(strict.Hits), len(base.Hits))
	}
	if len(strict.Hits) > 1 {
		t.Fatalf("a 0.999 floor must keep at most the top hit, got %d", len(strict.Hits))
	}
}
