package main

// Task 325 (user ruling 2026-09-25): autopilot requires the yolo approval
// mode. The matrix ask/auto/yolo × on/off is asserted at every enable entry:
// the composer selector (SetCollaborationModeForTab), the approval switch
// reverse linkage (SetToolApprovalModeForTab / SetComposerProfileForTab), the
// shared defaults gate, and the settings default (SetDesktopAutopilot). Zero
// SKIP: a missing precondition is a t.Fatal, never a skip.

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// autopilotGateTestApp builds an isolated App with one tab whose approval mode
// is preset, and turns the [desktop] autopilot preference on so refusals come
// from the task-325 gate rather than a missing runtime bound. The emitted
// notice codes are recorded.
func autopilotGateTestApp(t *testing.T, approvalMode string) (*App, *WorkspaceTab, *[]string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Desktop.Autopilot = true
	cfg.Desktop.AutopilotMaxRuntime = "8h"
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	codes := &[]string{}
	tab := testTab("ap-gate", t.TempDir())
	tab.toolApprovalMode = normalizeToolApprovalMode(approvalMode)
	// The live controller is the snapshot's first source for the current
	// approval mode — keep it in sync with the preset.
	if tab.Ctrl != nil {
		tab.Ctrl.SetToolApprovalMode(tab.toolApprovalMode)
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	// tabEventSink carries its own runtime emitter (a different instance than
	// the App-level one), so the capture hooks the sink's emitter.
	tab.sink.runtimeEvents.emit = func(_ context.Context, _ string, payload ...any) {
		for _, p := range payload {
			switch wire := p.(type) {
			case wireEventTab:
				if wire.Code != "" {
					*codes = append(*codes, wire.Code)
				}
			case correlatedWireEventTab:
				if wire.Code != "" {
					*codes = append(*codes, wire.Code)
				}
			}
		}
	}
	tab.sink.setContext(context.Background()) // without a ctx the sink buffers runtime events instead of emitting
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})
	return app, tab, codes
}

func TestAutopilotGateAllowedMatrix(t *testing.T) {
	for mode, want := range map[string]bool{
		control.ToolApprovalYolo: true,
		"full":                   true,  // legacy yolo alias
		"bypass":                 true,  // legacy yolo alias
		control.ToolApprovalAsk:  false,
		control.ToolApprovalAuto: false,
		"":                       false, // empty normalizes to ask
		"nonsense":               false,
	} {
		if got := autopilotGateAllowed(mode); got != want {
			t.Fatalf("autopilotGateAllowed(%q) = %v, want %v", mode, got, want)
		}
	}
}

// waitForNoticeCode polls the recorded codes until want shows up. The runtime
// emitter drains its queue on a goroutine, so the notice lands asynchronously
// relative to the setter call that produced it.
func waitForNoticeCode(t *testing.T, codes *[]string, want string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, code := range *codes {
			if code == want {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestAutopilotCollaborationModeMatrix(t *testing.T) {
	// 3 approval modes × autopilot switch: ask/auto refuse the enable and fall
	// back to normal; yolo turns autopilot on.
	for _, mode := range []string{control.ToolApprovalAsk, control.ToolApprovalAuto, control.ToolApprovalYolo} {
		app, tab, codes := autopilotGateTestApp(t, mode)
		app.SetCollaborationModeForTab(tab.ID, "autopilot")
		wantAutopilot := mode == control.ToolApprovalYolo
		if tab.autopilot != wantAutopilot {
			t.Fatalf("mode %q: tab.autopilot = %v, want %v", mode, tab.autopilot, wantAutopilot)
		}
		if wantAutopilot {
			if tab.autopilotMaxRuntime <= 0 {
				t.Fatalf("mode %q: autopilot on must carry a runtime bound", mode)
			}
		} else {
			got := app.tabRuntimeSnapshot(tab).collaborationMode()
			if got != "normal" {
				t.Fatalf("mode %q: collaboration mode = %q, want normal (refused)", mode, got)
			}
			if !waitForNoticeCode(t, codes, NoticeCodeAutopilotRequiresYolo) {
				t.Fatalf("mode %q: refusal notice %q not emitted, got %v", mode, NoticeCodeAutopilotRequiresYolo, *codes)
			}
		}
	}
}

func TestAutopilotReverseLinkageOnApprovalModeChange(t *testing.T) {
	// Autopilot on (yolo), then the approval switch moves: ask/auto must turn
	// autopilot off (fail-closed) and notice; yolo keeps it on.
	for _, mode := range []string{control.ToolApprovalAsk, control.ToolApprovalAuto, control.ToolApprovalYolo} {
		app, tab, codes := autopilotGateTestApp(t, control.ToolApprovalYolo)
		app.SetCollaborationModeForTab(tab.ID, "autopilot")
		if !tab.autopilot {
			t.Fatalf("mode %q: precondition failed — autopilot should be on under yolo", mode)
		}
		*codes = nil
		app.SetToolApprovalModeForTab(tab.ID, mode)
		wantAutopilot := mode == control.ToolApprovalYolo
		if tab.autopilot != wantAutopilot {
			t.Fatalf("approval %q: tab.autopilot = %v, want %v", mode, tab.autopilot, wantAutopilot)
		}
		if tab.toolApprovalMode != mode {
			t.Fatalf("approval %q: tab.toolApprovalMode = %q (the switch itself must never be trapped)", mode, tab.toolApprovalMode)
		}
		if !wantAutopilot {
			if !waitForNoticeCode(t, codes, NoticeCodeAutopilotClosedOffYolo) {
				t.Fatalf("approval %q: reverse-linkage notice %q not emitted, got %v", mode, NoticeCodeAutopilotClosedOffYolo, *codes)
			}
		}
	}
}

func TestAutopilotReverseLinkageOnComposerProfile(t *testing.T) {
	app, tab, codes := autopilotGateTestApp(t, control.ToolApprovalYolo)
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !tab.autopilot {
		t.Fatal("precondition failed — autopilot should be on under yolo")
	}
	*codes = nil
	if _, err := app.SetComposerProfileForTab(tab.ID, "normal", control.ToolApprovalAuto, ""); err != nil {
		t.Fatalf("SetComposerProfileForTab: %v", err)
	}
	if tab.autopilot {
		t.Fatal("composer profile with auto approval must turn autopilot off")
	}
	if !waitForNoticeCode(t, codes, NoticeCodeAutopilotClosedOffYolo) {
		t.Fatalf("reverse-linkage notice %q not emitted, got %v", NoticeCodeAutopilotClosedOffYolo, *codes)
	}
}

func TestAutopilotGateOnNewTabDefaults(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Desktop.Autopilot = true
	cfg.Desktop.AutopilotMaxRuntime = "8h"
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}

	// Default approval is auto (config.Default) → the new tab must NOT start
	// unattended even though the [desktop] autopilot preference is on.
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "tab-gate-auto")
	if tab.autopilot {
		t.Fatal("new tab under auto approval default must not start unattended (task 325)")
	}
	if tab.autopilotMaxRuntime != 0 || tab.autopilotApprovalGrace != 0 {
		t.Fatal("refused autopilot must leave no partial bounds behind")
	}

	// With the yolo default the same preference turns autopilot on.
	cfg.Desktop.DefaultToolApprovalMode = "yolo"
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	tabYolo := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "tab-gate-yolo")
	if !tabYolo.autopilot {
		t.Fatal("new tab under yolo approval default should start unattended")
	}
	if tabYolo.toolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("tab approval mode = %q, want yolo", tabYolo.toolApprovalMode)
	}
}

func TestGateRestoredAutopilotDefaultsPure(t *testing.T) {
	on, runtime, grace := gateRestoredAutopilotDefaults(true, 8*time.Hour, 15*time.Second, control.ToolApprovalYolo)
	if !on || runtime != 8*time.Hour || grace != 15*time.Second {
		t.Fatalf("yolo passes through: on=%v runtime=%v grace=%v", on, runtime, grace)
	}
	for _, mode := range []string{control.ToolApprovalAsk, control.ToolApprovalAuto, ""} {
		on, runtime, grace := gateRestoredAutopilotDefaults(true, 8*time.Hour, 15*time.Second, mode)
		if on || runtime != 0 || grace != 0 {
			t.Fatalf("mode %q must be refused with zeroed bounds, got on=%v runtime=%v grace=%v", mode, on, runtime, grace)
		}
	}
	// A preference that is already off stays off regardless of the mode.
	if on, _, _ = gateRestoredAutopilotDefaults(false, 8*time.Hour, 0, control.ToolApprovalYolo); on {
		t.Fatal("off preference must stay off")
	}
}

func TestSetDesktopAutopilotRequiresYoloDefault(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}

	// Default approval is auto → enabling the autopilot default is refused.
	if err := app.SetDesktopAutopilot(true, "8h", ""); err == nil {
		t.Fatal("enabling the autopilot default under a non-yolo approval default must be refused")
	} else if !strings.Contains(err.Error(), "yolo") {
		t.Fatalf("refusal should name the yolo requirement, got: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Desktop.Autopilot {
		t.Fatal("refused enable must not persist the autopilot flag")
	}

	// Under yolo the same call succeeds.
	cfg.Desktop.DefaultToolApprovalMode = "yolo"
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := app.SetDesktopAutopilot(true, "8h", ""); err != nil {
		t.Fatalf("enabling the autopilot default under yolo should succeed: %v", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if !cfg.Desktop.Autopilot {
		t.Fatal("accepted enable must persist the autopilot flag")
	}

	// Disabling never requires yolo.
	if err := app.SetDesktopAutopilot(false, "", ""); err != nil {
		t.Fatalf("disabling must always be allowed: %v", err)
	}
}
