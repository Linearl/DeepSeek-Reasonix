package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"reasonix/internal/sessioninbox"
)

// 任务709 桥侧行为：自动暂停（重启恢复/重开遗留）在开轮前被唤醒；用户暂停
// 依旧绝对静默；唤醒失败的持续错误按 579 有界预算计 strike；用户在唤醒与
// 认领之间恰好接手队列（ErrPaused）属竞态类，不烧预算。

// autoPausedTarget 构造一个自动暂停、有排队项的目标。
func autoPausedTarget(id string, log *runLog, resume func() error) idleTurnTargetView {
	t := idleTurnIdleTarget(id, log)
	t.autoPaused = true
	t.resume = resume
	return t
}

// 验收（709③）：自动暂停目标满 N 后——先唤醒（resume 恰一次）再开轮。
func TestIdleTurnBridgeWakesAutoPausedTarget(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	var mu sync.Mutex
	resumes := 0
	target := autoPausedTarget("c-stale", log, func() error {
		mu.Lock()
		resumes++
		mu.Unlock()
		return nil
	})

	b.sweepTargets([]idleTurnTargetView{target}, now)
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Second))

	got := waitRunLog(t, log, 1)
	if len(got) != 1 || got[0] != "item-c-stale" {
		t.Fatalf("auto-paused stale target must be woken and opened exactly once, got %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if resumes != 1 {
		t.Fatalf("resume must be called exactly once before the open, got %d", resumes)
	}
}

// 唤醒失败（持久性错误）按预算计 strike：3 次后封存并触发用尽上报，run 零调用。
func TestIdleTurnBridgeResumeFailureBurnsBudget(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	var mu sync.Mutex
	hookCalls := 0
	target := autoPausedTarget("c-stuck", log, func() error {
		return errors.New("disk transaction failed")
	})
	target.exhausted = func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
		mu.Lock()
		defer mu.Unlock()
		hookCalls++
		if lastErr == nil || attempts != idleTurnMaxAttempts {
			t.Errorf("exhaustion hook must carry attempts=%d and the resume error", idleTurnMaxAttempts)
		}
	}

	b.sweepTargets([]idleTurnTargetView{target}, now)
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Since(start)))
		mu.Lock()
		done := hookCalls > 0
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	calls := hookCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("exhaustion hook must fire exactly once, got %d", calls)
	}
	time.Sleep(50 * time.Millisecond)
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("a failing resume must never reach run(), got %v", got)
	}
	if !b.exhausted[idleTurnItemKey("c-stuck", "item-c-stuck")] {
		t.Fatal("stuck wake must be marked exhausted (visible, not silent)")
	}
}

// 接线防护：autoPaused 却没有 resume seam 是装配错误——按持续性失败封存，
// 不形成热循环，也绝不裸开轮（否则 ClaimItem 必然 ErrPaused）。
func TestIdleTurnBridgeAutoPausedWithoutResumeSeamSuppresses(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	target := autoPausedTarget("c-nowire", log, nil)

	b.sweepTargets([]idleTurnTargetView{target}, now)
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Since(start)))
		if b.exhausted[idleTurnItemKey("c-nowire", "item-c-nowire")] {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !b.exhausted[idleTurnItemKey("c-nowire", "item-c-nowire")] {
		t.Fatal("missing resume seam must be reported through the budget, not ignored")
	}
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("no open without a resume seam, got %v", got)
	}
}

// 用户在唤醒与认领之间接手队列：run 返回 ErrPaused 属竞态类——不烧预算、
// 不封存，队列易主后同一 item 还能再被尝试。
func TestIdleTurnBridgeUserPausedMidWakeIsRaceNotStrike(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	var mu sync.Mutex
	attempts := 0
	fail := true
	target := autoPausedTarget("c-race", log, func() error { return nil })
	target.run = func(ctx context.Context, id string) error {
		log.add(id)
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if fail && n == 1 {
			return sessioninbox.ErrPaused
		}
		return nil
	}

	b.sweepTargets([]idleTurnTargetView{target}, now)
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Second))
	if got := waitRunLog(t, log, 1); len(got) != 1 {
		t.Fatalf("first wake+open attempt missing, got %v", got)
	}
	// ErrPaused 后预算必须原封未动：轮询推进 sweep（等 in-flight 清空），
	// 下一次尝试立即成功。
	fail = false
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Since(start)))
		mu.Lock()
		done := attempts >= 2
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("ErrPaused must not burn the budget — immediate retry expected, attempts=%d", attempts)
	}
	if b.exhausted[idleTurnItemKey("c-race", "item-c-race")] {
		t.Fatal("ErrPaused must never exhaust the opening budget")
	}
}
