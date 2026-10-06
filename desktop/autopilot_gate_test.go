package main

// Task 325 (user ruling 2026-09-25): autopilot requires the yolo approval
// mode. The matrix ask/auto/yolo × on/off is asserted at every enable entry:
// the composer selector (SetCollaborationModeForTab), the approval switch
// reverse linkage (SetToolApprovalModeForTab / SetComposerProfileForTab), the
// shared defaults gate, and the settings default (SetDesktopAutopilot). Zero
// SKIP: a missing precondition is a t.Fatal, never a skip.

import (
	"context"
	"os"
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
		"full":                   true, // legacy yolo alias
		"bypass":                 true, // legacy yolo alias
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
			// X4 断点 B: the applied flag must reach the composer's wire value —
			// this used to render "normal" forever (view structurally incapable
			// of "autopilot"), hiding the state the user just switched on.
			if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "autopilot" {
				t.Fatalf("mode %q: collaboration mode = %q, want autopilot (applied)", mode, got)
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

// X4 断点 B precedence pin: a running goal still wins the wire value (the
// pre-existing trichotomy), and clearing the goal hands the view back to
// autopilot — the bare-toggle shape the composer's indicator keys on.
// TestInitialTabBuildCarriesAutopilotTriple (X4 断点 A): the initial build's
// boot.Options literal is the one build path that historically dropped the
// autopilot triple — every rebuild site carried it, so the flag silently died
// on every fresh app start and restart restore until some later rebuild. The
// literal is inline in buildTabControllerWithContextCore, so this pins it at
// the source level: the Options block must name all three fields.
func TestInitialTabBuildCarriesAutopilotTriple(t *testing.T) {
	src, err := os.ReadFile("tabs.go")
	if err != nil {
		t.Fatal(err)
	}
	const literal = "buildTabControllerBootFenced(buildCtx, extensionGen, boot.Options{"
	start := strings.Index(string(src), literal)
	if start < 0 {
		t.Fatalf("initial build Options literal not found: %q", literal)
	}
	block := string(src[start:])
	if end := strings.Index(block, "\n\t})"); end >= 0 {
		block = block[:end]
	}
	for _, field := range []string{"Autopilot:", "MaxRuntime:", "AutopilotApprovalGrace:"} {
		if !strings.Contains(block, field) {
			t.Fatalf("initial build Options dropped the autopilot field %q — the flag would silently die on every fresh start", field)
		}
	}
}

func TestAutopilotViewYieldsToRunningGoalThenReturns(t *testing.T) {
	app, tab, _ := autopilotGateTestApp(t, control.ToolApprovalYolo)
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !tab.autopilot {
		t.Fatal("precondition failed — autopilot should be on under yolo")
	}
	if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "autopilot" {
		t.Fatalf("bare autopilot view = %q, want autopilot", got)
	}
	if err := app.SetGoalForTab(tab.ID, "unattended objective"); err != nil {
		t.Fatal(err)
	}
	if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "goal" {
		t.Fatalf("autopilot+goal view = %q, want goal (documented precedence)", got)
	}
	if err := app.SetGoalForTab(tab.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "autopilot" {
		t.Fatalf("autopilot view after goal clear = %q, want autopilot", got)
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
	on, runtime, grace, askEnabled, askWait, askAutoContinue := gateRestoredAutopilotDefaults(true, 8*time.Hour, 15*time.Second, true, 15*time.Second, true, control.ToolApprovalYolo)
	if !on || runtime != 8*time.Hour || grace != 15*time.Second {
		t.Fatalf("yolo passes through: on=%v runtime=%v grace=%v", on, runtime, grace)
	}
	if !askEnabled || askWait != 15*time.Second {
		t.Fatalf("yolo passes the task-477 pair through: askEnabled=%v askWait=%v", askEnabled, askWait)
	}
	if !askAutoContinue {
		t.Fatal("yolo passes the task-544 ask auto-continue switch through")
	}
	for _, mode := range []string{control.ToolApprovalAsk, control.ToolApprovalAuto, ""} {
		on, runtime, grace, askEnabled, askWait, askAutoContinue := gateRestoredAutopilotDefaults(true, 8*time.Hour, 15*time.Second, true, 15*time.Second, true, mode)
		if on || runtime != 0 || grace != 0 {
			t.Fatalf("mode %q must be refused with zeroed bounds, got on=%v runtime=%v grace=%v", mode, on, runtime, grace)
		}
		if askEnabled || askWait != 0 {
			t.Fatalf("mode %q must clear the task-477 pair too, got askEnabled=%v askWait=%v", mode, askEnabled, askWait)
		}
		if askAutoContinue {
			t.Fatalf("mode %q must clear the task-544 switch too", mode)
		}
	}
	// A preference that is already off stays off regardless of the mode.
	if on, _, _, _, _, _ = gateRestoredAutopilotDefaults(false, 8*time.Hour, 0, false, 0, false, control.ToolApprovalYolo); on {
		t.Fatal("off preference must stay off")
	}
}

// Task 477: the ask-timeout sub-option is a plain desktop preference — no yolo
// precondition of its own (it only ever bites while the run is unattended),
// and the seconds dial is validated by the config setter.
func TestSetDesktopAutopilotAskTimeoutPersistsAndBounds(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}

	if err := app.SetDesktopAutopilotAskTimeout(true, 0); err != nil {
		t.Fatalf("on with the built-in default (0) must be accepted: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if !cfg.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatal("enable did not persist")
	}
	if got := cfg.AutopilotAskWaitSecondsEffective(); got != 15 {
		t.Fatalf("unset dial reads as %d, want the built-in 15", got)
	}

	if err := app.SetDesktopAutopilotAskTimeout(true, 7200); err == nil {
		t.Fatal("7200s must be refused (ceiling 3600)")
	}
	if err := app.SetDesktopAutopilotAskTimeout(true, 90); err != nil {
		t.Fatalf("90s must be accepted: %v", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if got := cfg.AutopilotAskWaitSecondsEffective(); got != 90 {
		t.Fatalf("dial persisted as %d, want 90", got)
	}

	if err := app.SetDesktopAutopilotAskTimeout(false, 0); err != nil {
		t.Fatalf("disable must always be allowed: %v", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if cfg.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatal("disable did not persist")
	}
	if cfg.Desktop.AutopilotAskWaitSeconds != 90 {
		t.Fatalf("the dial must survive the switch going off, got %d", cfg.Desktop.AutopilotAskWaitSeconds)
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

// Task 544: the ask auto-continue sub-option is a plain desktop preference —
// the setter persists both states and it stays independent of the task-477
// timeout pair (flipping one never moves the other).
func TestSetDesktopAutopilotAskAutoContinuePersists(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}

	if err := app.SetDesktopAutopilotAskAutoContinue(true); err != nil {
		t.Fatalf("enable = %v, want nil", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if !cfg.Desktop.ExperimentalAutopilotAskAutoContinue {
		t.Fatal("enable did not persist")
	}
	if err := app.SetDesktopAutopilotAskTimeout(true, 0); err != nil {
		t.Fatalf("enabling the 477 pair = %v", err)
	}
	if err := app.SetDesktopAutopilotAskAutoContinue(false); err != nil {
		t.Fatalf("disable = %v, want nil", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if cfg.Desktop.ExperimentalAutopilotAskAutoContinue {
		t.Fatal("disable did not persist")
	}
	if !cfg.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatal("disabling 544 must not touch the independent 477 switch")
	}
}
