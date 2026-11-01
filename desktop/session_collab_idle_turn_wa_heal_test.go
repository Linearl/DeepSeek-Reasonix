package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// tl1311: a session wedged by the provider mid-turn can come back
// no-longer-writable (stale/missing write authority); every later cross-session
// delivery then dies on admission and the inbox piles up. The idle-turn bridge
// now offers the repair (the same resource remount a manual tab reopen runs)
// BEFORE counting the failure as an opening strike. These tests pin the
// contract on the sweep core, following the stall-heal seam discipline.

// waProbe counts run/heal calls across the async opening goroutines; waits
// poll it the same way waitRunLog polls the run log.
type waProbe struct {
	mu        sync.Mutex
	runs      int
	heals     int
	recoverAt int // run attempt number from which the run succeeds (0 = never)
}

func (p *waProbe) run(ctx context.Context, id string) error {
	p.mu.Lock()
	p.runs++
	attempt := p.runs
	recover := p.recoverAt
	p.mu.Unlock()
	if recover > 0 && attempt >= recover {
		return nil // authority repaired; the re-driven head now consumes
	}
	return fmt.Errorf("dispatch inbox item: %w", agent.ErrSessionWriteAuthorityStale)
}

func (p *waProbe) heal(ok bool) func() bool {
	return func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.heals++
		return ok
	}
}

func (p *waProbe) counts() (runs, heals int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runs, p.heals
}

// waitForRuns polls until the run attempt count reaches want (the sweep's
// opening goroutine is asynchronous by contract).
func (p *waProbe) waitForRuns(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runs, _ := p.counts(); runs >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	runs, _ := p.counts()
	t.Fatalf("run calls = %d, want %d within the wait window", runs, want)
}

// The write-authority failure must not burn the 579 budget: heal fires, no
// strike is recorded, and every later sweep may re-drive the same head — one
// attempt per tick, never a retry storm, never an exhausted receipt. When the
// repair lands, the very next pass consumes the item through the normal path
// (one re-injection, idempotent by the claim state machine).
func TestSweepHealsNoLongerWritableTargetWithoutStrike(t *testing.T) {
	b := newIdleTurnBridge()
	p := &waProbe{recoverAt: 3} // passes 1-2 fail (each healed), pass 3 succeeds
	view := idleTurnTargetView{
		contactID:          "c-unwritable",
		queuedID:           "item-stale",
		run:                p.run,
		healWriteAuthority: p.heal(true),
	}
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now) // starts the idle clock only
	for i := 1; i <= 3; i++ {
		pass := i
		b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay+time.Duration(pass)*time.Second))
		p.waitForRuns(t, pass)
	}
	runs, heals := p.counts()
	if heals != 2 {
		t.Fatalf("heal calls = %d, want exactly one per failing attempt (2), not per tick (3)", heals)
	}
	if runs != 3 {
		t.Fatalf("run calls = %d, want one per sweep pass (3), no retry storm", runs)
	}
	key := idleTurnItemKey("c-unwritable", "item-stale")
	if len(b.failCounts) != 0 || b.exhausted[key] {
		t.Fatalf("write-authority failures burned the opening budget: failCounts=%v exhausted=%v", b.failCounts, b.exhausted[key])
	}
	if b.inFlight["c-unwritable"] {
		t.Fatal("in-flight flag leaked after the consumed turn")
	}
}

// A declined repair (nil seam, live foreign holder, removed tab) keeps the
// 任务579 three-strike semantics byte for byte: strikes accumulate, the head
// is suppressed at the third, and the item stays queued.
func TestSweepWriteAuthorityHealDeclineBurnsBudget(t *testing.T) {
	b := newIdleTurnBridge()
	p := &waProbe{}
	view := idleTurnTargetView{
		contactID:          "c-unwritable",
		queuedID:           "item-stale",
		run:                p.run,
		healWriteAuthority: p.heal(false),
	}
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now)
	for i := 1; i <= 3; i++ {
		pass := i
		b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay+time.Duration(pass)*time.Second))
		p.waitForRuns(t, pass)
	}
	runs, heals := p.counts()
	key := idleTurnItemKey("c-unwritable", "item-stale")
	if !b.exhausted[key] {
		t.Fatal("a declined repair must keep the 579 three-strike exhaustion")
	}
	if runs != 3 {
		t.Fatalf("run calls = %d, want exactly the three-strike attempts", runs)
	}
	if heals != 3 {
		t.Fatalf("heal calls = %d, want one per failing attempt", heals)
	}
}

// The classification is the typed family, not string matching: a wrapped
// stale error is recognized, a plain provider rejection is not (a rejected
// turn is the 579 strike path, not a repair trigger).
func TestSweepWriteAuthorityClassificationFamily(t *testing.T) {
	wrappedStale := fmt.Errorf("dispatch inbox item: %w", agent.ErrSessionWriteAuthorityStale)
	wrappedMissing := fmt.Errorf("run: %w", agent.ErrSessionWriteAuthorityMissing)
	if !isSessionWriteAuthorityError(wrappedStale) {
		t.Fatal("wrapped ErrSessionWriteAuthorityStale must classify as the repair family")
	}
	if !isSessionWriteAuthorityError(wrappedMissing) {
		t.Fatal("wrapped ErrSessionWriteAuthorityMissing must classify as the repair family")
	}
	if isSessionWriteAuthorityError(errors.New("The request was rejected by the provider")) {
		t.Fatal("a provider rejection must NOT classify as the repair family (it stays a 579 strike)")
	}
	if isSessionWriteAuthorityError(nil) {
		t.Fatal("nil must not classify as the repair family")
	}
}

// The heal and the strike path never both fire for one failure: the failure
// is either repaired (budget cleared) or struck — never half of each.
func TestSweepWriteAuthorityHealOrStrikeNotBoth(t *testing.T) {
	b := newIdleTurnBridge()
	p := &waProbe{recoverAt: 2} // pass 1 fails+heals, pass 2 succeeds
	view := idleTurnTargetView{
		contactID:          "c-unwritable",
		queuedID:           "item-stale",
		run:                p.run,
		healWriteAuthority: p.heal(true),
	}
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now) // pass 1: idle clock starts, no open
	b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay+time.Second))
	p.waitForRuns(t, 1)
	b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay+2*time.Second))
	p.waitForRuns(t, 2)
	key := idleTurnItemKey("c-unwritable", "item-stale")
	if b.exhausted[key] || len(b.failCounts) != 0 {
		t.Fatalf("repaired failure leaked strike state: failCounts=%v exhausted=%v", b.failCounts, b.exhausted[key])
	}
}
