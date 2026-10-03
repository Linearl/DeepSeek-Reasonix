package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务461-P2 验收：更新重启标记的状态判据 + 门禁消费语义 + 兜底②不重放。
//
// 判据：update-restart-marker.json 存在 = 上一次退出是更新重启（自动恢复放行，
// 读后即删）；不存在 = 普通启动（自动恢复整体禁用，未触发的名册弃置并点名）。

func writeMarkerForTest(t *testing.T, reason, version string) {
	t.Helper()
	writeUpdateRestartMarker(reason, version)
	if _, err := os.Stat(updateRestartMarkerPath()); err != nil {
		t.Fatalf("precondition: marker file must exist after write: %v", err)
	}
}

func TestUpdateRestartMarkerGateOpensOnceAndConsumes(t *testing.T) {
	isolateDesktopUserDirs(t)
	resetUpdateRestartGateForTest()
	t.Cleanup(resetUpdateRestartGateForTest)

	writeMarkerForTest(t, "publish", "v1.39.0")

	if !updateRestartResumeAllowed() {
		t.Fatal("a launch continuing an update relaunch must open the resume gate")
	}
	// 读后即删：标记不跨两次启动生效。
	if _, err := os.Stat(updateRestartMarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("the marker must be consumed (deleted) on first read, stat err = %v", err)
	}
	// 同一进程内重复询问保持同一裁决（tabs restore 逐 tab 调用）。
	if !updateRestartResumeAllowed() {
		t.Fatal("the once-per-process verdict must stay open")
	}
}

func TestUpdateRestartMarkerGateClosedWithoutMarker(t *testing.T) {
	isolateDesktopUserDirs(t)
	resetUpdateRestartGateForTest()
	t.Cleanup(resetUpdateRestartGateForTest)

	// 上一次重启（手动）登记过一个待恢复会话：普通启动必须不恢复，且名册弃置。
	session := filepath.Join(t.TempDir(), "blocked.session.jsonl")
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging must succeed")
	}

	if updateRestartResumeAllowed() {
		t.Fatal("a launch without the update marker must keep the resume gate closed")
	}
	if left := rosterPaths(t); len(left) != 0 {
		t.Fatalf("a non-update launch must drop the unfired roster ("+restartResumeDroppedMarker+"), left = %v", left)
	}
}

func TestUpdateRestartMarkerNotWrittenByPlainRestartPath(t *testing.T) {
	isolateDesktopUserDirs(t)
	resetUpdateRestartGateForTest()
	t.Cleanup(resetUpdateRestartGateForTest)

	// 正常重启路径（RestartDesktop）不经过任何 writeUpdateRestartMarker 调用点；
	// 这里钉住判据本身：无更新动作时文件不存在，门禁关闭。
	if _, err := os.Stat(updateRestartMarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("no marker must exist without an update-driven restart, stat err = %v", err)
	}
	if updateRestartResumeAllowed() {
		t.Fatal("gate must stay closed without the marker")
	}
}

// interruptedToolProbeController is a stubSessionAPI fake that answers the
// 兜底② probe directly, so the restore-point chain can be asserted without
// building a real executor.
type interruptedToolProbeController struct {
	stubSessionAPI
	path     string
	pending  []provider.ToolCallRecord
	probed   int
	probeHit bool
	mu       sync.Mutex
}

func (c *interruptedToolProbeController) SessionPath() string { return c.path }

func (c *interruptedToolProbeController) HasPendingToolRecovery() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probed++
	c.probeHit = len(c.pending) > 0
	return c.probeHit
}

func (c *interruptedToolProbeController) SetInboxPausedPassive(bool) error { return nil }

func (c *interruptedToolProbeController) SettleRestartInterruptedEffects() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.pending)
	c.pending = nil
	return n
}

// 兜底②（roster 路径）：最后一步是中断的工具调用时，登记照常消费（不复活），
// 但不提交续跑提示、不结算 fence（核实栏留给人工）。
func TestResumeSkipsReplayWhenInterruptedToolPending(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "stuck-in-tool.session.jsonl")
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging must succeed")
	}

	ctrl := &interruptedToolProbeController{path: session, pending: []provider.ToolCallRecord{interruptedWriteRecord()}}
	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	submits := 0
	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		submits++
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	if submits != 0 {
		t.Fatalf("an interrupted tool call must block the replay submit, got %d submits", submits)
	}
	if left := rosterPaths(t); len(left) != 0 {
		t.Fatalf("the roster entry stays consumed (no deferred surprise resume), left = %v", left)
	}
	if got := ctrl.SettleRestartInterruptedEffects(); got == 0 {
		t.Fatal("the fence must stay up for manual review (pending records NOT settled)")
	}
}

// 兜底②（goal 路径）：同一规则挡住 autopilot 恢复的重放。
func TestAutopilotResumeSkipsReplayWhenInterruptedToolPending(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "goal-stuck.session.jsonl")

	ctrl := &interruptedToolProbeController{path: session, pending: []provider.ToolCallRecord{interruptedWriteRecord()}}
	tab := &WorkspaceTab{ID: "t2", SessionPath: session, Ready: true, Ctrl: ctrl, autopilot: true, goal: "watch the inbox"}

	submits := 0
	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		submits++
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app := &App{}
	app.maybeResumeAutopilotTab(tab)

	if submits != 0 {
		t.Fatalf("an interrupted tool call must block the autopilot replay, got %d submits", submits)
	}
	ctrl.mu.Lock()
	probed := ctrl.probed
	ctrl.mu.Unlock()
	if probed == 0 {
		t.Fatal("the backstop probe must run before any submit")
	}
}

// 对照：无挂起工具记录时，roster 恢复照常提交（450 语义不回退）。
func TestResumeStillSubmitsWithoutPendingTools(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "clean.session.jsonl")
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging must succeed")
	}

	ctrl := &interruptedToolProbeController{path: session}
	tab := &WorkspaceTab{ID: "t3", SessionPath: session, Ready: true, Ctrl: ctrl}

	submits := make(chan string, 1)
	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		submits <- tabID
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	select {
	case got := <-submits:
		if got != "t3" {
			t.Fatalf("resume submit on tab %q, want t3", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a clean session must still auto-resume (450 semantics preserved)")
	}
}
