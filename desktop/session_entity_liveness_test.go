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
)

// tl 1266 — 装机重启后会话 runtime 未重站且 UI 无感知：tab 挂 running 残影
// （绿点+已工作计数）但 contact directory 查无实体，「继续」把消息塞进已死
// 执行体（20261030 用户实锤）。三方向验收：
//   - 方向①：重启恢复的骨架 tab（实体查无）投影为「已断开」可见态
//     （failed 相位 + session_missing issue），不再装普通 starting；
//   - 方向②：实体查无的 tab 不得投 running 残影（绿点/待确认/后台计数/
//     可取消 全清）；
//   - 方向③：提交受理链前置实体校验，查无返回断开哨兵错误（不落入
//     ErrTurnRunning 的静默降级）。
// 全部为读取型投影：不注册表状态变更，实体在盘时旧行为逐字不变（对照组）。

func TestSessionEntityExistsBoundaries(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()

	// 空路径 = 未落盘的新会话（首轮发送前），没有实体可断。
	if !sessionEntityExists("") {
		t.Fatal("empty session path must count as existing (nothing to disconnect)")
	}
	if tabSessionEntityMissing("   ") {
		t.Fatal("blank session path must not count as missing")
	}

	// 在盘文件 = 实体在。
	path := filepath.Join(dir, "live.jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"hi\"}\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if !sessionEntityExists(path) || tabSessionEntityMissing(path) {
		t.Fatal("on-disk session file must count as existing")
	}

	// 查无 = 断开。
	missing := filepath.Join(dir, "gone.jsonl")
	if !tabSessionEntityMissing(missing) {
		t.Fatal("absent session file must count as missing")
	}

	// 清理待执行（延迟删除标记在案）= 按查无，与恢复/名册面同口径。
	if err := agent.MarkCleanupPending(path, "test"); err != nil {
		t.Fatalf("mark cleanup pending: %v", err)
	}
	if !tabSessionEntityMissing(path) {
		t.Fatal("cleanup-pending session must count as missing")
	}
}

// 方向②对照组+实验组：控制器真在跑（blockingRunner 挂住）时绿点照常；
// 同一状态删除实体文件后，tabMeta 必须清掉全部 running 残影并挂
// session_missing issue。
func TestTabMetaClearsRunningGhostWhenSessionEntityMissing(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "tl1266-ghost.jsonl", "topic_tl1266_ghost", "tl1266 ghost", "", "ghost prompt", time.Now())

	app := NewApp()
	sink := &tabEventSink{tabID: "tl1266_ghost", app: app}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "tl1266 ghost", Sink: sink})
	defer ctrl.Close()
	tab := &WorkspaceTab{
		ID:            "tl1266_ghost",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       "topic_tl1266_ghost",
		SessionPath:   path,
		Ctrl:          ctrl,
		Ready:         true,
		sink:          sink,
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	ctrl.Submit("keep running")
	<-runner.started

	tabMetaFor := func() TabMeta {
		for _, m := range app.ListTabs() {
			if m.ID == tab.ID {
				return m
			}
		}
		return TabMeta{}
	}

	// 对照组：实体在盘，running 投影逐字保留。
	meta := tabMetaFor()
	if !meta.Running {
		t.Fatal("control: running tab with on-disk entity must project running")
	}
	if meta.Runtime.Issue != nil {
		t.Fatalf("control: running tab with on-disk entity must not carry an issue, got %q", meta.Runtime.Issue.Code)
	}

	// 实验组：删掉实体（重启后 contact directory 查无的残影现场）。
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove session entity: %v", err)
	}
	meta = tabMetaFor()
	if meta.Running || meta.PendingPrompt || meta.BackgroundJobs != 0 || meta.Cancellable {
		t.Fatalf("ghost tab still claims work: running=%v pending=%v jobs=%d cancellable=%v",
			meta.Running, meta.PendingPrompt, meta.BackgroundJobs, meta.Cancellable)
	}
	if meta.Ready {
		t.Fatal("ghost tab must not project ready")
	}
	if meta.Runtime.Phase != sessionRuntimeFailed {
		t.Fatalf("ghost runtime phase = %q, want %q", meta.Runtime.Phase, sessionRuntimeFailed)
	}
	if meta.Runtime.Issue == nil || meta.Runtime.Issue.Code != sessionMissingRuntimeIssueCode {
		t.Fatalf("ghost runtime issue = %#v, want code %q", meta.Runtime.Issue, sessionMissingRuntimeIssueCode)
	}
	if !strings.Contains(meta.Runtime.Issue.Message, "会话已断开") {
		t.Fatalf("ghost issue message %q must carry the disconnect wording", meta.Runtime.Issue.Message)
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
}

// 方向①：重启恢复的骨架 tab（无控制器）声称的实体查无时，投影为「已断开」
// 可见态；实体在盘的骨架保持普通 starting（未加载语义不变，对照组）。
func TestTabMetaMarksRestoredSkeletonDisconnectedWhenEntityMissing(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "tl1266-skeleton.jsonl", "topic_tl1266_skeleton", "tl1266 skeleton", "", "skeleton prompt", time.Now())

	app := NewApp()
	tab := &WorkspaceTab{
		ID:            "tl1266_skeleton",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       "topic_tl1266_skeleton",
		SessionPath:   path,
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	tabMetaFor := func() TabMeta {
		for _, m := range app.ListTabs() {
			if m.ID == tab.ID {
				return m
			}
		}
		return TabMeta{}
	}

	// 对照组：实体在盘的骨架 = 普通 starting，无 issue（任务 619 未加载语义不动）。
	meta := tabMetaFor()
	if meta.Runtime.Phase != sessionRuntimeStarting {
		t.Fatalf("control: skeleton with on-disk entity phase = %q, want %q", meta.Runtime.Phase, sessionRuntimeStarting)
	}
	if meta.Runtime.Issue != nil {
		t.Fatalf("control: skeleton with on-disk entity must not carry an issue, got %q", meta.Runtime.Issue.Code)
	}

	// 实验组：实体查无（重启前旧实例从未落盘/被删）→「已断开」可见态。
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove session entity: %v", err)
	}
	meta = tabMetaFor()
	if meta.Runtime.Phase != sessionRuntimeFailed {
		t.Fatalf("disconnected skeleton phase = %q, want %q", meta.Runtime.Phase, sessionRuntimeFailed)
	}
	if meta.Runtime.Issue == nil || meta.Runtime.Issue.Code != sessionMissingRuntimeIssueCode {
		t.Fatalf("disconnected skeleton issue = %#v, want code %q", meta.Runtime.Issue, sessionMissingRuntimeIssueCode)
	}
	if meta.Ready {
		t.Fatal("disconnected skeleton must not project ready")
	}
}

// 方向③：提交受理链（beginRuntimeTurn 是全部前台 Submit/继续 入口的单点）
// 前置实体校验——查无返回断开哨兵，且必须先于 ErrTurnRunning 判定（消息不得
// 降级进已死执行体的队列）。
func TestTurnAdmissionRejectsMissingSessionEntity(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "tl1266-admission.jsonl", "topic_tl1266_admission", "tl1266 admission", "", "admission prompt", time.Now())

	app := NewApp()
	tab := &WorkspaceTab{
		ID:            "tl1266_admission",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       "topic_tl1266_admission",
		SessionPath:   path,
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	// 对照组：实体在盘，受理链继续走既有检查（这里落到 workspaceNotReady——
	// 骨架 tab 的既有精确报错），绝不能是断开哨兵。
	_, _, err := app.beginTabTurn(tab.ID, false)
	if err == nil {
		t.Fatal("control: bare skeleton admission unexpectedly succeeded")
	}
	if errors.Is(err, errSessionDisconnected) {
		t.Fatalf("control: admission with on-disk entity must not report disconnect, got: %v", err)
	}

	// 实验组：实体查无 → 断开哨兵。
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove session entity: %v", err)
	}
	_, _, err = app.beginTabTurn(tab.ID, false)
	if !errors.Is(err, errSessionDisconnected) {
		t.Fatalf("admission without entity err = %v, want errSessionDisconnected", err)
	}
	if !strings.Contains(err.Error(), "会话已断开") {
		t.Fatalf("disconnect error %q must carry the user-facing wording", err.Error())
	}
}
