package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// contextUsageFixture builds a minimal App with one tab whose non-nil
// controller lets the bounded gauge read run, a sink that forwards notices
// into a channel, and a shortened timeout — so the task-882 bounded
// context-usage read is testable in milliseconds without a real transcript.
type contextUsageFixture struct {
	app    *App
	tab    *WorkspaceTab
	notice chan event.Event
	logs   bytes.Buffer
}

func newContextUsageFixture(t *testing.T) *contextUsageFixture {
	t.Helper()
	app := NewApp()
	app.ctx = context.Background()
	app.contextUsageReadLimitOverride = 100 * time.Millisecond
	dir := t.TempDir()
	ag := agent.New(usageProvider{usage: &provider.Usage{}}, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	tab := &WorkspaceTab{
		ID:          "tab_context_fetch",
		Scope:       "global",
		Ready:       true,
		Ctrl:        control.New(control.Options{Executor: ag, Sink: event.Discard, SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl")}),
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	installNoopRuntimeEvents(app, tab.sink)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	f := &contextUsageFixture{app: app, tab: tab, notice: make(chan event.Event, 8)}
	tab.sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			f.notice <- e
		}
	}))
	// Capture the slog warn lines so the fallback leaves evidence in both
	// channels the operator can see: the structured log and the tab notice.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&f.logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		app.contextUsageReadStub = nil
	})
	return f
}

// contextComputeFor returns a distinguishable gauge read: used is the marker
// the assertions track, and the maintenance panel is populated so a fallback
// that blanked it would fail the comparison.
func contextComputeFor(used int) contextUsageCompute {
	return contextUsageCompute{
		used:         used,
		window:       200_000,
		compactRatio: 0.8,
		maintenance:  contextMaintenanceInfo(agent.ContextMaintenanceSnapshot{CanonicalTokens: used * 2, ProjectedTokens: used}),
	}
}

// waitForNotice asserts exactly one notice whose text contains all fragments
// arrives; extra notices or silence fail the test.
func (f *contextUsageFixture) waitForNotice(t *testing.T, fragments ...string) {
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
func (f *contextUsageFixture) assertNoNotice(t *testing.T) {
	t.Helper()
	select {
	case e := <-f.notice:
		t.Fatalf("unexpected notice on normal path: %q", e.Text)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestContextUsageForTabTimeoutServesCachedValue covers acceptance 1+2: a
// slow controller read must not hold the caller beyond the cap - the cached
// last gauge is served (the sidebar may briefly show the cached value, that
// is the defined degraded semantics) and the fallback leaves both a warn log
// (stage-tagged to pair with the frontend
// "switch-tab:ancillary context" breadcrumb) and a tab notice.
func TestContextUsageForTabTimeoutServesCachedValue(t *testing.T) {
	f := newContextUsageFixture(t)
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute { return contextComputeFor(1234) }
	if got := f.app.ContextUsageForTab(f.tab.ID); got.Used != 1234 {
		t.Fatalf("priming read used = %d, want 1234", got.Used)
	}

	// Now the source hangs far beyond the (shortened) cap.
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute {
		time.Sleep(2 * time.Second)
		return contextComputeFor(999)
	}
	started := time.Now()
	got := f.app.ContextUsageForTab(f.tab.ID)
	elapsed := time.Since(started)

	if got.Used != 1234 {
		t.Fatalf("fallback used = %d, want cached 1234", got.Used)
	}
	if got.Window != 200_000 || got.Maintenance == nil {
		t.Fatalf("fallback gauge = used %d window %d maintenance %v, want full cached compute", got.Used, got.Window, got.Maintenance)
	}
	// Bounded: a broken build that waited on the source would burn the full
	// 2s hang here; the cap plus dispatch overhead must stay near 100ms.
	if elapsed >= time.Second {
		t.Fatalf("ContextUsageForTab took %s, want well under the 2s hang (timeout cap broken)", elapsed)
	}
	f.waitForNotice(t, "timed out", "last known value")
	if line := f.logs.String(); !strings.Contains(line, "context usage read timed out") || !strings.Contains(line, "switch-tab:ancillary context") {
		t.Fatalf("warn log missing timeout line or ancillary stage tag: %s", line)
	}
}

// TestContextUsageForTabTimeoutWithoutCacheServesDefault covers the cold
// path: on the first read there is no cached value - the fetch must still
// return promptly with the defined default (used/window zero = the gauge
// hides itself, no fabricated maintenance panel), plus warn log and notice.
func TestContextUsageForTabTimeoutWithoutCacheServesDefault(t *testing.T) {
	f := newContextUsageFixture(t)
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute {
		time.Sleep(2 * time.Second)
		return contextComputeFor(1234)
	}
	started := time.Now()
	got := f.app.ContextUsageForTab(f.tab.ID)
	elapsed := time.Since(started)

	if got.Used != 0 || got.Window != 0 {
		t.Fatalf("timeout default = used %d window %d, want zeros (gauge hides itself)", got.Used, got.Window)
	}
	if got.Maintenance != nil || got.ContextBudget != nil {
		t.Fatalf("timeout default must not fabricate panels: %+v", got)
	}
	if elapsed >= time.Second {
		t.Fatalf("ContextUsageForTab took %s on cold cache, want bounded by the cap", elapsed)
	}
	f.waitForNotice(t, "timed out", "default")
	if line := f.logs.String(); !strings.Contains(line, "no cached value, serving default") {
		t.Fatalf("warn log missing cold-cache timeout line: %s", line)
	}
}

// TestContextUsageForTabNormalReadRefreshesCache covers acceptance 3: a
// healthy read returns the fresh value (behaviorally unchanged), refreshes
// the cache (a later timeout must fall back to the newest value), and emits
// no notice and no warn log.
func TestContextUsageForTabNormalReadRefreshesCache(t *testing.T) {
	f := newContextUsageFixture(t)
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute { return contextComputeFor(100) }
	if got := f.app.ContextUsageForTab(f.tab.ID); got.Used != 100 {
		t.Fatalf("first read used = %d, want 100", got.Used)
	}
	f.assertNoNotice(t)

	// Source moves; the next healthy read must serve the new value...
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute { return contextComputeFor(200) }
	if got := f.app.ContextUsageForTab(f.tab.ID); got.Used != 200 {
		t.Fatalf("second read used = %d, want fresh 200", got.Used)
	}
	f.assertNoNotice(t)

	// ...and the cache must now hold that newest value, not the first one.
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute {
		time.Sleep(2 * time.Second)
		return contextComputeFor(100)
	}
	started := time.Now()
	got := f.app.ContextUsageForTab(f.tab.ID)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("ContextUsageForTab took %s, want bounded by the cap", elapsed)
	}
	if got.Used != 200 {
		t.Fatalf("fallback used = %d, want refreshed cache 200", got.Used)
	}
	if line := f.logs.String(); !strings.Contains(line, "context usage read timed out") {
		t.Fatalf("warn log missing timeout line after cache refresh: %s", line)
	}
	f.waitForNotice(t, "timed out", "last known value")
}

// TestContextUsageForTabFastPathKeepsTelemetryLive pins the split: the
// session telemetry fields stay on the synchronous fast path and are never
// served from the gauge cache, so a cache fallback still reports fresh
// cost/totals next to the cached gauge.
func TestContextUsageForTabFastPathKeepsTelemetryLive(t *testing.T) {
	f := newContextUsageFixture(t)
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute { return contextComputeFor(100) }
	if got := f.app.ContextUsageForTab(f.tab.ID); got.Used != 100 {
		t.Fatalf("priming read used = %d, want 100", got.Used)
	}

	// Telemetry moves after the gauge was cached; a later (timed-out) read
	// must still report the fresh telemetry alongside the cached gauge.
	f.tab.recordUsage(event.Event{
		Usage:       &provider.Usage{TotalTokens: 140, RequestCount: 1},
		UsageSource: event.UsageSourceExecutor,
	})
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute {
		time.Sleep(2 * time.Second)
		return contextComputeFor(100)
	}
	got := f.app.ContextUsageForTab(f.tab.ID)
	if got.Used != 100 {
		t.Fatalf("fallback used = %d, want cached 100", got.Used)
	}
	if got.SessionTokens != 140 {
		t.Fatalf("session tokens = %d, want fresh 140 (telemetry must not be cached)", got.SessionTokens)
	}
}
