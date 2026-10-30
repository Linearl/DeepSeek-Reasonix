package main

import (
	"testing"
	"time"
)

// tl1288 sweep tests: the fourth 579-family shape must leave skip:running —
// a RUNNING turn whose body wedged holds the phantom busy projection forever
// (20261031 01:07 现场: 11+ minutes of skip:running on a tab showing a green
// dot + work counter, entity on disk so the 1266b projection never engaged).
// The heal probe rides the same seam discipline as the 796 finishing heal.

func stalledRunningView(heal func() (bool, time.Duration)) idleTurnTargetView {
	return idleTurnTargetView{
		contactID:          "c-wedged",
		running:            true,
		healStalledRunning: heal,
	}
}

// A wedged running target heals: the probe fires, and no turn opens this
// tick — the healed target continues, and the next sweep takes the normal
// path (skip:running no longer holds, so a queued head re-arms there).
func TestSweepHealsStalledRunningTarget(t *testing.T) {
	b := newIdleTurnBridge()
	heals := 0
	view := stalledRunningView(func() (bool, time.Duration) {
		heals++
		return true, 11 * time.Minute
	})
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now)
	if heals != 1 {
		t.Fatalf("heal probe calls = %d, want 1", heals)
	}
	if b.inFlight["c-wedged"] {
		t.Fatal("the healed target must not open a turn on the heal tick itself")
	}
}

// A probe that declines (controller-side preconditions failed: healthy long
// tool, background jobs, compaction) leaves the skip behaviour untouched —
// and does not retry more than the probe itself allows.
func TestSweepKeepsSkipWhenStallProbeDeclines(t *testing.T) {
	b := newIdleTurnBridge()
	heals := 0
	view := stalledRunningView(func() (bool, time.Duration) {
		heals++
		return false, 30 * time.Second
	})
	now := time.Now()
	for i := 0; i < 3; i++ {
		b.sweepTargets([]idleTurnTargetView{view}, now.Add(time.Duration(i)*4*time.Second))
	}
	if heals != 3 {
		t.Fatalf("heal probe calls = %d, want one per sweep pass", heals)
	}
}

// A nil seam keeps the pre-tl1288 behaviour byte for byte: running targets
// skip, nothing else changes.
func TestSweepNilStallHealKeepsLegacySkip(t *testing.T) {
	b := newIdleTurnBridge()
	view := idleTurnTargetView{contactID: "c-plain", running: true}
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now)
	// No panic and no in-flight marking: the target was skipped.
	if b.inFlight["c-plain"] {
		t.Fatal("a nil-seam running target must be skipped, not opened")
	}
}

// The finishing-only leak (796) owns the finishing=true shape: the running
// probe must not double-heal it.
func TestSweepFinishingShapeNotDoubleHealed(t *testing.T) {
	b := newIdleTurnBridge()
	runningHeals, finishingHeals := 0, 0
	view := idleTurnTargetView{
		contactID:    "c-finishing",
		running:      true, // RuntimeStatus merges finishing into Running
		finishing:    true,
		finishingFor: 10 * time.Minute,
		queuedID:     "item-stuck", // the 796 heal only arms behind a queued head
		heal: func() (bool, time.Duration) {
			finishingHeals++
			return true, 10 * time.Minute
		},
		healStalledRunning: func() (bool, time.Duration) {
			runningHeals++
			return true, 10 * time.Minute
		},
	}
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now)
	if runningHeals != 0 {
		t.Fatal("the finishing-only shape must heal through the 796 seam, not the running probe")
	}
	if finishingHeals != 1 {
		t.Fatalf("finishing heal calls = %d, want 1", finishingHeals)
	}
}
