package sandbox

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tl1583 (GitHub issue #92): the 250ms budget bounds only the caller's wait;
// the spawned walker keeps blocking inside EvalSymlinks syscalls long after
// the caller gave up, and a goroutine blocked in a syscall pins its OS
// thread. The 2026-11-11 desktop crash was 5591 timeouts in 2 minutes piling
// up thousands of abandoned walkers until Go hit its 10000-thread limit
// ("fatal error: thread exhaustion"). These pins hold the structural bound:
// live walkers never exceed the pool, overflow degrades to the cleaned path
// (the same fallback a timeout produces), and permits recycle once blocked
// walkers finish.

// waitForWalkerPool polls cond until it holds or the deadline expires.
func waitForWalkerPool(t *testing.T, deadline time.Duration, cond func() bool) {
	t.Helper()
	timeout := time.Now().Add(deadline)
	for time.Now().Before(timeout) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", deadline)
}

// pendingCanonicalFlights counts in-flight walks (flights registered but not
// yet resolved). Degraded-first callers register a flight for their background
// refresh goroutine; the count only settles once every such goroutine has run
// its post-walk bookkeeping.
func pendingCanonicalFlights() int {
	canonicalMu.Lock()
	defer canonicalMu.Unlock()
	return len(canonicalFlights)
}

// quiesceCanonicalEngineForTest fences off sibling-test stragglers. A
// fast-fail window left armed by an earlier test (resetCanonicalEngine does
// not own that seam) makes degraded callers spawn background refresh
// goroutines; one scheduled late could enter this test's setup window,
// capture whatever seam and pool are installed at that instant, and skew the
// exact walker accounting below. Disarming fast-fail and waiting for the
// flight map to drain proves every background refresh body has completed, so
// nothing but this test's own callers can touch the shrunken pool. Walkers
// already spawned by sibling tests keep their own captured pool (canonical.go
// releases on the captured channel), so they cannot steal permits either.
func quiesceCanonicalEngineForTest(t *testing.T) {
	t.Helper()
	resetCanonicalEngine(t)
	sweepCanonicalFastFailStateForTest()
	t.Cleanup(sweepCanonicalFastFailStateForTest)
	waitForWalkerPool(t, 5*time.Second, func() bool { return pendingCanonicalFlights() == 0 })
}

// shrinkWalkerPool swaps the pool for a small one and restores everything —
// including the fast-fail window these pins arm — so no state leaks into
// sibling tests.
func shrinkWalkerPool(t *testing.T, cap int) chan struct{} {
	t.Helper()
	prevSlots := canonicalWalkerSlots
	prevCap := canonicalWalkerMaxWalkers
	pool := make(chan struct{}, cap)
	canonicalWalkerSlots = pool
	canonicalWalkerMaxWalkers = cap
	t.Cleanup(func() {
		canonicalWalkerSlots = prevSlots
		canonicalWalkerMaxWalkers = prevCap
		canonicalFastFailUntil.Store(0)
	})
	return pool
}

// TestCanonicalWalkerCapBoundsLiveWalks is the tl1583 quantitative cap pin:
// fire 20 concurrent resolutions on 20 distinct paths through a 2-permit pool
// with the evaluator blocked — exactly 2 walkers may exist (2 evaluator
// entries), the other 18 callers must degrade to their cleaned paths while
// the gate is still shut, and once the blocked walkers finish, exactly the 2
// admitted walks land in the walk log and the pool must accept a new walk
// again (no permit leak).
func TestCanonicalWalkerCapBoundsLiveWalks(t *testing.T) {
	quiesceCanonicalEngineForTest(t)
	shrinkWalkerPool(t, 2)
	canonicalResolveTimeout = 5 * time.Second // blocked walkers must not time out mid-pin

	base := t.TempDir()
	const callers = 20

	gate := make(chan struct{})
	var inFlight, maxInFlight, evalCalls, done int32
	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&evalCalls, 1)
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			max := atomic.LoadInt32(&maxInFlight)
			if cur <= max || atomic.CompareAndSwapInt32(&maxInFlight, max, cur) {
				break
			}
		}
		<-gate // walker stays live (syscall-blocked shape) until the pin releases it
		atomic.AddInt32(&inFlight, -1)
		return p, nil
	}

	paths := make([]string, callers)
	for i := range paths {
		paths[i] = filepath.Join(base, "proj", "session", "tab", "leaf"+string(rune('a'+i)))
	}

	results := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer atomic.AddInt32(&done, 1)
			<-startGate
			results[i], errs[i] = ResolveAbsPath(paths[i])
		}(i)
	}
	close(startGate)

	// Saturation: exactly the pool's worth of walks may start; everyone else
	// degrades and returns while the gate is still shut.
	waitForWalkerPool(t, 2*time.Second, func() bool {
		return atomic.LoadInt32(&done) == callers-2
	})
	// The degraded callers armed fast-fail and registered background refresh
	// flights; wait until only the two blocked callers' flights remain, so no
	// refresh goroutine can schedule late, steal a freed permit and walk.
	waitForWalkerPool(t, 2*time.Second, func() bool { return pendingCanonicalFlights() == 2 })
	if got := atomic.LoadInt32(&maxInFlight); got != 2 {
		t.Fatalf("%d concurrent resolutions drove %d live walkers through a 2-permit pool, want exactly 2", callers, got)
	}
	if got := atomic.LoadInt32(&evalCalls); got != 2 {
		t.Fatalf("pool admitted %d walks, want exactly 2", got)
	}

	// Recovery: releasing the walkers must let every caller finish with the
	// regular contract (no error, a path back); exactly the 2 admitted walks
	// appear in the walk probe; the permits recycle so a fresh resolution
	// walks again instead of degrading forever.
	walksBefore := probeWalks.Load()
	close(gate)
	wg.Wait()
	if delta := probeWalks.Load() - walksBefore; delta != 2 {
		t.Fatalf("walk probe counted %d completed walks under a 2-permit pool, want exactly 2", delta)
	}
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if want := filepath.Clean(paths[i]); results[i] != want {
			t.Fatalf("caller %d = %q, want %q", i, results[i], want)
		}
	}
	waitForWalkerPool(t, 2*time.Second, func() bool { return len(canonicalWalkerSlots) == 0 })
	// The saturation degrades armed the fast-fail window; exit it so the
	// post-recovery resolution takes the synchronous walk path the pin needs
	// to observe (an armed window would serve degraded-first and refresh in
	// the background instead).
	canonicalFastFailUntil.Store(0)

	fresh := filepath.Join(base, "proj", "after-recovery")
	evalCallsBefore := atomic.LoadInt32(&evalCalls)
	got, err := ResolveAbsPath(fresh)
	if err != nil {
		t.Fatalf("post-recovery resolve: %v", err)
	}
	if got != filepath.Clean(fresh) {
		t.Fatalf("post-recovery resolve = %q, want %q", got, filepath.Clean(fresh))
	}
	if atomic.LoadInt32(&evalCalls) <= evalCallsBefore {
		t.Fatal("post-recovery resolution must walk again — pool did not recycle its permits")
	}
}

// TestCanonicalWalkerPoolRecoversAfterAbandonedWalkerFinishes pins the
// incident shape end to end: a walk whose caller timed out (the budget only
// abandons the result, never the walker) keeps holding its permit — new
// resolutions degrade instead of spawning more syscall-blocked threads — and
// the permit comes back once the abandoned walker finishes climbing.
func TestCanonicalWalkerPoolRecoversAfterAbandonedWalkerFinishes(t *testing.T) {
	quiesceCanonicalEngineForTest(t)
	shrinkWalkerPool(t, 1)
	canonicalResolveTimeout = 50 * time.Millisecond // the caller gives up fast; the walker does not

	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var evalCalls int32
	canonicalEvalSymlinks = func(p string) (string, error) {
		atomic.AddInt32(&evalCalls, 1)
		<-gate // first call = the abandoned walker; later calls run free
		return p, nil
	}

	base := t.TempDir()
	abandoned := filepath.Join(base, "abandoned", "leaf")

	// The caller times out and gets the cleaned path; its walker stays live.
	got, err := ResolveAbsPath(abandoned)
	if err != nil {
		t.Fatalf("timed-out resolve: %v", err)
	}
	if want := filepath.Clean(abandoned); got != want {
		t.Fatalf("timed-out resolve = %q, want cleaned %q", got, want)
	}
	if atomic.LoadInt32(&evalCalls) != 1 {
		t.Fatalf("fixture broken: %d evaluator calls before the pin, want 1", atomic.LoadInt32(&evalCalls))
	}
	waitForWalkerPool(t, 2*time.Second, func() bool { return len(canonicalWalkerSlots) == 1 })

	// Pool saturation: a second path must NOT spawn another walker — it
	// degrades to the cleaned path immediately (fast-fail is armed by the
	// timeout above, so this returns without any synchronous wait).
	saturated := filepath.Join(base, "saturated", "leaf")
	started := time.Now()
	got2, err := ResolveAbsPath(saturated)
	if err != nil {
		t.Fatalf("saturated resolve: %v", err)
	}
	if elapsed := time.Since(started); elapsed > canonicalResolveTimeout {
		t.Fatalf("saturated resolve waited %v, want an immediate degrade", elapsed)
	}
	if want := filepath.Clean(saturated); got2 != want {
		t.Fatalf("saturated resolve = %q, want cleaned %q", got2, want)
	}
	if atomic.LoadInt32(&evalCalls) != 1 {
		t.Fatalf("saturated resolution walked %d times total, want 1 (no walker may spawn past the pool)", atomic.LoadInt32(&evalCalls))
	}
	// The saturated resolve registered a background refresh flight; wait for
	// it to finish degrading against the full pool before releasing the
	// abandoned walker, so the refresh can never schedule late, steal the
	// freed permit and walk (which would inflate the post-recovery count).
	waitForWalkerPool(t, 2*time.Second, func() bool { return pendingCanonicalFlights() == 0 })

	// The abandoned walker finishes; its permit must come back and a fresh
	// resolution must walk again.
	release()
	waitForWalkerPool(t, 2*time.Second, func() bool { return len(canonicalWalkerSlots) == 0 })
	canonicalFastFailUntil.Store(0) // quiet window: exit degraded-first deterministically

	after := filepath.Join(base, "after-recovery", "leaf")
	// ResolveAbsPathFresh pins that the uncached approval-identity funnel
	// rides the same pool: it must re-acquire the recycled permit and walk.
	got3, err := ResolveAbsPathFresh(after)
	if err != nil {
		t.Fatalf("post-recovery resolve: %v", err)
	}
	if want := filepath.Clean(after); got3 != want {
		t.Fatalf("post-recovery resolve = %q, want %q", got3, want)
	}
	if atomic.LoadInt32(&evalCalls) != 2 {
		t.Fatalf("post-recovery evaluator calls = %d, want 2 (permit recycled, walk resumed)", atomic.LoadInt32(&evalCalls))
	}
}
