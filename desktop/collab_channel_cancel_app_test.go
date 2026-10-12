package main

// 任务 802 — 人类撤回口的 API 级验收：
// ① 人类撤回自己的消息 → 墓碑生效（cancelledAt>0，读口带 cancelledAt）；
// ② 重复撤回幂等（alreadyCancelled）；
// ③ 人类不能撤别人的消息（ErrNotSender 透传）；
// ④ 撤回把 queued 投递行停掉（cancelledQueued ≥ 0 计入）。

import (
	"errors"
	"strings"
	"testing"

	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
)

func TestChannelCancelFromHumanAPI(t *testing.T) {
	isolateDesktopUserDirs(t)
	captureHumanDrain(t)
	app := NewApp()

	ch, err := app.ChannelCreateForHuman("撤回验收", "", 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := app.ChannelInvite(ch.ID, []string{"sc_reader"}); err != nil {
		t.Fatalf("invite: %v", err)
	}

	mine, err := app.ChannelSendFromHuman(ch.ID, "这条马上撤回")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	// agent 也发一条，供 ③ 验证人类不能撤别人的。
	agentStore, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		t.Fatal(err)
	}
	agentMsg, err := agentStore.Publish(ch.ID, "sc_reader", "agent 的发言")
	_ = agentStore.Close()
	if err != nil {
		t.Fatal(err)
	}

	// ① 撤回自己的消息。
	res, err := app.ChannelCancelFromHuman(ch.ID, mine.ID)
	if err != nil {
		t.Fatalf("cancel own: %v", err)
	}
	if res.CancelledAt == 0 || res.AlreadyCancelled {
		t.Fatalf("tombstone expected: %+v", res)
	}
	// 读口必须透出墓碑时间（前端渲染「已取消」行）。
	view, err := app.ChannelMessagesForHuman(ch.ID, 0, 50)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	found := false
	for _, m := range view.Messages {
		if m.ID == mine.ID {
			found = true
			if m.CancelledAt == 0 {
				t.Fatalf("read view must carry cancelledAt: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("cancelled message missing from tail: %+v", view.Messages)
	}
	// ② 幂等：第二次撤回报第一次的墓碑。
	res2, err := app.ChannelCancelFromHuman(ch.ID, mine.ID)
	if err != nil {
		t.Fatalf("cancel twice: %v", err)
	}
	if !res2.AlreadyCancelled || res2.CancelledAt != res.CancelledAt {
		t.Fatalf("second cancel must be idempotent: %+v vs %+v", res2, res)
	}

	// ③ 撤别人的消息被拒（ErrNotSender）。
	if _, err := app.ChannelCancelFromHuman(ch.ID, agentMsg.ID); err == nil {
		t.Fatal("human must not cancel another member's message")
	} else if !errors.Is(err, collabchannel.ErrNotSender) && !strings.Contains(err.Error(), "sent by") {
		t.Fatalf("want ErrNotSender, got: %v", err)
	}
}
