package control

// 任务807 回归：finishing 窗口期的 inbox item 不再永久滞留。
//
// 796 交付报告 §6.1 实测踩坑：finishing 窗口期间入队的 follow-up 会被挂
// running 态且无人释放（onInboxTurnDone 已跑过、迟到的入队无人认领）。
// 本文件钉住两条闸门边界（正常关窗 / 796 heal 重开）的校正语义：
//  1. 窗口期入队的 item 在关窗后照常被消费（基线不变量，防回归）；
//  2. 无主 running 认领（认领后管线死亡的残留：running 且未登记任何归属）
//     在两条边界被校正回 queued 并随即消费——不改写成 Uncertain 人工检视、
//     不暂停队列、不永久 running；
//  3. 认领中（admittingOwnership 已登记）的 item 不被误释放（竞态防护）。

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// wedgeableDispatchController = newInboxDispatchController 的可楔住变体：
// 第一个 TurnDone 在扇出里卡到 release 关闭为止（真实 796 泄漏窗构造），
// 之后每个 TurnDone 照常送 done。sink 必须经 Options 注入——屏障语义在
// coalescer 的 checked emit 里，事后替换 c.sink 不经过它。
func wedgeableDispatchController(t *testing.T) (*Controller, *inboxDispatchRunner, <-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	dir := t.TempDir()
	runner := &inboxDispatchRunner{inputs: make(chan string, 8)}
	done := make(chan struct{}, 8)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var first atomic.Int32
	c := New(Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind != event.TurnDone {
				return
			}
			if first.Add(1) == 1 {
				entered <- struct{}{}
				<-release
			}
			done <- struct{}{}
		}),
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() {
		unblock()
		c.Close()
		c.autosaveWG.Wait()
	})
	return c, runner, done, entered, unblock
}

// wedgeFinishingGate runs one turn into the wedged fan-out and waits for the
// leak window to open (running=false + finishing=true).
func wedgeFinishingGate(t *testing.T, c *Controller, entered <-chan struct{}) {
	t.Helper()
	started := make(chan struct{}, 1)
	c.runGuarded(func(context.Context) error {
		started <- struct{}{}
		return nil
	})
	<-started
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn done never reached the fan-out")
	}
	if st := c.RuntimeStatus(); !st.Running || !st.Finishing {
		t.Fatalf("wedge setup failed: %+v", st)
	}
}

// strayClaim simulates a claim whose owning pipeline died before it could
// register active ownership: durable running, controller tracks nothing
// (796 §6.1 的「无人认领」残留形态).
func strayClaim(t *testing.T, c *Controller, id string) {
	t.Helper()
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimItem(id); err != nil {
		t.Fatal(err)
	}
}

// itemState reads one item's durable state WITHOUT Controller.InboxSnapshot
// (which runs store-level recovery and would rewrite the stray to Uncertain
// before the gate boundary gets its pass).
func itemState(t *testing.T, c *Controller, id string) sessioninbox.InboxState {
	t.Helper()
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	meta, _, err := st.ReadItem(id)
	if err != nil {
		t.Fatal(err)
	}
	return meta.State
}

// requireNoUncertain asserts the reconciliation outcome is requeue-and-consume,
// never the false human-review park.
func requireNoUncertain(t *testing.T, c *Controller) {
	t.Helper()
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range st.Snapshot().Items {
		if item.State == sessioninbox.StateUncertain {
			t.Fatalf("stray must not be parked as uncertain: %+v", st.Snapshot().Items)
		}
	}
}

// 轨迹①（796 §6.1 端到端基线）：wedge → 窗口期入队 → 闸门正常关闭 →
// item 被关窗 kick 消费。防回归不变量：窗口期入队不丢、不挂。
func TestFinishingGateCloseConsumesWindowEraEnqueue(t *testing.T) {
	c, runner, done, entered, unblock := wedgeableDispatchController(t)
	wedgeFinishingGate(t, c, entered)

	rec, err := c.TryEnqueueFollowup(InboxRequest{Submit: "窗口期入队-807", Source: "t807"})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemState(t, c, rec.ItemID); got != sessioninbox.StateQueued {
		t.Fatalf("window-era enqueue must stay queued during the window, got %s", got)
	}

	unblock()
	waitForInboxTurnDone(t, c, done)
	input := waitForInboxDispatch(t, c, runner)
	if !strings.Contains(input, "窗口期入队-807") {
		t.Fatalf("window-era enqueue must be consumed after the gate closes, got %q", input)
	}
	waitForInboxTurnDone(t, c, done)
	if items := c.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("consumed window-era item must leave the inbox, items = %+v", items)
	}
}

// 轨迹②（修复主场景）：无主 running 认领在闸门正常关闭时被校正回 queued，
// 并被随后的关窗 kick 直接再消费——不 Uncertain、不暂停、不永久 running。
func TestUnownedClaimRequeuedAtFinishingGateClose(t *testing.T) {
	// 无 wedge：普通控制器一轮 turn 的关窗即是闸门边界（校正 + kick 再消费）。
	c, runner, done := newInboxDispatchController(t)
	// EnqueueInbox（不 kick 派发）：空闲控制器上 TryEnqueueFollowup 会立即
	// 派发消费，模拟「认领后管线死亡」必须绕开派发器。
	rec, err := c.EnqueueInbox(InboxRequest{Submit: "无主认领-807", Source: "t807"})
	if err != nil {
		t.Fatal(err)
	}
	strayClaim(t, c, rec.ItemID)
	if c.inbox.ownsItem(rec.ItemID) {
		t.Fatal("stray simulation must not be owned")
	}
	if got := itemState(t, c, rec.ItemID); got != sessioninbox.StateRunning {
		t.Fatalf("stray setup failed, state = %s", got)
	}

	// 一轮普通 turn 的关窗即是闸门边界：校正 + kick 再消费。
	bodyRan := make(chan struct{}, 1)
	c.runGuarded(func(context.Context) error {
		bodyRan <- struct{}{}
		return nil
	})
	select {
	case <-bodyRan:
	case <-time.After(5 * time.Second):
		t.Fatal("turn body never ran")
	}
	waitForInboxTurnDone(t, c, done)
	input := waitForInboxDispatch(t, c, runner)
	if !strings.Contains(input, "无主认领-807") {
		t.Fatalf("released stray must be re-consumed at the gate close, got %q", input)
	}
	waitForInboxTurnDone(t, c, done)
	requireNoUncertain(t, c)
	if items := c.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("re-consumed stray must leave the inbox, items = %+v", items)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if st.Snapshot().Paused {
		t.Fatal("stray requeue must not pause the queue")
	}
}

// 轨迹③（796 heal 桥语义覆盖）：无主 running 认领在 HealStuckFinishing
// 重开闸门时被校正回 queued。heal 的 dispatch kick（真控制器路径）随即
// 再消费——屏障解除后端到端闭环。
func TestUnownedClaimRequeuedAtHealReopen(t *testing.T) {
	c, runner, done, entered, unblock := wedgeableDispatchController(t)
	wedgeFinishingGate(t, c, entered)

	rec, err := c.TryEnqueueFollowup(InboxRequest{Submit: "heal 校正-807", Source: "t807"})
	if err != nil {
		t.Fatal(err)
	}
	strayClaim(t, c, rec.ItemID)
	if got := itemState(t, c, rec.ItemID); got != sessioninbox.StateRunning {
		t.Fatalf("stray must stay running before the heal, got %s", got)
	}

	// heal 在屏障仍卡着时触发（真实 796 场景：桥 sweep 调 heal）。heal 尾部
	// 的 dispatch kick 会 claim 并卡在 emit 屏障后，放 goroutine 跑。
	healedCh := make(chan bool, 1)
	go func() {
		c.mu.Lock()
		c.finishingSince = time.Now().Add(-time.Hour)
		c.mu.Unlock()
		healed, _ := c.HealStuckFinishing(time.Minute)
		healedCh <- healed
	}()

	// 解锁屏障：heal kick 恢复 → 校正已生效 → T2 消费 stray。
	unblock()
	select {
	case healed := <-healedCh:
		if !healed {
			t.Fatal("wedged gate must heal")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("heal never returned after the barrier cleared")
	}
	waitForInboxTurnDone(t, c, done)
	input := waitForInboxDispatch(t, c, runner)
	if !strings.Contains(input, "heal 校正-807") {
		t.Fatalf("stray released at the heal boundary must be consumed, got %q", input)
	}
	waitForInboxTurnDone(t, c, done)
	requireNoUncertain(t, c)
	if items := c.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("heal-path stray must end consumed, items = %+v", items)
	}
}

// 竞态防护：认领中（admittingOwnership 已登记、active 未登记）的 item
// 不得被边界校正释放——否则会把正在入场的轮次拦腰打断。
func TestMidAdmissionClaimSurvivesGateBoundary(t *testing.T) {
	c, _, _, _, _ := wedgeableDispatchController(t)
	// EnqueueInbox（不 kick 派发）：item 必须保持 queued 供受控认领。
	rec, err := c.EnqueueInbox(InboxRequest{Submit: "认领中-807", Source: "t807"})
	if err != nil {
		t.Fatal(err)
	}
	// 精确模拟 TrySubmitInboxItem 的 claim-to-active 间隙：admission 已登记、
	// ClaimItem 已落库、active 尚未登记。
	c.inbox.trackAdmission(rec.ItemID)
	t.Cleanup(func() { c.inbox.untrackAdmission(rec.ItemID) })
	strayClaim(t, c, rec.ItemID)

	c.releaseUnownedInboxClaims("test_probe")

	if got := itemState(t, c, rec.ItemID); got != sessioninbox.StateRunning {
		t.Fatalf("mid-admission claim must survive the gate boundary, got %s", got)
	}
}
