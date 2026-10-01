package main

import (
	"context"
	"strings"
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

// TestEffortForTabTimeoutServesCachedValue covers acceptance 1: a hung effort
// source must not block the caller beyond the cap - the cached last value is
// served and the fallback leaves a notice behind.
func TestEffortForTabTimeoutServesCachedValue(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("priming read current = %q, want high", got.Current)
	}

	// Now the source hangs far beyond the (shortened) cap.
	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second)
		return effortInfoFor("low")
	}
	started := time.Now()
	got := f.app.EffortForTab(f.tab.ID)
	elapsed := time.Since(started)

	if got.Current != "high" {
		t.Fatalf("fallback current = %q, want cached high", got.Current)
	}
	// Bounded: a broken build that waited on the source would burn the full
	// 2s hang here; the cap plus dispatch overhead must stay near 100ms.
	if elapsed >= time.Second {
		t.Fatalf("EffortForTab took %s, want well under the 2s hang (timeout cap broken)", elapsed)
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
// read returns the fresh value, refreshes the cache (a later timeout must
// fall back to the newest value), and emits no notice.
func TestEffortForTabNormalReadRefreshesCache(t *testing.T) {
	f := newEffortFetchFixture(t)
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("low") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "low" {
		t.Fatalf("first read current = %q, want low", got.Current)
	}
	f.assertNoNotice(t)

	// Source moves; the next healthy read must serve the new value...
	f.app.effortReadStub = func(string) EffortInfo { return effortInfoFor("high") }
	if got := f.app.EffortForTab(f.tab.ID); got.Current != "high" {
		t.Fatalf("second read current = %q, want fresh high", got.Current)
	}
	f.assertNoNotice(t)

	// ...and the cache must now hold that newest value, not the first one.
	f.app.effortReadStub = func(string) EffortInfo {
		time.Sleep(2 * time.Second)
		return effortInfoFor("low")
	}
	started := time.Now()
	got := f.app.EffortForTab(f.tab.ID)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("EffortForTab took %s, want bounded by the cap", elapsed)
	}
	if got.Current != "high" {
		t.Fatalf("fallback current = %q, want refreshed cache high", got.Current)
	}
	f.waitForNotice(t, "timed out", "last known value")
}
