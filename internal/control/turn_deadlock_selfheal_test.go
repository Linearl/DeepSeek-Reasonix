package control

import (
	"errors"
	"testing"
	"time"
)

// newStalledController builds a controller whose running turn is an abandoned
// interrupt: cancel was requested long ago and the body never returned. This
// is the task 303 precondition (12:07 zero-response sample).
func newStalledController(t *testing.T) *Controller {
	t.Helper()
	c := &Controller{}
	c.mu.Lock()
	c.running = true
	c.interrupting = true
	c.interruptRequestedAt = time.Now().Add(-2 * turnInterruptStaleTimeout)
	c.mu.Unlock()
	return c
}

// TestSelfHealReopensStalledGate pins the task 303 core: a turn stuck past
// the stale threshold must be abandoned — the gate reopens, the interrupt
// state clears, and the generation bumps so the dead turn's late completion
// is recognized as stale. The full admit→spawn path needs a wired controller
// (sink/executor); the state machine itself is what deadlocks, so it is
// tested directly here.
func TestSelfHealReopensStalledGate(t *testing.T) {
	c := newStalledController(t)
	c.mu.Lock()
	stalledFor := c.selfHealStalledLocked(time.Now())
	running := c.running
	interrupting := c.interrupting
	stamp := c.interruptRequestedAt
	gen := c.turnGeneration
	c.mu.Unlock()
	if stalledFor < 2*turnInterruptStaleTimeout {
		t.Fatalf("stalledFor = %v, want >= %v (report how long it was stuck)", stalledFor, 2*turnInterruptStaleTimeout)
	}
	if running {
		t.Fatal("self-heal must reopen the admission gate (running=false)")
	}
	if interrupting {
		t.Fatal("interrupt state must be cleared by the self-heal")
	}
	if !stamp.IsZero() {
		t.Fatalf("interrupt stamp must be cleared, got %v", stamp)
	}
	if gen != 1 {
		t.Fatalf("turnGeneration = %d, want 1 (one self-heal bump)", gen)
	}
	// The gate is open now: the stalled precondition is gone, so a second
	// heal must be a no-op.
	c.mu.Lock()
	again := c.selfHealStalledLocked(time.Now())
	c.mu.Unlock()
	if again != 0 {
		t.Fatalf("second heal = %v, want 0 (already healed)", again)
	}
}

// TestSelfHealSkipsLiveTurn pins the negative: a fresh interrupt inside the
// threshold must NOT be treated as stalled — the running turn keeps its gate.
func TestSelfHealSkipsLiveTurn(t *testing.T) {
	c := &Controller{}
	c.mu.Lock()
	c.running = true
	c.interrupting = true
	c.interruptRequestedAt = time.Now() // just requested, well under 45s
	stalledFor := c.selfHealStalledLocked(time.Now())
	gen := c.turnGeneration
	running := c.running
	interrupting := c.interrupting
	stamp := c.interruptRequestedAt
	c.mu.Unlock()
	if stalledFor != 0 {
		t.Fatalf("stalledFor = %v, want 0 (live interrupt must not heal)", stalledFor)
	}
	if gen != 0 {
		t.Fatalf("turnGeneration = %d, want 0 (no heal for a live interrupt)", gen)
	}
	if !running || !interrupting || stamp.IsZero() {
		t.Fatalf("live turn state must be untouched: running=%v interrupting=%v stamp=%v",
			running, interrupting, stamp)
	}
}

// TestStaleCompletionAfterHealIsIgnored pins the generation guard: a completion
// stamped with an older generation must not clear the current turn's gate.
func TestStaleCompletionAfterHealIsIgnored(t *testing.T) {
	c := &Controller{}
	stale := &guardedTurnCompletion{generation: 0}
	c.mu.Lock()
	c.turnGeneration = 1 // a heal happened after this completion spawned
	c.running = true
	c.mu.Unlock()

	c.finishGuardedTurn(errors.New("late failure from abandoned turn"), stale)

	c.mu.Lock()
	running := c.running
	finishing := c.finishing
	c.mu.Unlock()
	if !running {
		t.Fatal("stale completion must not clear the gate the current turn owns")
	}
	if finishing {
		t.Fatal("stale completion must not open a finishing window")
	}
}
