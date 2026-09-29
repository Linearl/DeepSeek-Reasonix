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
	"reasonix/internal/provider"
)

// Task 385b acceptance at the App layer: switching the output style rebuilds
// the ACTIVE session immediately through the shared rebuildSetting path
// (runtimeRebuildMu build+swap, same orchestration as model/effort switches),
// never touches other tabs, defers instead of killing a streaming turn, and
// stays failure-atomic. The fixture mirrors runtime_rebuilt_event_test.go —
// that is the sanctioned e2e rebuild harness for this chain.

type outputStyleFixture struct {
	app     *App
	tab     *WorkspaceTab
	mu      sync.Mutex
	rebuilt []string
}

// newOutputStyleFixture builds an App with one active tab carrying a real
// controller. providers==nil saves a config with no usable model, so the
// rebuild's boot build fails (the failure-injection case); otherwise the
// old/new provider pair from the runtime-rebuild tests is configured.
func newOutputStyleFixture(t *testing.T, withProviders bool) *outputStyleFixture {
	t.Helper()
	isolateDesktopUserDirs(t)
	if withProviders {
		setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
		cfg := config.Default()
		cfg.DefaultModel = "old/old-model"
		cfg.Desktop.ProviderAccess = []string{"old"}
		cfg.Providers = []config.ProviderEntry{
			{Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		}
		if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
			t.Fatalf("save config: %v", err)
		}
	} else {
		// No providers: the next boot build cannot resolve a model and must
		// fail while the old controller stays live (atomic failure path).
		cfg := config.Default()
		cfg.DefaultModel = ""
		cfg.Providers = nil
		if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
			t.Fatalf("save config: %v", err)
		}
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "output-style-instant.jsonl")
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "old", Sink: event.Discard})

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {} // keep workspace reconcile off the real event bridge

	f := &outputStyleFixture{app: app}
	tab := &WorkspaceTab{
		ID:            "tab_output_style",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		Ready:         true,
		model:         "old/old-model",
		Ctrl:          ctrl,
		sink:          &tabEventSink{tabID: "tab_output_style", app: app, ctx: context.Background()},
		disabledMCP:   map[string]ServerView{},
	}
	observed := func(_ context.Context, name string, payload ...any) {
		if name != "runtime:rebuilt" {
			return
		}
		tabID := ""
		if len(payload) > 0 {
			tabID, _ = payload[0].(string)
		}
		f.mu.Lock()
		f.rebuilt = append(f.rebuilt, tabID)
		f.mu.Unlock()
	}
	tab.sink.runtimeEvents.emit = observed
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	f.tab = tab
	t.Cleanup(func() {
		// Close the REAL controller this fixture built — tests may swap
		// tab.Ctrl for a stub (streaming/defer cases) that has nothing to
		// close and panics through the embedded nil SessionAPI.
		ctrl.Close()
	})
	return f
}

func (f *outputStyleFixture) waitRebuilds(t *testing.T, want int, step string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.rebuilt)
		f.mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("after %s: runtime:rebuilt events = %v, want %d", step, f.rebuilt, want)
}

func mustPersistedOutputStyle(t *testing.T, want string) {
	t.Helper()
	cfg, err := config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Agent.OutputStyle != want {
		t.Fatalf("persisted [agent] output_style = %q, want %q", cfg.Agent.OutputStyle, want)
	}
}

// TestOutputStyleSwitchRebuildsActiveSessionOnly pins instant apply plus
// per-session isolation: the active tab swaps to a fresh controller (and the
// switch back to default rebuilds again — a no-style runtime), while the
// other tab's controller object is never touched.
func TestOutputStyleSwitchRebuildsActiveSessionOnly(t *testing.T) {
	f := newOutputStyleFixture(t, true)

	// Second tab: a stub controller that must survive untouched.
	other := &WorkspaceTab{
		ID:            "tab_other",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		Ready:         true,
		model:         "old/old-model",
		Ctrl:          &retargetRuntimeController{path: filepath.Join(config.SessionDir(), "other.jsonl")},
		sink:          &tabEventSink{tabID: "tab_other", app: f.app, ctx: context.Background()},
		disabledMCP:   map[string]ServerView{},
	}
	f.app.tabs[other.ID] = other
	f.app.tabOrder = append(f.app.tabOrder, other.ID)
	otherCtrl := other.Ctrl
	oldCtrl := f.tab.Ctrl

	warning, err := f.app.SetOutputStyle("concise")
	if err != nil {
		t.Fatalf("SetOutputStyle(concise): %v", err)
	}
	if warning != "" {
		t.Fatalf("healthy instant apply must not warn, got %q", warning)
	}
	f.waitRebuilds(t, 1, "style switch")
	if f.tab.Ctrl == oldCtrl {
		t.Fatal("active tab must be swapped to a rebuilt controller")
	}
	if other.Ctrl != otherCtrl {
		t.Fatal("the other tab must not be rebuilt (per-session isolation)")
	}
	f.mu.Lock()
	if len(f.rebuilt) != 1 || f.rebuilt[0] != f.tab.ID {
		f.mu.Unlock()
		t.Fatalf("exactly one rebuild carrying the active tab id, got %v", f.rebuilt)
	}
	f.mu.Unlock()
	mustPersistedOutputStyle(t, "concise")

	// Switch back to default: another rebuild, and the persisted value clears
	// so the rebuilt runtime carries no style at all.
	warning, err = f.app.SetOutputStyle("")
	if err != nil {
		t.Fatalf("SetOutputStyle(default): %v", err)
	}
	if warning != "" {
		t.Fatalf("default switch must not warn, got %q", warning)
	}
	f.waitRebuilds(t, 2, "switch back to default")
	mustPersistedOutputStyle(t, "")
	if other.Ctrl != otherCtrl {
		t.Fatal("the other tab must stay untouched after the default switch")
	}
}

// TestOutputStyleSwitchDefersDuringStreamingTurn pins the streaming-defer
// path: a running turn is never killed, the rebuild is queued for the
// deferred-rebuild loop (which replays it once the turn finishes), and the
// persistence still lands immediately with a warning returned to the banner.
func TestOutputStyleSwitchDefersDuringStreamingTurn(t *testing.T) {
	f := newOutputStyleFixture(t, true)
	streaming := &retargetRuntimeController{
		path:   filepath.Join(config.SessionDir(), "streaming.jsonl"),
		status: control.RuntimeStatus{Running: true},
	}
	f.tab.Ctrl = streaming
	f.mu.Lock()
	f.rebuilt = nil // start clean; no rebuild may fire
	f.mu.Unlock()

	warning, err := f.app.SetOutputStyle("concise")
	if err != nil {
		t.Fatalf("deferred switch must not error: %v", err)
	}
	if !strings.Contains(warning, "current turn") {
		t.Fatalf("defer warning must say the turn is in the way, got %q", warning)
	}
	if !f.app.deferredRebuildPending(f.tab.ID) {
		t.Fatal("the rebuild must be queued for replay after the turn")
	}
	if f.tab.Ctrl != streaming {
		t.Fatal("a streaming controller must not be swapped out")
	}
	if !streaming.RuntimeStatus().Running {
		t.Fatal("the streaming turn must still be running (never killed)")
	}
	mustPersistedOutputStyle(t, "concise")
	f.mu.Lock()
	if len(f.rebuilt) != 0 {
		f.mu.Unlock()
		t.Fatalf("no rebuild may fire while the turn streams, got %v", f.rebuilt)
	}
	f.mu.Unlock()
}

// TestOutputStyleBuildFailureKeepsOldRuntime pins failure atomicity: a rebuild
// whose boot build fails (no resolvable model) surfaces the error, does not
// warn, and leaves the old controller object in place — while the already-
// persisted choice stays on disk (persist-first contract).
func TestOutputStyleBuildFailureKeepsOldRuntime(t *testing.T) {
	f := newOutputStyleFixture(t, false)
	oldCtrl := f.tab.Ctrl

	warning, err := f.app.SetOutputStyle("concise")
	if err == nil {
		t.Fatal("a failing build must surface the error to the caller")
	}
	if warning != "" {
		t.Fatalf("a real failure must not be softened into a warning, got %q", warning)
	}
	if f.tab.Ctrl != oldCtrl {
		t.Fatal("failure must be atomic: the old runtime stays live")
	}
	if f.app.deferredRebuildPending(f.tab.ID) {
		t.Fatal("a hard build failure must not be retried forever")
	}
	mustPersistedOutputStyle(t, "concise")
}
