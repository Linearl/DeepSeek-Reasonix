package control

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

func withProcessAliveForTest(t *testing.T, alive func(int) bool) {
	t.Helper()
	orig := agent.ProcessAlive
	agent.ProcessAlive = alive
	t.Cleanup(func() { agent.ProcessAlive = orig })
}

// tl 1201 验收④：zombie（持有 pid 已死）场景不得再提示「关掉另一窗口」——
// 用户无窗可关，是死循环提示；必须改为「重启以清」的可操作指引。
// 验收④的反向半边：活持有者（真实双窗口）保持原提示不变。
func TestSessionLeaseHintForClassifiesHolderLiveness(t *testing.T) {
	withProcessAliveForTest(t, func(pid int) bool { return pid == os.Getpid()+1 })

	deadErr := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{
		PID:        os.Getpid() + 999999,
		AcquiredAt: time.Date(2026, 10, 27, 17, 1, 0, 0, time.Local),
	}}
	liveErr := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: os.Getpid() + 1}}
	selfErr := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: os.Getpid()}}

	t.Run("nil error keeps the close hint", func(t *testing.T) {
		if got := SessionLeaseHintFor(nil); got != SessionLeaseCloseHint {
			t.Fatalf("hint = %q, want the classic close hint", got)
		}
	})

	t.Run("unknown holder keeps the close hint", func(t *testing.T) {
		if got := SessionLeaseHintFor(errors.New("boom")); got != SessionLeaseCloseHint {
			t.Fatalf("hint = %q, want the classic close hint", got)
		}
		if got := SessionLeaseHintFor(&agent.SessionLeaseError{Path: "p"}); got != SessionLeaseCloseHint {
			t.Fatalf("identity-less hint = %q, want the classic close hint", got)
		}
	})

	t.Run("dead holder gets the restart hint, never close-a-window", func(t *testing.T) {
		got := SessionLeaseHintFor(deadErr)
		if !strings.Contains(got, "crashed Reasonix process") ||
			!strings.Contains(got, "restart Reasonix") {
			t.Fatalf("zombie hint = %q, want crashed-process + restart guidance", got)
		}
		if !strings.Contains(got, "pid ") || strings.Contains(got, SessionLeaseCloseHint) {
			t.Fatalf("zombie hint = %q, must name the pid and must not loop the close-window advice", got)
		}
	})

	t.Run("live foreign holder keeps the close hint", func(t *testing.T) {
		if got := SessionLeaseHintFor(liveErr); got != SessionLeaseCloseHint {
			t.Fatalf("live-holder hint = %q, want the classic close hint", got)
		}
	})

	t.Run("self holder points at this instance", func(t *testing.T) {
		got := SessionLeaseHintFor(selfErr)
		if !strings.Contains(got, "this Reasonix instance") {
			t.Fatalf("self hint = %q, want the own-instance guidance", got)
		}
	})
}

func TestSessionInUseMessageNamesCrashedProcess(t *testing.T) {
	withProcessAliveForTest(t, func(pid int) bool { return pid == os.Getpid()+1 })

	dead := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{
		PID:        os.Getpid() + 999999,
		AcquiredAt: time.Date(2026, 10, 27, 17, 1, 0, 0, time.Local),
	}}
	got := SessionInUseMessage(dead)
	if !strings.Contains(got, "crashed Reasonix process") || !strings.Contains(got, "restart Reasonix") {
		t.Fatalf("zombie message = %q, want crashed-process + restart guidance", got)
	}

	live := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: os.Getpid() + 1}}
	if got := SessionInUseMessage(live); !strings.Contains(got, "in use by another Reasonix process (pid") {
		t.Fatalf("live message = %q, want the live-holder wording", got)
	}

	if got := SessionInUseMessage(errors.New("boom")); got != "this session is in use by another Reasonix window or process" {
		t.Fatalf("fallback message = %q", got)
	}
}

// tl1367 line 1: every holder branch must name WHO holds the session —
// process name and image path, hostname, acquire time, and the holder
// runtime's writer id — so neither the user nor the log needs to capture the
// scene by hand.
func withProcessImageNameForControlTest(t *testing.T, name func(int) (string, string)) {
	t.Helper()
	orig := agent.ProcessImageName
	agent.ProcessImageName = name
	t.Cleanup(func() { agent.ProcessImageName = orig })
}

func TestSessionInUseMessageCarriesHolderIdentity(t *testing.T) {
	withProcessAliveForTest(t, func(pid int) bool { return pid == 5000 })
	withProcessImageNameForControlTest(t, func(pid int) (string, string) {
		return "reasonix-desktop.exe", `C:\Program Files\Reasonix\reasonix-desktop.exe`
	})

	live := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{
		PID:        5000,
		Hostname:   "DESKTOP-XYZ",
		AcquiredAt: time.Date(2026, 11, 3, 12, 34, 0, 0, time.Local),
		WriterID:   "writer-abc",
	}}
	got := SessionInUseMessage(live)
	for _, want := range []string{
		"pid 5000",
		"reasonix-desktop.exe",
		`C:\Program Files\Reasonix\reasonix-desktop.exe`,
		"host DESKTOP-XYZ",
		"writer writer-abc",
		"since 12:34",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("live message %q must contain %q", got, want)
		}
	}
}

func TestSessionInUseMessageNamesPidReuseStaleRecord(t *testing.T) {
	withProcessAliveForTest(t, func(pid int) bool { return true })
	withProcessImageNameForControlTest(t, func(pid int) (string, string) {
		return "everything-scraper.exe", `C:\tools\everything-scraper.exe`
	})

	reused := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: 6000, WriterID: "w-old"}}
	got := SessionInUseMessage(reused)
	if !strings.Contains(got, "stale lease record") || !strings.Contains(got, "pid 6000") {
		t.Fatalf("reused-pid message = %q, want the stale-record wording with the pid", got)
	}
	if !strings.Contains(got, "everything-scraper.exe") {
		t.Fatalf("reused-pid message = %q, must name the program that now owns the pid", got)
	}
	if strings.Contains(got, SessionLeaseCloseHint) {
		t.Fatalf("reused-pid message must not send the user closing a window that does not exist, got %q", got)
	}
	if got := SessionLeaseHintFor(reused); !strings.Contains(got, "another program") || !strings.Contains(got, "force-archive") {
		t.Fatalf("reused-pid hint = %q, want force-archive/restart guidance", got)
	}
}

func TestSessionLeaseHintForZombieKeepsIdentityFreeOfWindowAdvice(t *testing.T) {
	withProcessAliveForTest(t, func(int) bool { return false })
	dead := &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: 7000}}
	got := SessionLeaseHintFor(dead)
	if !strings.Contains(got, "pid 7000") || !strings.Contains(got, "restart Reasonix") {
		t.Fatalf("zombie hint = %q, want pid + restart guidance", got)
	}
}
