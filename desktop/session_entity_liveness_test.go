package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// tl 1266 — 会话实体查无的三向防线。实体事实源 = 会话 .jsonl 本身（与
// contact directory 同一磁盘语义）。三方向断言：
//   ① 重启恢复的骨架 tab：实体查无 → 投影「已断开」issue（可见态）；
//   ② 活 tab 谎报 busy（running 残影）：实体查无 → 绿点/计数/可终止全清；
//   ③ 「继续/发送」前置校验：实体查无 → 明确断开报错（不再静默降级进队列）。

func newEntityLivenessApp(t *testing.T) *App {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.readyHook = func() {}
	return app
}

func registerEntityLivenessTab(t *testing.T, app *App, tab *WorkspaceTab) {
	t.Helper()
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.mu.Unlock()
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})
}

// 方向②: the controller claims a running turn while the session entity is
// gone from disk — the tab strip must NOT report running (no green dot, no
// worked counter, nothing cancellable) and must carry the session_missing
// disconnect issue instead. With the entity present the same controller still
// reports running normally (control leg).
func TestTabMetaClearsRunningGhostWhenSessionEntityMissing(t *testing.T) {
	app := newEntityLivenessApp(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "ghost.jsonl", "topic_ghost", "Ghost", "", "ghost prompt", time.Now())
	sink := &tabEventSink{tabID: "ghost", app: app}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "ghost", Sink: sink})
	tab := &WorkspaceTab{
		ID:          "ghost",
		Scope:       "global",
		TopicID:     "topic_ghost",
		SessionPath: path,
		Ready:       true,
		Ctrl:        ctrl,
		sink:        sink,
		disabledMCP: map[string]ServerView{},
	}
	registerEntityLivenessTab(t, app, tab)

	ctrl.Submit("keep running")
	<-runner.started

	// Control leg: entity on disk → the running claim is reported as-is.
	if m := app.tabMeta(tab, true); !m.Running || !m.Cancellable {
		t.Fatalf("tabMeta with entity present: running=%v cancellable=%v, want running+cancellable", m.Running, m.Cancellable)
	}

	// Entity disappears (deleted / cleanup / previous-instance leftover).
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove session entity: %v", err)
	}
	m := app.tabMeta(tab, true)
	if m.Running || m.PendingPrompt || m.BackgroundJobs != 0 || m.Cancellable || m.CancelRequested {
		t.Fatalf("tabMeta with entity gone reports ghost activity: running=%v pendingPrompt=%v jobs=%d cancellable=%v cancelRequested=%v",
			m.Running, m.PendingPrompt, m.BackgroundJobs, m.Cancellable, m.CancelRequested)
	}
	if m.Ready {
		t.Fatalf("tabMeta with entity gone still reports ready")
	}
	if m.Runtime.Issue == nil || m.Runtime.Issue.Code != sessionMissingRuntimeIssueCode {
		t.Fatalf("tabMeta with entity gone carries issue %+v, want code %q", m.Runtime.Issue, sessionMissingRuntimeIssueCode)
	}
	if !strings.Contains(m.Runtime.Issue.Message, "会话已断开") {
		t.Fatalf("disconnect issue message = %q, want 会话已断开 prefix", m.Runtime.Issue.Message)
	}

	// The projection is honest again once the entity reappears (self-heal).
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"ghost prompt\"}\n"), 0o644); err != nil {
		t.Fatalf("recreate session entity: %v", err)
	}
	if m := app.tabMeta(tab, true); !m.Running {
		t.Fatalf("tabMeta after entity reappeared: running=false, want running claim restored")
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
}

// 方向①: a restored skeleton (no controller yet) whose persisted session
// entity is gone projects the disconnect issue so the strip can show 「已断开」
// instead of a plain starting tab. Phase stays "starting" so the lazy
// click-to-build contract (task 619 ②) still fires; the build then fails with
// the same honest message (covered by the build-path behavior).
func TestTabMetaMarksRestoredSkeletonDisconnectedWhenEntityMissing(t *testing.T) {
	app := newEntityLivenessApp(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	missingPath := filepath.Join(dir, "gone.jsonl")
	skeleton := &WorkspaceTab{
		ID:          "skeleton",
		Scope:       "global",
		TopicID:     "topic_skeleton",
		SessionPath: missingPath,
		sink:        &tabEventSink{tabID: "skeleton", app: app},
		disabledMCP: map[string]ServerView{},
	}
	registerEntityLivenessTab(t, app, skeleton)

	m := app.tabMeta(skeleton, false)
	if m.Runtime.Issue == nil || m.Runtime.Issue.Code != sessionMissingRuntimeIssueCode {
		t.Fatalf("skeleton tabMeta issue = %+v, want code %q", m.Runtime.Issue, sessionMissingRuntimeIssueCode)
	}
	if m.Runtime.Phase != sessionRuntimeStarting {
		t.Fatalf("skeleton tabMeta phase = %q, want %q (lazy build must stay armed)", m.Runtime.Phase, sessionRuntimeStarting)
	}
	if m.Running {
		t.Fatalf("skeleton tabMeta reports running, want false")
	}

	// Control leg: a skeleton whose entity exists carries no disconnect issue.
	alivePath := writeTopicSessionWithPrompt(t, dir, "alive.jsonl", "topic_alive", "Alive", "", "alive prompt", time.Now())
	alive := &WorkspaceTab{
		ID:          "skeleton_alive",
		Scope:       "global",
		TopicID:     "topic_alive",
		SessionPath: alivePath,
		sink:        &tabEventSink{tabID: "skeleton_alive", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.mu.Lock()
	app.tabs[alive.ID] = alive
	app.tabOrder = append(app.tabOrder, alive.ID)
	app.mu.Unlock()
	if m := app.tabMeta(alive, false); m.Runtime.Issue != nil {
		t.Fatalf("alive-entity skeleton tabMeta carries issue %+v, want none", m.Runtime.Issue)
	}
}

// 方向③: the submit path validates the session entity before admitting the
// turn. A missing entity returns the explicit disconnect error — never the
// "turn already running" shape the frontend silently degrades into the
// durable queue (the incident's 「点了继续没反应」). With the entity present
// the guard passes through and the pre-existing busy rejection keeps its own
// sentinel.
func TestSubmitToMissingSessionEntityRejectedWithFeedback(t *testing.T) {
	app := newEntityLivenessApp(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "continue.jsonl", "topic_continue", "Continue", "", "continue prompt", time.Now())
	sink := &tabEventSink{tabID: "continue", app: app}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "continue", Sink: sink})
	tab := &WorkspaceTab{
		ID:          "continue",
		Scope:       "global",
		TopicID:     "topic_continue",
		SessionPath: path,
		Ready:       true,
		Ctrl:        ctrl,
		sink:        sink,
		disabledMCP: map[string]ServerView{},
	}
	registerEntityLivenessTab(t, app, tab)

	ctrl.Submit("occupy the executor")
	<-runner.started

	// Entity gone while the controller still claims busy: the guard must fire
	// BEFORE the busy rejection, so the user gets explicit feedback instead of
	// a silent enqueue into the dead executor's queue.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove session entity: %v", err)
	}
	err := app.SubmitToTab(tab.ID, "继续")
	if !errors.Is(err, errSessionDisconnected) {
		t.Fatalf("SubmitToTab with entity gone err = %v, want errSessionDisconnected", err)
	}
	if !strings.Contains(err.Error(), "会话已断开") || !strings.Contains(err.Error(), "重新打开") {
		t.Fatalf("disconnect error text = %q, want 会话已断开…重新打开", err.Error())
	}
	if strings.Contains(strings.ToLower(err.Error()), "turn already running") {
		t.Fatalf("disconnect error must not use the busy shape that degrades silently: %q", err.Error())
	}

	// Entity present: the guard passes through; the wedged-running controller
	// keeps its own pre-existing busy sentinel (unchanged semantics).
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"continue prompt\"}\n"), 0o644); err != nil {
		t.Fatalf("recreate session entity: %v", err)
	}
	err = app.SubmitToTab(tab.ID, "继续")
	if !errors.Is(err, control.ErrTurnRunning) {
		t.Fatalf("SubmitToTab with entity present err = %v, want control.ErrTurnRunning (guard pass-through)", err)
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
}

// 方向①（重建路径判定缝）: pinned 路径「ok 但无可恢复会话」的两种 nil 必须
// 分流——文件还在的空占位照旧当新会话起点，文件已消失的实体查无返回断开
// 哨兵（诚实失败），而不是静默把 tab 重置成空会话。
func TestMissingPinnedSessionResumeErrSplitsPlaceholderFromGone(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone.jsonl")
	if err := missingPinnedSessionResumeErr(gone, nil); !errors.Is(err, errSessionDisconnected) {
		t.Fatalf("gone pinned path with nothing to resume err = %v, want errSessionDisconnected", err)
	}
	alive := writeTopicSessionWithPrompt(t, dir, "placeholder.jsonl", "topic_ph", "PH", "", "ph", time.Now())
	if err := missingPinnedSessionResumeErr(alive, nil); err != nil {
		t.Fatalf("existing empty placeholder err = %v, want nil (fresh-start contract unchanged)", err)
	}
	if err := missingPinnedSessionResumeErr(gone, &agent.Session{}); err != nil {
		t.Fatalf("non-nil resume session err = %v, want nil (resume wins)", err)
	}
	if err := missingPinnedSessionResumeErr("", nil); err != nil {
		t.Fatalf("empty path err = %v, want nil (fresh session has no entity to lose)", err)
	}
}

// 双边锚的放行半边：stale-binding 自愈窗口（tab 持久化锚健在、控制器还指着
// 已不存在的旧路径）不得被守卫/投影拦截——下一次提交正是既有「提交即修复」
// 契约的触发器（app_commands_2 stale-binding 家族钉的场景）。
func TestSessionEntityGuardPassesStaleBindingWhenTabAnchorExists(t *testing.T) {
	app := newEntityLivenessApp(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	anchorPath := writeTopicSessionWithPrompt(t, dir, "anchor.jsonl", "topic_anchor", "Anchor", "", "anchor prompt", time.Now())
	staleCtrl := control.New(control.Options{
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "wrong.jsonl"), // does not exist: stale binding
		Label:       "stale",
		Sink:        event.Discard,
	})
	tab := &WorkspaceTab{
		ID:          "stale_binding",
		Scope:       "global",
		TopicID:     "topic_anchor",
		SessionPath: anchorPath,
		Ready:       true,
		Ctrl:        staleCtrl,
		sink:        &tabEventSink{tabID: "stale_binding", app: app},
		disabledMCP: map[string]ServerView{},
	}
	registerEntityLivenessTab(t, app, tab)

	if err := app.sessionEntityAdmissionErr(tab.ID); err != nil {
		t.Fatalf("guard must pass the stale-binding self-heal window, got %v", err)
	}
	if m := app.tabMeta(tab, true); m.Runtime.Issue != nil {
		t.Fatalf("tabMeta must not project a disconnect for the stale-binding window, got %+v", m.Runtime.Issue)
	}

	// 双锚都查无才是断开：把 tab 锚也删掉后，守卫照常生效。tabMeta 投影是
	// 残影 scoped 的（只在控制器声明活跃工作时清除虚假 running），空闲 tab
	// 的用户反馈由提交守卫承担——两层各管各的验收面。
	if err := os.Remove(anchorPath); err != nil {
		t.Fatalf("remove anchor: %v", err)
	}
	if err := app.sessionEntityAdmissionErr(tab.ID); !errors.Is(err, errSessionDisconnected) {
		t.Fatalf("guard with both anchors gone err = %v, want errSessionDisconnected", err)
	}
	if m := app.tabMeta(tab, true); m.Runtime.Issue != nil {
		t.Fatalf("idle tab with both anchors gone must stay ghost-projection-free, got %+v", m.Runtime.Issue)
	}
}

// sessionEntityExists 单测：空路径=新会话（视为存在）、清理待执行=查无、
// 普通文件=存在、目录/缺失=查无。
func TestSessionEntityExistsSemantics(t *testing.T) {
	isolateDesktopUserDirs(t)
	if !sessionEntityExists("") {
		t.Fatalf("empty path (fresh session) must count as existing")
	}
	if sessionEntityExists(filepath.Join(t.TempDir(), "absent.jsonl")) {
		t.Fatalf("missing file must not count as existing")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "real.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sessionEntityExists(file) {
		t.Fatalf("existing file must count as existing")
	}
	if sessionEntityExists(dir) {
		t.Fatalf("a directory is not a session entity")
	}
	pending := filepath.Join(dir, "pending.jsonl")
	if err := os.WriteFile(pending, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent.CleanupPendingPath(pending), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sessionEntityExists(pending) {
		t.Fatalf("cleanup-pending entity must count as missing")
	}
}
