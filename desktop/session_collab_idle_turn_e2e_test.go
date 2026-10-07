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
	dir := t.TempDir()
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
// controller's live snapshot — the same flattening sweepIdleInboxTurns does.
func (e *idleTurnE2E) sweepBridge(b *idleTurnBridge, contactID string, at time.Time) {
	view := idleTurnTargetView{contactID: contactID}
	snap := e.ctrl.InboxSnapshot()
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
