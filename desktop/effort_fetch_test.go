package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
)

// effortFetchFixture builds a minimal App with one tab whose sink forwards
// notices into a channel, plus a stubbed effort source and a shortened
// timeout, so the task-421 bounded fetch is testable in milliseconds without
// real config disk I/O.
type effortFetchFixture struct {
	app    *App
	tab    *WorkspaceTab
	notice chan event.Event
}

func newEffortFetchFixture(t *testing.T) *effortFetchFixture {
	t.Helper()
	app := NewApp()
	app.ctx = context.Background()
	app.effortReadLimitOverride = 100 * time.Millisecond
	tab := &WorkspaceTab{
		ID:          "tab_effort_fetch",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	installNoopRuntimeEvents(app, tab.sink)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	f := &effortFetchFixture{app: app, tab: tab, notice: make(chan event.Event, 8)}
	tab.sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			f.notice <- e
		}
	}))
	t.Cleanup(func() { app.effortReadStub = nil })
	return f
}

func effortInfoFor(level string) EffortInfo {
	return EffortInfo{Supported: true, Current: level, Levels: []string{"low", "high"}}
}

// TestEffortReadBreakdownLogThreshold gates the task-639 breakdown threshold
// semantics: the log line is an evidence channel for slow reads, so it must
// stay silent at 100ms and fire at 200ms — proving the threshold compares
// milliseconds, not raw durations (a Duration-vs-untyped-const slip would
// turn the gate into 150ns and log every warm read). Task 889: the line now
// also carries the reconcile sub-split (memo vs walk), the snapshot sub-split
// (wait/stat/load) and the after_timeout pairing — asserted here so a field
// can never silently drop out of the evidence channel.
func TestEffortReadBreakdownLogThreshold(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logEffortReadBreakdown(effortReadBreakdown{
		tabID:     "tab_threshold",
		total:     100 * time.Millisecond,
		reconcile: 90 * time.Millisecond,
		snapshot:  10 * time.Millisecond,
	})
	if buf.Len() != 0 {
		t.Fatalf("breakdown logged below threshold: %s", buf.String())
	}

	logEffortReadBreakdown(effortReadBreakdown{
		tabID:            "tab_threshold",
		total:            200 * time.Millisecond,
		reconcile:        190 * time.Millisecond,
		memo:             40 * time.Millisecond,
		walk:             150 * time.Millisecond,
		memoHit:          false,
		snapshot:         10 * time.Millisecond,
		snapshotWait:     3 * time.Millisecond,
		snapshotStat:     4 * time.Millisecond,
		snapshotLoad:     3 * time.Millisecond,
		resolve:          0,
		snapshotReloaded: false,
		afterTimeout:     true,
	})
	line := buf.String()
	if !strings.Contains(line, "effort read breakdown") {
		t.Fatalf("no breakdown line at/above threshold, got %q", line)
	}
	for _, field := range []string{
		`"reconcile_ms":190`,
		`"snapshot_reloaded":false`,
		// Task 889 sub-splits: without these the congestion-window question
		// "reconcile 是 memo key 还是 walk？snapshot 是等锁还是 load？"
		// would still need cross-reading the phase logs.
		`"memo_ms":40`,
		`"reconcile_walk_ms":150`,
		`"memo_hit":false`,
		`"snapshot_wait_ms":3`,
		`"snapshot_stat_ms":4`,
		`"snapshot_load_ms":3`,
		`"after_timeout":true`,
	} {
		if !strings.Contains(line, field) {
			t.Fatalf("breakdown line missing %s: %s", field, line)
		}
	}
}

// waitForNotice asserts exactly one notice whose text contains all fragments
// arrives; extra notices or silence fail the test.
func (f *effortFetchFixture) waitForNotice(t *testing.T, fragments ...string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-f.notice:
			if e.Kind != event.Notice {
				continue
			}
			match := true
			for _, fragment := range fragments {
				if !strings.Contains(e.Text, fragment) {
					match = false
					break
				}
			}
			if match {
				return
			}
			t.Fatalf("unexpected notice text %q, want fragments %v", e.Text, fragments)
		case <-deadline:
			t.Fatalf("no notice containing %v arrived", fragments)
		}
	}
}

// assertNoNotice proves the normal (non-timeout) path stays silent.
func (f *effortFetchFixture) assertNoNotice(t *testing.T) {
	t.Helper()
	select {
	case e := <-f.notice:
		t.Fatalf("unexpected notice on normal path: %q", e.Text)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestEffortForTabTimeoutServesCachedValue covers the task-1161 fast path and
// the task-421 degradation contract behind it. With a servable cache entry
// (fingerprint unchanged, age in window) a hung source costs the caller
// nothing: the cached value serves instantly, no notice. With the fast path
// expired, the bounded fetch serves the cached value within the cap and the
// fallback leaves a notice behind.
func TestEffortForTabTimeoutServesCachedValue(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("priming read current = %q, want high", got.Current)
	}

	// Task 1161: the source hangs far beyond the (shortened) cap, but the
	// cache entry is fresh and its inputs unchanged — the read must not wait
	// at all.
	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second)
		return effortInfoFor("low")
	}
	started := time.Now()
	got := f.app.EffortForTab(f.tab.ID)
	elapsed := time.Since(started)
	if got.Current != "high" {
		t.Fatalf("fast-path current = %q, want cached high", got.Current)
	}
	if elapsed >= f.app.effortReadLimit() {
		t.Fatalf("EffortForTab took %s with a servable cache entry, want an instant serve", elapsed)
	}
	f.assertNoNotice(t)

	// Expire the fast path: the same hung source now hits the task-421
	// bounded fetch, which serves the cached value within the cap, with
	// notice.
	f.app.effortStaleServeAgeOverride = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	started = time.Now()
	got = f.app.EffortForTab(f.tab.ID)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("degraded EffortForTab took %s, want bounded by the cap", elapsed)
	}
	if got.Current != "high" {
		t.Fatalf("fallback current = %q, want cached high", got.Current)
	}
	f.waitForNotice(t, "timed out", "last known value")
}

// TestEffortForTabTimeoutWithoutCacheServesDefault covers acceptance 2: on
// the first read there is no cached value - the fetch must still return
// promptly with a defined default (auto / unsupported) plus a notice, never
// block, and never fabricate a supported level.
func TestEffortForTabTimeoutWithoutCacheServesDefault(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second)
		return effortInfoFor("high")
	}
	started := time.Now()
	got := f.app.EffortForTab(f.tab.ID)
	elapsed := time.Since(started)

	if got.Supported {
		t.Fatal("timeout default must not claim support")
	}
	if got.Current != "auto" || len(got.Levels) != 0 {
		t.Fatalf("timeout default = %+v, want Current auto with empty levels", got)
	}
	if elapsed >= time.Second {
		t.Fatalf("EffortForTab took %s on cold cache, want bounded by the cap", elapsed)
	}
	f.waitForNotice(t, "timed out", "default")
}

// TestEffortForTabNormalReadRefreshesCache covers acceptance 3: a healthy
// read returns the fresh value, refreshes the cache (a later hung read serves
// that newest value from the fast path, then from the background refresh),
// and emits no notice. Task 1161: a source change with an in-window cache
// entry fast-serves the cached value until the window lapses or the refresh
// lands — the second read expires the fast path explicitly to prove the
// synchronous read still tracks the source.
func TestEffortForTabNormalReadRefreshesCache(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("low") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "low" {
		t.Fatalf("first read current = %q, want low", got.Current)
	}
	f.assertNoNotice(t)

	// Source moves; with the fast path expired the synchronous read must
	// serve the new value (and re-prime the cache with it)...
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	f.app.effortStaleServeAgeOverride = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("second read current = %q, want fresh high", got.Current)
	}
	f.assertNoNotice(t)

	// ...and the cache must now hold that newest value, not the first one.
	// Task 1161: the hung read cannot answer synchronously (the entry is
	// fresh and unchanged), so it serves the cached high instantly while the
	// single-flight background refresh recomputes; once it lands, the cache
	// holds low and the next read serves it.
	f.app.effortStaleServeAgeOverride = 0 // restore the production window
	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second)
		return effortInfoFor("low")
	}
	started := time.Now()
	got := f.app.EffortForTab(f.tab.ID)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("EffortForTab took %s, want an instant cache serve", elapsed)
	}
	if got.Current != "high" {
		t.Fatalf("fast-path current = %q, want the still-cached high", got.Current)
	}
	f.assertNoNotice(t)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cached, _, ok := f.app.cachedEffortForTab(f.tab.ID); ok && cached.Current == "low" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("low") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "low" {
		t.Fatalf("post-refresh current = %q, want the refreshed low", got.Current)
	}
}

// TestEffortForTabFreshCacheInvalidatedByEffortSwitch pins the red line of
// the task-1161 fast path: an effort switch rewrites the tab's effort
// override, the fingerprint no longer matches the cache, and the read must go
// synchronous — a switched-to value can never be answered with the
// pre-switch cached one (that would read as "the switch did not take").
func TestEffortForTabFreshCacheInvalidatedByEffortSwitch(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("priming read current = %q, want high", got.Current)
	}

	low := "low"
	f.app.mu.Lock()
	f.tab.effort = &low
	f.app.mu.Unlock()
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("low") }

	served := make(chan EffortInfo, 1)
	go func() { served <- f.app.EffortForTab(f.tab.ID) }()
	select {
	case got := <-served:
		if got.Current != "low" {
			t.Fatalf("post-switch read current = %q, want the synchronous low", got.Current)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-switch read never returned")
	}
	f.assertNoNotice(t)
}

// TestEffortForTabStaleServeSingleFlightsRefresh pins the storm half of the
// task-1161 fast path: N reads served from cache while a refresh is in
// flight spawn ONE background recompute, not N.
func TestEffortForTabStaleServeSingleFlightsRefresh(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("priming read current = %q, want high", got.Current)
	}

	var refreshes atomic.Int32
	release := make(chan struct{})
	f.app.effortReadStub = func(string) EffortInfo {
		refreshes.Add(1)
		<-release
		return effortInfoFor("low")
	}
	for i := 0; i < 5; i++ {
		if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
			t.Fatalf("fast-path read %d current = %q, want cached high", i, got.Current)
		}
	}
	time.Sleep(100 * time.Millisecond) // let any stampede surface
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("background refreshes = %d after 5 served reads, want 1 (single flight did not collapse)", got)
	}
	close(release)
}

// effortFastServeReadBudgetMs is the per-read ceiling for ten consecutive
// switch-tab effort reads against a pathologically slow source, encoding the
// task-1161 acceptance "10 switches, all < 1s" as an in-repo gate. Each read
// simulates the memo-miss + slow-walk shape (a multi-second recompute); the
// fast path must answer every one from cache in microseconds.
const effortFastServeReadBudgetMs = 1000

// TestEffortSwitchTenReadsAllUnderBudget is the task-1161 acceptance gate:
// with a servable cache entry, ten consecutive effort reads against a source
// that takes seconds each stay far under one second apiece — before 1161
// every one of these waited out the full 2s cap (and showed the stale value
// as a degradation notice instead of a normal serve).
func TestEffortSwitchTenReadsAllUnderBudget(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadLimitOverride = 2 * time.Second
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("priming read current = %q, want high", got.Current)
	}

	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(3 * time.Second) // memo-miss + slow reconcile-walk shape
		return effortInfoFor("low")
	}
	worst := time.Duration(0)
	for i := 0; i < 10; i++ {
		start := time.Now()
		got := f.app.EffortForTab(f.tab.ID)
		if got.Current != "high" {
			t.Fatalf("read %d current = %q, want cached high", i, got.Current)
		}
		if elapsed := time.Since(start); elapsed > worst {
			worst = elapsed
		}
	}
	t.Logf("worst of 10 served reads against a 3s source: %s", worst)
	if worst.Milliseconds() >= effortFastServeReadBudgetMs {
		t.Fatalf("worst read %s >= %dms budget (fast path failed to serve from cache)", worst, effortFastServeReadBudgetMs)
	}
	f.assertNoNotice(t)
}
