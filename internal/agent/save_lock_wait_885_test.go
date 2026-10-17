package agent

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 885（跨会话 save 锁互等恶化）验收测试。
//
// 机制定性（代码实证见 wt-885 交付汇报）：
//   - 锁键 = canonical 会话文件路径，每文件一把 in-process mutex（sessionSaveLocks），
//     外加每文件一把跨进程 .lock —— 两个不同会话文件的键永不相同；
//   - 现场观测的「互等」是同键排队：同一会话的多个 saver/loader 排在同一把锁后面，
//     而持有者做 O(文件) 的重 IO（全量 load / digest+rewrite），队列等待随文件
//     增长单调恶化（791ms → 11973ms）；
//   - 根治：阻塞 save 路径的 in-process 锁等待加上界（sessionSavePathLockWait），
//     超时返回 ErrSessionFileLockHeld 走既有降级/恢复机制，不再无界排队。

// releaseOnce wraps a lock release func so tests can release on both the
// success and the failure path without double-unlock panics.
func releaseOnce(release func()) func() {
	var once sync.Once
	return func() { once.Do(release) }
}

// TestCrossSessionSavesDoNotShareLockKey 断言锁键按会话文件独立：会话 A 的锁被
// 持有时，会话 B 的 save 立即完成（等待有上界），证明不存在跨会话共享锁键。
func TestCrossSessionSavesDoNotShareLockKey(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "sess-a.jsonl")
	pathB := filepath.Join(dir, "sess-b.jsonl")

	if gotA, gotB := canonicalSessionSavePath(pathA), canonicalSessionSavePath(pathB); gotA == gotB {
		t.Fatalf("distinct session files share one save-path lock key %q", gotA)
	}

	// Simulate a slow critical section on A (e.g. a full replay holder).
	holdA := releaseOnce(HoldSessionSavePathForTest(pathA))
	defer holdA()

	sB := NewSession("sys")
	sB.Add(provider.Message{Role: provider.RoleUser, Content: "cross-session probe"})
	startB := time.Now()
	if err := sB.SaveSnapshot(pathB); err != nil {
		t.Fatalf("cross-session save: %v", err)
	}
	if elapsed := time.Since(startB); elapsed > 2*time.Second {
		t.Fatalf("cross-session save waited %v on session A's lock; want < 2s (per-file lock keys)", elapsed)
	}
	if wait := sB.lastSaveLockWaitMs.Load(); wait > 1000 {
		t.Fatalf("cross-session save lock wait = %dms, want < 1000ms", wait)
	}
	loaded, err := LoadSession(pathB)
	if err != nil {
		t.Fatalf("load cross-session save: %v", err)
	}
	if msgs := loaded.Snapshot(); len(msgs) != 2 || msgs[1].Content != "cross-session probe" {
		t.Fatalf("cross-session save did not persist, msgs=%d", len(msgs))
	}
}

// TestSameSessionSaveWaitChainBounded 复现现场的同键排队链：慢持有者期间，同路径的
// save 排队等待；持有者释放后等待结束（等待被持有者时长封顶，不再无界增长）。
func TestSameSessionSaveWaitChainBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queued.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "queued probe"})

	hold := releaseOnce(HoldSessionSavePathForTest(path))
	done := make(chan error, 1)
	go func() { done <- s.SaveSnapshot(path) }()

	// While the holder is in place the save must not complete.
	select {
	case err := <-done:
		hold()
		t.Fatalf("save completed while its own save-path lock was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	hold()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued save after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued save did not complete after the holder released")
	}
	// The observed wait is the holder's remaining hold, not an unbounded queue:
	// it must be recorded and bounded by the holder duration + slack.
	if wait := s.lastSaveLockWaitMs.Load(); wait < 150 {
		t.Fatalf("queued save lock wait = %dms, want >= 150ms (it did queue)", wait)
	} else if wait > 5000 {
		t.Fatalf("queued save lock wait = %dms, want <= 5000ms (bounded by holder release)", wait)
	}
}

// TestBlockingSavePathWaitBounded 是根治验收：阻塞 save 的 in-process 锁等待有上界。
// 持有者不释放时，save 在预算内以 ErrSessionFileLockHeld 失败，而不是无界排队。
func TestBlockingSavePathWaitBounded(t *testing.T) {
	prevWait := sessionSavePathLockWait
	sessionSavePathLockWait = 300 * time.Millisecond
	t.Cleanup(func() { sessionSavePathLockWait = prevWait })

	path := filepath.Join(t.TempDir(), "wedged.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "wedged holder"})

	hold := releaseOnce(HoldSessionSavePathForTest(path)) // wedged holder

	start := time.Now()
	err := s.SaveSnapshot(path)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrSessionFileLockHeld) {
		hold()
		t.Fatalf("save behind wedged holder err = %v, want ErrSessionFileLockHeld", err)
	}
	if elapsed > 3*time.Second {
		hold()
		t.Fatalf("bounded save wait took %v, want <= 3s (budget 300ms + slack)", elapsed)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		hold()
		t.Fatalf("declined save wrote %q (stat err=%v); want no file", path, statErr)
	}

	// After the holder releases, the same save succeeds — the failure was the
	// bound, not a broken path.
	hold()
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("save after holder released: %v", err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("load after recovery: %v", err)
	}
	if msgs := loaded.Snapshot(); len(msgs) != 2 {
		t.Fatalf("recovered save persisted %d msgs, want 2", len(msgs))
	}
}

// TestConcurrentSessionSavesBothBounded 验收目标本体：两会话并发 save，彼此等待
// 有界——即使 A 的锁被慢持有者占住，B 的 save 也不被拖住，A 的 save 在自己的
// 预算内有界失败。
func TestConcurrentSessionSavesBothBounded(t *testing.T) {
	prevWait := sessionSavePathLockWait
	sessionSavePathLockWait = 400 * time.Millisecond
	t.Cleanup(func() { sessionSavePathLockWait = prevWait })

	dir := t.TempDir()
	pathA := filepath.Join(dir, "conc-a.jsonl")
	pathB := filepath.Join(dir, "conc-b.jsonl")

	holdA := releaseOnce(HoldSessionSavePathForTest(pathA))
	defer holdA()

	sA := NewSession("sys")
	sA.Add(provider.Message{Role: provider.RoleUser, Content: "a"})
	sB := NewSession("sys")
	sB.Add(provider.Message{Role: provider.RoleUser, Content: "b"})

	var wg sync.WaitGroup
	var errA, errB error
	var elapsedA, elapsedB time.Duration
	wg.Add(2)
	go func() {
		defer wg.Done()
		start := time.Now()
		// A queues behind its own wedged holder: bounded failure.
		errA = sA.SaveSnapshot(pathA)
		elapsedA = time.Since(start)
	}()
	go func() {
		defer wg.Done()
		start := time.Now()
		errB = sB.SaveSnapshot(pathB)
		elapsedB = time.Since(start)
	}()
	wg.Wait()

	if !errors.Is(errA, ErrSessionFileLockHeld) {
		t.Fatalf("queued session A save err = %v, want ErrSessionFileLockHeld", errA)
	}
	if elapsedA > 5*time.Second {
		t.Fatalf("session A save wait %v, want bounded by its budget", elapsedA)
	}
	if errB != nil {
		t.Fatalf("session B save err = %v, want nil (its key is independent)", errB)
	}
	if elapsedB > 2*time.Second {
		t.Fatalf("session B save wait %v, want < 2s while A is contended", elapsedB)
	}
}
