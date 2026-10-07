package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/sessioninbox"
)

// 任务 569 验收映射（桌面泵侧空闲开轮桥）：
// ① 空闲 + 排队 follow-up ⇒ N 秒后经 RunInboxTurn 开轮（有可检索日志、产出）；
// ② 空闲但无排队项 ⇒ 零开轮；
// ③ 开关关 = 桥整体不运行（行为等价）；
// ④ 非 active tab 判据生效（active tab 永不开轮）。
// 外加：in-flight 去重（一个目标不并发两轮）、running/paused 不开轮、
// 准入竞态失败（ErrTurnRunning 等）不算桥故障且不阻断后续 pass。

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
	b.sweepTargets([]idleTurnTargetView{target}, now, true)
	// 未满 N：仍不开轮。
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay-time.Second), true)

	// 满 N：开轮，且消费的就是排队项。
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Second), true)
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
	b.sweepTargets([]idleTurnTargetView{quiet}, start, true)
	b.sweepTargets([]idleTurnTargetView{quiet}, start.Add(sessionCollabIdleTurnDelay*2), true)

	// 降级落箱（下一条 pass 才看得见 queued 项）。
	withItem := idleTurnIdleTarget("c1", log)
	b.sweepTargets([]idleTurnTargetView{withItem}, start.Add(sessionCollabIdleTurnDelay*2+4*time.Second), true)
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
		b.sweepTargets([]idleTurnTargetView{quiet}, start.Add(time.Duration(step)*time.Minute), true)
	}
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("idle without a queued item must never open a turn, got %d", len(got))
	}
}

// 验收③：开关关 = 桥不运行（即使空闲已超 N 且有排队项）。
func TestIdleTurnBridgeDisabledIsBehaviourallyInert(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	target := idleTurnIdleTarget("c1", log)

	for step := 0; step < 3; step++ {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(time.Duration(step)*10*time.Minute), false)
	}
	time.Sleep(50 * time.Millisecond) // 给误启动的 goroutine 一个暴露窗口
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("switch off must keep the pump delivery-only, got %v", got)
	}
}

// 验收④：active tab 永不开轮，即使空闲超 N 且有排队项。
func TestIdleTurnBridgeSkipsActiveTab(t *testing.T) {
	b := newIdleTurnBridge()
	now := time.Now()
	log := &runLog{}
	target := idleTurnIdleTarget("c1", log)
	target.activeTab = true

	b.sweepTargets([]idleTurnTargetView{target}, now, true)
	b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay*2), true)
	time.Sleep(50 * time.Millisecond)
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("active tab must never be auto-started, got %v", got)
	}
}

// 边界补充：running / paused / 无 run 钩子的目标不开轮。
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
		b.sweepTargets(all, now.Add(time.Duration(step)*time.Minute), true)
	}
	time.Sleep(50 * time.Millisecond)
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("running/paused/hookless targets must never be started, got %v", got)
	}
}

// in-flight 去重：run 阻塞期间重复 sweep 不开第二轮（一个目标至多一轮在飞）。
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
	b.sweepTargets([]idleTurnTargetView{slow}, now, true)                              // 观测空闲
	b.sweepTargets([]idleTurnTargetView{slow}, now.Add(sessionCollabIdleTurnDelay), true) // 满足 N，开轮
	b.sweepTargets([]idleTurnTargetView{slow}, now.Add(2*sessionCollabIdleTurnDelay), true) // 在飞，必须跳过
	close(release)
	got := waitRunLog(t, log, 1)
	if len(got) != 1 {
		t.Fatalf("expected exactly one in-flight turn for the target, got %v", got)
	}
}

// 铁律 2：默认关 —— config 零值下开关必须为 false。
func TestSessionCollabIdleTurnDisabledByDefault(t *testing.T) {
	if (config.AgentConfig{}).ExperimentalCollabIdleTurn {
		t.Fatalf("experimental_collab_idle_turn must default to off (iron rule 2)")
	}
}

// 桥错误路径：RunInboxTurn 的竞态失败（ErrTurnRunning / ErrInvalidState）不算
// 桥故障，不阻断后续 pass 再次尝试。
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
	b.sweepTargets([]idleTurnTargetView{target}, now, true)
	// 轮询推进 sweep：每次 goroutine 清掉 in-flight 后，下一次 sweep 触发新尝试。
	start := time.Now()
	for {
		b.sweepTargets([]idleTurnTargetView{target}, now.Add(sessionCollabIdleTurnDelay+time.Since(start)), true)
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
}
