package control

// 任务808 回归：TryEnqueueFollowup 降级路径的内建重试链（579 的 5s/15s/45s
// runtime-unpublished 有界重试）与桥（desktop idle-turn bridge 的
// RunInboxTurn 原生消费）之间的消费竞争收口。
//
// 796 交付报告 §6.2 实测踩坑：降级路径会 arm 内建调度重试链，与桥存在消费
// 竞争。耐久层本身 claim-once（同一 item 不可能双轮），真实缺陷面是重试链
// 的记账：runtimeRetryItem 单槽 + 预算跨 item 继承——桥消费掉钉住的 item 后，
// 新卡死的队首继承别人烧掉的预算（首驱直接落到 45s），用尽报告张冠李戴。
// 本文件钉住两形态：
//  1. 链在手、桥抢走队首 → 链让位（stand-down）：不再重驱、不再武装、
//     不出用尽报告；消费恰好一次（无双消费、无丢失）。
//  2. 卡死队首换人 → 预算复位：新队首拿到完整的 5/15/45 自有预算，用尽
//     报告（若到）报的是它自己——不互相覆盖。

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/sessioninbox"
)

// gatedUnpublishedAdmission parks the dispatcher INSIDE beforeInboxDispatch
// until the gate closes, then answers the 579 dead-path error. It models the
// race window between the chain's failed attempt and the bridge's claim.
type gatedUnpublishedAdmission struct {
	mu      sync.Mutex
	gate    chan struct{}
	entered chan struct{}
	closed  bool
}

func newGatedUnpublishedAdmission() *gatedUnpublishedAdmission {
	return &gatedUnpublishedAdmission{
		gate:    make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
}

func (a *gatedUnpublishedAdmission) admit(*Controller) (func(), error) {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return nil, ErrInboxRuntimeUnpublished
	}
	a.entered <- struct{}{}
	<-a.gate
	return nil, ErrInboxRuntimeUnpublished
}

func (a *gatedUnpublishedAdmission) release() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		close(a.gate)
	}
}

// waitNoRuntimeRetry asserts the chain stays disarmed for a grace period
// (deterministic seam: nothing arrives on the capture channel).
func waitNoRuntimeRetry(t *testing.T, retries <-chan struct {
	delay time.Duration
	fire  func()
}) {
	t.Helper()
	select {
	case armed := <-retries:
		t.Fatalf("retry chain must stay disarmed, got an arm with delay %s", armed.delay)
	case <-time.After(300 * time.Millisecond):
	}
}

// consumeViaBridge consumes one inbox item through the bridge's native path
// (RunInboxTurn — bypasses beforeInboxDispatch, like the desktop idle-turn
// bridge) and waits for the durable ack to complete.
func consumeViaBridge(t *testing.T, c *Controller, itemID string) {
	t.Helper()
	completed := make(chan error, 1)
	go func() { completed <- c.RunInboxTurn(context.Background(), itemID) }()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatalf("bridge consumption failed: %v", err)
		}
	case <-time.After(inboxDispatchTestTimeout):
		t.Fatal("bridge consumption never completed")
	}
}

// 形态一：链武装后桥抢走队首 → 链让位——不再武装、不出报告、无双消费。
func TestRuntimeRetryStandsDownWhenBridgeConsumesPinnedItem(t *testing.T) {
	c, runner, _ := newInboxDispatchController(t)
	admission := newGatedUnpublishedAdmission()
	c.SetBeforeInboxDispatch(admission.admit)
	retries := captureRuntimeRetries(c)
	exhausted := make(chan InboxDispatchExhausted, 1)
	c.SetOnInboxDispatchExhausted(func(info InboxDispatchExhausted) { exhausted <- info })

	rec, err := c.TryEnqueueFollowup(InboxRequest{
		Submit: "被桥抢走的队首-808", Source: "collab:sender-1",
		CollabMsgID: "msg_808", CollabMailTo: "target-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 派发器首次尝试按在 beforeDispatch 里（链即将武装的真实窗口）。
	select {
	case <-admission.entered:
	case <-time.After(inboxDispatchTestTimeout):
		t.Fatal("dispatcher never reached the gated admission")
	}

	// 桥在窗口内原生消费队首（RunInboxTurn 绕过 beforeDispatch）。
	consumeViaBridge(t, c, rec.ItemID)
	// 同步轮（RunInboxTurn）不向普通 sink 发 TurnDone——其返回即代表持久 ack 已完成。

	// 放行派发器：attempt 答 unpublished → runtimePending → 钉住 B →
	// 武装前的队首校验发现 B 已被消费 → 链让位。
	admission.release()

	waitNoRuntimeRetry(t, retries)
	select {
	case info := <-exhausted:
		t.Fatalf("a consumed head must not produce an exhaustion report: %+v", info)
	case <-time.After(300 * time.Millisecond):
	}
	c.inbox.mu.Lock()
	pinned := c.inbox.runtimeRetryItem.ID
	c.inbox.mu.Unlock()
	if pinned != "" {
		t.Fatalf("stand-down must clear the retry pin, got %q", pinned)
	}
	// 恰好一次消费：先取走桥那轮的合法输入，再断言没有第二次。
	c.autosaveWG.Wait()
	select {
	case input := <-runner.inputs:
		if !strings.Contains(input, "被桥抢走的队首-808") {
			t.Fatalf("bridge turn input mismatch: %q", input)
		}
	case <-time.After(inboxDispatchTestTimeout):
		t.Fatal("bridge consumption never reached the runner")
	}
	select {
	case input := <-runner.inputs:
		t.Fatalf("no duplicate consumption allowed, got extra input %q", input)
	case <-time.After(300 * time.Millisecond):
	}
	if items := c.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("consumed item must leave the inbox, items = %+v", items)
	}
}

// 形态二：卡死队首换人 → 新队首拿到完整自有预算（5/15/45），用尽报告报它
// 自己；item 不丢失。
func TestRuntimeRetryFreshBudgetWhenStuckHeadChanges(t *testing.T) {
	c, _, _ := newInboxDispatchController(t)
	admission := &runtimeUnpublishedAdmission{}
	c.SetBeforeInboxDispatch(admission.admit)
	retries := captureRuntimeRetries(c)
	exhausted := make(chan InboxDispatchExhausted, 1)
	c.SetOnInboxDispatchExhausted(func(info InboxDispatchExhausted) { exhausted <- info })

	recB, err := c.TryEnqueueFollowup(InboxRequest{
		Submit: "先卡死的 B-808", Source: "collab:sender-1", CollabMsgID: "msg_B", CollabMailTo: "t1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// B 烧掉预算的前两格：arm(5s) → fire → arm(15s)，第三次 pending。
	armed := <-retries
	if armed.delay != 5*time.Second {
		t.Fatalf("first arm delay = %s, want 5s", armed.delay)
	}
	armed.fire()
	armed = <-retries
	if armed.delay != 15*time.Second {
		t.Fatalf("second arm delay = %s, want 15s", armed.delay)
	}

	// 桥原生消费 B（预算中途换队首的真实形态）。
	consumeViaBridge(t, c, recB.ItemID)
	// 同步轮（RunInboxTurn）不向普通 sink 发 TurnDone——其返回即代表持久 ack 已完成。

	// 新队首 C 落队 → kick → unpublished → 钉住 C。
	recC, err := c.TryEnqueueFollowup(InboxRequest{
		Submit: "接棒的 C-808", Source: "collab:sender-2", CollabMsgID: "msg_C", CollabMailTo: "t2",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 烧掉 pending 的 15s 格：attempt 落在 C（新队首）。修一笔账：
	// C 换钉时预算必须已复位——本次 arm 是 C 的第一驱，5s 起步。
	armed.fire()
	armed = <-retries
	if armed.delay != 5*time.Second {
		t.Fatalf("fresh head must restart the budget (5s), got %s — cross-item bleed", armed.delay)
	}
	armed.fire()
	armed = <-retries
	if armed.delay != 15*time.Second {
		t.Fatalf("second fresh arm delay = %s, want 15s", armed.delay)
	}
	armed.fire()
	armed = <-retries
	if armed.delay != 45*time.Second {
		t.Fatalf("third fresh arm delay = %s, want 45s", armed.delay)
	}
	armed.fire()

	// 用尽报告报 C 自己（坐标不张冠李戴），且 C 不丢失。
	select {
	case info := <-exhausted:
		if info.ItemID != recC.ItemID {
			t.Fatalf("exhaustion report must name the current stuck head %s, got %s", recC.ItemID, info.ItemID)
		}
		if info.CollabMsgID != "msg_C" || info.CollabMailTo != "t2" || info.Source != "collab:sender-2" {
			t.Fatalf("exhaustion report lost the current head's receipt coordinates: %+v", info)
		}
		if info.Attempts != len(inboxRuntimeRetryBackoff) {
			t.Fatalf("exhaustion attempts = %d, want %d", info.Attempts, len(inboxRuntimeRetryBackoff))
		}
	case <-time.After(inboxDispatchTestTimeout):
		t.Fatal("exhaustion report never arrived for the fresh head")
	}
	snap := c.InboxSnapshot()
	if len(snap.Items) != 1 || snap.Items[0].ID != recC.ItemID || snap.Items[0].State != sessioninbox.StateQueued {
		t.Fatalf("exhausted C must stay queued (no loss), snapshot = %+v", snap)
	}
}
