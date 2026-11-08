package control

// tl1491 复现钉：回合进行中每 30s 的自动存盘 tick（autosaveWhileRunning →
// midTurnAutosaveSnapshot）命中快照冲突时，冲突梯子
// （recoverSnapshotConflictLadder）会走进 adopt 分支并调 adoptDiskSession →
// executor.SetSession，把正在运行的回合脚下的会话对象整个换掉。SetSession
// 的合同是「仅空闲时调用」（internal/agent/agent_session.go；rotation 面的
// beginRotation 也拒绝 running/finishing 时换会话）——回合中 adopt 让后续
// append 落进被换入的盘上对象，内存里未落盘的回合尾巴与运行态全部脱钩。
//
// 现场构造（确定性，不靠真实 30s 计时）：回合用阻塞 runner 挂住（running
// 状态），转录安静（长工具调用的形态）；tick 存盘先落一次基线；第二写者
// 在盘上追加一条死尾巴后停写（盘 = 内存 + 尾巴）；下一个 tick 存盘 →
// stale-prefix 冲突 → 梯子 → adopt。
//
// 修后契约（tl1491）：tick 的存盘带 deferConflictRecovery——回合中冲突保持
// 现对象、因果打进冲突诊断文件、不写盘不 fork，恢复整体推迟到回合边界的
// 阻塞存盘（finishInFlightTurn）重跑同一架梯子；边界/空闲路径的 adopt 语义
// 不变（TestSnapshotConflictAtIdleStillAdoptsDisk 锁住这一点）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tickUntil 驱动真实的 tick 存盘直到 cond 成立：tick 是 nonBlocking 形态，
// 存盘锁被回合侧短暂持有时静默 decline（P18-R3 契约），所以按真实节奏重试
// 而不是单发。
func tickUntil(t *testing.T, c *Controller, cond func() bool, msg string) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		_ = c.midTurnAutosaveSnapshot()
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func conflictLogHas(path, outcome string) bool {
	raw, err := os.ReadFile(store.SessionConflictLog(path))
	return err == nil && strings.Contains(string(raw), `"`+outcome+`"`)
}

// appendDeadTail 模拟第二写者：加载盘上转录、追加一条 assistant 死尾巴后
// 保存。盘上从此 = 内存转录 + 尾巴——stale-prefix 形态（内存快照是盘上转
// 录的陈旧前缀），内存侧下一次安静保存即判该冲突。
func appendDeadTail(t *testing.T, path string) {
	t.Helper()
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("second writer LoadSession: %v", err)
	}
	other.Add(provider.Message{Role: provider.RoleAssistant, Content: "ghost-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("second writer SaveSnapshot: %v", err)
	}
}

// assertConflictDiagnostic 断言冲突诊断文件里落了指定 outcome 的因果记录。
func assertConflictDiagnostic(t *testing.T, path, outcome string) {
	t.Helper()
	raw, err := os.ReadFile(store.SessionConflictLog(path))
	if err != nil {
		t.Fatalf("read conflict diagnostic log: %v", err)
	}
	if !strings.Contains(string(raw), `"`+outcome+`"`) {
		t.Fatalf("conflict diagnostic log missing outcome %q:\n%s", outcome, string(raw))
	}
}

// TestMidTurnAutosaveConflictKeepsLiveSessionMidTurn 是 tl1491 的主钉：回合
// 进行中 tick 存盘撞上 stale-prefix 冲突时，executor 的会话对象必须原样保
// 留，冲突恢复推迟到回合边界。
func TestMidTurnAutosaveConflictKeepsLiveSessionMidTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	release := make(chan struct{})
	c := New(Options{
		Runner:      blockingRunner{session: sess, release: release},
		Executor:    exec,
		SessionDir:  dir,
		SessionPath: path,
		Label:       "tl1491",
	})
	// 与 P18 测试同序：先等 autosaver 退出，再放开阻塞的回合。
	defer c.autosaveWG.Wait()
	defer close(release)

	c.Send("tl1491 mid-turn conflict probe")
	waitForRunning(t, c)

	// 回合开始后的第一个安静 tick：sys+user 落盘，盘上与内存一致。
	tickUntil(t, c, func() bool {
		b, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(b), "tl1491 mid-turn conflict probe")
	}, "first mid-turn tick never persisted the transcript")

	// 第二写者推进盘上转录后停写：盘 = 内存 + ghost 尾巴。
	appendDeadTail(t, path)

	// 回合仍在运行（runner 阻塞中、转录安静），下一个 tick 存盘 →
	// stale-prefix 冲突 → 冲突梯子。修前：梯子 adopt 换掉会话对象（红）；
	// 修后：tick 记因推迟，诊断文件出现 deferred 行（绿）。
	before := c.executorSession()
	var lastErr error
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		lastErr = c.midTurnAutosaveSnapshot()
		if c.executorSession() != before || conflictLogHas(path, "conflict_recovery_deferred_turn_running") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if after := c.executorSession(); after != before {
		t.Fatalf("tl1491 复现：回合中冲突梯子 adopt 换掉了 executor 会话对象 (before=%p after=%p)——SetSession 仅空闲时调用，回合中禁止换绑",
			before, after)
	}
	if lastErr != nil {
		t.Fatalf("deferred mid-turn conflict surfaced as a tick error: %v", lastErr)
	}
	if got := len(sess.Snapshot()); got != 2 {
		t.Fatalf("live transcript mutated by the conflicted tick: %d messages, want the untouched sys+user pair", got)
	}
	if got := agent.RecoveryForkCountForTopic(path); got != 0 {
		t.Fatalf("mid-turn conflict recovery forked (%d copies); recovery must stay deferred to the turn boundary", got)
	}
	assertConflictDiagnostic(t, path, "conflict_recovery_deferred_turn_running")
}

// TestSnapshotConflictAtIdleStillAdoptsDisk 锁住边界/空闲语义不被 tl1491
// 收窄：同一 stale-prefix 现场，空闲时（无回合）存盘冲突照旧 adopt 盘上较
// 新转录——推迟只属于回合中的 tick 存盘，不是对 adopt 的一刀切禁令。
func TestSnapshotConflictAtIdleStillAdoptsDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := New(Options{
		Runner:      exec,
		Executor:    exec,
		SessionDir:  dir,
		SessionPath: path,
		Label:       "tl1491-idle",
	})
	defer c.autosaveWG.Wait()

	sess.Add(provider.Message{Role: provider.RoleUser, Content: "idle seed"})
	if err := c.SnapshotDurable(); err != nil {
		t.Fatalf("idle seed save: %v", err)
	}
	appendDeadTail(t, path)

	before := c.executorSession()
	if err := c.SnapshotDurable(); err != nil {
		t.Fatalf("idle conflicted save: %v", err)
	}
	if after := c.executorSession(); after == before {
		t.Fatal("idle conflict did not adopt the newer disk transcript; boundary adopt semantics regressed")
	}
	msgs := c.executorSession().Snapshot()
	if len(msgs) != 3 || msgs[2].Content != "ghost-tail" {
		t.Fatalf("adopted transcript = %d messages, want sys+seed+ghost-tail", len(msgs))
	}
	// adopt 后保存周期恢复正常：再存一次不再冲突。
	if err := c.SnapshotDurable(); err != nil {
		t.Fatalf("save after idle adopt: %v", err)
	}
}
