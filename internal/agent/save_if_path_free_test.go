package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// P14 (SetActiveTab 移出锁等待) 的 agent 层验收：锁被占时 SaveSnapshotIfPathFree
// 立即干净拒绝（attempted=false、无错误、不落盘），锁空闲时语义与 SaveSnapshot
// 完全一致。

func TestSaveSnapshotIfPathFreeUncontended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "free.jsonl")
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "p14 probe"})

	attempted, err := s.SaveSnapshotIfPathFree(path)
	if err != nil {
		t.Fatalf("SaveSnapshotIfPathFree: %v", err)
	}
	if !attempted {
		t.Fatal("attempted = false on an uncontended path, want true")
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession after free-path snapshot: %v", err)
	}
	msgs := loaded.Snapshot()
	if len(msgs) != 2 || msgs[1].Content != "p14 probe" {
		t.Fatalf("reloaded messages = %d entries, want system + the saved user message", len(msgs))
	}

	// A second attempt stays (true, nil): the up-to-date fast path is still a
	// completed snapshot from the caller's point of view.
	attempted, err = s.SaveSnapshotIfPathFree(path)
	if err != nil || !attempted {
		t.Fatalf("second SaveSnapshotIfPathFree = (%v, %v), want (true, nil)", attempted, err)
	}
}

func TestSaveSnapshotIfPathFreeBusyDeclinesWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.jsonl")
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "queued behind a loader"})

	unlock := lockSessionSavePath(path) // simulates a slow reader/writer on the same path
	// No defer: the mutex is released explicitly mid-test so the post-release
	// leg can save through the uncontended path exactly once.

	type attempt struct {
		attempted bool
		err       error
	}
	done := make(chan attempt, 1)
	go func() {
		a, err := s.SaveSnapshotIfPathFree(path)
		done <- attempt{a, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("SaveSnapshotIfPathFree on busy path returned error %v, want clean decline", r.err)
		}
		if r.attempted {
			t.Fatal("attempted = true on a busy save path, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SaveSnapshotIfPathFree blocked on a busy save path; want immediate decline")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("busy-path attempt wrote %q (stat err=%v), want no file", path, err)
	}

	// After the holder releases, the same session saves normally.
	unlock()
	attempted, err := s.SaveSnapshotIfPathFree(path)
	if err != nil || !attempted {
		t.Fatalf("post-release SaveSnapshotIfPathFree = (%v, %v), want (true, nil)", attempted, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file missing after released-path snapshot: %v", err)
	}
}

// TestTryWithSessionSaveLocksDeclinesWhenBusy exercises the authoritative gate
// directly (the advisory probe in SaveSnapshotIfPathFree can pass just before
// someone else takes the lock; the try-lock below must still decline).
func TestTryWithSessionSaveLocksDeclinesWhenBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.jsonl")
	s := NewSession("system")

	ran := false
	fn := func() error { ran = true; return nil }

	unlock := lockSessionSavePath(path)
	attempted, err := s.tryWithSessionSaveLocks(path, fn)
	if err != nil {
		t.Fatalf("tryWithSessionSaveLocks on busy path: %v", err)
	}
	if attempted {
		t.Fatal("attempted = true on a busy save path, want false")
	}
	if ran {
		t.Fatal("save body ran while the save-path mutex was held elsewhere")
	}
	unlock()

	attempted, err = s.tryWithSessionSaveLocks(path, fn)
	if err != nil {
		t.Fatalf("tryWithSessionSaveLocks after release: %v", err)
	}
	if !attempted || !ran {
		t.Fatalf("after release = (attempted %v, ran %v), want (true, true)", attempted, ran)
	}
}

func TestSavePathLockFreeReflectsHeldMutex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.jsonl")
	if !savePathLockFree(path) {
		t.Fatal("savePathLockFree = false for a never-locked path, want true")
	}
	unlock := lockSessionSavePath(path)
	if savePathLockFree(path) {
		unlock()
		t.Fatal("savePathLockFree = true while the mutex is held, want false")
	}
	unlock()
	if !savePathLockFree(path) {
		t.Fatal("savePathLockFree = false after release, want true")
	}
}
