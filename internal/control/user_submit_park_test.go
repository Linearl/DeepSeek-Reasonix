package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/filelock"
	"reasonix/internal/provider"
	"reasonix/internal/store"
	"reasonix/internal/tool"
)

// 任务581 回归（580 诊断延伸）：用户手发提交的受理链在 running/finishing 门上
// 必须 park（FIFO 排队）而非静默丢弃，最终以正式 turn 落主 jsonl；turn 末尾的
// 阻塞 save 在跨进程锁竞争下不得丢用户消息（失败留 in-flight 标记，恢复路径保
// 用户文本）。三个用例都构造真实 controller + 真实会话文件，断言以磁盘 jsonl
// 为准；任何构造失败一律 t.Fatal，禁止 SKIP。

func persistingTurnController(t *testing.T, sink event.Sink) (*Controller, string) {
	t.Helper()
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("answer")}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag, Sink: sink, SessionPath: sessionPath})
	return c, sessionPath
}

// jsonlUserTextExists reads the session transcript from disk (test-only full
// read; the P18 red line governs production hot paths) and reports whether a
// user-authored message carries the submitted text.
func jsonlUserTextExists(t *testing.T, sessionPath, text string) bool {
	t.Helper()
	sess, err := agent.LoadSession(sessionPath)
	if err != nil {
		return false
	}
	for _, m := range sess.Snapshot() {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, text) {
			return true
		}
		if m.Role == provider.RoleUser && strings.Contains(m.RawContent, text) {
			return true
		}
	}
	return false
}

func waitForUserTextInJsonl(t *testing.T, sessionPath, text string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if jsonlUserTextExists(t, sessionPath, text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("user text %q never reached the session jsonl at %s", text, sessionPath)
}

// 中断态（running 门）：提交在运行中的 turn 后面排队并请求中断，当前轮让位后
// FIFO drain 以正式 turn 消费它，用户文本落盘。
func TestUserSubmitParksBehindRunningTurnAndPersists(t *testing.T) {
	c, sessionPath := persistingTurnController(t, event.Discard)

	started := make(chan struct{})
	block := make(chan struct{})
	c.runGuarded(func(context.Context) error {
		close(started)
		<-block
		return nil
	})
	<-started

	text := "581 回归：running 态提交必须排队不丢"
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.SubmitDisplay(text, text)
	}()

	// 中断态断言：park 语义对运行中的轮请求优雅中断（与 steer fallback 同机制）。
	deadline := time.Now().Add(10 * time.Second)
	for {
		c.mu.Lock()
		interrupting := c.interrupting
		parked := len(c.parkedTurns)
		c.mu.Unlock()
		if interrupting && parked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("user submit was not parked with an interrupt request (interrupting=%v parked=%d)", interrupting, parked)
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(block) // 当前轮让位 → finish → FIFO drain 起排队的用户 turn
	<-done
	waitForUserTextInJsonl(t, sessionPath, text)
	waitIdleAdmission(t, c)
}

// finishing 窗口态：TurnDone 投递窗口内到达的用户提交同样 park（旧契约在这里
// 直接丢弃——580 事发窗口正是这类「受理了却什么都没发生」的盲区）。
func TestUserSubmitParksInFinishingWindowAndPersists(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	sink := holdFinishingWindow(release, entered, nil)
	c, sessionPath := persistingTurnController(t, sink)

	c.runGuarded(func(context.Context) error { return nil })
	<-entered // finishing 窗口被钉住

	text := "581 回归：finishing 窗口提交必须排队不丢"
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.SubmitDisplay(text, text)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		c.mu.Lock()
		parked := len(c.parkedTurns)
		c.mu.Unlock()
		if parked == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("user submit during the finishing window was not parked (parked=%d)", parked)
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(release) // 窗口关闭 → drain 起排队的用户 turn
	<-done
	waitForUserTextInJsonl(t, sessionPath, text)
	waitIdleAdmission(t, c)
}

// 锁竞争态：跨进程会话文件锁被持有时，turn 末尾的阻塞 save 等待后失败——
// 契约是留 in-flight 标记（可恢复）而不是丢消息；锁释放后补一次 save，用户
// 文本必落盘，且中断恢复路径（marker 清理）不得剥掉用户文本。
func TestTurnEndSaveSurvivesSessionFileLockContention(t *testing.T) {
	c, sessionPath := persistingTurnController(t, event.Discard)
	text := "581 回归：锁竞争下提交不可丢"

	held, err := filelock.Acquire(context.Background(), store.SessionLockFile(sessionPath))
	if err != nil {
		t.Fatalf("acquire cross-process session lock: %v", err)
	}

	turnDone := make(chan error, 1)
	go func() { turnDone <- c.runTurnWithRaw(context.Background(), text, text) }()

	// save 在有界等待后以 ErrSessionFileLockHeld 失败：turn 返回但转录未落盘。
	select {
	case <-turnDone:
	case <-time.After(30 * time.Second):
		t.Fatal("turn did not return while the session file lock was held")
	}
	if jsonlUserTextExists(t, sessionPath, text) {
		t.Fatalf("transcript became durable while the cross-process lock was still held")
	}

	held() // 释放锁 → 补 save
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if running {
		t.Fatal("controller still claims running after the synchronous turn returned")
	}
	if err := c.Snapshot(); err != nil {
		t.Fatalf("post-contention snapshot: %v", err)
	}
	waitForUserTextInJsonl(t, sessionPath, text)

	// 中断恢复语义：marker 清理路径必须保用户文本（PreserveUser）。
	c.recoverInterruptedTurn(sessionPath)
	if !jsonlUserTextExists(t, sessionPath, text) {
		t.Fatal("interrupted-turn recovery dropped the user text")
	}
}
