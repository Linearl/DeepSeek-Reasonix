package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// tl1352 C+ 验收：RewriteAndSaveCompact 单锁窗竞态消除。
//
// 现场（20261031 recovery 副本风暴，tl1352）：runOverflowDiet 的旧形态
// Rewrite → SaveRewriteCompact 两步之间存在窗口——内存已折叠（rewriteVersion
// 已 bump），盘上仍是旧字节+旧 CAS 账本。前台 checkpoint/autosave 在此窗口
// 内落笔即与 diet 同盘竞态，败方把 mismatch 判为 foreign diverged →
// recoverSnapshotConflict(forceRewrite=true) → fork recovery 副本。修复后
// 折叠、内存重写、CAS 压实全部发生在同一把按 canonical path 的保存锁窗内，
// 中间态对其余同路径保存者不可观测。
//
// 三条验收钉：
// ① 锁窗钉：保存路径锁被他人持有时，fold（=内存重写的前奏）绝不能执行——
//   旧形态里 Rewrite 就发生在锁窗外，这正是被消除的竞态本体。
// ② 产物钉：锁窗完成后盘上字节=折叠意图，且前台快照保存紧随其后干净成功
//   （CAS 基线连续，无 conflict/fork 诱因）。
// ③ 交错钉：后台 diet 折叠保存与前台快照保存同会话同路径交错 N 轮，双方
//   零 ErrSessionSnapshotConflict、零 ErrSessionFileLockHeld。

// dietFixture1352 builds a schema-1 session on disk with enough bulk that a
// real DietSessionMessages fold would fire, and returns it loaded for live use.
func dietFixture1352(t *testing.T, name string) (*Session, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	sess := NewSession("sys")
	for i := 0; i < 30; i++ {
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("t", 40_000)})
	}
	if err := sess.Save(path); err != nil {
		t.Fatalf("persist fixture: %v", err)
	}
	live, err := LoadSession(path)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return live, path
}

func Test1352RewriteAndSaveCompactHoldsSingleLockWindow(t *testing.T) {
	live, path := dietFixture1352(t, "lockwindow.jsonl")

	release := HoldSessionSavePathForTest(path)
	foldRuns := make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() {
		done <- live.RewriteAndSaveCompact(path, "tl1352_test", func(complete []provider.Message) []provider.Message {
			select {
			case foldRuns <- struct{}{}:
			default:
			}
			return []provider.Message{{Role: provider.RoleUser, Content: "[folded]"}}
		})
	}()

	// 锁被测试持有期间，fold 一次都不许跑（旧形态的 Rewrite 就在锁窗外）。
	const probe = 600 * time.Millisecond
	deadline := time.Now().Add(probe)
	for time.Now().Before(deadline) {
		select {
		case <-foldRuns:
			t.Fatal("fold ran while the save-path lock was held elsewhere — the in-memory rewrite escaped the lock window (the exact tl1352 race)")
		case err := <-done:
			t.Fatalf("call finished while the lock was still held (err=%v) — it never queued on the save-path mutex", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	release()

	// 释放后必须完成且 fold 恰好一次。若 30s 锁预算内拿不到锁或锁内重入
	// 死锁，这里会超时——两种失败都直接指向实现缺陷。
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RewriteAndSaveCompact after lock release: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock or budget exhaustion: RewriteAndSaveCompact did not complete after lock release")
	}
	select {
	case <-foldRuns:
	default:
		t.Fatal("fold must have run exactly once inside the lock window")
	}
}

func Test1352RewriteAndSaveCompactProductAndCleanForegroundSave(t *testing.T) {
	live, path := dietFixture1352(t, "product.jsonl")

	if err := live.RewriteAndSaveCompact(path, "tl1352_test", func(complete []provider.Message) []provider.Message {
		return []provider.Message{{Role: provider.RoleUser, Content: "[folded]"}}
	}); err != nil {
		t.Fatalf("RewriteAndSaveCompact: %v", err)
	}

	// 产物钉：盘上字节=折叠意图（逐条重载比对）。
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload after compact rewrite: %v", err)
	}
	got := reloaded.Snapshot()
	if len(got) != 1 || got[0].Content != "[folded]" {
		t.Fatalf("disk transcript after compact rewrite = %d messages (first=%q), want the single folded product", len(got), firstContent1352(got))
	}

	// 前台快照保存紧随其后必须干净成功：CAS 基线由同一锁窗内落盘并推进，
	// 前台不observed到任何中间态——这正是旧形态里诱发了 fork 的场景。
	if err := reloaded.SaveSnapshot(path); err != nil {
		t.Fatalf("foreground save right after compact rewrite must not conflict: %v", err)
	}
}

func Test1352RewriteAndSaveCompactInterleavesWithForegroundSaves(t *testing.T) {
	live, path := dietFixture1352(t, "interleave.jsonl")

	const rounds = 20
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	wg.Add(2)
	go func() { // 前台 autosave/checkpoint 形态：同会话快照保存循环
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := live.SaveSnapshot(path); err != nil {
				errs <- fmt.Errorf("foreground round %d: %w", i, err)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	go func() { // 后台 diet 形态：折叠+压实保存循环
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			err := live.RewriteAndSaveCompact(path, "tl1352_test", func(complete []provider.Message) []provider.Message {
				return []provider.Message{{Role: provider.RoleUser, Content: fmt.Sprintf("[folded-%d]", i)}}
			})
			if err != nil {
				errs <- fmt.Errorf("diet round %d: %w", i, err)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("interleaved saves must all succeed with zero conflicts: %v", err)
	}
	if _, err := LoadSession(path); err != nil {
		t.Fatalf("final reload after interleaving: %v", err)
	}
}

func firstContent1352(msgs []provider.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0].Content
}
