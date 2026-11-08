package control

// tl1357 用户消息投递丢失三形态 · 复现钉（形态②，control 层）。
//
// 形态②（cancel 不丢引导）：turn 被 Cancel 时，已受理（steer_accepted）未
// 注入的持久 steer 必须回到重放队列（queued follow-up）并被收尾 kick 派发
// 为下一轮输入，而不是随 TurnDone 被 ack 掉。修前（红）该行被直接
// AckDequeue（消费），消息蒸发；修后（绿）重放。
// 复现基建（tl1357Harness）见 inbox_tl1357_delivery_test.go。

import (
	"strings"
	"testing"

	"reasonix/internal/sessioninbox"
)

func TestCancelRequeuesAcceptedSteer(t *testing.T) {
	h := newTL1357Harness(t)
	h.startBlockingTurn(t)

	const guidance = "tl1357形态②：中断后这条指令必须还在"
	rec, err := h.c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: guidance})
	if err != nil {
		t.Fatal(err)
	}
	st, err := h.c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	// 复制 trySteerInboxItem 受理成功时的持久足迹：steer_accepted + active 归属。
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	h.c.inbox.mu.Lock()
	h.c.inbox.trackActive(rec.ItemID)
	h.c.inbox.mu.Unlock()

	// 用户停止（tl1357 修复B：cancel 在发信号前武装 requeue）。
	h.c.Cancel()
	h.releaseTurn()
	h.waitTurnDone(t)

	// 回到重放队列：queued follow-up，可派发、未停用。
	snap := h.c.InboxSnapshot()
	if len(snap.Items) != 1 || snap.Items[0].ID != rec.ItemID {
		t.Fatalf("cancel 后 steer 必须仍在队列（不得被 ack 掉），items = %+v", snap.Items)
	}
	item := snap.Items[0]
	if item.State != sessioninbox.StateQueued {
		t.Fatalf("重放队列状态 = %s, want queued（uncertain+暂停 = 退回人工检视，不合本钉）", item.State)
	}
	if item.Intent != sessioninbox.IntentFollowup {
		t.Fatalf("重放意图 = %s, want followup", item.Intent)
	}
	if got := itemBodyAvailable(t, h.c, rec.ItemID); !strings.Contains(got, guidance) {
		t.Fatalf("持久层正文回读 = %q, want it to contain %q", got, guidance)
	}

	// 收尾 kick 自动派发：消息作为下一轮输入进入会话，不依赖 desktop 队列存活。
	h.waitDispatch(t, guidance)
	h.waitTurnDone(t)
	if items := h.c.InboxSnapshot().Items; len(items) != 0 {
		t.Fatalf("重放完成后队列应清空，items = %+v", items)
	}
}
