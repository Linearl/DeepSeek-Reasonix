package agent

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// tl1563 已读推进（口径 20261112 msg_04cdadbd：已读 = 消息进入成员上下文 且
// 该成员回合运行中）：成员侧群聊派活经 inbox 直投消费的两个面——drain_inbox
// 的 MailStore 层与 sessioninbox 层——消费即推进 fanout.read_at。本文件钉：
// 推进命中、peek（settle=false）不推进（peek 契约=零副作用）、直投个人邮件
// 不误伤（空匹配零行）。控制层开轮主链在 internal/control 同名测试文件。

// mustChannelReadRows opens the collabchannel store and returns per-member
// fanout rows for the channel.
func mustChannelReadRows(t *testing.T, mailDir, ref string) []collabchannel.FanoutRow {
	t.Helper()
	s, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.FanoutStates(ref)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// MailStore 层：群聊 fan-out 副本被 drain_inbox 消费 ⇒ 该成员行 read_at 推进，
// 他人行不动；peek 不推进；settle 再消费推进（幂等守卫下第二条才动）。
func TestDrainInboxMailboxLayerAdvancesChannelReadAt(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(t.TempDir(), "mail")

	cs, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CreateChannel("dev", "", 0, "sc_me", "sc_peer"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Publish("dev", "sc_boss", "please do the thing"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs.Close()

	mePath := drainFixture(t, dir, "me", "Me", "sc_me")
	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: mePath,
		CurrentContactID:   "sc_me",
	}

	// 边界前半：peek（settle=false）把内容给模型看但不结算——peek 契约是
	// 零副作用（peek never consumes anything），read_at 不得推进。
	peek := execDrain(t, cfg, `{"settle":false}`)
	if peek.Took != 1 {
		t.Fatalf("peek took %d, want 1", peek.Took)
	}
	for _, r := range mustChannelReadRows(t, mailDir, "dev") {
		if r.ReadAt != 0 {
			t.Fatalf("peek must not advance read_at: %+v", r)
		}
	}

	// 消费（settle=true，等价于成员回合内的工具调用）：进入上下文且回合运行
	// 中 ⇒ sc_me 行推进；sc_peer 行不覆写。
	got := execDrain(t, cfg, `{"settle":true}`)
	if got.Took != 1 {
		t.Fatalf("drain took %d, want 1", got.Took)
	}
	for _, r := range mustChannelReadRows(t, mailDir, "dev") {
		want := r.Member == "sc_me"
		if (r.ReadAt != 0) != want {
			t.Fatalf("read rows broken after drain: %+v", r)
		}
	}
}

// 直投个人邮件（无群聊来源戳）不触发推进路径的误伤——通道过滤（Channel
// 非空）后根本不开 collabchannel 库，个人邮件编号对 fanout 表空匹配。
func TestDrainInboxDirectMailDoesNotTouchChannelRows(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(t.TempDir(), "mail")

	cs, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CreateChannel("dev", "", 0, "sc_me", "sc_peer"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Publish("dev", "sc_boss", "@sc_peer channel copy for peer only"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs.Close()

	mail := sessioncollab.NewMailStore(mailDir)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_boss",
		To:   "sc_me",
		Body: "direct session mail, no channel stamp",
	}); err != nil {
		t.Fatal(err)
	}

	mePath := drainFixture(t, dir, "me", "Me", "sc_me")
	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: mePath,
		CurrentContactID:   "sc_me",
	}
	got := execDrain(t, cfg, `{"settle":true}`)
	if got.Took != 1 {
		t.Fatalf("drain took %d, want 1 (direct mail only)", got.Took)
	}
	// sc_me 没有群聊 fan-out 行；sc_peer 的行保持未读——直投消费不越权。
	for _, r := range mustChannelReadRows(t, mailDir, "dev") {
		if r.Member == "sc_me" {
			t.Fatalf("sc_me must have no fanout row: %+v", r)
		}
		if r.ReadAt != 0 {
			t.Fatalf("direct mail drain must not advance %s: %+v", r.Member, r)
		}
	}
}

// sessioninbox 层：泵消费 MailStore 后条目带 task 309 簿记（CollabMsgID=
// chf_ 投递编号 + CollabMailTo）排进会话队列，drain_inbox 消费该层同样推进。
func TestDrainInboxSessionInboxLayerAdvancesChannelReadAt(t *testing.T) {
	// 推进 helper 在 sessioninbox 层走 config.SessionCollabMailDir()——
	// REASONIX_STATE_HOME 把它指进测试目录，保证与测试的 collabchannel 库
	// 同一物理存储。
	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	mailDir := config.SessionCollabMailDir()

	cs, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CreateChannel("dev", "", 0, "sc_me"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Publish("dev", "sc_boss", "queued group task"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	var fanoutID string
	for _, r := range func() []collabchannel.FanoutRow {
		s, err := collabchannel.Open(mailDir)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		rows, err := s.FanoutStates("dev")
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}() {
		if r.Member == "sc_me" {
			fanoutID = r.FanoutID
		}
	}
	if fanoutID == "" {
		t.Fatal("sc_me fanout row missing")
	}
	cs.Close()

	// 模拟宿主泵消费后的队列态：条目带投递编号簿记。
	sessionPath := filepath.Join(t.TempDir(), "sess.jsonl")
	writeEmpty(t, sessionPath)
	store, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(sessioninbox.EnqueueRequest{
		Envelope: sessioninbox.PromptEnvelope{
			DisplayText:  "[跨会话消息·群聊] group task",
			RawText:      "[跨会话消息·群聊] group task",
			SubmitText:   "[跨会话消息·群聊] group task",
			CollabMsgID:  fanoutID,
			CollabMailTo: "sc_me",
		},
		Source:      "collab:sc_boss",
		Idempotency: "tl1563-inbox-1",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	msgs, settled := drainInboxFromSessionInbox(sessionPath, "", true, 10)
	if len(msgs) != 1 || len(settled) != 1 {
		t.Fatalf("settle drain: msgs=%d settled=%d, want 1/1", len(msgs), len(settled))
	}
	rows := mustChannelReadRows(t, mailDir, "dev")
	if len(rows) != 1 || rows[0].Member != "sc_me" || rows[0].ReadAt == 0 {
		t.Fatalf("sessioninbox-layer consume must advance read_at: %+v", rows)
	}
}
