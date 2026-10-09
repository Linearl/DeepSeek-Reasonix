package main

// 任务 579 验收①②的端到端证据（今晨故障复现 + 修复验证）：
//
// 目标会话完全空闲（detached runtime：beforeInboxDispatch 一律答
// ErrInboxRuntimeUnpublished，即 2026-10-07 实测的死点）→ 发送方投 steer →
// 降级为排队 follow-up（570 回执 queued_followup）→ 空闲开轮桥两拍内真开
// 一轮（RunInboxTurn）并处理该消息（runner 收到原文）→ 收件箱清空、无重复
// turn。followup 直投同理（验收②）。
//
// 用真实 Controller + 真实 MailStore + 真实 runCollabDelivery，不 mock 消费
// 链；只有 App 这个壳不在场（它属于 wails 进程，不属于本验收的语义）。

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// idleTurnE2E bundles the real controller, its runner input log and the test
// mail store for one acceptance scenario.
type idleTurnE2E struct {
	ctrl   *control.Controller
	mail   *sessioncollab.MailStore
	mu     sync.Mutex
	inputs []string
}

func newIdleTurnE2E(t *testing.T) *idleTurnE2E {
	t.Helper()
	return newIdleTurnE2EAt(t, t.TempDir())
}

// newIdleTurnE2EAt builds the harness on an existing directory — the 任务709
// reopen scenario stands a second controller on the SAME session path to
// reproduce what a stale session's runtime looks like after a restart.
func newIdleTurnE2EAt(t *testing.T, dir string) *idleTurnE2E {
	t.Helper()
	e := &idleTurnE2E{
		mail: sessioncollab.NewMailStoreWithHopLimit(dir, 8),
	}
	e.ctrl = control.New(control.Options{
		Runner: idleTurnRunner(func(input string) {
			e.mu.Lock()
			e.inputs = append(e.inputs, input)
			e.mu.Unlock()
		}),
		Sink:        event.FuncSink(func(event.Event) {}),
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(func() { e.ctrl.Close() })
	// detached runtime 死点：宿主准入永远答「runtime 未发布」（2026-10-07
	// 实测死点——beforeInboxDispatch 在可见 tab 里找不到 owner）。
	e.ctrl.SetBeforeInboxDispatch(func(*control.Controller) (func(), error) {
		return nil, control.ErrInboxRuntimeUnpublished
	})
	return e
}

// idleTurnRunner adapts a closure to the control.Runner shape.
type idleTurnRunner func(string)

func (f idleTurnRunner) Run(_ context.Context, input string) error {
	f(input)
	return nil
}

func (e *idleTurnE2E) snapshotInputs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.inputs...)
}

func (e *idleTurnE2E) waitForInputs(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := e.snapshotInputs(); len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return e.snapshotInputs()
}

// deliverSteer delivers one steer through the REAL pump contract
// (runCollabDelivery → enqueue → degrade → queued_followup receipt) and
// returns (steered, inboxItemID, mailMessageID).
func (e *idleTurnE2E) deliverSteer(t *testing.T, from, to, body string) (bool, string, string) {
	t.Helper()
	sent, err := e.mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: from, To: to, Body: body, Delivery: "steer", Hop: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	itemID := ""
	d := collabDelivery{
		enqueue: func(msg sessioncollab.MailMessage, body string) (bool, error) {
			// deliverOne 的 detached steer 分支同款：TryEnqueueAndSteer。
			req := control.InboxRequest{
				Intent: sessioninbox.IntentSteer, Display: body, Raw: body, Submit: body,
				Source: "collab:" + msg.From, Idempotency: "collab:" + msg.ID,
				ReceiptRequested: false, CollabMsgID: msg.ID, CollabMailTo: msg.To,
			}
			rec, err := e.ctrl.TryEnqueueAndSteer(req)
			if err != nil {
				return false, err
			}
			itemID = rec.ItemID
			return sessionCollabReceiptSteered(string(rec.Disposition)), nil
		},
		notify:    func(sessioncollab.MailMessage, string, string) {},
		deriveHop: func(msg sessioncollab.MailMessage) (int, error) { return 0, nil },
		render:    sessionCollabDeliveryText,
		recordReceipt: func(msg sessioncollab.MailMessage, outcome, detail string) {
			_ = e.mail.RecordDeliveryReceipt(context.Background(), sessioncollab.DeliveryReceipt{
				MessageID: msg.ID, From: msg.From, To: msg.To,
				Delivery: msg.Delivery, Outcome: outcome, Detail: detail,
			})
		},
	}
	delivered, _, err := runCollabDelivery(e.mail, to, d)
	if err != nil || delivered != 1 {
		t.Fatalf("delivery = %d, %v", delivered, err)
	}
	return false, itemID, sent.ID
}

// sweepBridge runs the real bridge logic over one target view built from the
// controller's live snapshot — the same flattening sweepIdleInboxTurns does,
// 任务709 起含暂停来源（用户暂停/自动暂停）与唤醒 seam。
func (e *idleTurnE2E) sweepBridge(b *idleTurnBridge, contactID string, at time.Time) {
	view := idleTurnTargetView{contactID: contactID}
	snap := e.ctrl.InboxSnapshot()
	view.paused = snap.Paused && snap.UserPaused
	view.autoPaused = snap.Paused && !snap.UserPaused
	view.resume = func() error {
		_, err := e.ctrl.ResumeInboxAutoPause()
		return err
	}
	for _, item := range snap.Items {
		if item.State == sessioninbox.StateQueued {
			view.queuedID = item.ID
			view.queuedCollabMsgID = item.CollabMsgID
			view.queuedCollabMailTo = item.CollabMailTo
			view.queuedSource = item.Source
			break
		}
	}
	view.run = e.ctrl.RunInboxTurn
	b.sweepTargets([]idleTurnTargetView{view}, at)
}

// 验收①（今晨故障复现 + 修复验证）：完全空闲的 detached 目标收到 steer →
// 降级排队（570 回执可查）→ 桥两拍内真开一轮并处理该消息，不重复开轮。
func TestIdleTurnEndToEndSteerDegradeThenBridgeOpensTurn(t *testing.T) {
	e := newIdleTurnE2E(t)
	b := newIdleTurnBridge()
	const body = "579 验收①：空闲目标必须被唤醒"
	steered, itemID, mailID := e.deliverSteer(t, "c-sender", "c-target", body)
	if steered {
		t.Fatal("an idle target cannot take a steer — it must degrade")
	}
	if itemID == "" {
		t.Fatal("degraded steer must keep a durable inbox item")
	}
	// 570 状态面：降级已按 messageId 可查。
	if r, ok := e.mail.DeliveryReceipt(mailID); !ok || r.Outcome != sessioncollab.ReceiptQueuedFollowup {
		if ok {
			t.Fatalf("degraded steer receipt = %+v, want queued_followup", r)
		}
		t.Fatal("degraded steer must leave a queryable delivery receipt")
	}

	// 桥第一拍起空闲时钟，第二拍（满 N）开轮消费。
	base := time.Now()
	e.sweepBridge(b, "c-target", base)
	e.sweepBridge(b, "c-target", base.Add(sessionCollabIdleTurnDelay+4*time.Second))

	got := e.waitForInputs(t, 1)
	// runner 收到的是渲染后的完整 prompt（含系统包装），判定以原文承载为准。
	if len(got) != 1 || !strings.Contains(got[0], body) {
		t.Fatalf("bridge must open exactly one turn consuming the degraded steer, got %v", got)
	}
	// 收件箱清空；再扫一拍不得产生重复 turn（幂等）。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(e.ctrl.InboxSnapshot().Items) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap := e.ctrl.InboxSnapshot(); len(snap.Items) != 0 {
		t.Fatalf("consumed item must leave the inbox, snapshot = %+v", snap)
	}
	e.sweepBridge(b, "c-target", base.Add(sessionCollabIdleTurnDelay+8*time.Second))
	time.Sleep(50 * time.Millisecond)
	if got := e.snapshotInputs(); len(got) != 1 {
		t.Fatalf("no duplicate turns allowed, inputs = %v", got)
	}
}

// 验收②：本身就是 followup 的直投，同样由桥稳定开轮。
func TestIdleTurnEndToEndExplicitFollowupOpensTurn(t *testing.T) {
	e := newIdleTurnE2E(t)
	b := newIdleTurnBridge()
	const body = "579 验收②：followup 直投也必须被消费"
	rec, err := e.ctrl.TryEnqueueFollowup(control.InboxRequest{
		Intent: sessioninbox.IntentFollowup, Display: body, Raw: body, Submit: body,
		Source: "collab:c-sender", Idempotency: "collab:fu-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Disposition != sessioninbox.DispositionQueuedFollowup {
		t.Fatalf("idle followup must queue, disposition = %q", rec.Disposition)
	}

	base := time.Now()
	e.sweepBridge(b, "c-target", base)
	e.sweepBridge(b, "c-target", base.Add(sessionCollabIdleTurnDelay+4*time.Second))

	got := e.waitForInputs(t, 1)
	if len(got) != 1 || !strings.Contains(got[0], body) {
		t.Fatalf("bridge must open exactly one turn for the explicit followup, got %v", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := e.snapshotInputs(); len(got) != 1 {
		t.Fatalf("no duplicate turns allowed, inputs = %v", got)
	}
}

// 任务709 验收主场景（708 实测复现）：长期闲置（stale）会话——上一轮 turn 被
// 进程结束打断（更新重启/强杀），重开时恢复把在飞孤儿改写为 Uncertain 并
// 「自动暂停」收件箱（非用户意志）→ 新协作投递落箱 queued_followup → 修复前：
// 桥把 paused 一律当用户持有，永久静默，等人工唤醒；修复后：桥在开轮前原子
// 解除自动暂停并消费新投递；Uncertain 孤儿留在待检视架子上不被打扰。
func TestIdleTurnEndToEndAutoPausedStaleSessionOpensTurn(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")

	// 第一段生命周期：一条 turn 在飞时进程结束——落一条 StateRunning 在飞孤儿。
	orphanStore, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := orphanStore.Enqueue(sessioninbox.EnqueueRequest{
		Intent:      sessioninbox.IntentFollowup,
		Envelope:    sessioninbox.PromptEnvelope{DisplayText: "709 在飞孤儿：被进程结束打断的上一轮", SubmitText: "orphan"},
		Idempotency: "collab:orphan-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orphanStore.ClaimItem(orphan.ItemID); err != nil {
		t.Fatal(err)
	}
	orphanStore.Close()

	// 第二段生命周期：同一路径重开（= stale 会话被投递时现场站起的 runtime）。
	e := newIdleTurnE2EAt(t, dir)
	b := newIdleTurnBridge()
	snap := e.ctrl.InboxSnapshot()
	if !snap.Paused || snap.UserPaused || !snap.Recovered {
		t.Fatalf("stale reopen must carry an AUTOMATIC recovery pause (Paused && !UserPaused && Recovered), got %+v", snap)
	}

	const fresh = "709 新投递：stale 会话必须分钟级被消费"
	steered, itemID, mailID := e.deliverSteer(t, "c-sender", "c-target", fresh)
	if steered {
		t.Fatal("paused target cannot take a steer — it must degrade")
	}
	if itemID == "" {
		t.Fatal("degraded steer must keep a durable inbox item")
	}
	// 708 现场钉住：落库结局就是 queued_followup（修复前的静默绿灯）。
	if r, ok := e.mail.DeliveryReceipt(mailID); !ok || r.Outcome != sessioncollab.ReceiptQueuedFollowup {
		t.Fatalf("stale-session delivery receipt = %+v, want queued_followup", r)
	}

	// 桥两拍内唤醒 + 消费新投递。
	base := time.Now()
	e.sweepBridge(b, "c-target", base)
	e.sweepBridge(b, "c-target", base.Add(sessionCollabIdleTurnDelay+4*time.Second))

	got := e.waitForInputs(t, 1)
	if len(got) != 1 || !strings.Contains(got[0], fresh) {
		t.Fatalf("the fresh delivery must be consumed after the wake, got %v", got)
	}
	if snap := e.ctrl.InboxSnapshot(); snap.Paused {
		t.Fatalf("wake must clear the automatic pause, snapshot = %+v", snap)
	}
	// Uncertain 孤儿留在架子上待人工检视——唤醒不得把它卷进任何轮。
	snap = e.ctrl.InboxSnapshot()
	if len(snap.Items) != 1 || snap.Items[0].ID != orphan.ItemID || snap.Items[0].State != sessioninbox.StateUncertain {
		t.Fatalf("recovered orphan must stay shelved for review, snapshot = %+v", snap)
	}
	e.sweepBridge(b, "c-target", base.Add(3*sessionCollabIdleTurnDelay))
	time.Sleep(50 * time.Millisecond)
	if got := e.snapshotInputs(); len(got) != 1 {
		t.Fatalf("exactly one turn expected (shelved orphan untouched), got %v", got)
	}
}

// 任务709 边界回归：用户显式暂停（queue panel / SetInboxPaused）依旧绝对
// 静默——即便有新投递与排队工作，桥也不越。暂停是用户意志。
func TestIdleTurnEndToEndUserPausedInboxStaysUntouched(t *testing.T) {
	e := newIdleTurnE2E(t)
	b := newIdleTurnBridge()
	const leftover = "709 用户持有：暂停中的遗留项"
	if _, err := e.ctrl.EnqueueInbox(control.InboxRequest{
		Intent: sessioninbox.IntentFollowup, Display: leftover, Raw: leftover, Submit: leftover,
		Source: "collab:c-sender", Idempotency: "collab:userpause-1",
	}); err != nil {
		t.Fatal(err)
	}
	// 用户显式暂停（走人入口，留 UserPaused 来源）。
	if err := e.ctrl.SetInboxPaused(true); err != nil {
		t.Fatal(err)
	}
	if snap := e.ctrl.InboxSnapshot(); !snap.Paused || !snap.UserPaused {
		t.Fatalf("explicit pause must carry user provenance: %+v", snap)
	}
	// 系统侧唤醒原语对用户暂停必须是 no-op。
	if resumed, err := e.ctrl.ResumeInboxAutoPause(); err != nil || resumed {
		t.Fatalf("ResumeInboxAutoPause must refuse a user pause, got %v, %v", resumed, err)
	}

	const fresh = "709 用户暂停期间到达的投递"
	steered, _, mailID := e.deliverSteer(t, "c-sender", "c-target", fresh)
	if steered {
		t.Fatal("paused target cannot take a steer")
	}
	if r, ok := e.mail.DeliveryReceipt(mailID); !ok || r.Outcome != sessioncollab.ReceiptQueuedFollowup {
		t.Fatalf("delivery receipt = %+v, want queued_followup", r)
	}

	base := time.Now()
	for step := 0; step < 4; step++ {
		e.sweepBridge(b, "c-target", base.Add(time.Duration(step)*(sessionCollabIdleTurnDelay+4*time.Second)))
	}
	time.Sleep(100 * time.Millisecond)
	if got := e.snapshotInputs(); len(got) != 0 {
		t.Fatalf("a user-held queue must never be auto-opened, inputs = %v", got)
	}
	if snap := e.ctrl.InboxSnapshot(); !snap.Paused || !snap.UserPaused || len(snap.Items) != 2 {
		t.Fatalf("user pause must hold with both items intact: %+v", snap)
	}
}
