package collabinbox

import (
	"context"
	"testing"

	"reasonix/internal/sessioncollab"
)

// 任务461 P8 ②：系统类邮件（回执/平台状态通知）自动已读——不计入未读、
// 不顶 badge；真实对话邮件（mention）照常计未读。全部真断言。

func TestSystemMailAutoReadNotCountedUnread(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_peer", To: "sc_me", Body: "已读回执：你的消息 msg_1 已进入本会话上下文", Kind: "system",
	})
	// 无戳邮件走 mention 桶（fixtureStore 的 resolver 不认识 sc_peer）。
	deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_peer", To: "sc_me", Body: "正经的派活消息",
	})

	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 2 {
		t.Fatalf("want 2 entries, got %d", snap.Total)
	}
	unread, err := store.List(ctx, Query{Unread: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if unread.Total != 1 {
		t.Fatalf("unread total = %d, want 1 (system mail must not count)", unread.Total)
	}
	if unread.Entries[0].Bucket != BucketMention {
		t.Fatalf("the only unread entry should be the mention mail, got %s", unread.Entries[0].Bucket)
	}
	for _, e := range snap.Entries {
		if e.Bucket == BucketSystem && !e.Read {
			t.Fatalf("system entry %s must be auto-read", e.ID)
		}
	}
}
