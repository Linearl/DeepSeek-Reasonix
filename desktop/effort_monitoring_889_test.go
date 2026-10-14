package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Task 889 acceptance tests: every segment the congestion-window question
// needs must be assertable from emitted evidence; pure observability, no
// verdict/fallback/value path is exercised here.

// captureSlog redirects the default logger into a buffer for one test.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestReconcileObservedSplitsMemoKeyFromWalk pins the reconcile observation
// contract the breakdown line rides on: the first pass is a memo miss (no
// walk sub-phases to report beyond the memo check itself), the settled pass
// reports memoHit with a measured memo-key cost and zero walk — which is
// exactly the "settled tab 跳过路径此前无观测" gap task 889 closes.
func TestReconcileObservedSplitsMemoKeyFromWalk(t *testing.T) {
	app, tab, _ := newReconcileMemoFixture(t)

	var marks []string
	mark := func(phase string) { marks = append(marks, phase) }

	// First pass: a zero-value memo cannot be fresh — miss + memo-first
	// marks. No memoMs magnitude assert: this machine's ~ms clock lets a
	// healthy key span read 0s (pinned in TestEffortReadBreakdownLogThreshold).
	_, _, obs := app.reconcileTabWithPinnedSessionMetaObserved(tab, mark)
	if obs.memoHit {
		t.Fatal("first reconcile reported a memo hit against a zero-value memo")
	}
	if len(marks) == 0 || marks[0] != "memo" {
		t.Fatalf("phase trail marks start with %v, want memo first", marks)
	}

	// Settled pass: the memo serves, walk is zero by construction — the
	// "settled tab 跳过路径此前无观测" gap task 889 closes is exactly this
	// memoHit=true attribution.
	marks = nil
	_, _, obs = app.reconcileTabWithPinnedSessionMetaObserved(tab, mark)
	if !obs.memoHit {
		t.Fatal("settled reconcile missed the fresh memo")
	}
	if obs.walkMs != 0 {
		t.Fatalf("settled memo hit reports walkMs=%v, want 0", obs.walkMs)
	}
	if len(marks) != 1 || marks[0] != "memo" {
		t.Fatalf("settled path marks = %v, want only memo (no walk sub-steps)", marks)
	}

	// A registry rewrite busts the memo (freshness contract): the next pass
	// is a miss again — the observation follows the memo, it does not cache.
	rewriteProjectsRegistry(t)
	_, _, obs = app.reconcileTabWithPinnedSessionMetaObserved(tab, mark)
	if obs.memoHit {
		t.Fatal("reconcile still reports a memo hit after the registry changed")
	}
}

// TestEffortReadMarksPhasesOnTrail runs the real read path against a
// registered trail and asserts every top-level segment announces itself —
// reconcile (with its memo sub-step), snapshot (with the cold-load sub-step),
// model, done — plus after_timeout=false for a read that completed. This is
// the end-to-end "打点断言" for the breakdown/trail wiring.
func TestEffortReadMarksPhasesOnTrail(t *testing.T) {
	app, tab, _ := newSnapshotEffortFixture(t, snapshotTestConfig("low", "high"))

	gen := app.beginEffortReadPhase(tab.ID)
	t.Cleanup(func() { app.finishEffortReadPhase(tab.ID, gen) })

	// Cold read: the first snapshot access pays the load, so the trail must
	// show the snapshot.load sub-step too.
	if got := effortDirectRead(app, tab.ID); !got.Supported {
		t.Fatalf("prime read = %+v, want supported", got)
	}
	rendered := app.effortPhaseTrailString(tab.ID, gen)
	for _, phase := range []string{"start=", "reconcile=", "memo=", "snapshot=", "snapshot.load=", "model=", "done="} {
		if !strings.Contains(rendered, phase) {
			t.Fatalf("phase trail %q missing %q", rendered, phase)
		}
	}
	if app.effortReadAfterTimeout(tab.ID) {
		t.Fatal("completed read reports after_timeout")
	}

	// Warm read: no reload, so snapshot.load must drop out of the trail
	// while the top-level segments remain.
	app.finishEffortReadPhase(tab.ID, gen)
	gen = app.beginEffortReadPhase(tab.ID)
	t.Cleanup(func() { app.finishEffortReadPhase(tab.ID, gen) })
	if got := effortDirectRead(app, tab.ID); !got.Supported {
		t.Fatalf("warm read = %+v, want supported", got)
	}
	rendered = app.effortPhaseTrailString(tab.ID, gen)
	if strings.Contains(rendered, "snapshot.load") {
		t.Fatalf("warm read trail claims a load: %q", rendered)
	}
	for _, phase := range []string{"reconcile=", "snapshot.stat=", "model="} {
		if !strings.Contains(rendered, phase) {
			t.Fatalf("warm phase trail %q missing %q", rendered, phase)
		}
	}
}

// TestCachedConfigForRootTimedSplitsWaitStatLoad pins the snapshot timing
// split: a cold access must record the load it paid, a warm one must record
// zero load (proof the 609 snapshot still short-circuits), and both must
// announce their sub-steps through the mark callback — the segment contract
// the phase trail rides on. Magnitude assertions stay on the load only: this
// machine's clock has ~ms granularity, so the µs-scale stat span can
// legitimately measure 0 (see the granularity note in the reconcile test).
func TestCachedConfigForRootTimedSplitsWaitStatLoad(t *testing.T) {
	app, tab, _ := newSnapshotEffortFixture(t, snapshotTestConfig("low", "high"))
	// The exact root the effort read resolves against (an unset global tab
	// reads root ""), so this measures the snapshot that read would hit.
	root := tab.WorkspaceRoot

	var coldMarks []string
	_, cold, err := app.cachedConfigForRootTimed(root, func(segment string) {
		coldMarks = append(coldMarks, segment)
	})
	if err != nil {
		t.Fatalf("cold timed read: %v", err)
	}
	if cold.load <= 0 {
		t.Fatalf("cold snapshot reports no load: %+v", cold)
	}
	if strings.Join(coldMarks, ",") != "stat,load" {
		t.Fatalf("cold sub-step marks = %v, want [stat load]", coldMarks)
	}

	var warmMarks []string
	_, warm, err := app.cachedConfigForRootTimed(root, func(segment string) {
		warmMarks = append(warmMarks, segment)
	})
	if err != nil {
		t.Fatalf("warm timed read: %v", err)
	}
	if warm.load != 0 {
		t.Fatalf("warm snapshot paid a load: %+v", warm)
	}
	if strings.Join(warmMarks, ",") != "stat" {
		t.Fatalf("warm sub-step marks = %v, want [stat] (no reload)", warmMarks)
	}
}

// TestEffortTimeoutWarnCarriesPhaseTrail is the task-889 headline: when the
// 2s cap fires, the warn line names the phase the read is standing in and
// how long it has been there, and the trail is flagged so the eventual
// breakdown pairs back. Completion then drops the record — the in-flight
// state never outlives the attempt.
func TestEffortTimeoutWarnCarriesPhaseTrail(t *testing.T) {
	f := newEffortFetchFixture(t)
	buf := captureSlog(t)

	f.app.effortReadStub = func(tabID string) EffortInfo {
		f.app.effortPhaseMark(tabID, "reconcile")
		f.app.effortPhaseMark(tabID, "reconcile.path")
		time.Sleep(400 * time.Millisecond)
		return effortInfoFor("low")
	}

	// No cache primed: the timeout branch serves the defined default.
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "auto" {
		t.Fatalf("timeout fallback current = %q, want auto (no cache primed)", got.Current)
	}
	line := buf.String()
	if !strings.Contains(line, "effort read timed out") {
		t.Fatalf("no timeout warn logged: %s", line)
	}
	if !strings.Contains(line, `"phases"`) ||
		!strings.Contains(line, "reconcile.path=") ||
		!strings.Contains(line, "start=") {
		t.Fatalf("timeout warn missing phase trail: %s", line)
	}
	// The pairing flag is live while the abandoned read still runs — the
	// eventual breakdown line reads it and emits after_timeout=true.
	if !f.app.effortReadAfterTimeout(f.tab.ID) {
		t.Fatal("timeout did not flag the in-flight trail for after_timeout pairing")
	}

	// The attempt's record is dropped when the abandoned read completes.
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.app.effortPhase.mu.Lock()
		pending := len(f.app.effortPhase.trails)
		f.app.effortPhase.mu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("phase trail still registered %ds after completion", 3)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestEffortPhaseTrailGenerationOwnsReplacement keeps two concurrent
// attempts for one tab honest: a newer attempt replaces the record, the
// stale attempt's render/flag/drop are generation-checked into no-ops, and
// a mark with no trail at all (a non-EffortForTab read) stays silent.
func TestEffortPhaseTrailGenerationOwnsReplacement(t *testing.T) {
	app := NewApp()

	// No registered trail: marks are no-ops, render is empty.
	app.effortPhaseMark("tab_gen", "reconcile")
	if got := app.effortPhaseTrailString("tab_gen", 0); got != "" {
		t.Fatalf("render without a trail = %q, want empty", got)
	}

	stale := app.beginEffortReadPhase("tab_gen")
	current := app.beginEffortReadPhase("tab_gen")
	if stale == current {
		t.Fatalf("generation did not advance: both %d", stale)
	}

	// The stale attempt can neither read nor flag the successor's record.
	if got := app.effortPhaseTrailString("tab_gen", stale); got != "" {
		t.Fatalf("stale generation rendered %q, want empty", got)
	}
	app.markEffortReadTimedOut("tab_gen", stale)
	if app.effortReadAfterTimeout("tab_gen") {
		t.Fatal("stale generation flagged the current attempt's trail")
	}
	app.finishEffortReadPhase("tab_gen", stale)
	if got := app.effortPhaseTrailString("tab_gen", current); got == "" {
		t.Fatal("stale finish deleted the current attempt's trail")
	}

	// The current generation owns render, flag, and drop.
	app.markEffortReadTimedOut("tab_gen", current)
	if !app.effortReadAfterTimeout("tab_gen") {
		t.Fatal("current generation's flag not visible")
	}
	app.finishEffortReadPhase("tab_gen", current)
	if got := app.effortPhaseTrailString("tab_gen", current); got != "" {
		t.Fatalf("trail survived its own finish: %q", got)
	}
}
