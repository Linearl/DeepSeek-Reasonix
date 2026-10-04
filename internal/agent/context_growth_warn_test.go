package agent

import "testing"

// 任务461-P13② growth watch: a view that balloons between two maintenance
// checks must raise exactly one sharp-growth warning per turn (re-armed by the
// cooldown or a turn change), and steady growth must stay silent. Pure
// observation — the assertions cover the trigger, the dedup and the reset.

func growthWatchAgent() *Agent {
	// sess.compaction is a value type: the zero value is the fresh watch state.
	return &Agent{agentConfig: agentConfig{contextWindow: 100_000, compactRatio: defaultCompactRatio}}
}

func TestObserveContextGrowthWarnsOnSharpDelta(t *testing.T) {
	a := growthWatchAgent()
	if a.observeContextGrowth(10_000) {
		t.Fatal("first observation has no previous baseline and must never warn")
	}
	// +12k over a 100k window is well under the 40% warn ratio: steady growth.
	if a.observeContextGrowth(22_000) {
		t.Fatal("steady growth below the warn ratio must stay silent")
	}
	// +40k crosses exactly the 40 percent ratio boundary: warn once.
	if !a.observeContextGrowth(62_000) {
		t.Fatal("a 40 percent-of-window jump between checks must raise the sharp-growth warning")
	}
	if a.sess.compaction.lastPrepareEst != 62_000 {
		t.Fatalf("baseline not updated: got %d, want 62000", a.sess.compaction.lastPrepareEst)
	}
}

func TestObserveContextGrowthDedupesWithinTurnUntilCooldown(t *testing.T) {
	a := growthWatchAgent()
	a.activeTurnCreatedAt.Store(1111)
	a.observeContextGrowth(10_000)
	if !a.observeContextGrowth(60_000) {
		t.Fatal("first sharp jump in the turn must warn")
	}
	// Same turn, cooldown not elapsed: further sharp jumps stay silent.
	if a.observeContextGrowth(110_000) {
		t.Fatal("a second sharp jump in the same turn inside the cooldown must be deduped")
	}
	// A new turn re-arms the warning immediately.
	a.activeTurnCreatedAt.Store(2222)
	if !a.observeContextGrowth(160_000) {
		t.Fatal("a sharp jump in a new turn must warn again")
	}
	// A shrinking view never warns and keeps the baseline fresh.
	if a.observeContextGrowth(50_000) {
		t.Fatal("a decreasing estimate must never warn")
	}
	if a.sess.compaction.lastPrepareEst != 50_000 {
		t.Fatalf("baseline not updated on shrink: got %d, want 50000", a.sess.compaction.lastPrepareEst)
	}
}

func TestObserveContextGrowthIgnoresDegenerateInputs(t *testing.T) {
	var nilAgent *Agent
	if nilAgent.observeContextGrowth(50_000) {
		t.Fatal("nil agent must not warn")
	}
	a := growthWatchAgent()
	a.agentConfig.contextWindow = 0 // no window known: the watch has no scale
	if a.observeContextGrowth(10_000) || a.observeContextGrowth(90_000) {
		t.Fatal("without a window the growth watch must stay silent")
	}
	if a.observeContextGrowth(0) || a.observeContextGrowth(-1) {
		t.Fatal("non-positive estimates must be ignored entirely")
	}
	if a.sess.compaction.lastPrepareEst != 0 {
		t.Fatalf("non-positive estimates must not move the baseline: got %d, want 0", a.sess.compaction.lastPrepareEst)
	}
}
