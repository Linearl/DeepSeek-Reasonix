package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 946（进程内 save-path mutex 无界等待根治）验收测试。
//
// 背景：885 把阻塞 save 路径的 in-process 锁等待有界化，但
// lockSessionSavePath（LoadSession / hydrate / 维护 / 恢复共用）仍是
// 无界 mu.Lock——closing_tab 链上一个卡死的锁持有者会把 hydrate 排到
// 20s 全进程 watchdog 强杀为止，用户当窗发出的消息落进强杀窗
// （2026-10-16「发信无反应」现场）。
//
// 本文件钉住三个验收面向：
//   1. 锁被持有时：等待有上界，超时以 ErrSessionFileLockHeld 明确报错；
//   2. 短暂锁竞争：预算内拿到锁，不误报（正常读写语义零破坏）；
//   3. 锁空闲时：load/save 零回归。

// TestLoadSessionWaitBoundedBehindWedgedHolder 是根治验收本体：路径锁被
// 卡死持有者占住时，LoadSession 在预算内以 ErrSessionFileLockHeld 失败，
// 错误信息带路径与预算（可行动：指名哪个会话被谁占了多久），而不是无界排队。
func TestLoadSessionWaitBoundedBehindWedgedHolder(t *testing.T) {
	prevWait := sessionSavePathLockWait
	sessionSavePathLockWait = 300 * time.Millisecond
	t.Cleanup(func() { sessionSavePathLockWait = prevWait })

	path := filepath.Join(t.TempDir(), "wedged.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "seed"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	hold := releaseOnce(HoldSessionSavePathForTest(path)) // wedged holder
	defer hold()

	start := time.Now()
	_, err := LoadSession(path)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrSessionFileLockHeld) {
		t.Fatalf("LoadSession behind wedged holder err = %v, want ErrSessionFileLockHeld", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("bounded load wait took %v, want <= 3s (budget 300ms + slack)", elapsed)
	}
	// 报错要可行动：错误文本必须指名被占的路径，用户/日志能对上是哪个会话。
	if want := canonicalSessionSavePath(path); !containsFold(err.Error(), want) {
		t.Fatalf("timeout error %q does not name the wedged path %q", err.Error(), want)
	}
}

// TestLoadSessionTailWaitBoundedBehindWedgedHolder 把 hydrate 首帧入口
// （LoadSessionTail——发信链上真正排到 watchdog 的那条路）单独钉住。
func TestLoadSessionTailWaitBoundedBehindWedgedHolder(t *testing.T) {
	prevWait := sessionSavePathLockWait
	sessionSavePathLockWait = 300 * time.Millisecond
	t.Cleanup(func() { sessionSavePathLockWait = prevWait })

	path := filepath.Join(t.TempDir(), "wedged-tail.jsonl")
	hold := releaseOnce(HoldSessionSavePathForTest(path))
	defer hold()

	start := time.Now()
	_, err := LoadSessionTail(path)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrSessionFileLockHeld) {
		t.Fatalf("LoadSessionTail behind wedged holder err = %v, want ErrSessionFileLockHeld", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("bounded tail load wait took %v, want <= 3s", elapsed)
	}
}

// TestBriefContentionDoesNotFalseAlarm 是反误报钉子：持有者在预算内释放，
// 排队方正常拿到锁完成读写——短暂锁竞争不产生任何错误。
func TestBriefContentionDoesNotFalseAlarm(t *testing.T) {
	prevWait := sessionSavePathLockWait
	sessionSavePathLockWait = 5 * time.Second
	t.Cleanup(func() { sessionSavePathLockWait = prevWait })

	path := filepath.Join(t.TempDir(), "brief.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "seed"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	hold := releaseOnce(HoldSessionSavePathForTest(path))
	// 持有 150ms 后释放（远小于 5s 预算），排队的 load 必须成功。
	go func() {
		time.Sleep(150 * time.Millisecond)
		hold()
	}()

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession behind brief holder: %v", err)
	}
	if msgs := loaded.Snapshot(); len(msgs) != 2 {
		t.Fatalf("brief-contention load persisted %d msgs, want 2", len(msgs))
	}
}

// TestIdleSavePathZeroRegression 是零回归钉子：锁空闲时 save + load 全链路
// 照常完成，lastSaveLockWaitMs 记录的等待接近零。
func TestIdleSavePathZeroRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idle.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "idle probe"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("idle save: %v", err)
	}
	if wait := s.lastSaveLockWaitMs.Load(); wait > 1000 {
		t.Fatalf("idle save lock wait = %dms, want < 1000ms", wait)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("idle load: %v", err)
	}
	if msgs := loaded.Snapshot(); len(msgs) != 2 || msgs[1].Content != "idle probe" {
		t.Fatalf("idle round trip msgs=%d, want 2 with content", len(msgs))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("idle save did not land: %v", err)
	}
}

// TestCrossSessionLockKeysStayIndependent 复核 885 的按文件锁键独立性在
// 946 改动后仍成立：A 会话被卡死持有时，B 会话的 load 立即完成。
func TestCrossSessionLockKeysStayIndependent(t *testing.T) {
	prevWait := sessionSavePathLockWait
	sessionSavePathLockWait = 5 * time.Second
	t.Cleanup(func() { sessionSavePathLockWait = prevWait })

	dir := t.TempDir()
	pathA := filepath.Join(dir, "sess-a.jsonl")
	pathB := filepath.Join(dir, "sess-b.jsonl")

	sB := NewSession("sys")
	sB.Add(provider.Message{Role: provider.RoleUser, Content: "cross probe"})
	if err := sB.SaveSnapshot(pathB); err != nil {
		t.Fatalf("save B: %v", err)
	}

	holdA := releaseOnce(HoldSessionSavePathForTest(pathA))
	defer holdA()

	start := time.Now()
	loaded, err := LoadSession(pathB)
	if err != nil {
		t.Fatalf("load B while A held: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cross-session load waited %v, want < 2s (per-file lock keys)", elapsed)
	}
	if msgs := loaded.Snapshot(); len(msgs) != 2 {
		t.Fatalf("cross-session load msgs=%d, want 2", len(msgs))
	}
}

// containsFold 是小写的子串包含（Windows 路径大小写不敏感，错误文本里的
// canonical 键与测试断言可能差一个盘符大小写）。
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
