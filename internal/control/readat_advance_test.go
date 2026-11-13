package control

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// tl1563 已读推进全链（验收 1）+ 边界（验收 2）。口径 20261112 msg_04cdadbd：
// 已读 = 消息进入成员上下文 且 该成员回合运行中。成员消费群聊派活走 inbox
// 直投（fan-out → MailStore → sessioninbox → 开轮注入），链上唯一可持句柄是
// chf_ 投递编号（task 309 CollabMsgID 簿记）；本文件钉控制层开轮主链：
//
//   - turnStarted（条目成为开工上下文且回合开始运行）⇒ read_at 推进；
//   - 被拒 busy（条目回退 queued，消息未进入上下文）⇒ read_at 不推进。

// setupChannelFanout builds a channel with member sc_me, publishes one group
// message and drains the fan-out, returning the member's chf_ delivery id.
func setupChannelFanout(t *testing.T, mailDir string) string {
	t.Helper()
	cs, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if _, err := cs.CreateChannel("dev", "", 0, "sc_me"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Publish("dev", "sc_boss", "please handle the group task"); err != nil {
		t.Fatal(err)
	}
	stats, err := cs.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Delivered != 1 {
		t.Fatalf("fan-out stats: %+v", stats)
	}
	rows, err := cs.FanoutStates("dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Member != "sc_me" {
		t.Fatalf("fanout rows: %+v", rows)
	}
	return rows[0].FanoutID
}

func mustReadAtOf(t *testing.T, mailDir string) int64 {
	t.Helper()
	cs, err := collabchannel.Open(mailDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	rows, err := cs.FanoutStates("dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("fanout rows: %+v", rows)
	}
	return rows[0].ReadAt
}

func newInboxTestController(t *testing.T, dir string) (*Controller, chan event.Event) {
	t.Helper()
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	sink, done, _ := collectSink()
	c := New(Options{
		Runner:        appendingRunner{session: sess},
		Executor:      exec,
		Sink:          sink,
		SessionDir:    dir,
		SessionPath:   filepath.Join(dir, "s.jsonl"),
		WorkspaceRoot: dir,
	})
	return c, done
}

// 全链（验收 1）：群聊派活经 inbox 直投排队（条目带 chf_ 簿记），自动开轮把
// 条目注入开工上下文且回合开始运行 ⇒ 发送者 own 消息下该成员 read_at 非空
// （前端 ChannelMessageStream readBy 标注据此渲染「XX 已读」）。
func TestTrySubmitInboxItemAdvancesChannelReadAtOnTurnStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", dir)
	mailDir := config.SessionCollabMailDir()
	fanoutID := setupChannelFanout(t, mailDir)
	if got := mustReadAtOf(t, mailDir); got != 0 {
		t.Fatalf("fresh delivery must not be read, read_at=%d", got)
	}

	c, done := newInboxTestController(t, dir)
	rec, err := c.EnqueueInbox(InboxRequest{
		Display:      "[跨会话消息·群聊] group task",
		Raw:          "[跨会话消息·群聊] group task",
		Submit:       "[跨会话消息·群聊] group task",
		Source:       "collab:sc_boss",
		CollabMsgID:  fanoutID,
		CollabMailTo: "sc_me",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.TrySubmitInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Disposition != sessioninbox.DispositionStarted {
		t.Fatalf("disposition = %q, want started", got.Disposition)
	}
	waitForDone(t, done)
	if readAt := mustReadAtOf(t, mailDir); readAt == 0 {
		t.Fatal("turn start must advance fanout read_at (验收 1 全链)")
	}
}

// 边界（验收 2 前半）：仅投递未开轮——条目已排队但回合被竞争 turn 挡下
// （RejectedBusy 回退 queued），消息未进入任何开工上下文 ⇒ read_at 不推进。
func TestTrySubmitInboxItemRejectedBusyKeepsChannelReadAtUnadvanced(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", dir)
	mailDir := config.SessionCollabMailDir()
	fanoutID := setupChannelFanout(t, mailDir)

	c, done := newInboxTestController(t, dir)
	rec, err := c.EnqueueInbox(InboxRequest{
		Display:      "[跨会话消息·群聊] group task",
		Submit:       "[跨会话消息·群聊] group task",
		Source:       "collab:sc_boss",
		CollabMsgID:  fanoutID,
		CollabMailTo: "sc_me",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 竞争 turn 在 admission 前插入并挂住——被拒条目回退 queued（先例同
	// TestTrySubmitInboxItemRaces... 的 busy 形态）。
	competingStarted := make(chan struct{})
	releaseCompeting := make(chan struct{})
	c.inbox.mu.Lock()
	c.inbox.beforePreparedAdmission = func() {
		if result := c.runGuarded(func(context.Context) error {
			close(competingStarted)
			<-releaseCompeting
			return nil
		}); result != turnStarted {
			t.Errorf("competing admission = %v, want turnStarted", result)
		}
		select {
		case <-competingStarted:
		case <-time.After(time.Second):
			t.Error("competing turn did not start")
		}
	}
	c.inbox.mu.Unlock()

	receipt, err := c.TrySubmitInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Disposition != sessioninbox.DispositionRejectedBusy {
		t.Fatalf("disposition = %q, want rejected_busy", receipt.Disposition)
	}
	meta, _, err := c.ReadInboxItem(rec.ItemID)
	if err != nil || meta.State != sessioninbox.StateQueued {
		t.Fatalf("rejected item = %+v err=%v, want durable queued", meta, err)
	}
	// 边界断言（竞争 turn 仍挂住，dispatch 因此不可能重开条目）：条目被拒
	// 回退 queued，消息未进入任何开工上下文 ⇒ read_at 不推进。
	if readAt := mustReadAtOf(t, mailDir); readAt != 0 {
		t.Fatalf("rejected (never in context) must not advance read_at, got %d", readAt)
	}

	// 收尾（先例同款）：暂停队列防竞争 turn 完成后的 dispatch kick 重开条目
	// （重开即合法开轮+合法推进，与本测试断言无关但会污染收尾时序），再释放
	// 竞争 turn 并消费其 TurnDone。
	if err := c.SetInboxPaused(true); err != nil {
		t.Fatal(err)
	}
	c.inbox.mu.Lock()
	c.inbox.beforePreparedAdmission = nil
	c.inbox.mu.Unlock()
	close(releaseCompeting)
	waitForDone(t, done)
	c.autosaveWG.Wait()
	// 收尾复核：全过程中 read_at 保持未推进。
	if readAt := mustReadAtOf(t, mailDir); readAt != 0 {
		t.Fatalf("read_at advanced without a consuming turn: %d", readAt)
	}
}
