package sessioncollab

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 任务548 P0-1（源头拒发）：hop>0 却无可解析父线程的组合必须在写盘前被拒。
// 历史行为是静默把空 threadId 盖成自身 id 落盘，随后被消费泵的溯源门丢弃
// （"hop=N claimed but threadId does not name a parent"），且拒收仍结算 seen
// ——2026-10-06 事故里四封派单因此「SEEN 但从未送达」。修复后：发送方在
// 自己的调用返回值里立刻看到错误，磁盘上一个字节都不落。
func TestDeliverRefusesHopWithoutParentThread(t *testing.T) {
	mail := NewMailStore(t.TempDir())

	// 事故形态一：hop=2 且不带 thread_id（本次事故四封派单的形态）。
	_, err := mail.Deliver(context.Background(), MailMessage{From: "sc_from", To: "sc_target", Body: "派单", Hop: 2})
	if !errors.Is(err, ErrHopWithoutParentThread) {
		t.Fatalf("hop>0 without threadId must be refused with ErrHopWithoutParentThread, got %v", err)
	}
	if !strings.Contains(err.Error(), "hop=2") || !strings.Contains(err.Error(), "thread_id") {
		t.Fatalf("the refusal must name the hop and the next step: %v", err)
	}

	// 事故形态二：显式自指（threadId == 自身 id，盖章后 isReply=false 同样过不了溯源门）。
	_, err = mail.Deliver(context.Background(), MailMessage{ID: "msg_self", From: "sc_from", To: "sc_target", Body: "派单", Hop: 2, ThreadID: "msg_self"})
	if !errors.Is(err, ErrHopWithoutParentThread) {
		t.Fatalf("hop>0 with a self-referencing threadId must be refused too, got %v", err)
	}

	// 拒发不得落盘：目标收件箱必须保持为空（不进 seen 的前提是先不进 inbox）。
	box, err := mail.Inbox("sc_target")
	if err != nil {
		t.Fatal(err)
	}
	if len(box) != 0 {
		t.Fatalf("a refused send must write nothing: target inbox holds %d messages", len(box))
	}

	// 合法路径零回归：hop=0 新链照常接受，空 threadId 照常盖章为自身 id。
	first, err := mail.Deliver(context.Background(), MailMessage{From: "sc_from", To: "sc_target", Body: "chain start"})
	if err != nil {
		t.Fatalf("hop=0 new chain must be accepted: %v", err)
	}
	if first.ThreadID != first.ID {
		t.Fatalf("a new chain must be stamped with its own id, got threadId=%s id=%s", first.ThreadID, first.ID)
	}

	// 合法路径零回归：hop>0 + 非空（非自指）threadId 照常放行——Deliver 只做
	// 形态校验，链路溯源仍由消费泵的 verifyHop 权威裁定。
	if _, err := mail.Deliver(context.Background(), MailMessage{From: "sc_from", To: "sc_target", Body: "reply", Hop: 1, ThreadID: first.ID}); err != nil {
		t.Fatalf("hop>0 with a parent threadId must still be accepted: %v", err)
	}
}
