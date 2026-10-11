package control

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
)

// 任务796 tests: the finishing-only leak (a TurnDone fan-out that wedges
// forever) is the third 579-family shape — RuntimeStatus().Running stays
// merged-true, the desktop idle-turn bridge skips the target, and a queued
// follow-up strands unattended. HealStuckFinishing reopens the admission
// gate; these tests pin its preconditions, its effect, and the
// stale-finisher guard that keeps the wedged finisher's deferred cleanup
// from racing the healed gate.
//
// Constraint discovered while building these tests (and honored by the 修复
// 选型): the TurnDone fan-out is a synchronous ordering barrier — while the
// wedged Emit holds it, every later checked emit (a new turn's TurnStarted
// included) blocks behind it. A heal therefore re-opens the ADMISSION gate,
// but a turn admitted while the fan-out is still wedged cannot proceed; the
// bridge-side self-heal path relies on the fan-out either not being the
// wedged point or the desktop-side alarm driving a human/teardown recovery.

// holdFirstTurnDone blocks the FIRST TurnDone delivery until release closes
// and reports the wedged state through entered; everything else passes.
func holdFirstTurnDone(release <-chan struct{}, entered chan<- struct{}) event.Sink {
	var first atomic.Int32
	return event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone && first.Add(1) == 1 {
			entered <- struct{}{}
			<-release
		}
	})
}

// wedgeFinishingWindow parks a controller in a wedged fan-out: one turn has
// finished, its TurnDone Emit never returns, running=false + finishing=true.
func wedgeFinishingWindow(t *testing.T, release chan struct{}) *Controller {
	t.Helper()
	entered := make(chan struct{}, 1)
	c := New(Options{Sink: holdFirstTurnDone(release, entered)})
	started := make(chan struct{}, 1)
	c.runGuarded(func(context.Context) error {
		started <- struct{}{}
		return nil
	})
	<-started
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn done never reached the fan-out")
	}
	return c
}

// ageFinishingSince back-dates the window stamp: a real wedge is discovered
// minutes later, and no test should wait out the 90s threshold.
func ageFinishingSince(t *testing.T, c *Controller, age time.Duration) {
	t.Helper()
	c.mu.Lock()
	c.finishingSince = time.Now().Add(-age)
	c.mu.Unlock()
}

func TestHealStuckFinishingRefusesHealthyWindow(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c := wedgeFinishingWindow(t, release)
	defer c.Close()

	healed, stalled := c.HealStuckFinishing(FinishingStuckTimeout)
	if healed || stalled != 0 {
		t.Fatalf("fresh finishing window healed (healed=%v stalled=%v) — heal must respect the threshold", healed, stalled)
	}
	if !c.RuntimeStatus().Finishing {
		t.Fatal("healthy window must keep its finishing flag")
	}
}

func TestHealStuckFinishingReopensWedgedGate(t *testing.T) {
	release := make(chan struct{})
	c := wedgeFinishingWindow(t, release)
	defer c.Close()
	ageFinishingSince(t, c, 10*time.Minute)

	healed, stalled := c.HealStuckFinishing(FinishingStuckTimeout)
	if !healed {
		t.Fatal("wedged finishing-only window must heal")
	}
	if stalled < FinishingStuckTimeout {
		t.Fatalf("stalled = %v, want ≥ threshold", stalled)
	}
	st := c.RuntimeStatus()
	if st.Running || st.Finishing || st.FinishingSinceUnix != 0 {
		t.Fatalf("post-heal status = running:%v finishing:%v since:%d, want all clear", st.Running, st.Finishing, st.FinishingSinceUnix)
	}
	// The wedged fan-out still holds the event barrier, so admitting here
	// would block on the wedged TurnDone upstream (see the file comment).
	// Release it first: the T1 finisher's deferred cleanup must SKIP (its
	// generation went stale at the heal) instead of touching the gate.
	close(release)
	waitIdleAdmission(t, c)

	// The gate actually reopens: a submit admits as a fresh turn instead of
	// parking behind the dead window.
	bodyRan := make(chan struct{}, 1)
	if got := c.runGuarded(func(context.Context) error {
		bodyRan <- struct{}{}
		return nil
	}); got != turnStarted {
		t.Fatalf("admission after heal = %v, want turnStarted", got)
	}
	select {
	case <-bodyRan:
	case <-time.After(5 * time.Second):
		t.Fatal("body did not run after the heal")
	}
	waitIdleAdmission(t, c)
}

func TestHealStuckFinishingRefusesWhileRunning(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	c := New(Options{})
	started := make(chan struct{}, 1)
	c.runGuarded(func(context.Context) error {
		started <- struct{}{}
		<-block
		return nil
	})
	<-started
	defer c.Close()

	if healed, _ := c.HealStuckFinishing(FinishingStuckTimeout); healed {
		t.Fatal("a running turn must never be healed")
	}
}

func TestHealStuckFinishingRefusesWithParkedTurns(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c := wedgeFinishingWindow(t, release)
	defer c.Close()

	if got := c.runGuarded(func(context.Context) error { return nil }); got != turnParked {
		t.Fatalf("admission = %v, want turnParked (drain may still be alive)", got)
	}
	ageFinishingSince(t, c, 10*time.Minute)
	if healed, _ := c.HealStuckFinishing(FinishingStuckTimeout); healed {
		t.Fatal("a finishing window with parked bodies must not heal — the FIFO drain may still be flowing")
	}
}

// TestStaleFinisherCleanupSkippedAfterHeal pins the 任务796 stale-finisher
// guard with a fully real sequence: the wedged T1 finisher's own sink
// callback performs the heal (bumping the generation), so when the T1 Emit
// returns and T1's DEFERRED cleanup runs, its generation is stale. Without
// the guard the deferred drain would pop the parked T3 and spawn it
// CONCURRENTLY with the live T2 (spawnGuardedTurn runs bodies directly, no
// admission); with it T3 stays parked until T2 finishes and drains FIFO.
func TestStaleFinisherCleanupSkippedAfterHeal(t *testing.T) {
	var c *Controller
	healedGen := make(chan uint64, 1)
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind != event.TurnDone {
			return
		}
		// The wedged-finisher recovery moment: back-date the window stamp the
		// way a real discovery would find it, then heal from inside the
		// fan-out's own call stack.
		c.mu.Lock()
		c.finishingSince = time.Now().Add(-time.Hour)
		c.mu.Unlock()
		healed, _ := c.HealStuckFinishing(time.Minute)
		if healed {
			c.mu.Lock()
			healedGen <- c.turnGeneration
			c.mu.Unlock()
		}
	})
	c = New(Options{Sink: sink})
	defer c.Close()

	// T1 runs to completion; its TurnDone delivery triggers the heal above
	// inside the sink, then returns and reaches T1's deferred cleanup.
	t1Started := make(chan struct{}, 1)
	c.runGuarded(func(context.Context) error {
		t1Started <- struct{}{}
		return nil
	})
	<-t1Started

	var gen uint64
	select {
	case gen = <-healedGen:
	case <-time.After(5 * time.Second):
		t.Fatal("the heal never fired from the fan-out")
	}
	if gen == 0 {
		t.Fatal("heal must bump the turn generation")
	}

	// The healed gate takes T2 (stays running), T3 parks behind it.
	t2Started := make(chan struct{}, 1)
	t2Block := make(chan struct{})
	if got := c.runGuarded(func(context.Context) error {
		t2Started <- struct{}{}
		<-t2Block
		return nil
	}); got != turnStarted {
		t.Fatalf("T2 admission = %v, want turnStarted", got)
	}
	<-t2Started

	t3Ran := make(chan struct{}, 1)
	// runGuardedOrPark is the user-words path: it PARKS behind a running turn
	// (plain runGuarded drops — the steer posture). The stale finisher must
	// not spawn this parked body over the live T2.
	if got := c.runGuardedOrPark(func(context.Context) error {
		t3Ran <- struct{}{}
		return nil
	}); got != turnParked {
		t.Fatalf("T3 admission = %v, want turnParked", got)
	}

	// T1's finisher has by now returned through its deferred cleanup — give
	// it a beat, then assert T3 was NOT spawned over the live T2.
	deadline := time.Now().Add(2 * time.Second)
	for c.RuntimeStatus().Finishing {
		if time.Now().After(deadline) {
			break // T1's defer window is over either way
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-t3Ran:
		t.Fatal("stale finisher cleanup spawned the parked turn over the live T2")
	case <-time.After(300 * time.Millisecond):
	}

	// T2 finishes normally; its own window drains T3 FIFO, untouched.
	close(t2Block)
	select {
	case <-t3Ran:
	case <-time.After(10 * time.Second):
		t.Fatal("parked T3 never ran after T2 finished")
	}
	waitIdleAdmission(t, c)
}

func TestRuntimeStatusExposesFinishingWindow(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c := wedgeFinishingWindow(t, release)
	defer c.Close()

	st := c.RuntimeStatus()
	if !st.Running || !st.Finishing || st.FinishingSinceUnix == 0 {
		t.Fatalf("wedged status = running:%v finishing:%v since:%d — the observability fields must expose the leak",
			st.Running, st.Finishing, st.FinishingSinceUnix)
	}
	// A clean idle controller reports no window at all.
	idle := New(Options{})
	defer idle.Close()
	if s := idle.RuntimeStatus(); s.Finishing || s.FinishingSinceUnix != 0 {
		t.Fatalf("idle status = finishing:%v since:%d, want clear", s.Finishing, s.FinishingSinceUnix)
	}
}
