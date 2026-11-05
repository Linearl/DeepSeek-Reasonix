package main

// tl1404: 归档路径的终局保证。正常归档 → 强制归档(撕 lease+save 锁)之后,
// 任何残余死局形态(缺文件+lease 残留活体 / holder unknown+save 锁长持 /
// fork·构建循环占住全局门禁)都必须在有限时间(≤10s)内归档成功——终局兜底
// (进程级清场:cancel 运行、撕本进程全部相关锁、绕过 guard 直拆)也算成功。
// 本文件先钉三种死局形态(红)与「可正常归档绝不触发兜底」的验收面。

import (
	"errors"
	"os"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// archiveDeadlineBudget is the user-visible ceiling for one archive call:
// every deadlock form must converge inside it (fallback included).
const archiveDeadlineBudget = 10 * time.Second

// stageTerminalArchiveTopic stages a single-session topic the way the force
// tests do, returning both the transcript path and the project root so the
// sidebar-entry assertions can read the topic registry.
func stageTerminalArchiveTopic(t *testing.T, name, topicID, title string) (string, string) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, title); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, dir, name, topicID, title, projectRoot, "preserve me", time.Now())
	return sessionPath, projectRoot
}

// 形态①(钉):转录文件已消失(tl1363 僵尸条目族)+lease 残留活体(tl1392 孤儿
// 形态:锁句柄仍被本进程一个无绑定的 lease 对象持有,盘上身份已撕成垃圾)。
// 强制归档必须一次成功且零残留:孤儿被清扫、锁文件消失、转录不复活、topic
// 注册清掉。tl1383/tl1392 的阶梯已能收敛此形态——本钉防止终局兜底改动回退它。
func TestTrashTopicForceSurvivesMissingFileWithLiveLeaseResidue(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_terminal_missing_file"
	sessionPath, projectRoot := stageTerminalArchiveTopic(t, "terminal-missing.jsonl", topicID, "Terminal missing")
	key := sessionRuntimeKey(sessionPath)

	// 残留活体:acquire 一个孤儿 lease(无任何 tab 绑定),把锁文件里的身份
	// 撕成垃圾(句柄保持活体),再把转录文件删掉——「文件不存在 + lease 残留」。
	orphan, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("stage orphan lease: %v", err)
	}
	t.Cleanup(func() { orphan.Release() })
	corruptLeaseLockContent(t, store.SessionLeaseLock(key))
	if err := os.Remove(sessionPath); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}

	started := time.Now()
	if err := NewApp().TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce with missing file + live lease residue = %v, want success within the budget", err)
	}
	if elapsed := time.Since(started); elapsed > archiveDeadlineBudget {
		t.Fatalf("archive took %v, want <%v even through the fallback", elapsed, archiveDeadlineBudget)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("missing-file session resurrected by the archive: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseLock(key)); !os.IsNotExist(err) {
		t.Fatalf("orphan lease lock file survived the archive: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseInfo(key)); !os.IsNotExist(err) {
		t.Fatalf("lease identity record survived the archive: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared", got)
	}
	// 归档后同路径必须可被干净地重新 acquire——孤儿条目/锁句柄零残留的实锤。
	reattach, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("reacquire after archive = %v, want a clean lease state", err)
	}
	reattach.Release()
}

// 形态②(红):holder unknown(无任何 lease 身份记录)+save 锁长持(卡死的
// saver/外部句柄形态——本进程一个裸句柄长持 save 锁,无身份、无 lease 对象、
// 无 tab)。现有阶梯:正常归档被 unknown-holder 分类拒绝;强制归档的 heal 能
// 拿下 lease 锁却在 save 锁上二次失败,最终落进 after-force 死局文案——本测
// 要求强制归档在预算内成功(终局兜底绕过 guard 直拆也算成功)。
//
// Windows 语义注:持有方句柄无 FILE_SHARE_DELETE,归档无法删除仍被持有的
// .lock 文件——终局兜底对它尽力删除并留 slog;测试退场(cleanup unlock)即
// 模拟卡死 saver 最终消亡,残留文件随之可删。
func TestTrashTopicForceSurvivesUnknownHolderWithLongHeldSaveLock(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_terminal_save_lock"
	sessionPath, projectRoot := stageTerminalArchiveTopic(t, "terminal-save-lock.jsonl", topicID, "Terminal save lock")

	// save 锁长持:一个无身份的裸句柄(卡死 saver 形态)。
	unlockSave, err := agent.TryLockSessionFileForTest(sessionPath)
	if err != nil {
		t.Fatalf("hold save lock: %v", err)
	}
	t.Cleanup(unlockSave)

	// 阶梯第一级:正常归档被拒,且拒绝落在 unknown-holder(forceable)家族。
	busyErr := NewApp().TrashTopic(topicID)
	if !errors.Is(busyErr, errSessionBusyUnidentifiedHolder) {
		t.Fatalf("regular TrashTopic against a held save lock = %v, want the unidentified-holder family", busyErr)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("refused archive removed the session: %v", err)
	}

	// 阶梯第二级→终局:强制归档必须在预算内收敛为成功(当前实现死于
	// after-force 死局文案——本断言即红点)。
	started := time.Now()
	if err := NewApp().TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce with unknown holder + long-held save lock = %v, want terminal-fallback success", err)
	}
	if elapsed := time.Since(started); elapsed > archiveDeadlineBudget {
		t.Fatalf("force archive took %v, want <%v even through the fallback", elapsed, archiveDeadlineBudget)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("force archive left the transcript in place: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseInfo(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("lease identity residue survived the archive: %v", err)
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("cleanup-pending marker survived a successful archive")
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared (sidebar entry gone)", got)
	}
}

// 验收钉:兜底仅终局——可正常归档/强制归档可收敛的形态绝不进入终局兜底。
// 正常归档干净 topic、强制归档干净 topic,两次兜底计数都必须零增量;三死局
// 形态里,①②③各自的收敛路径由上方测试钉住(①走强制路径清扫,②走终局,
// ③走限时等门)。
func TestTerminalFallbackNotTriggeredWhenArchiveSucceedsWithoutIt(t *testing.T) {
	isolateDesktopUserDirs(t)

	before := terminalArchiveFallbackRuns.Load()
	topicID := "topic_terminal_pin_normal"
	_, projectRoot := stageTerminalArchiveTopic(t, "terminal-pin-normal.jsonl", topicID, "Terminal pin normal")
	if err := NewApp().TrashTopic(topicID); err != nil {
		t.Fatalf("regular TrashTopic on a clean topic = %v, want plain success", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared", got)
	}
	if after := terminalArchiveFallbackRuns.Load(); after != before {
		t.Fatalf("a successful regular archive entered the terminal fallback (runs %d -> %d)", before, after)
	}

	topicID2 := "topic_terminal_pin_force"
	sessionPath2, _ := stageTerminalArchiveTopic(t, "terminal-pin-force.jsonl", topicID2, "Terminal pin force")
	if err := NewApp().TrashTopicForce(topicID2); err != nil {
		t.Fatalf("TrashTopicForce on a clean topic = %v, want success via the force path", err)
	}
	if _, err := os.Stat(sessionPath2); !os.IsNotExist(err) {
		t.Fatalf("force archive left the transcript in place: %v", err)
	}
	if after := terminalArchiveFallbackRuns.Load(); after != before {
		t.Fatalf("a clean-topic force archive entered the terminal fallback (runs %d -> %d)", before, after)
	}
}

// 验收钉:真实占用(活的、可指认的其它 Reasonix 进程)永不进入终局兜底——
// tl1367 line 1 契约在终局阶梯下原样保持:强制归档拒绝、holder 锁与身份
// 原样、会话完好;holder 退场后正常归档一次成功。布景与 tl1367 的活外部
// holder 测试同源(真实跨进程锁)。
func TestTerminalFallbackSkipsLiveForeignHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_terminal_pin_live_foreign"
	sessionPath, _ := stageTerminalArchiveTopic(t, "terminal-pin-live-foreign.jsonl", topicID, "Terminal pin live foreign")

	releaseHolder := spawnForeignLeaseHolder(t, sessionPath)
	before := terminalArchiveFallbackRuns.Load()
	app := NewApp()
	forceErr := app.TrashTopicForce(topicID)
	if forceErr == nil {
		releaseHolder()
		t.Fatal("force archive must not succeed against a live foreign holder, not even through the terminal fallback")
	}
	if after := terminalArchiveFallbackRuns.Load(); after != before {
		releaseHolder()
		t.Fatalf("the terminal fallback ran against a live foreign holder (runs %d -> %d)", before, after)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		releaseHolder()
		t.Fatalf("refused archive removed the session: %v", err)
	}
	releaseHolder()
	if err := app.TrashTopic(topicID); err != nil {
		t.Fatalf("regular archive after holder release: %v", err)
	}
}

// 形态③(红):fork 循环中归档。连续 fork 触发的 controller 构建把
// runtimeRebuildMu+runtimeAdmissionMu 两个全局门禁保持热态;现有归档对门禁
// 一律 TryLock 快败——正常归档与强制归档都会立刻弹「finishing another
// session change」让用户无限重试。终局兜底必须限时等门(预算内),门禁释放后
// 一次归档成功。
func TestTrashTopicForceSurvivesForkLoopGateContention(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_terminal_fork_loop"
	sessionPath, projectRoot := stageTerminalArchiveTopic(t, "terminal-fork-loop.jsonl", topicID, "Terminal fork loop")

	app := NewApp()
	// fork 循环的门禁占用模型:构建(fork 每次都重建 controller)持
	// runtimeRebuildMu+runtimeAdmissionMu,直到测试放行(模型化「一次构建
	// 在飞」;真实 fork 循环即该形态连续发生)。
	gatesTaken := make(chan struct{})
	releaseGates := make(chan struct{})
	gatesDone := make(chan struct{})
	go func() {
		defer close(gatesDone)
		app.runtimeRebuildMu.Lock()
		app.runtimeAdmissionMu.Lock()
		close(gatesTaken)
		<-releaseGates
		app.runtimeAdmissionMu.Unlock()
		app.runtimeRebuildMu.Unlock()
	}()
	t.Cleanup(func() {
		select {
		case <-releaseGates:
		default:
			close(releaseGates)
		}
		<-gatesDone
	})
	<-gatesTaken

	// 阶梯第一级:正常归档快败(errTopicArchiveBusy)——门禁热态下不允许
	// 阻塞等待,这条保持快败语义不变。
	if err := app.TrashTopic(topicID); !errors.Is(err, errTopicArchiveBusy) {
		t.Fatalf("regular TrashTopic under hot gates = %v, want errTopicArchiveBusy", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("refused archive removed the session: %v", err)
	}

	// 门禁中途释放(模拟 fork 构建最终完成):强制归档必须在预算内等到门禁
	// 并完成归档(当前实现立刻快败 busy——本断言即红点)。
	time.AfterFunc(300*time.Millisecond, func() {
		select {
		case <-releaseGates:
		default:
			close(releaseGates)
		}
	})
	started := time.Now()
	if err := app.TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce under fork-loop gate contention = %v, want terminal success after the gates free", err)
	}
	if elapsed := time.Since(started); elapsed > archiveDeadlineBudget {
		t.Fatalf("force archive took %v, want <%v even while waiting for the gates", elapsed, archiveDeadlineBudget)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("force archive left the transcript in place: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared", got)
	}
}
