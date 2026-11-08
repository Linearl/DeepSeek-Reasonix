package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// waitForEffortSettled waits out the background effort rebuild for tabID
// (tl1388): a switch that declined the fast paths returns from
// SetEffortForTab before the swap lands. When this returns, every queued
// rebuild for the tab has settled — swapped, served by a fast path on
// re-check, or failed. Call it from the main test goroutine; a stuck rebuild
// fails the test at the deadline.
func waitForEffortSettled(t *testing.T, a *App, tabID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for a.effortRebuildInFlight(tabID) {
		if time.Now().After(deadline) {
			t.Fatalf("async effort rebuild for %s did not settle in time", tabID)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// asyncEffortTestConfig saves the official-DeepSeek config the jobs-handoff
// harness uses: the declared vocabulary keeps NormalizeEffort accepting every
// tier, while the stub executor's nil provider exposes no per-request
// vocabulary — both fast paths decline and the switch takes the fallback.
func asyncEffortTestConfig(t *testing.T) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "official-ds/deepseek-v4-pro"
	cfg.Desktop.ProviderAccess = []string{"official-ds"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "official-ds", Kind: "openai", BaseURL: "https://api.deepseek.com",
			Model: "deepseek-v4-pro", Models: []string{"deepseek-v4-pro"},
			SupportedEfforts: []string{"disabled", "low", "high", "max"},
			APIKeyEnv:        "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if dir := config.SessionDir(); os.MkdirAll(dir, 0o755) != nil {
		t.Fatalf("mkdir session dir")
	}
}

// asyncEffortTab builds the harness tab: a stub controller whose executor has
// no per-request vocabulary (fast paths decline) over the saved official-ds
// config, with a runtime-event recorder wired on both lanes. The outgoing
// controller comes back too so tests can assert the swap.
func asyncEffortTab(t *testing.T, app *App, dir string, record func(name string)) (*WorkspaceTab, *control.Controller) {
	t.Helper()
	oldPath := filepath.Join(dir, "old.jsonl")
	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})
	t.Cleanup(oldCtrl.Close)

	app.readyHook = func() {}
	tab := reloadRuntimeTab(t, app, dir, oldCtrl)
	tab.sink = &tabEventSink{tabID: tab.ID, app: app, ctx: context.Background()}
	tab.sink.runtimeEvents.emit = func(_ context.Context, name string, _ ...any) { record(name) }
	// The stub executor's provider exposes no per-request vocabulary, so the
	// fast paths decline and the fallback (async since tl1388) runs.
	tab.model = "official-ds/deepseek-v4-pro"
	return tab, oldCtrl
}

// TestSetEffortForTabAsyncFallbackReturnsBeforeSwap pins tl1388 acceptance ②:
// a switch both fast paths decline returns from SetEffortForTab without
// holding the call — and the composer with it — for the runtime rebuild. The
// spawn is logged synchronously (path=fallback-async), the rebuild settles in
// the background (effortRebuildInFlight drains, replacement controller
// installed, depth landed, runtime:rebuilt fence emitted).
func TestSetEffortForTabAsyncFallbackReturnsBeforeSwap(t *testing.T) {
	asyncEffortTestConfig(t)
	var mu sync.Mutex
	var eventNames []string
	app := NewApp()
	app.ctx = context.Background()
	app.runtimeEvents.emit = func(_ context.Context, name string, _ ...any) {
		mu.Lock()
		eventNames = append(eventNames, name)
		mu.Unlock()
	}
	buf := captureSlog(t)
	dir := config.SessionDir()
	tab, oldCtrl := asyncEffortTab(t, app, dir, func(name string) {
		mu.Lock()
		eventNames = append(eventNames, name)
		mu.Unlock()
	})

	started := time.Now()
	if err := app.SetEffortForTab(tab.ID, "low"); err != nil {
		t.Fatalf("declined effort switch returned an error: %v", err)
	}
	// The spawn line is synchronous with the call: the rebuild is already
	// scheduled in the background while this assertion runs. captureSlog
	// installs the JSON handler (same shape the cross-map pins assert).
	spawnLog := buf.String()
	if !strings.Contains(spawnLog, `"path":"fallback-async"`) && !strings.Contains(spawnLog, "path=fallback-async") {
		t.Fatalf("declined switch did not log the async spawn line:\n%s", spawnLog)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("SetEffortForTab took %s; acceptance ② is a non-blocking return", elapsed)
	}

	waitForEffortSettled(t, app, tab.ID)

	newCtrl, ok := tab.Ctrl.(*control.Controller)
	if !ok || newCtrl == oldCtrl {
		t.Fatalf("tab.Ctrl = %T, want the rebuilt replacement controller", tab.Ctrl)
	}
	app.mu.RLock()
	effort := tab.effort
	app.mu.RUnlock()
	if effort == nil || *effort != "low" {
		t.Fatalf("tab.effort after the async rebuild = %v, want low", effort)
	}
	mu.Lock()
	defer mu.Unlock()
	hasFence := false
	for _, name := range eventNames {
		if name == "runtime:rebuilt" {
			hasFence = true
		}
	}
	if !hasFence {
		t.Fatalf("runtime:rebuilt fence missing after the async swap: %v", eventNames)
	}
	// The rebuild itself ran (the build-end line exists only after a real
	// runtime build; fast paths never produce it).
	if !strings.Contains(buf.String(), "desktop: runtime build end") {
		t.Fatalf("runtime build end line missing — the rebuild never ran:\n%s", buf.String())
	}
}

// TestSetEffortForTabTwoRapidSwitchesLandLastLevel pins the coalescing
// end-state: two switches issued back to back (the first still rebuilding in
// the background) are both accepted instantly, the queue drains, and the tab
// lands at the LAST requested depth. Whether the second switch coalesced onto
// the running rebuild or spawned after its exit depends on timing; the
// observable contract is the landed level, not the path.
func TestSetEffortForTabTwoRapidSwitchesLandLastLevel(t *testing.T) {
	asyncEffortTestConfig(t)
	app := NewApp()
	app.ctx = context.Background()
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	dir := config.SessionDir()
	tab, _ := asyncEffortTab(t, app, dir, func(string) {})

	if err := app.SetEffortForTab(tab.ID, "low"); err != nil {
		t.Fatalf("first switch refused: %v", err)
	}
	started := time.Now()
	if err := app.SetEffortForTab(tab.ID, "high"); err != nil {
		t.Fatalf("second switch refused: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("second switch blocked %s; a switch arriving behind a running rebuild must coalesce, not freeze", elapsed)
	}

	waitForEffortSettled(t, app, tab.ID)

	app.mu.RLock()
	effort := tab.effort
	app.mu.RUnlock()
	if effort == nil || *effort != "high" {
		t.Fatalf("tab.effort after two rapid switches = %v, want high (last request wins)", effort)
	}
}
