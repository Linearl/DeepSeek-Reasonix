package sandbox

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// sweepCanonicalFastFailStateForTest resets every piece of engine state the
// fast-fail tests touch. Callers must restore the seams themselves.
func sweepCanonicalFastFailStateForTest() {
	canonicalMu.Lock()
	canonicalCache = map[string]canonicalEntry{}
	canonicalFlights = map[string]*canonicalFlight{}
	canonicalMu.Unlock()
	canonicalFastFailUntil.Store(0)
}

// TestResolveCanonicalPathFastFailServesDegradedThenRefreshesInBackground is
// the tl1526 pin: after a walk timeout the next cache miss returns the cleaned
// path immediately (the load chain pays zero synchronous waits), and the
// background walk lands the resolved identity in the cache for later callers.
func TestResolveCanonicalPathFastFailServesDegradedThenRefreshesInBackground(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	alt := filepath.Join(dir, "alt")
	for _, path := range []string{real, alt} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create fixture dirs: %v", err)
		}
	}
	evalCalls := 0
	slowBurn := 1
	var slowMu sync.Mutex
	previousEval := canonicalEvalSymlinks
	canonicalEvalSymlinks = func(path string) (string, error) {
		// Simulate a transient disk-pressure spike: exactly one walk runs past
		// the budget; the deepest existing ancestor resolves to `alt` so the
		// background refresh result is distinguishable from the degraded
		// cleaned path.
		if filepath.Clean(path) == real {
			evalCalls++
			slowMu.Lock()
			burn := slowBurn
			slowBurn--
			slowMu.Unlock()
			if burn > 0 {
				time.Sleep(40 * time.Millisecond)
			}
			return alt, nil
		}
		return previousEval(path)
	}
	defer func() { canonicalEvalSymlinks = previousEval }()
	previousTimeout := canonicalResolveTimeout
	canonicalResolveTimeout = 5 * time.Millisecond
	defer func() { canonicalResolveTimeout = previousTimeout }()
	previousTTL, previousPositiveTTL := canonicalCacheTTL, canonicalCachePositiveTTL
	canonicalCacheTTL = 20 * time.Millisecond
	canonicalCachePositiveTTL = 20 * time.Millisecond
	defer func() {
		canonicalCacheTTL = previousTTL
		canonicalCachePositiveTTL = previousPositiveTTL
	}()
	sweepCanonicalFastFailStateForTest()
	defer sweepCanonicalFastFailStateForTest()

	target := filepath.Join(real, "leaf")
	cleanTarget := filepath.Clean(target)

	started := time.Now()
	got, err := resolveCanonicalPath(target)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*canonicalResolveTimeoutBudget {
		t.Fatalf("first walk must respect the budget, took %v", elapsed)
	}
	if got != cleanTarget {
		t.Fatalf("timed-out walk must degrade to the cleaned input, got %q want %q", got, cleanTarget)
	}

	time.Sleep(25 * time.Millisecond) // expire the degraded cache entry, fast-fail stays armed
	started = time.Now()
	got, err = resolveCanonicalPath(target)
	if err != nil {
		t.Fatalf("resolve after timeout: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Millisecond {
		t.Fatalf("degraded-first call must not wait on a walk, took %v", elapsed)
	}
	if got != cleanTarget {
		t.Fatalf("degraded-first call must serve the cleaned input, got %q want %q", got, cleanTarget)
	}

	want := filepath.Join(alt, "leaf")
	deadline := time.Now().Add(3 * time.Second)
	for {
		time.Sleep(15 * time.Millisecond)
		got, err = resolveCanonicalPath(target)
		if err == nil && got == filepath.Clean(want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background walk never landed the resolved identity: last %q, want %q", got, want)
		}
	}
	if evalCalls == 0 {
		t.Fatalf("fixture broken: the real-ancestor eval branch never ran")
	}
}

// TestCanonicalFastFailDisarmsAfterQuietWindow pins the exit: a quiet window
// lets normal walks resume instead of degrading forever.
func TestCanonicalFastFailDisarmsAfterQuietWindow(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatalf("write probe file: %v", err)
	}
	previousTTL, previousPositiveTTL := canonicalCacheTTL, canonicalCachePositiveTTL
	canonicalCacheTTL = time.Millisecond
	canonicalCachePositiveTTL = time.Millisecond
	defer func() {
		canonicalCacheTTL = previousTTL
		canonicalCachePositiveTTL = previousPositiveTTL
	}()
	previousWindow := canonicalFastFailWindow
	canonicalFastFailWindow = 30 * time.Millisecond
	defer func() { canonicalFastFailWindow = previousWindow }()
	sweepCanonicalFastFailStateForTest()
	defer sweepCanonicalFastFailStateForTest()
	// Arm the window directly: this pin tests the exit, not the arming path.
	canonicalFastFailUntil.Store(time.Now().Add(canonicalFastFailWindow).UnixNano())

	walksBefore := probeWalks.Load()
	if _, err := resolveCanonicalPath(probe); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if probeWalks.Load() != walksBefore {
		t.Fatalf("armed fast-fail must serve without a walk")
	}
	time.Sleep(40 * time.Millisecond) // window quiets
	if _, err := resolveCanonicalPath(probe); err != nil {
		t.Fatalf("resolve after window: %v", err)
	}
	if probeWalks.Load() == walksBefore {
		t.Fatalf("quiet window must let normal walks resume")
	}
}
