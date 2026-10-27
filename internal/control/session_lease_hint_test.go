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
