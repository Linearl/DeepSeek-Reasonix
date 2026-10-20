package collabinbox

// 任务1017 批A（2026-10-20）：boot 期 stale 锁 sidecar 回收的契约测试。
// 2026-10-19 现场：崩溃重启后 sidecar（pid=51264）残留 1h43m 才被下次取件
// 回收——boot 入口就是为了把这个等待归零。防误杀边界与竞争路径回收一致：
// 存活持有者（含 pid 复用后看似存活）一字不动；不可解析内容不动（never guess）。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testOnlyDeterministicallyDeadPID sits beyond every platform's realistic pid
// range — deterministically dead without racing pid reuse (same seam as the
// sessioncollab twin's stalePID).
const testOnlyDeterministicallyDeadPID = 999999999

// TestReclaimStaleHolderSidecarAtBoot pins the boot recycle contract for the
// aggregate-index lock: a provably-dead holder's sidecar is cleared and
// reported at boot, a live holder's is never touched, unparsable/absent stays.
func TestReclaimStaleHolderSidecarAtBoot(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Format(time.RFC3339)
	holder := filepath.Join(dir, lockName+".holder")

	stale := fmt.Sprintf("pid=%d held_since=%s", testOnlyDeterministicallyDeadPID, now)
	if err := os.WriteFile(holder, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReclaimStaleHolderSidecarAtBoot(dir); got != stale {
		t.Fatalf("boot reclaim must report the cleared content, got %q", got)
	}
	if _, err := os.Stat(holder); !os.IsNotExist(err) {
		t.Fatalf("boot reclaim must remove the stale sidecar, stat err=%v", err)
	}

	live := fmt.Sprintf("pid=%d held_since=%s", os.Getpid(), now)
	if err := os.WriteFile(holder, []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReclaimStaleHolderSidecarAtBoot(dir); got != "" {
		t.Fatalf("a live holder's sidecar must never be cleared at boot (防误杀), got %q", got)
	}
	b, err := os.ReadFile(holder)
	if err != nil || string(b) != live {
		t.Fatalf("live holder sidecar must survive boot reclaim verbatim, got %q err=%v", string(b), err)
	}

	if err := os.WriteFile(holder, []byte("not a sidecar line"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReclaimStaleHolderSidecarAtBoot(dir); got != "" {
		t.Fatalf("unparsable sidecar must be left alone (never guess), got %q", got)
	}

	if got := ReclaimStaleHolderSidecarAtBoot(t.TempDir()); got != "" {
		t.Fatalf("absent sidecar must read as nothing-to-do, got %q", got)
	}
	if got := ReclaimStaleHolderSidecarAtBoot("  "); got != "" {
		t.Fatalf("blank mailDir must be a no-op, got %q", got)
	}
}

// TestClearStaleHolderIfDeadDelegatesToSharedBody pins that the Store method
// and the boot entry share one body: identical behavior on the same fixture.
func TestClearStaleHolderIfDeadDelegatesToSharedBody(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, nil)
	stale := fmt.Sprintf("pid=%d held_since=%s", testOnlyDeterministicallyDeadPID, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(s.lockHolderPath(), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.clearStaleHolderIfDead(); got != stale {
		t.Fatalf("store method must report the cleared content, got %q", got)
	}
	if _, err := os.Stat(s.lockHolderPath()); !os.IsNotExist(err) {
		t.Fatalf("store method must remove the stale sidecar, stat err=%v", err)
	}
}
