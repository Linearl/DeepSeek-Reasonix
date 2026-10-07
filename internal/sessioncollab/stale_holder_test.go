package sessioncollab

// 任务511 复发断根（2026-10-07）：.mail.lock.holder 的 stale 回收 + History
// 最后一个静默空分支（ReadDir 失败）的诚实化。断言全部硬判定，无 SKIP。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/baseproc/pidalive"
	"reasonix/internal/filelock"
)

// stalePID sits beyond every platform's realistic pid range — deterministically
// dead (a real spawned-then-reaped pid would race pid reuse on Windows).
const stalePID = 999999999

// TestMailStoreClearStaleHolderOnlyDeadPid pins the recycle contract: a dead
// pid's sidecar is removed and reported; a live holder's (this process) is
// never touched (防误杀硬边界); an unparsable one is left alone (never guess).
func TestMailStoreClearStaleHolderOnlyDeadPid(t *testing.T) {
	s := NewMailStore(t.TempDir())
	now := time.Now().Format(time.RFC3339)

	stale := fmt.Sprintf("pid=%d held_since=%s", stalePID, now)
	if err := os.WriteFile(s.lockHolderPath(), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.clearStaleHolderIfDead(); got != stale {
		t.Fatalf("clearing must report the removed content, got %q", got)
	}
	if _, err := os.Stat(s.lockHolderPath()); !os.IsNotExist(err) {
		t.Fatalf("stale sidecar must be removed, stat err=%v", err)
	}

	live := fmt.Sprintf("pid=%d held_since=%s", os.Getpid(), now)
	if err := os.WriteFile(s.lockHolderPath(), []byte(live), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.clearStaleHolderIfDead(); got != "" {
		t.Fatalf("a live holder's sidecar must never be cleared (防误杀), got %q", got)
	}
	if holder := s.LockHolderInfo(); holder != live {
		t.Fatalf("live holder sidecar must survive verbatim, got %q", holder)
	}

	if err := os.WriteFile(s.lockHolderPath(), []byte("not a sidecar line"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.clearStaleHolderIfDead(); got != "" {
		t.Fatalf("unparsable sidecar must be left alone, got %q", got)
	}
	if holder := s.LockHolderInfo(); holder != "not a sidecar line" {
		t.Fatalf("unparsable sidecar must survive, got %q", holder)
	}
}

// TestHistoryBusyWithStaleHolderRecyclesAndDegrades: the 511-relapse shape —
// sidecar names a long-dead pid, another live holder owns the lock. History
// must degrade (availability ruling intact) AND recycle the stale sidecar, so
// the degraded warn stops naming a zombie.
func TestHistoryBusyWithStaleHolderRecyclesAndDegrades(t *testing.T) {
	s := NewMailStore(t.TempDir())
	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_me", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf("pid=%d held_since=%s", stalePID, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(s.lockHolderPath(), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	// 一个存活持有者占住锁（本进程内另一句柄，等价另一窗口）。
	wedge, err := filelock.TryAcquire(filepath.Join(s.Dir(), ".mail.lock"))
	if err != nil {
		t.Fatalf("wedge the mail lock: %v", err)
	}
	defer wedge()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	rows, degraded := s.History(ctx)
	if !degraded {
		t.Fatal("a live holder's lock must still degrade the read (availability > lock purity)")
	}
	if rows != nil {
		t.Fatalf("degraded history carries no rows, got %d", len(rows))
	}
	if _, statErr := os.Stat(s.lockHolderPath()); !os.IsNotExist(statErr) {
		t.Fatalf("the stale sidecar must be recycled on the busy path, stat err=%v", statErr)
	}
}

// TestHistoryBusyWithLiveHolderKeepsSidecar（防误杀·busy 路径）: a LIVE
// holder's sidecar must survive a busy recycle untouched — the recycle keys on
// the recorded pid's liveness, never on the lock being busy.
func TestHistoryBusyWithLiveHolderKeepsSidecar(t *testing.T) {
	s := NewMailStore(t.TempDir())
	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_me", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	// 先做一次健康读：sidecar 记下本进程（活 pid）。
	if _, degraded := s.History(context.Background()); degraded {
		t.Fatal("precondition: the first read must be healthy")
	}
	live := s.LockHolderInfo()
	if !strings.HasPrefix(live, "pid=") {
		t.Fatalf("precondition: sidecar stamped with a live pid, got %q", live)
	}

	wedge, err := filelock.TryAcquire(filepath.Join(s.Dir(), ".mail.lock"))
	if err != nil {
		t.Fatalf("wedge the mail lock: %v", err)
	}
	defer wedge()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, degraded := s.History(ctx); !degraded {
		t.Fatal("the wedged lock must degrade the read")
	}
	if holder := s.LockHolderInfo(); holder != live {
		t.Fatalf("a live holder's sidecar must survive the busy path verbatim (防误杀), got %q", holder)
	}
}

// TestHistoryReadDirFailureIsDegradedNotSilentEmpty (任务511 复发③): the last
// silent-empty branch — "cannot see the library" must travel as degraded=true,
// never as an honest-looking empty table.
func TestHistoryReadDirFailureIsDegradedNotSilentEmpty(t *testing.T) {
	s := NewMailStore(t.TempDir())
	if _, err := s.Deliver(context.Background(), MailMessage{From: "sc_a", To: "sc_me", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	s.listDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("library unreadable") }

	rows, degraded := s.History(context.Background())
	if !degraded {
		t.Fatal("an unreadable library must be reported degraded, not silently empty")
	}
	if rows != nil {
		t.Fatalf("an unreadable library carries no rows, got %d", len(rows))
	}

	// seam 缺省 = os.ReadDir：注入清空后恢复健康语义。
	s.listDir = nil
	rows, degraded = s.History(context.Background())
	if degraded {
		t.Fatal("the default listing must be healthy")
	}
	if len(rows) != 1 {
		t.Fatalf("healthy history returns the letter, got %d rows", len(rows))
	}
}

// TestParseHolderPid pins the shared parser both locks' recycle depends on.
func TestParseHolderPid(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"pid=123 held_since=2026-10-05T23:45:16+08:00", 123},
		{"pid=999999999 held_since=x", 999999999},
		{"", 0},
		{"garbage", 0},
		{"pid=abc", 0},
		{"pid=-5", 0},
		{"pid=", 0},
		{"held_since=y pid=77", 77}, // field order free
	}
	for _, c := range cases {
		if got := pidalive.ParseHolderPid(c.in); got != c.want {
			t.Fatalf("ParseHolderPid(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
