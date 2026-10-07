package collabinbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/sessioncollab"
)

// task 461 P1 acceptance（461 卡）：锁被外部进程持有时，收件箱操作 ≤5s 返回
// 含锁占用语义的明确错误而非无限挂起；锁等待期间取消请求 ctx（用户点停止）
// ≤1s 生效。断言全部硬判定，无 SKIP。
//
// 任务511 复发断根（2026-10-07）在本面新增 stale holder 活检查：holder 侧车
// 指认的 pid 已死 ⇒ 下一次读取自动清掉并正常返回（验收①）；存活持有者的
// 侧车绝不清（防误杀）；面板读取必留 INFO 痕（验收②）。

// newLockedStore builds a store over a temp mail dir whose inbox lock is held
// by an OS-level file lock — equivalent to another Reasonix window/process
// owning it (same-process different-handle conflicts at the OS layer exactly
// like a second process).
func newLockedStore(t *testing.T) (*Store, func()) {
	t.Helper()
	s := New(t.TempDir(), nil)
	if err := os.MkdirAll(s.mailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := filelock.TryAcquire(filepath.Join(s.mailDir, lockName))
	if err != nil {
		t.Fatalf("hold inbox lock: %v", err)
	}
	return s, held
}

// 任务461 P11 升级了读路径语义：锁被楔住时 List 不再报错，而是降级无锁直读
// （Degraded=true，数据不空）——该行为由 degraded_read_test.go 钉死。本文件
// 保留写路径（ApplyRetention）的有界语义：锁被占 → 预算内返错。
func TestApplyRetentionErrorsWithinBudgetWhenLockHeldElsewhere(t *testing.T) {
	s, held := newLockedStore(t)
	defer held()

	started := time.Now()
	_, err := s.ApplyRetention(context.Background())
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("ApplyRetention must fail while the inbox lock is held elsewhere, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ApplyRetention error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "lock busy") {
		t.Fatalf("error lacks lock-busy semantics (锁被占用): %v", err)
	}
	if elapsed > lockWaitTimeout+2*time.Second {
		t.Fatalf("ApplyRetention waited %v, want ≤ lockWaitTimeout(%v)+slack", elapsed, lockWaitTimeout)
	}
}

func TestWritePathReturnsWhenRequestCancelledWhileLockHeld(t *testing.T) {
	s, held := newLockedStore(t)
	defer held()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)

	started := time.Now()
	_, err := s.ApplyRetention(ctx)
	elapsed := time.Since(started)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyRetention error = %v, want context.Canceled", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("returned after %v, want <1s after request cancel (task 461 P1 停止 SLA)", elapsed)
	}
}

// stalePID sits beyond every platform's realistic pid range (Windows pids are
// DWORD multiples of 4 but practically stay in the millions; Linux pid_max
// caps at 4194304), so it is deterministically dead — a real spawned-then-
// reaped pid would race pid reuse on Windows.
const stalePID = 999999999

// 任务511 复发断根验收①（端到端）：holder 侧车指认一个已死的 pid（等价于
// 「pid 26048 死后侧车残留 1.5 天」的事故现场）→ 下一次面板读取必须正常返回
// 数据（OS 锁已随进程死亡释放，读取不受影响），且死 pid 不再被任何侧车内容
// 指认——自动回收生效。
func TestListClearsStaleHolderSidecarAndReturnsNormally(t *testing.T) {
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_me", Body: "relapse recovery"})

	stale := fmt.Sprintf("pid=%d held_since=%s", stalePID, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(store.lockHolderPath(), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	snap, err := store.List(context.Background(), Query{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Degraded {
		t.Fatal("a dead recorded holder must not degrade the read — the OS lock died with the process")
	}
	if snap.Total != 1 {
		t.Fatalf("panel must show the letter after stale-holder recovery, got total=%d", snap.Total)
	}
	if holder := store.LockHolderInfo(); strings.Contains(holder, fmt.Sprintf("pid=%d", stalePID)) {
		t.Fatalf("stale holder sidecar must be recycled on the next read, got %q", holder)
	}

	// 验收③（连续 3 次点开正常）：同一现场连开三次——回收一次性完成不复活，
	// 节流闸下快路径照常，数据恒在、恒不降级。
	for i := 1; i <= 3; i++ {
		snap, err := store.List(context.Background(), Query{}, true)
		if err != nil {
			t.Fatalf("panel open #%d: %v", i, err)
		}
		if snap.Degraded {
			t.Fatalf("panel open #%d must not degrade", i)
		}
		if snap.Total != 1 {
			t.Fatalf("panel open #%d must show the letter, got total=%d", i, snap.Total)
		}
		if holder := store.LockHolderInfo(); strings.Contains(holder, fmt.Sprintf("pid=%d", stalePID)) {
			t.Fatalf("panel open #%d: stale holder resurfaced: %q", i, holder)
		}
	}
}

// 任务511 复发断根（单元面）：回收契约三分支——死 pid ⇒ 删除并上报；活 pid
// （本进程）⇒ 绝不动（防误杀硬边界）；不可解析 ⇒ 无法判定不动（never guess）。
func TestClearStaleHolderIfDeadRemovesOnlyDeadPidSidecar(t *testing.T) {
	store, _ := fixtureStore(t)
	now := time.Now().Format(time.RFC3339)

	// 死 pid：删。
	stale := fmt.Sprintf("pid=%d held_since=%s", stalePID, now)
	if err := os.WriteFile(store.lockHolderPath(), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := store.clearStaleHolderIfDead(); got != stale {
		t.Fatalf("clearing must report the removed content, got %q", got)
	}
	if _, err := os.Stat(store.lockHolderPath()); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar must be removed, stat err=%v", err)
	}

	// 活 pid（本进程必然存活）：一字不动。
	live := fmt.Sprintf("pid=%d held_since=%s", os.Getpid(), now)
	if err := os.WriteFile(store.lockHolderPath(), []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := store.clearStaleHolderIfDead(); got != "" {
		t.Fatalf("a live holder's sidecar must never be cleared (防误杀), got %q", got)
	}
	if holder := store.LockHolderInfo(); holder != live {
		t.Fatalf("live holder sidecar must survive verbatim, got %q", holder)
	}

	// 不可解析：无法判定，不动。
	if err := os.WriteFile(store.lockHolderPath(), []byte("garbage without a pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := store.clearStaleHolderIfDead(); got != "" {
		t.Fatalf("unparsable sidecar must be left alone, got %q", got)
	}
	if holder := store.LockHolderInfo(); holder != "garbage without a pid" {
		t.Fatalf("unparsable sidecar must survive, got %q", holder)
	}
}

// 任务511 复发断根（写路径面）：独占锁 busy 且侧车指认死进程 ⇒ 错误信息如实
// 说「陈旧侧车已清」，不再把死进程当持锁者指认；侧车文件被回收。
func TestWritePathWithStaleHolderClearsSidecarInError(t *testing.T) {
	store, _ := fixtureStore(t)
	stale := fmt.Sprintf("pid=%d held_since=%s", stalePID, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(store.lockHolderPath(), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	wedge, err := filelock.TryAcquire(store.lockFilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer wedge()

	_, err = store.Dismiss(context.Background(), []string{"msg_x"})
	if err == nil {
		t.Fatal("contended write must fail with the bounded budget")
	}
	if !strings.Contains(err.Error(), "stale holder sidecar cleared") {
		t.Fatalf("busy error must report the stale-sidecar recycle, got: %v", err)
	}
	if strings.Contains(err.Error(), fmt.Sprintf("pid=%d", stalePID)) {
		t.Fatalf("busy error must not name the dead pid as a holder, got: %v", err)
	}
	if _, statErr := os.Stat(store.lockHolderPath()); !os.IsNotExist(statErr) {
		t.Fatalf("stale sidecar must be recycled on the busy path, stat err=%v", statErr)
	}
}

// 任务511 复发断根验收②：面板路径（applyRetention=true）每次读取必留一条
// INFO 痕（含 total/degraded）——「点开零后端日志」的静默通道从此可判：
// 这条在 = 请求落到了数据层。查询工具/徽标路径（false）不打，无噪音。
func TestPanelReadLeavesInfoTrailQueryPathDoesNot(t *testing.T) {
	store, mail := fixtureStore(t)
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_me", Body: "trail"})

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	snap, err := store.List(context.Background(), Query{Bucket: BucketAll}, true)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 1 {
		t.Fatalf("precondition: panel read returns the letter, got total=%d", snap.Total)
	}
	if trail := buf.String(); !strings.Contains(trail, "collab inbox: panel read") {
		t.Fatalf("panel read must leave an INFO trail, got: %q", trail)
	}

	buf.Reset()
	if _, err := store.List(context.Background(), Query{}, false); err != nil {
		t.Fatal(err)
	}
	if trail := buf.String(); strings.Contains(trail, "collab inbox: panel read") {
		t.Fatalf("query/badge path (applyRetention=false) must not log the panel trail, got: %q", trail)
	}
}
