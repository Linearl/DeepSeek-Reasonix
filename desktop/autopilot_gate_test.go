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
	// Task 465 two-axis matrix (user ruling 2026-10-05): the composer tier
	// switch AUTO-SATISFIES the task-325 yolo precondition — ask/auto move to
	// yolo with the decision recorded (assumed_yolo notice), yolo passes
	// through unchanged. The refusal path itself only survives on the
	// settings-default entry (TestSetDesktopAutopilotRequiresYoloDefault).
	for _, mode := range []string{control.ToolApprovalAsk, control.ToolApprovalAuto, control.ToolApprovalYolo} {
		app, tab, codes := autopilotGateTestApp(t, mode)
		app.SetCollaborationModeForTab(tab.ID, "autopilot")
		if !tab.autopilot {
			t.Fatalf("mode %q: tab.autopilot = false, want true (the tier switch auto-satisfies yolo)", mode)
		}
		if tab.autopilotMaxRuntime <= 0 {
			t.Fatalf("mode %q: autopilot on must carry a runtime bound", mode)
		}
		// Autopilot implies yolo: the tab posture must land there in the same
		// switch, whatever it was before.
		if tab.toolApprovalMode != control.ToolApprovalYolo {
			t.Fatalf("mode %q: tab.toolApprovalMode = %q, want yolo (assumed)", mode, tab.toolApprovalMode)
		}
		// X4 断点 B: the applied flag must reach the composer's wire value —
		// this used to render "normal" forever (view structurally incapable
		// of "autopilot"), hiding the state the user just switched on.
		if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "autopilot" {
			t.Fatalf("mode %q: collaboration mode = %q, want autopilot (applied)", mode, got)
		}
		if mode == control.ToolApprovalYolo {
			// Straight yolo entry is not an assumption — no decision record.
			if len(*codes) > 0 {
				t.Fatalf("mode %q: unexpected notices %v (yolo entry must not record an assumption)", mode, *codes)
			}
			continue
		}
		// ask/auto: the assumption decision is recorded user-visibly.
		if !waitForNoticeCode(t, codes, NoticeCodeAutopilotAssumedYolo) {
			t.Fatalf("mode %q: assumed-yolo notice %q not emitted, got %v", mode, NoticeCodeAutopilotAssumedYolo, *codes)
		}
	}
}

// Task 465: the two-axis matrix keeps the second axis (task dimension) intact
// across a first-axis (approval/autopilot) switch, and vice versa — a
// plan/goal/normal switch must not clear the autopilot flag, and switching to
// the autopilot tier must not clear a running goal (goal × autopilot is a
// legal product state).
func TestAutopilotTierPreservesTaskDimension(t *testing.T) {
	app, tab, _ := autopilotGateTestApp(t, control.ToolApprovalAsk)
	// goal × autopilot: the goal survives the tier switch.
	if err := app.SetGoalForTab(tab.ID, "unattended objective"); err != nil {
		t.Fatal(err)
	}
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !tab.autopilot {
		t.Fatal("tier switch must turn autopilot on")
	}
	if got := app.tabRuntimeSnapshot(tab).currentGoal(); got != "unattended objective" {
		t.Fatalf("goal after autopilot tier switch = %q, want it preserved (goal × autopilot)", got)
	}
	if got := app.tabRuntimeSnapshot(tab).collaborationMode(); got != "goal" {
		t.Fatalf("view = %q, want goal (label precedence goal>autopilot; raw flag is the first axis)", got)
	}
	// dim-2 switches must not clear the first axis.
	for _, dim2 := range []string{"goal", "plan", "normal"} {
		app.SetCollaborationModeForTab(tab.ID, dim2)
		if !tab.autopilot {
			t.Fatalf("dim-2 switch %q cleared the autopilot flag (axes must be independent)", dim2)
		}
		if tab.toolApprovalMode != control.ToolApprovalYolo {
			t.Fatalf("dim-2 switch %q moved approval off yolo while autopilot is on", dim2)
		}
	}
	// plan survives the autopilot tier switch too (plan × autopilot = plan-yolo
	// posture, a pre-existing legal tab mode).
	app.SetCollaborationModeForTab(tab.ID, "plan")
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !tabModeHasPlan(tab.mode) || !tab.autopilot {
		t.Fatalf("plan × autopilot: plan=%v autopilot=%v, want both preserved", tabModeHasPlan(tab.mode), tab.autopilot)
	}
	// The reverse linkage stays the only dim-1 way off autopilot.
	app.SetToolApprovalModeForTab(tab.ID, control.ToolApprovalAsk)
	if tab.autopilot {
		t.Fatal("approval leaving yolo must still turn autopilot off (325 reverse linkage)")
	}
}

// Task 465 X4 断点 C: the bare autopilot toggle (no goal, hence no goal-state
// sidecar) must survive a restart via the desktopTabEntry column — gate-
// filtered, so a stale true under ask/auto restores attended.
func TestRestoreTabEntryCarriesAutopilotFlag(t *testing.T) {
	app, tab, _ := autopilotGateTestApp(t, control.ToolApprovalAsk)
	app.SetCollaborationModeForTab(tab.ID, "autopilot")
	if !tab.autopilot {
		t.Fatal("precondition failed — tier switch should enable autopilot")
	}
	entry := persistedDesktopTabEntry(tab)
	if !entry.Autopilot {
		t.Fatal("persisted entry lost the autopilot flag (X4 断点 C would drop it on restart)")
	}
	if entry.ToolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("persisted approval = %q, want yolo (flag and posture persist together)", entry.ToolApprovalMode)
	}
	// Restore-side gate (same call the restore path makes): under yolo the
	// flag comes back with its bounds; under ask/auto it must refuse with
	// zeroed bounds so a stale entry can never resurrect unattended.
	prefOn, prefRuntime, prefGrace, prefAskEnabled, prefAskWait := desktopAutopilotDefaults()
	if !prefOn {
		t.Fatal("precondition failed — the test config turns the autopilot preference on")
	}
	on, runtime, grace, askEnabled, askWait := gateRestoredAutopilotDefaults(entry.Autopilot, prefRuntime, prefGrace, prefAskEnabled, prefAskWait, control.ToolApprovalYolo)
	if !on || runtime <= 0 {
		t.Fatalf("restore under yolo must carry the flag with its runtime bound, got on=%v runtime=%v", on, runtime)
	}
	if grace != prefGrace || askEnabled != prefAskEnabled || askWait != prefAskWait {
		t.Fatalf("restore under yolo must pass the preference dials through, got grace=%v askEnabled=%v askWait=%v (want %v/%v/%v)", grace, askEnabled, askWait, prefGrace, prefAskEnabled, prefAskWait)
	}
	on, runtime, grace, askEnabled, askWait = gateRestoredAutopilotDefaults(entry.Autopilot, prefRuntime, prefGrace, prefAskEnabled, prefAskWait, control.ToolApprovalAsk)
	if on || runtime != 0 || grace != 0 || askEnabled || askWait != 0 {
		t.Fatalf("restore under ask must refuse with zeroed bounds, got on=%v runtime=%v grace=%v askEnabled=%v askWait=%v", on, runtime, grace, askEnabled, askWait)
	}
}

// X4 断点 C file-format pin: the desktopTabsFile JSON round-trip must carry
// the autopilot column — a renamed tag would silently restore every bare
// unattended tab as attended (the flag lost on disk, no compile error).
func TestDesktopTabsFileRoundTripsAutopilotColumn(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := testTab("ap-persist", t.TempDir())
	tab.toolApprovalMode = control.ToolApprovalYolo
	// The live controller is the persistence read's first source — keep it in
	// sync or the entry persists an empty approval and the flag would come
	// back gate-refused.
	if tab.Ctrl != nil {
		tab.Ctrl.SetToolApprovalMode(tab.toolApprovalMode)
	}
	tab.autopilot = true
	tab.autopilotMaxRuntime = 8 * time.Hour
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	a := app
	a.mu.Lock()
	a.saveTabsLocked()
	a.mu.Unlock()

	f := loadTabsFile()
	if len(f.Tabs) != 1 {
		t.Fatalf("tabs file holds %d entries, want 1", len(f.Tabs))
	}
	if !f.Tabs[0].Autopilot {
		t.Fatal("tabs file lost the autopilot column on round-trip (X4 断点 C)")
	}
	if f.Tabs[0].ToolApprovalMode != control.ToolApprovalYolo {
		t.Fatalf("tabs file approval = %q, want yolo", f.Tabs[0].ToolApprovalMode)
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
