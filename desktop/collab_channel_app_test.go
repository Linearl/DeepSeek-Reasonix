package main

// 任务 803 — 群聊 channel 人类化后端（Phase A）的 Wails 面验收测试。
// API 级场景：人类建群（自己=群主）→ 邀请 agent 会话 → 人类发言（同链路）→
// fan-out 对 human 跳过邮箱投递只落库 → UI 可见即已读 → 移除成员 → 群主规则。
// 与 collab_view_app_test.go 同一隔离模式（isolateDesktopUserDirs + 裸 App）。

import (
	"context"
	"testing"

	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// channelHumanSpawn 的同步替身：捕获 job 并立即执行，让测试确定性地观察
// fan-out 结果（生产是 go job() 后台错峰）。
func captureHumanDrain(t *testing.T) {
	t.Helper()
	restore := channelHumanSpawn
	t.Cleanup(func() { channelHumanSpawn = restore })
	channelHumanSpawn = func(job func()) { job() }
}

func TestChannelHumanAPIEndToEnd(t *testing.T) {
	isolateDesktopUserDirs(t)
	captureHumanDrain(t)
	app := NewApp()

	// ① 人类建群：自己=群主+成员。
	ch, err := app.ChannelCreateForHuman("发布协调", "微信式建群", 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ch.OwnerType != collabchannel.MemberTypeHuman || ch.OwnerID != collabchannel.HumanLocal {
		t.Fatalf("human must own the group: %+v", ch)
	}
	if len(ch.MemberDetails) != 1 || ch.MemberDetails[0].Contact != collabchannel.HumanLocal ||
		ch.MemberDetails[0].Type != collabchannel.MemberTypeHuman {
		t.Fatalf("founding member wrong: %+v", ch.MemberDetails)
	}

	// ② 邀请 agent 会话（群主权限）。
	ch, err = app.ChannelInvite(ch.ID, []string{"sc_dev6", "sc_merge"})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if len(ch.Members) != 3 {
		t.Fatalf("invited members: %+v", ch.Members)
	}

	// ③ 人类发言：同 Publish 链路；后台 drain 同步跑完。
	msg, err := app.ChannelSendFromHuman(ch.ID, "大家好，我是人类")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if msg.Sender != collabchannel.HumanLocal || msg.Body == "" {
		t.Fatalf("published message: %+v", msg)
	}

	// ④ agent 成员各收到一封 followup；human 收件箱必须为空（④跳过投递只落库）。
	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	for _, member := range []string{"sc_dev6", "sc_merge"} {
		inbox, err := mail.Inbox(member)
		if err != nil || len(inbox) != 1 || inbox[0].From != collabchannel.HumanLocal {
			t.Fatalf("%s inbox: %v %+v", member, err, inbox)
		}
		if inbox[0].Delivery != string(sessioncollab.DeliveryFollowup) {
			t.Fatalf("fan-out must stay followup: %+v", inbox[0])
		}
	}
	if humanInbox, err := mail.Inbox(collabchannel.HumanLocal); err != nil || len(humanInbox) != 0 {
		t.Fatalf("human must never be mailed: %v %+v", err, humanInbox)
	}

	// ⑤ session 发言后人类读消息：UI 可见即已读（human 行 delivered+read）。
	if _, err := app.ChannelSendFromHuman(ch.ID, "占位"); err != nil { // 触发 drain 语义一致性（human 发言路径）
		t.Fatal(err)
	}
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ch.ID, "sc_dev6", "agent 汇报进度"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	// 生产 drain 由 publish 侧 kick；此处直接跑一遍（等价 channel_send 的后台 job）。
	drain, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drain.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = drain.Close()

	view, err := app.ChannelMessagesForHuman(ch.ID, 0, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(view.Messages) != 3 {
		t.Fatalf("message tail: %+v", view.Messages)
	}
	// per-recipient 已读：human 对看过的行已读，agent 行不越权。
	store2, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	rows, err := store2.FanoutStates(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	humanReadRows := 0
	for _, r := range rows {
		if r.Member == collabchannel.HumanLocal {
			if r.State != "delivered" || r.ReadAt == 0 {
				t.Fatalf("visible human row must be delivered+read: %+v", r)
			}
			humanReadRows++
		}
		if r.Member == "sc_merge" && r.ReadAt != 0 {
			t.Fatalf("human reading must not touch another member's rows: %+v", r)
		}
	}
	if humanReadRows != 1 { // 只有一条 agent 发言 fan 给 human
		t.Fatalf("human read rows = %d, want 1", humanReadRows)
	}

	// ⑥ 移除成员（群主权限）。
	ch, err = app.ChannelRemoveMember(ch.ID, "sc_merge")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(ch.Members) != 2 {
		t.Fatalf("after remove: %+v", ch.Members)
	}

	// ⑦ 群主退群拒绝（最后一个群主退出拒绝）。
	if err := app.ChannelLeaveHuman(ch.ID); err == nil {
		t.Fatal("owner leave must be refused through the wails face")
	}

	// ⑧ 人类不是群主时，管理操作拒绝；退群可以。
	otherStore, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		t.Fatal(err)
	}
	defer otherStore.Close()
	scOwned, err := otherStore.CreateChannelWithOwner("sc群", "", 0, collabchannel.MemberTypeSession, "sc_owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ChannelInvite(scOwned.ID, []string{"sc_x"}); err == nil {
		t.Fatal("human must not invite into a channel it does not own")
	}
	if _, err := app.ChannelRemoveMember(scOwned.ID, "sc_owner"); err == nil {
		t.Fatal("human must not remove members from a channel it does not own")
	}
	if err := otherStore.JoinHuman(scOwned.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.ChannelLeaveHuman(scOwned.ID); err != nil {
		t.Fatalf("human (non-owner) leave must work: %v", err)
	}
}
