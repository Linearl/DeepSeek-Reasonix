package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/sessioninbox"
	"reasonix/internal/store"
)

// 任务868 状态机级测试：恢复门 × 会话类型 × 条目类型。
//
// 与 840 钉桌面调用顺序的 fake 不同，这里的桩（gateStoreController）包着一
// 颗**真** sessioninbox.Store：跨进程重启用 store_test.go 同款手法模拟
// （manifest 的 runId 改写后重开 → loadOrInitLocked 走 RunID 变更分支），
// 门形态（Paused+Recovered、UserPaused 溯源位、ClaimItem 准入闸）全部是
// manifest 状态机真身，不是手绘快照。桌面策略（autoContinueInboxGateForTab）
// 在其上做矩阵断言。

// gateStoreController bridges the desktop policy to a real Store. Only the
// seams the policy touches are wired; everything else stays a nil-port stub.
type gateStoreController struct {
	stubSessionAPI
	mu          sync.Mutex
	store       *sessioninbox.Store
	interrupted bool // HasPendingToolRecovery face (461-P2 兜底②)
	settleCalls int
	kicks       int
	order       []string
}

func (c *gateStoreController) InboxSnapshot() sessioninbox.InboxSnapshot {
	return c.store.Snapshot()
}

func (c *gateStoreController) ResumeInboxAutoPause() (bool, error) {
	cleared, err := c.store.ResumeAutoPause()
	c.mu.Lock()
	c.order = append(c.order, "resume")
	c.mu.Unlock()
	return cleared, err
}

func (c *gateStoreController) SettlePlannedRestartGuidance() int {
	c.mu.Lock()
	c.settleCalls++
	c.order = append(c.order, "guidance")
	c.mu.Unlock()
	return 0
}

func (c *gateStoreController) HasPendingToolRecovery() bool { return c.interrupted }

func (c *gateStoreController) NotifyInboxRuntimeReady() {
	c.mu.Lock()
	c.kicks++
	c.mu.Unlock()
}

func (c *gateStoreController) kickCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.kicks
}

func (c *gateStoreController) settleCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settleCalls
}

func (c *gateStoreController) orderNow() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.order...)
}

// openGateStore opens a real Store on the session's inbox directory.
func openGateStore(t *testing.T, sessionPath string) *sessioninbox.Store {
	t.Helper()
	st, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("open session inbox: %v", err)
	}
	return st
}

// enqueueGateItem queues one follow-up — the 1508 场景's item shape (collab
// receipts and idle-turn prompts degrade to queued follow-ups).
func enqueueGateItem(t *testing.T, st *sessioninbox.Store, text string) {
	t.Helper()
	_, err := st.Enqueue(sessioninbox.EnqueueRequest{
		Intent:   sessioninbox.IntentFollowup,
		Envelope: sessioninbox.PromptEnvelope{DisplayText: text, RawText: text, SubmitText: text},
	})
	if err != nil {
		t.Fatalf("enqueue %q: %v", text, err)
	}
}

// simulateRestart flips the manifest's run id so the next Open takes the
// cross-process recovery branch (same technique as internal/sessioninbox
// store_test.go — ProcessRunID is process-stable).
func simulateRestart(t *testing.T, sessionPath string) {
	t.Helper()
	manifest := filepath.Join(store.SessionInboxDir(sessionPath), "manifest.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	rewritten := strings.Replace(string(data), sessioninbox.ProcessRunID(), "other-run-id-0000", 1)
	if rewritten == string(data) {
		t.Fatal("precondition: manifest must carry the current process run id")
	}
	if err := os.WriteFile(manifest, []byte(rewritten), 0o644); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
}

// restartGateFixture builds the real restart gate: N queued follow-ups, then a
// simulated process restart. Returns the reopened store and asserts the gate
// shape actually arose (the loadOrInitLocked contract this task builds on).
func restartGateFixture(t *testing.T, sessionPath string, queued int) *sessioninbox.Store {
	t.Helper()
	st := openGateStore(t, sessionPath)
	for i := 0; i < queued; i++ {
		enqueueGateItem(t, st, "回执/空闲轮 follow-up "+string(rune('a'+i)))
	}
	st.Close()
	simulateRestart(t, sessionPath)
	reopened := openGateStore(t, sessionPath)
	t.Cleanup(reopened.Close)
	snap := reopened.Snapshot()
	if !snap.Paused || !snap.Recovered || snap.UserPaused {
		t.Fatalf("precondition: restart must raise the gate (Paused+Recovered, no user bit), got %+v", snap)
	}
	if snap.RecoveredN != queued {
		t.Fatalf("precondition: recovered count = %d, want %d", snap.RecoveredN, queued)
	}
	return reopened
}

// TestUnattendedRestartGateAutoContinues is the 1508 主形：无人值守（autopilot）
// 守护会话 + 15 条积压 follow-up + 跨进程重启 → 门自动解除、派发闸重开、
// 派发被踢、guidance settle 先于清门（840 顺序）。门解除后 ClaimItem 必须能
// 认领队首——「后续消息无法注入」的死锁面被实证打开。
func TestUnattendedRestartGateAutoContinues(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "guardian.session.jsonl")
	st := restartGateFixture(t, session, 15)

	ctrl := &gateStoreController{store: st}
	app := &App{}
	tab := &WorkspaceTab{ID: "t1", Ready: true, Ctrl: ctrl, autopilot: true}

	app.autoContinueInboxGateForTab(tab)

	after := st.Snapshot()
	if after.Paused || after.Recovered || after.RecoveredN != 0 {
		t.Fatalf("the unattended gate must be cleared, got %+v", after)
	}
	if after.UserPaused {
		t.Fatal("auto-continue must not stamp user provenance")
	}
	if got := ctrl.kickCount(); got != 1 {
		t.Fatalf("the dispatch kick must fire exactly once, got %d", got)
	}
	if got := ctrl.settleCount(); got != 1 {
		t.Fatalf("the planned-restart guidance settle must fire exactly once, got %d", got)
	}
	order := ctrl.orderNow()
	if len(order) < 2 || order[0] != "guidance" || order[1] != "resume" {
		t.Fatalf("guidance settle must run BEFORE the gate clears (840 ordering), order = %v", order)
	}
	// 派发准入闸实证重开：队首可认领（868 验收①的死锁面）。
	if err := st.ClaimItem(after.Items[0].ID); err != nil {
		t.Fatalf("claiming the queue head after auto-continue: %v", err)
	}
}

// TestAttendedRestartGateKept 是不回归面（868 验收②）：同样门形态的有人会话
// 完全不被触碰——门、横幅数据、派发闸逐字节保持现状。
func TestAttendedRestartGateKept(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "attended.session.jsonl")
	st := restartGateFixture(t, session, 3)

	ctrl := &gateStoreController{store: st}
	app := &App{}
	tab := &WorkspaceTab{ID: "t1", Ready: true, Ctrl: ctrl, autopilot: false}

	app.autoContinueInboxGateForTab(tab)

	after := st.Snapshot()
	if !after.Paused || !after.Recovered {
		t.Fatalf("an attended session's gate must stay untouched, got %+v", after)
	}
	if got := ctrl.kickCount(); got != 0 {
		t.Fatalf("an attended session must not be kicked, got %d kicks", got)
	}
	if got := ctrl.settleCount(); got != 0 {
		t.Fatalf("an attended session must not settle guidance, got %d settles", got)
	}
}

// TestUnattendedUserHeldPauseKept（868 验收④的另一面）：用户显式持有
// （「保持暂停」按钮落下的 UserPaused 位）不可被无人值守策略越过——709
// 绝对静默不受本件影响；store 层原子判定是最后防线。
func TestUnattendedUserHeldPauseKept(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "user-held.session.jsonl")
	st := restartGateFixture(t, session, 2)
	if err := st.SetUserPaused(true); err != nil {
		t.Fatalf("user pause: %v", err)
	}

	ctrl := &gateStoreController{store: st}
	app := &App{}
	tab := &WorkspaceTab{ID: "t1", Ready: true, Ctrl: ctrl, autopilot: true}

	app.autoContinueInboxGateForTab(tab)

	after := st.Snapshot()
	if !after.Paused || !after.UserPaused {
		t.Fatalf("a user-held pause must survive the unattended policy, got %+v", after)
	}
	if got := ctrl.kickCount(); got != 0 {
		t.Fatalf("a user-held pause must not be kicked, got %d", got)
	}
}

// TestUnattendedPureReviewShelfKept：门形态成立但没有可认领的排队条目
// （全部 Uncertain 检视停靠行）时门保持——纯待人工检视的架子是 709 面向
// 「回来的人」的人审面，自动继续对它无事可做也不该抹横幅。
func TestUnattendedPureReviewShelfKept(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "review-shelf.session.jsonl")
	st := openGateStore(t, session)
	for i := 0; i < 2; i++ {
		enqueueGateItem(t, st, "会被重启改写的 running 条目 "+string(rune('a'+i)))
	}
	// 两条都推到 running，重启后 loadOrInit 把它们改写为 Uncertain：
	// Paused+Recovered 成立、队首为空——「纯检视架」门形态。
	for _, item := range st.Snapshot().Items {
		if err := st.SetState(item.ID, sessioninbox.StateRunning, ""); err != nil {
			t.Fatalf("mark running: %v", err)
		}
	}
	st.Close()
	simulateRestart(t, session)
	reopened := openGateStore(t, session)
	t.Cleanup(reopened.Close)
	snap := reopened.Snapshot()
	if !snap.Paused || !snap.Recovered {
		t.Fatalf("precondition: restart gate must be up, got %+v", snap)
	}
	for _, item := range snap.Items {
		if item.State == sessioninbox.StateQueued {
			t.Fatalf("precondition: no queued head expected, got %+v", snap.Items)
		}
	}

	ctrl := &gateStoreController{store: reopened}
	app := &App{}
	tab := &WorkspaceTab{ID: "t1", Ready: true, Ctrl: ctrl, autopilot: true}

	app.autoContinueInboxGateForTab(tab)

	after := reopened.Snapshot()
	if !after.Paused || !after.Recovered {
		t.Fatalf("a pure review shelf must keep its gate, got %+v", after)
	}
	if got := ctrl.kickCount(); got != 0 {
		t.Fatalf("a pure review shelf must not be kicked, got %d", got)
	}
	if got := ctrl.settleCount(); got != 0 {
		t.Fatalf("a pure review shelf must not settle guidance, got %d", got)
	}
}

// TestUnattendedInterruptedToolKeepsGate：461-P2 兜底②照抄——末步是中断工具
// 调用的无人值守会话门保持（「中断的工具需要核实」评审面板是人审面），
// guidance settle 也不跑（恢复链没接管会话，停靠行维持人审，与 840 同判）。
func TestUnattendedInterruptedToolKeepsGate(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "tool-stuck.session.jsonl")
	st := restartGateFixture(t, session, 2)

	ctrl := &gateStoreController{store: st, interrupted: true}
	app := &App{}
	tab := &WorkspaceTab{ID: "t1", Ready: true, Ctrl: ctrl, autopilot: true}

	app.autoContinueInboxGateForTab(tab)

	after := st.Snapshot()
	if !after.Paused || !after.Recovered {
		t.Fatalf("an interrupted-tool session must keep its gate, got %+v", after)
	}
	if got := ctrl.kickCount(); got != 0 {
		t.Fatalf("an interrupted-tool session must not be kicked, got %d", got)
	}
	if got := ctrl.settleCount(); got != 0 {
		t.Fatalf("an interrupted-tool session must not settle guidance, got %d", got)
	}
}

// TestUnattendedGateDialOffKept：task 254 用户裁决的 dial=off 表示「一切会话
// 手动控制」——无人值守清门同样让位，门保留。
func TestUnattendedGateDialOffKept(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	t.Setenv("REASONIX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[desktop]\nautonomous_update_resume = \"off\"\n"), 0o644); err != nil {
		t.Fatalf("write dial-off config: %v", err)
	}
	if mode := (&App{}).autonomousUpdateResumeMode(); mode != "off" {
		t.Fatalf("precondition: dial must read off, got %q", mode)
	}

	session := filepath.Join(t.TempDir(), "dial-off.session.jsonl")
	st := restartGateFixture(t, session, 2)

	ctrl := &gateStoreController{store: st}
	app := &App{}
	tab := &WorkspaceTab{ID: "t1", Ready: true, Ctrl: ctrl, autopilot: true}

	app.autoContinueInboxGateForTab(tab)

	after := st.Snapshot()
	if !after.Paused || !after.Recovered {
		t.Fatalf("dial off must keep the gate, got %+v", after)
	}
	if got := ctrl.kickCount(); got != 0 {
		t.Fatalf("dial off must not kick, got %d", got)
	}
}

// TestUnattendedNoGateIsNoop：无人值守但无门（新会话/已清）→ 逐字节无操作，
// 不踢派发不结算不留痕（防止恢复点变成常驻扰动源）。
func TestUnattendedNoGateIsNoop(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "fresh.session.jsonl")
	st := openGateStore(t, session)
	t.Cleanup(st.Close)
	enqueueGateItem(t, st, "普通排队条目（无重启门）")

	ctrl := &gateStoreController{store: st}
	app := &App{}
	tab := &WorkspaceTab{ID: "t1", Ready: true, Ctrl: ctrl, autopilot: true}

	app.autoContinueInboxGateForTab(tab)

	if got := ctrl.kickCount(); got != 0 {
		t.Fatalf("a gateless inbox must not be kicked, got %d", got)
	}
	if got := ctrl.settleCount(); got != 0 {
		t.Fatalf("a gateless inbox must not settle guidance, got %d", got)
	}
	if len(ctrl.orderNow()) != 0 {
		t.Fatalf("a gateless inbox must not be touched at all: %+v", ctrl.orderNow())
	}
}
