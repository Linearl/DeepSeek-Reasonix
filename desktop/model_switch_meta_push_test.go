package main

// tl1389 P1: a settled model switch must push "tab:meta" (the composer's
// model name used to wait for the frontend's own MetaForTab → ContextUsage →
// Effort pull chain), and a switch that took the rebuild fallback must
// announce "model:applying" while the rebuild runs — the task-1301 effort
// seam carried to the model side. Both events ride the runtime event
// channel; the tests stub app.runtimeEvents.emit to capture them.

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// personaBoundaryTestConfig saves a config whose default is the official
// DeepSeek-V4-Pro endpoint (persona applies) plus a plain provider to cross
// the persona boundary with (the only config-only rebuild trigger that works
// with a live controller).
func personaBoundaryTestConfig(t *testing.T) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "ds/deepseek-v4-pro"
	cfg.Providers = []config.ProviderEntry{
		{Name: "ds", Kind: "openai", BaseURL: "https://api.deepseek.com",
			Model: "deepseek-v4-pro", APIKeyEnv: "REASONIX_TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com",
			Model: "model-b1", APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

type capturedRuntimeEvent struct {
	name string
	args []any
}

type runtimeEventRecorder struct {
	mu     sync.Mutex
	events []capturedRuntimeEvent
}

func (r *runtimeEventRecorder) record(_ context.Context, name string, payload ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, capturedRuntimeEvent{name: name, args: payload})
}

func (r *runtimeEventRecorder) snapshot() []capturedRuntimeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedRuntimeEvent(nil), r.events...)
}

func (r *runtimeEventRecorder) count(name string) int {
	return countRuntimeEvents(r.snapshot(), name)
}

func countRuntimeEvents(events []capturedRuntimeEvent, name string) int {
	n := 0
	for _, e := range events {
		if e.name == name {
			n++
		}
	}
	return n
}

// waitFor polls the recorder until the predicate holds — the async runtime
// emitter drains its queue on a goroutine, so a settled switch and its
// pushed events are only eventually-ordered against the test.
func (r *runtimeEventRecorder) waitFor(t *testing.T, label string, pred func([]capturedRuntimeEvent) bool) []capturedRuntimeEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		events := r.snapshot()
		if pred(events) {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; events=%v", label, eventNames(events))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSetModelForTabFastPathPushesTabMeta: the per-request fast path must
// emit exactly one "tab:meta" carrying the new label, and never
// "model:applying" (fast paths give instant feedback; an applying hint would
// flash for nothing).
func TestSetModelForTabFastPathPushesTabMeta(t *testing.T) {
	modelFastPathTestConfig(t)
	app, tab, wrapped, _, _ := activeTabApp(t, provAStubProvider{}, savedConfigResolver(t))
	rec := &runtimeEventRecorder{}
	app.runtimeEvents.emit = rec.record

	if err := app.SetModelForTab("test", "prov-a/model-a2"); err != nil {
		t.Fatalf("fast path switch: %v", err)
	}
	events := rec.waitFor(t, "tab:meta", func(events []capturedRuntimeEvent) bool {
		for _, e := range events {
			if e.name == tabMetaRefreshEventChannel {
				return true
			}
		}
		return false
	})
	metaPushes := 0
	for _, e := range events {
		if e.name != tabMetaRefreshEventChannel {
			continue
		}
		metaPushes++
		evt, ok := e.args[0].(TabMetaRefreshEvent)
		if !ok {
			t.Fatalf("tab:meta payload type %T, want TabMetaRefreshEvent", e.args[0])
		}
		if evt.TabID != "test" {
			t.Fatalf("tab:meta tabId = %q, want test", evt.TabID)
		}
		if evt.Meta.Label != "model-a2" {
			t.Fatalf("tab:meta label = %q, want model-a2 (the boot-derived label)", evt.Meta.Label)
		}
	}
	if metaPushes != 1 {
		t.Fatalf("fast path pushed tab:meta %d times, want exactly 1", metaPushes)
	}
	if rec.count("model:applying") != 0 {
		t.Fatal("fast path emitted model:applying; only the rebuild fallback may")
	}
	if app.controllerForTab(tab) != control.SessionAPI(wrapped) {
		t.Fatal("fast path replaced the controller; push must ride the fast path without a rebuild")
	}
}

// TestSetModelForTabRebuildEmitsApplyingThenMeta: a persona-boundary crossing
// declines the fast path, so the call holds for the whole runtime rebuild —
// it must emit "model:applying" when the rebuild starts and exactly one
// "tab:meta" with the new label at settle, in that order.
func TestSetModelForTabRebuildEmitsApplyingThenMeta(t *testing.T) {
	personaBoundaryTestConfig(t)
	app := NewApp()
	app.ctx = context.Background()
	rec := &runtimeEventRecorder{}
	app.runtimeEvents.emit = rec.record

	tab := &WorkspaceTab{
		ID:          "tab_model_applying",
		Scope:       "global",
		Ready:       true,
		model:       "ds/deepseek-v4-pro",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	oldCtrl := control.New(control.Options{
		Executor: agent.New(nil, nil, agent.NewSession("old system"), agent.Options{}, event.Discard),
		Sink:     tab.sink,
	})
	tab.Ctrl = oldCtrl
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
	})

	if err := app.SetModelForTab(tab.ID, "prov-b/model-b1"); err != nil {
		t.Fatalf("rebuild switch: %v", err)
	}
	// Judge the snapshot waitFor returns, not a fresher one: a live count could
	// see tab:meta that the returned events slice does not hold yet.
	events := rec.waitFor(t, "model:applying + tab:meta", func(events []capturedRuntimeEvent) bool {
		return countRuntimeEvents(events, "model:applying") > 0 && countRuntimeEvents(events, tabMetaRefreshEventChannel) > 0
	})
	applyingIdx, metaIdx := -1, -1
	for i, e := range events {
		switch e.name {
		case "model:applying":
			applyingIdx = i
		case tabMetaRefreshEventChannel:
			metaIdx = i
			evt, ok := e.args[0].(TabMetaRefreshEvent)
			if !ok {
				t.Fatalf("tab:meta payload type %T, want TabMetaRefreshEvent", e.args[0])
			}
			if evt.TabID != tab.ID {
				t.Fatalf("tab:meta tabId = %q, want %s", evt.TabID, tab.ID)
			}
			if evt.Meta.Label != "model-b1" {
				t.Fatalf("tab:meta label = %q, want model-b1", evt.Meta.Label)
			}
		}
	}
	if applyingIdx < 0 {
		t.Fatalf("rebuild fallback never emitted model:applying; events=%v", eventNames(events))
	}
	if metaIdx < 0 {
		t.Fatalf("rebuild settle never pushed tab:meta; events=%v", eventNames(events))
	}
	if applyingIdx > metaIdx {
		t.Fatalf("tab:meta arrived before model:applying; the applying hint must lead the settle push")
	}
	if rec.count(tabMetaRefreshEventChannel) != 1 {
		t.Fatalf("rebuild pushed tab:meta %d times, want exactly 1", rec.count(tabMetaRefreshEventChannel))
	}
}

// eventNames renders the captured sequence for failure output.
func eventNames(events []capturedRuntimeEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.name)
	}
	return out
}
