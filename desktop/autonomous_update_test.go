package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/installlayout"
)

// fakeStagingDir drops a staged build into installRoot/staging. Binaries named
// in members are written; omitted members stay missing so health-bit paths can
// be exercised.
func fakeStagingDir(t *testing.T, root string, version string, complete bool) {
	t.Helper()
	dir := filepath.Join(root, "staging")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
	members := []string{installlayout.DesktopBinaryName()}
	if complete {
		members = append(members, installlayout.CLIBinaryName())
	}
	for _, name := range members {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// addVersionTreeWithCLI writes a version tree whose CLI binary presence is
// controlled, so versionTreeHealthy's both-members check can be exercised.
func addVersionTreeWithCLI(t *testing.T, root, version string, withCLI bool) {
	t.Helper()
	dir := filepath.Join(root, installlayout.VersionsDirName, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, installlayout.DesktopBinaryName()), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withCLI {
		if err := os.WriteFile(filepath.Join(dir, installlayout.CLIBinaryName()), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListVersionsReportsHealthAndStaging(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	// fakeInstallRoot writes desktop binaries only; the active tree gets its
	// CLI so the health check has a fully healthy row to compare against.
	addVersionTreeWithCLI(t, root, active, true)
	// An older tree missing the CLI: list_versions must brand it unhealthy so
	// the model never stages a rollback that bricks servepool (task 248).
	addVersionTreeWithCLI(t, root, "v1.38.3-20260922-0900", false)
	fakeStagingDir(t, root, "1.38.3-20260924-1200", true)

	app := &App{}
	controller := newAutonomousUpdateController(app)
	versions, active, staging, err := controller.ListVersions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if active != "v1.38.3-20260923-1010" {
		t.Fatalf("active = %q", active)
	}
	if staging != "v1.38.3-20260924-1200" {
		t.Fatalf("staging label = %q (version.txt must be normalized onto the v-prefixed install name)", staging)
	}
	byVersion := map[string]toolVersionHealth{}
	for _, v := range versions {
		byVersion[v.Version] = v
	}
	healthyTree, ok := byVersion["v1.38.3-20260923-1010"]
	if !ok || !healthyTree.Healthy || !healthyTree.Active {
		t.Fatalf("active tree missing or mislabeled: %+v", healthyTree)
	}
	unhealthyTree, ok := byVersion["v1.38.3-20260922-0900"]
	if !ok || unhealthyTree.Healthy {
		t.Fatalf("CLI-less tree must be unhealthy (task 248): %+v", unhealthyTree)
	}
	staged, ok := byVersion["v1.38.3-20260924-1200"]
	if !ok || !staged.Staging {
		t.Fatalf("staging row missing or mislabeled: %+v", staged)
	}
}

// toolVersionHealth mirrors tool.VersionHealth without importing the package
// twice under a different name in assertions.
type toolVersionHealth = struct {
	Version     string `json:"version"`
	Active      bool   `json:"active"`
	Healthy     bool   `json:"healthy"`
	Staging     bool   `json:"staging"`
	ModTimeUnix int64  `json:"modTimeUnix"`
}

func TestSetTargetStagesAndValidates(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	addVersionTreeWithCLI(t, root, "v1.38.3-20260922-0900", true)
	app := &App{}
	controller := newAutonomousUpdateController(app)

	// A bare version label is normalized onto the v-prefixed install name.
	description, err := controller.SetTarget(t.Context(), "1.38.3-20260922-0900")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(description, "v1.38.3-20260922-0900") {
		t.Fatalf("description should name the normalized version: %q", description)
	}
	app.autonomousMu.Lock()
	pending := app.autonomousPending
	app.autonomousMu.Unlock()
	if pending.kind != "installed" || pending.version != "v1.38.3-20260922-0900" {
		t.Fatalf("pending = %+v", pending)
	}

	// The active version is a no-op swap and must be refused.
	if _, err := controller.SetTarget(t.Context(), active); err == nil {
		t.Fatal("staging the active version must be refused")
	}
	// An unknown version is refused.
	if _, err := controller.SetTarget(t.Context(), "v9.9.9"); err == nil {
		t.Fatal("staging an unknown version must be refused")
	}

	// Staging without version.txt is a named-path refusal.
	broken := &App{}
	controller = newAutonomousUpdateController(broken)
	fakeStagingDir(t, root, "", false) // no version.txt
	if _, err := controller.SetTarget(t.Context(), "staging"); err == nil {
		t.Fatal("staging without version.txt must be refused")
	}
}

func TestExecuteTargetRequiresStagedTarget(t *testing.T) {
	isolateDesktopUserDirs(t)
	fakeInstallRoot(t, nil, "")
	app := &App{}
	// tl1585 钉面同步: the tool face queues every turn once the gate widens, so
	// the no-target refusal is boundary-waiter territory — pin the shared body
	// the waiter runs (executeStagedTarget), not the ctx gate.
	controller := autonomousUpdateController{app: app}
	if _, err := controller.executeStagedTarget(""); err == nil {
		t.Fatal("execute without set_target must be refused")
	}
}

func TestExecuteTargetRollbackSwitchesAndStagesResume(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	rollback := "v1.38.3-20260922-0900"
	root := fakeInstallRoot(t, []string{active, rollback}, active)
	addVersionTreeWithCLI(t, root, rollback, true)
	fakeVersionedConfig(t, true)

	callerSession := filepath.Join(t.TempDir(), "caller.session.jsonl")
	callerCtrl := &retargetRuntimeController{status: control.RuntimeStatus{Running: true}, path: callerSession}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: callerSession, Ctrl: callerCtrl, autopilot: true, Ready: true},
	}}
	// The busy guard must exempt the calling session's own turn (task 254):
	// the caller is RUNNING, and the swap still has to go through.
	if busy := app.restartBusyReason(callerSession); busy != "" {
		t.Fatalf("caller session must be exempt: %s", busy)
	}
	if busy := app.restartBusyReason(""); busy == "" {
		t.Fatal("an empty caller session must stay busy-blocked")
	}

	// A second running tab blocks the swap even with the exemption.
	other := filepath.Join(t.TempDir(), "other.session.jsonl")
	app.tabs["t2"] = &WorkspaceTab{ID: "t2", SessionPath: other, Ctrl: &retargetRuntimeController{status: control.RuntimeStatus{Running: true}, path: other}, Ready: true}
	if busy := app.restartBusyReason(callerSession); busy == "" {
		t.Fatal("another tab's running turn must still refuse the swap")
	}
	delete(app.tabs, "t2")

	// tl1585 钉面同步: this pin's subject is the swap+resume machinery (the
	// boundary waiter's body), not the ctx gate — drive executeStagedTarget
	// directly.
	controller := autonomousUpdateController{app: app}
	versionSwitchStartLauncher = func(string, int) error { return nil }
	versionSwitchQuit = func(*App) {}

	if _, err := controller.SetTarget(t.Context(), rollback); err != nil {
		t.Fatal(err)
	}
	result, err := controller.executeStagedTarget(callerSession)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "do not retry") {
		t.Fatalf("result must warn against retrying: %q", result)
	}
	// The pointer moved: this is the visible effect of the rollback.
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != rollback {
		t.Fatalf("active version = %q, want %q", ptr.ActiveVersion, rollback)
	}
	// The pending target is consumed.
	app.autonomousMu.Lock()
	consumed := app.autonomousPending.kind == ""
	app.autonomousMu.Unlock()
	if !consumed {
		t.Fatal("execute must consume the staged target")
	}
	// The resume marker is staged for the autopilot caller.
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 1 || state.Sessions[0].Path != callerSession {
		t.Fatalf("resume marker = %+v, want exactly the caller session", state.Sessions)
	}
}

func TestStageAutonomousUpdateResumeSkipsNonAutopilot(t *testing.T) {
	isolateDesktopUserDirs(t)
	callerSession := filepath.Join(t.TempDir(), "attended.session.jsonl")
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: callerSession, Ctrl: &retargetRuntimeController{path: callerSession}, autopilot: false, Ready: true},
	}}
	app.stageAutonomousUpdateResume(callerSession)
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 0 {
		t.Fatalf("attended session must not be staged for resume: %+v", state.Sessions)
	}
}

// TestStageAutonomousUpdateResumeDial covers the user-ruled three scopes
// (task 254): off stages nothing, goal_autopilot stages only the autopilot
// caller, all additionally stages every other mid-turn session.
func TestStageAutonomousUpdateResumeDial(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		attended  bool
		wantCount int
	}{
		{mode: "off", attended: false, wantCount: 0},
		{mode: "off", attended: true, wantCount: 0},
		{mode: "goal_autopilot", attended: false, wantCount: 1},
		{mode: "goal_autopilot", attended: true, wantCount: 0},
		{mode: "all", attended: false, wantCount: 2},
		{mode: "all", attended: true, wantCount: 2},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			callerSession := filepath.Join(t.TempDir(), "caller.session.jsonl")
			otherSession := filepath.Join(t.TempDir(), "other-running.session.jsonl")
			app := &App{tabs: map[string]*WorkspaceTab{
				"t1": {ID: "t1", SessionPath: callerSession, Ctrl: &retargetRuntimeController{path: callerSession, status: control.RuntimeStatus{Running: true}}, autopilot: !tc.attended, Ready: true},
				"t2": {ID: "t2", SessionPath: otherSession, Ctrl: &retargetRuntimeController{path: otherSession, status: control.RuntimeStatus{Running: true}}, Ready: true},
			}}
			if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume(tc.mode) }); err != nil {
				t.Fatal(err)
			}
			app.stageAutonomousUpdateResume(callerSession)
			state := readAutonomousUpdateResumeFile()
			if len(state.Sessions) != tc.wantCount {
				t.Fatalf("mode %q: staged %d sessions, want %d: %+v", tc.mode, len(state.Sessions), tc.wantCount, state.Sessions)
			}
		})
	}
}

func TestMaybeResumeAutonomousUpdateTabConsumesMarkerOnce(t *testing.T) {
	isolateDesktopUserDirs(t)
	sessionPath := filepath.Join(t.TempDir(), "resumed.session.jsonl")
	app := &App{}
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{
		{Path: sessionPath, StagedAt: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	// Ready tab whose session matches the marker. SubmitToTab may refuse (no
	// wails runtime here) — the marker must be consumed regardless, so a
	// failed resume can never resurrect on a later ordinary restart.
	ctrl := &retargetRuntimeController{path: sessionPath}
	tab := &WorkspaceTab{ID: "t1", SessionPath: sessionPath, Ready: true, Ctrl: ctrl}
	app.maybeResumeAutonomousUpdateTab(tab)
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 0 {
		t.Fatalf("marker must be consumed on first sight: %+v", state.Sessions)
	}
	// Task 263 fix 2 + 300: the auto-resume owns the "continue" decision, so it
	// clears the recovery pause (the banner's precondition) in the same hand-off —
	// one channel: no "review before resuming" notice while the resume is
	// already on its way. Task 300 pins the PASSIVE form: unpause without a
	// dispatch, so leftover pending guidance never replays into the conversation.
	if len(ctrl.inboxPausedCalls) != 1 || ctrl.inboxPausedCalls[0] {
		t.Fatalf("auto-resume must clear the recovery pause exactly once, passively (unpause): %+v", ctrl.inboxPausedCalls)
	}

	// An unrelated session's restore must neither touch nor consume the entry.
	keep := filepath.Join(t.TempDir(), "other.session.jsonl")
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{
		{Path: keep, StagedAt: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	app.maybeResumeAutonomousUpdateTab(&WorkspaceTab{ID: "t2", SessionPath: sessionPath, Ready: true})
	state = readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 1 || state.Sessions[0].Path != keep {
		t.Fatalf("unrelated restore must leave the marker alone: %+v", state.Sessions)
	}
}

// TestResumeMarkerRoundTripsJSON guards the file format: the resume path reads
// it back with plain json.Unmarshal, so the shape is part of the contract.
func TestResumeMarkerRoundTripsJSON(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{
		{Path: "a.jsonl", StagedAt: 7},
	}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(autonomousUpdateResumePath())
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["sessions"]; !ok {
		t.Fatalf("marker file must carry a sessions array: %s", raw)
	}
}

// ── tl1500 刀④返修（2026-11-09 用户拍板二轮①）+ tl1585 扩全回合（2026-11-14
// 用户令）钉族 ─────────────────────────
// 修前红 = 刀④拒绝语义（mid_turn_host_restart_refused）；修后绿 = 排队 +
// 回合边界自动执行。tl1585 把门槛从心跳回合扩至全回合：前台「立即执行」面退役
// （其 quitGrace+relaunch 链与回合收尾之间是竞态，包92 装机实例=拦腰打断），
// 心跳与前台同门排队。

// stubBoundaryRestartSeams swaps every process-lifecycle seam the queued
// boundary restart can reach (publish/switch/plain-restart faces) for no-ops,
// pointing the install root at the scratch layout. Returns the launcher
// handoff counter — the observable effect of a committed restart path.
//
// The launcher/quit stubs are deliberately NOT restored: both restart paths
// arm a delayed-quit goroutine (750ms-3s) that may fire AFTER the test
// returned and Cleanup ran — restoring the real quitApp there would kill the
// whole test process. Same convention as the pre-existing tests, which assign
// these vars without restoring them; only the path seam (restartResolveInstallRoot)
// is restored, since scratch roots must not leak into other tests.
func stubBoundaryRestartSeams(t *testing.T, root string) *atomic.Int32 {
	t.Helper()
	origResolve := restartResolveInstallRoot
	t.Cleanup(func() {
		restartResolveInstallRoot = origResolve
	})
	var calls atomic.Int32
	restartResolveInstallRoot = func(string) (string, error) { return root, nil }
	restartStartLauncher = func(string, int) error { calls.Add(1); return nil }
	restartQuit = func(*App) {}
	versionSwitchStartLauncher = func(string, int) error { calls.Add(1); return nil }
	versionSwitchQuit = func(*App) {}
	// The plain-restart face launchability-checks the launcher before it will
	// proceed; the scratch layout needs one to hand off to.
	if err := os.WriteFile(filepath.Join(root, installlayout.LauncherBinaryName()), []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &calls
}

// waitForBoundaryRestart polls until cond holds — the turn-boundary waiter
// runs on its own goroutine, so the test must synchronize on the observable
// effect instead of sleeping.
func waitForBoundaryRestart(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// boundaryQueueSnapshot reads the pending-restart slot under its mutex.
func boundaryQueueSnapshot(app *App) (pendingKind, queuedAction string, slotEmpty bool) {
	app.autonomousMu.Lock()
	defer app.autonomousMu.Unlock()
	pendingKind = app.autonomousPending.kind
	if app.autonomousBoundaryRestart != nil {
		queuedAction = app.autonomousBoundaryRestart.action
		return pendingKind, queuedAction, false
	}
	return pendingKind, "", true
}

// 钉①: heartbeat-turn execute queues instead of refusing — the staged target
// is NOT consumed mid-turn (no swap, no launcher handoff), the note carries the
// queued marker, and after the turn context ends the boundary waiter consumes
// the staged target through the existing rollback path (pointer moved).
func TestHeartbeatTurnExecuteQueuesForTurnBoundary(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	rollback := "v1.38.3-20260922-0900"
	root := fakeInstallRoot(t, []string{active, rollback}, active)
	addVersionTreeWithCLI(t, root, rollback, true)
	fakeVersionedConfig(t, true)
	launcherCalls := stubBoundaryRestartSeams(t, root)

	app := &App{}
	controller := newAutonomousUpdateController(app)
	if _, err := controller.SetTarget(t.Context(), rollback); err != nil {
		t.Fatal(err)
	}

	turnCtx, cancel := context.WithCancel(control.WithHeartbeatTurn(t.Context()))
	note, err := controller.ExecuteTarget(turnCtx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, midTurnRestartQueuedMarker) {
		t.Fatalf("queued note must carry the marker (修前红=refused): %q", note)
	}
	if !strings.Contains(note, "queued") {
		t.Fatalf("note must tell the model the restart is queued: %q", note)
	}
	pendingKind, queuedAction, slotEmpty := boundaryQueueSnapshot(app)
	if pendingKind != "installed" {
		t.Fatalf("queued execute must NOT consume the staged target mid-turn, kind=%q", pendingKind)
	}
	if slotEmpty || queuedAction != "execute" {
		t.Fatalf("slot must hold the execute request (empty=%v action=%q)", slotEmpty, queuedAction)
	}
	if ptr, ptrErr := installlayout.ReadCurrent(root); ptrErr != nil || ptr.ActiveVersion != active {
		t.Fatalf("no swap may happen mid-turn: ptr=%+v err=%v", ptr, ptrErr)
	}
	if launcherCalls.Load() != 0 {
		t.Fatalf("no launcher handoff mid-turn: %d", launcherCalls.Load())
	}

	// 回合结束（转录落盘、TurnDone 发出之后守卫才 cancel ctx——测试里直接
	// cancel 模拟该边界）→ 排队者执行既有 rollback 路径。launcher 计数一并
	// 入条件：startLauncher 在 writeUpdateRestartMarker 之后，条件成立即整条
	// swap 链已走完，测试返回时不会有 goroutine 再触碰用户目录。
	cancel()
	waitForBoundaryRestart(t, "turn-boundary rollback execute", func() bool {
		if launcherCalls.Load() == 0 {
			return false
		}
		ptr, err := installlayout.ReadCurrent(root)
		return err == nil && ptr.ActiveVersion == rollback
	})
	pendingKind, _, _ = boundaryQueueSnapshot(app)
	if pendingKind != "" {
		t.Fatalf("boundary execution must consume the staged target, kind=%q", pendingKind)
	}
	_, _, slotEmpty = boundaryQueueSnapshot(app)
	if !slotEmpty {
		t.Fatal("boundary execution must drain the queue slot")
	}
}

// 钉①(restart 动作): heartbeat-turn RestartOnly queues the same way and the
// boundary waiter runs the plain-restart core (launcher handoff observable).
func TestHeartbeatTurnRestartQueuesAndRunsAtBoundary(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	launcherCalls := stubBoundaryRestartSeams(t, root)

	app := &App{}
	controller := newAutonomousUpdateController(app)
	turnCtx, cancel := context.WithCancel(control.WithHeartbeatTurn(t.Context()))
	note, err := controller.RestartOnly(turnCtx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, midTurnRestartQueuedMarker) {
		t.Fatalf("queued note must carry the marker: %q", note)
	}
	_, queuedAction, slotEmpty := boundaryQueueSnapshot(app)
	if slotEmpty || queuedAction != "restart" {
		t.Fatalf("slot must hold the restart request (empty=%v action=%q)", slotEmpty, queuedAction)
	}
	if launcherCalls.Load() != 0 {
		t.Fatalf("no launcher handoff mid-turn: %d", launcherCalls.Load())
	}

	cancel()
	waitForBoundaryRestart(t, "turn-boundary plain restart", func() bool {
		return launcherCalls.Load() > 0
	})
	_, _, slotEmpty = boundaryQueueSnapshot(app)
	if !slotEmpty {
		t.Fatal("boundary execution must drain the queue slot")
	}
}

// 钉②: a second restart_update call in the SAME turn only reports the
// existing queue — the first request keeps owning the slot (restart first wins;
// a later execute call does not overwrite it). The test CANCELS inside its body
// and waits for the queued restart to run, so no waiter goroutine outlives the
// test touching paths whose isolation has already been torn down.
func TestHeartbeatTurnRestartQueueDedupesPerTurn(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	launcherCalls := stubBoundaryRestartSeams(t, root)

	app := &App{}
	controller := newAutonomousUpdateController(app)
	turnCtx, cancel := context.WithCancel(control.WithHeartbeatTurn(t.Context()))

	first, err := controller.RestartOnly(turnCtx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, midTurnRestartQueuedMarker) {
		t.Fatalf("first call must queue: %q", first)
	}
	second, err := controller.ExecuteTarget(turnCtx, "")
	if err != nil {
		t.Fatalf("the dedup face must not error the model into retrying: %v", err)
	}
	if !strings.Contains(second, midTurnRestartQueuedMarker) || !strings.Contains(second, "duplicate") {
		t.Fatalf("second call must report the existing queue: %q", second)
	}
	_, queuedAction, slotEmpty := boundaryQueueSnapshot(app)
	if slotEmpty || queuedAction != "restart" {
		t.Fatalf("first request must keep owning the slot (empty=%v action=%q)", slotEmpty, queuedAction)
	}

	cancel()
	waitForBoundaryRestart(t, "dedup boundary restart", func() bool {
		return launcherCalls.Load() > 0
	})
}

// 钉③(tl1585 翻转，验收④): a foreground (no heartbeat stamp) call queues the
// same way — the pre-tl1585 immediate face is retired, and after the turn
// context ends the boundary waiter runs the same rollback body the heartbeat
// path uses (swap committed, staged target consumed, slot drained).
func TestForegroundTurnExecuteQueuesForTurnBoundary(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	rollback := "v1.38.3-20260922-0900"
	root := fakeInstallRoot(t, []string{active, rollback}, active)
	addVersionTreeWithCLI(t, root, rollback, true)
	fakeVersionedConfig(t, true)
	launcherCalls := stubBoundaryRestartSeams(t, root)

	app := &App{}
	controller := newAutonomousUpdateController(app)
	if _, err := controller.SetTarget(t.Context(), rollback); err != nil {
		t.Fatal(err)
	}

	// 前台回合 ctx：无心跳戳的普通可取消 ctx（生产链=回合守卫在收尾完成后
	// cancel；测试直接 cancel 模拟该边界）。
	turnCtx, cancel := context.WithCancel(t.Context())
	note, err := controller.ExecuteTarget(turnCtx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, midTurnRestartQueuedMarker) {
		t.Fatalf("foreground execute must queue (tl1585), got: %q", note)
	}
	pendingKind, queuedAction, slotEmpty := boundaryQueueSnapshot(app)
	if pendingKind != "installed" {
		t.Fatalf("queued foreground execute must NOT consume the staged target mid-turn, kind=%q", pendingKind)
	}
	if slotEmpty || queuedAction != "execute" {
		t.Fatalf("slot must hold the execute request (empty=%v action=%q)", slotEmpty, queuedAction)
	}
	if ptr, ptrErr := installlayout.ReadCurrent(root); ptrErr != nil || ptr.ActiveVersion != active {
		t.Fatalf("no swap may happen mid-turn: ptr=%+v err=%v", ptr, ptrErr)
	}
	if launcherCalls.Load() != 0 {
		t.Fatalf("no launcher handoff mid-turn: %d", launcherCalls.Load())
	}

	cancel()
	waitForBoundaryRestart(t, "turn-boundary foreground rollback", func() bool {
		if launcherCalls.Load() == 0 {
			return false
		}
		ptr, err := installlayout.ReadCurrent(root)
		return err == nil && ptr.ActiveVersion == rollback
	})
	pendingKind, _, _ = boundaryQueueSnapshot(app)
	if pendingKind != "" {
		t.Fatalf("boundary execution must consume the staged target, kind=%q", pendingKind)
	}
	_, _, slotEmpty = boundaryQueueSnapshot(app)
	if !slotEmpty {
		t.Fatal("boundary execution must drain the queue slot")
	}
}
