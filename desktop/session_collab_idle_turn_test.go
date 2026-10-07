package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/sessioninbox"
)

// 任务 569 验收映射（桌面泵侧空闲开轮桥）+ 任务 579 修订：
// ① 空闲 + 排队 follow-up ⇒ N 秒后经 RunInboxTurn 开轮（有可检索日志、产出）；
// ② 空闲但无排队项 ⇒ 零开轮；
// ③ 579：桥无开关，始终打开；唯一剩余门是守卫④（drain_inbox 互斥）；
// ④ 非 active tab 判据生效（active tab 永不开轮）。
// 579 补充件：开轮失败不再每 4s tick 无限重试——每卡住队首 3 次预算，用尽后
// 封存该 item（保留队列不丢弃）并触发用尽上报；竞态类失败（另一条准入路径
// 赢了）不烧预算；新队首 / 会话恢复活动会复位预算。
// 外加：in-flight 去重（一个目标不并发两轮）、running/paused 不开轮。

// runLog collects RunInboxTurn invocations from bridge goroutines without
// data races; waitCount blocks the test until the expected count lands.
type runLog struct {
	mu    sync.Mutex
	items []string
}

func (l *runLog) add(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, id)
}

func (l *runLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

func waitRunLog(t *testing.T, l *runLog, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := l.snapshot(); len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return l.snapshot()
}

func idleTurnIdleTarget(id string, log *runLog) idleTurnTargetView {
	return idleTurnTargetView{
		contactID: id,
		queuedID:  "item-" + id,
		run: func(ctx context.Context, itemID string) error {
			log.add(itemID)
			return nil
		},
	}
}

// 验收①：空闲 N 秒 + 排队 follow-up ⇒ 桥按排队项开一轮；未满 N 不开。
func TestIdleTurnBridgeOpensTurnAfterIdleWindow(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	target := idleTurnIdleTarget("c1", log)

	// 第一次观测：开始计时，不开轮。
	b.sweepTargets([]idleTurnTargetView{target}, now)
	// 未满 N：仍不开轮。
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay-time.Second))

	// 满 N：开轮，且消费的就是排队项。
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Second))
	got := waitRunLog(t, log, 1)
	if len(got) != 1 || got[0] != "item-c1" {
		t.Fatalf("expected exactly one RunInboxTurn call with the queued item, got %v", got)
	}
}

// 验收①（补充）：会话先空闲很久才降级落箱 ⇒ 落箱后一个泵周期内即消费，
// 不再等满 N（与「降级后 N 秒内被消费」的验收口径一致）。
func TestIdleTurnBridgeConsumesBacklogOnNextPass(t *testing.T) {
	b := newIdleTurnBridge()
	start := time.Now()
	log := &runLog{}
	quiet := idleTurnTargetView{contactID: "c1", run: func(context.Context, string) error { return nil }}

	// 消息到达前目标已空闲 ≥ N。
	b.sweepTargets([]idleTurnTargetView{quiet}, start)
	b.sweepTargets([]idleTurnTargetView{quiet}, start.Add(sessionCollabIdleTurnDelay*2))

	// 降级落箱（下一条 pass 才看得见 queued 项）。
	withItem := idleTurnIdleTarget("c1", log)
	b.sweepTargets([]idleTurnTargetView{withItem}, start.Add(sessionCollabIdleTurnDelay*2+4*time.Second))
	got := waitRunLog(t, log, 1)
	if len(got) != 1 || got[0] != "item-c1" {
		t.Fatalf("backlog must be consumed on the pass after it lands, got %v", got)
	}
}

// 验收②：空闲但无排队项 ⇒ 零开轮（无空轮）。
func TestIdleTurnBridgeNeverOpensAnEmptyTurn(t *testing.T) {
	b := newIdleTurnBridge()
	start := time.Now()
	log := &runLog{}
	quiet := idleTurnTargetView{contactID: "c1", run: func(context.Context, string) error { return nil }}

	for step := 0; step < 20; step++ {
		b.sweepTargets([]idleTurnTargetView{quiet}, start.Add(time.Duration(step)*time.Minute))
	}
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("idle without a queued item must never open a turn, got %d", len(got))
	}
}

// 验收④：active tab 永不开轮，即使空闲超 N 且有排队项（守卫回归①）。
func TestIdleTurnBridgeSkipsActiveTab(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	target := idleTurnIdleTarget("c1", log)
	target.activeTab = true

	b.sweepTargets([]idleTurnTargetView{target}, now)
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay*2))
	time.Sleep(50 * time.Millisecond)
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("active tab must never be auto-started, got %v", got)
	}
}

// 边界补充：running / paused / 无 run 钩子的目标不开轮（守卫回归②）。
func TestIdleTurnBridgeSkipsRunningPausedAndHookless(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	running := idleTurnIdleTarget("c-running", log)
	running.running = true
	paused := idleTurnIdleTarget("c-paused", log)
	paused.paused = true
	hookless := idleTurnIdleTarget("c-hookless", log)
	hookless.run = nil

	all := []idleTurnTargetView{running, paused, hookless}
	for step := 0; step < 3; step++ {
		b.sweepTargets(all, now.Add(time.Duration(step)*time.Minute))
	}
	time.Sleep(50 * time.Millisecond)
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("running/paused/hookless targets must never be started, got %v", got)
	}
}

// in-flight 去重：run 阻塞期间重复 sweep 不开第二轮（一个目标至多一轮在飞，
// 守卫回归③）。
func TestIdleTurnBridgeNeverStartsTwicePerTarget(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	release := make(chan struct{})
	slow := idleTurnTargetView{
		contactID: "c-slow",
		queuedID:  "item-slow",
		run: func(ctx context.Context, id string) error {
			log.add(id)
			<-release
			return nil
		},
	}
	b.sweepTargets([]idleTurnTargetView{slow}, now)                                   // 观测空闲
	b.sweepTargets([]idleTurnTargetView{slow}, now.Add(sessionCollabIdleTurnDelay))   // 满足 N，开轮
	b.sweepTargets([]idleTurnTargetView{slow}, now.Add(2*sessionCollabIdleTurnDelay)) // 在飞，必须跳过
	close(release)
	got := waitRunLog(t, log, 1)
	if len(got) != 1 {
		t.Fatalf("expected exactly one in-flight turn for the target, got %v", got)
	}
}

// 守卫回归④：drain_inbox 模式（experimental_collab_background_delivery）与桥互斥。
func TestIdleTurnBridgeMutuallyExclusiveWithBackgroundDelivery(t *testing.T) {
	if !idleTurnSweepActive(false) {
		t.Fatal("pump delivery mode must keep the idle-turn bridge on (579: always-on)")
	}
	if idleTurnSweepActive(true) {
		t.Fatal("drain_inbox mode must disable the idle-turn bridge (task 224 mutual exclusion)")
	}
}

// 桥错误路径：RunInboxTurn 的竞态失败（ErrTurnRunning / ErrInvalidState）不算
// 桥故障，不烧预算，不阻断后续 pass 再次尝试。
func TestIdleTurnBridgeToleratesAdmissionRaceErrors(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	var mu sync.Mutex
	attempts := 0
	target := idleTurnTargetView{
		contactID: "c1",
		queuedID:  "item-1",
		run: func(ctx context.Context, id string) error {
			mu.Lock()
			attempts++
			n := attempts
			mu.Unlock()
			if n == 1 {
				return control.ErrTurnRunning
			}
			return sessioninbox.ErrInvalidState
		},
	}
	b.sweepTargets([]idleTurnTargetView{target}, now)
	// 轮询推进 sweep：每次 goroutine 清掉 in-flight 后，下一次 sweep 触发新尝试。
	start := time.Now()
	for {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Since(start)))
		mu.Lock()
		done := attempts >= 2
		mu.Unlock()
		if done {
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatalf("bridge must retry after a lost admission race, attempts=%d", attempts)
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("expected exactly two attempts, got %d", attempts)
	}
	// 竞态失败不烧预算：不得进入用尽封存态。
	if b.exhausted[idleTurnItemKey("c1", "item-1")] {
		t.Fatal("admission race errors must not exhaust the opening budget")
	}
}

// 任务579 补充件：持续性开轮失败 ⇒ 恰好 3 次尝试，之后封存该队首（不再每
// 4s 无限重开），并触发用尽上报（回执坐标随行）。
func TestIdleTurnBridgeBoundedRetriesThenExhausts(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	var mu sync.Mutex
	hookCalls := 0
	hookSeen := ""
	target := idleTurnTargetView{
		contactID:          "c-stuck",
		queuedID:           "item-stuck",
		queuedCollabMsgID:  "msg_stuck",
		queuedCollabMailTo: "c-stuck",
		queuedSource:       "collab:sender-9",
		run: func(ctx context.Context, id string) error {
			log.add(id)
			return errors.New("persistent runtime failure")
		},
		exhausted: func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
			mu.Lock()
			defer mu.Unlock()
			hookCalls++
			hookSeen = collabMsgID
			if contactID != "c-stuck" || itemID != "item-stuck" || collabMailTo != "c-stuck" || source != "collab:sender-9" {
				t.Errorf("exhaustion hook lost coordinates: %s %s %s %s", contactID, itemID, collabMailTo, source)
			}
			if attempts != idleTurnMaxAttempts {
				t.Errorf("exhaustion attempts = %d, want %d", attempts, idleTurnMaxAttempts)
			}
			if lastErr == nil {
				t.Error("exhaustion hook must carry the last error")
			}
		},
	}

	// 先在基准时刻起一次空闲时钟，再连续 sweep（模拟 4s 泵连续 tick，每次
	// 时间戳 = now + N + 真实流逝；直接从 now+N 起钟会让空闲永远到不了 N）。
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
	seen := hookSeen
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("exhaustion hook must fire exactly once, got %d", calls)
	}
	if seen != "msg_stuck" {
		t.Fatalf("exhaustion hook lost the collab message id: %q", seen)
	}
	// 用尽后：不再开轮（封存），item 保留在调用方视角（桥不删队列）。
	time.Sleep(50 * time.Millisecond)
	got := log.snapshot()
	if len(got) != idleTurnMaxAttempts {
		t.Fatalf("expected exactly %d opening attempts, got %v", idleTurnMaxAttempts, got)
	}
	if !b.exhausted[idleTurnItemKey("c-stuck", "item-stuck")] {
		t.Fatal("stuck head must be marked exhausted")
	}
	// 继续 sweep：封存生效，尝试数不再增长。
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay*3))
	time.Sleep(50 * time.Millisecond)
	if got := log.snapshot(); len(got) != idleTurnMaxAttempts {
		t.Fatalf("exhausted head must stay suppressed, attempts = %v", got)
	}
}

// 任务579：用尽后换新队首 ⇒ 新消息拿到全新预算（旧账不继承）。
func TestIdleTurnBridgeFreshBudgetForNewHead(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	var mu sync.Mutex
	attempts := 0
	target := idleTurnTargetView{
		contactID: "c1",
		queuedID:  "item-old",
		run:       func(ctx context.Context, id string) error { return errors.New("persistent failure") },
	}
	b.sweepTargets([]idleTurnTargetView{target}, now)
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Since(start)))
		if b.exhausted[idleTurnItemKey("c1", "item-old")] {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !b.exhausted[idleTurnItemKey("c1", "item-old")] {
		t.Fatal("old head must be exhausted before this test is meaningful")
	}

	// 新消息成为队首：预算必须复位——新队首要重新走满 3 次才会再次用尽
	//（若旧账泄漏，新队首会被立即封存，一次尝试都不会有）。
	target.run = func(ctx context.Context, id string) error {
		mu.Lock()
		attempts++
		mu.Unlock()
		return errors.New("persistent failure too")
	}
	target.queuedID = "item-new"
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay*3))
	start2 := time.Now()
	deadline2 := start2.Add(5 * time.Second)
	for time.Now().Before(deadline2) {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay*3+time.Since(start2)))
		if b.exhausted[idleTurnItemKey("c1", "item-new")] {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !b.exhausted[idleTurnItemKey("c1", "item-new")] {
		t.Fatal("new head must get its own fresh budget and exhaust it again")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != idleTurnMaxAttempts {
		t.Fatalf("new head must spend exactly its own budget of %d attempts, got %d", idleTurnMaxAttempts, attempts)
	}
}

// 任务579：用尽后目标真正跑了一轮（running=true 被观测到）⇒ 预算复位，
// 同一 item 也可以再次被尝试。
func TestIdleTurnBridgeRunningObservationResetsBudget(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	fail := true
	target := idleTurnTargetView{
		contactID: "c1",
		queuedID:  "item-1",
		run: func(ctx context.Context, id string) error {
			log.add(id)
			if fail {
				return errors.New("persistent failure")
			}
			return nil
		},
	}
	b.sweepTargets([]idleTurnTargetView{target}, now)
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Since(start)))
		if b.exhausted[idleTurnItemKey("c1", "item-1")] {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !b.exhausted[idleTurnItemKey("c1", "item-1")] {
		t.Fatal("head must be exhausted before this test is meaningful")
	}

	// 会话活动被观测到（running=true 的那一拍）：预算复位，封存解除；
	// idle 时钟同步清零（跑过一轮后空闲窗口重新计时—— observeIdle 语义）。
	fail = false
	running := target
	running.running = true
	b.sweepTargets([]idleTurnTargetView{running}, now.Add(sessionCollabIdleTurnDelay*3))
	// 回到空闲起钟，再满一个 N：同一 item 可以再次被尝试，且这次成功。
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay*3+4*time.Second))
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay*4+8*time.Second))
	got := waitRunLog(t, log, idleTurnMaxAttempts+1)
	if len(got) != idleTurnMaxAttempts+1 {
		t.Fatalf("running observation must re-arm the budget, attempts = %v", got)
	}
}
