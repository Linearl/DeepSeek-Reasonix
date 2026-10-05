package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 485 P2a：退避时间表——5s 起步、逐次翻倍、5min 封顶。
func TestCollabRetryDelaySchedule(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, collabRetryBackoffBase},
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{4, 40 * time.Second},
		{6, 160 * time.Second},
		{7, collabRetryBackoffMax},
		{50, collabRetryBackoffMax},
	}
	for _, c := range cases {
		if got := collabRetryDelay(c.failures); got != c.want {
			t.Fatalf("collabRetryDelay(%d)=%v, want %v", c.failures, got, c.want)
		}
	}
}

// Task 485 P2a：连续失败的翻倍链与成功后的清零——泵不会再每 5s 打一轮
// refused 日志风暴。
func TestCollabRetryBackoffStateMachine(t *testing.T) {
	p := &sessionCollabPump{}
	now := time.Now()
	const contact = "sc_contact"

	if !p.contactRetryDue(contact, now) {
		t.Fatal("a contact with no failures must always be due")
	}

	p.deferContactRetry(contact, now)
	if st := p.retry[contact]; st.failures != 1 || !st.notBefore.Equal(now.Add(5*time.Second)) {
		t.Fatalf("first failure: failures=%d notBefore=%v, want 1 / +5s", st.failures, st.notBefore)
	}
	if p.contactRetryDue(contact, now) {
		t.Fatal("contact must be gated right after a failure")
	}
	later := now.Add(5 * time.Second)
	if !p.contactRetryDue(contact, later) {
		t.Fatal("contact must come due after the first backoff window")
	}

	p.deferContactRetry(contact, later)
	if st := p.retry[contact]; st.failures != 2 || !st.notBefore.Equal(later.Add(10*time.Second)) {
		t.Fatalf("second failure: failures=%d notBefore=%v, want 2 / +10s", st.failures, st.notBefore)
	}

	p.resetContactRetry(contact)
	if _, ok := p.retry[contact]; ok {
		t.Fatal("success must clear the backoff state")
	}
	if !p.contactRetryDue(contact, now) {
		t.Fatal("contact must be due again after a reset")
	}
}

// Task 485 P2b：holder 是本进程时，busy 文案不得再指向「另一个 Reasonix
// 窗口」（2026-10-05 现场：单进程自锁却渲染了误导性的别窗口提示）。
func TestSessionLeaseBusyErrorSelfHolderText(t *testing.T) {
	self := &sessionLeaseBusyError{err: &agent.SessionLeaseError{
		Path: "s.jsonl",
		Info: &agent.SessionLeaseInfo{PID: os.Getpid(), WriterID: "w"},
	}}
	got := self.Error()
	if !strings.Contains(got, "this Reasonix instance") {
		t.Fatalf("self-held busy error must name this instance, got %q", got)
	}
	if strings.Contains(got, "another Reasonix window") {
		t.Fatalf("self-held busy error must not blame another window, got %q", got)
	}
	if !strings.Contains(got, "pid") {
		t.Fatalf("self-held busy error should carry the pid for log correlation, got %q", got)
	}

	foreign := &sessionLeaseBusyError{err: &agent.SessionLeaseError{
		Path: "s.jsonl",
		Info: &agent.SessionLeaseInfo{PID: os.Getpid() + 4321, WriterID: "w"},
	}}
	gotForeign := foreign.Error()
	if !strings.Contains(gotForeign, "leftover background process") {
		t.Fatalf("foreign-holder text must keep the taskkill guidance, got %q", gotForeign)
	}

	// 无 Info 时保留历史通用文案（任务 272 基线不变）。
	generic := (&sessionLeaseBusyError{err: errors.New("x")}).Error()
	if !strings.Contains(generic, "another Reasonix window") {
		t.Fatalf("generic busy error lost its baseline text, got %q", generic)
	}
}
