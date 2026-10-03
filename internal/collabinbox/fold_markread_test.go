package collabinbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

// 任务461 P8 ③ 验收：①完全同内容的 9 条存量重复显示为 1 条（折叠+计数）；
// ②一键批量已读（MarkRead 结算整簇，seen 游标真实推进）；③旧于折叠窗的
// 同内容不合并。种子直接写收件箱文件（文件层探针，绕过投递层 dedup——
// 模拟修复前已落盘的污染数据），严禁调用 query_collab_mail 工具本体。

func seedInboxMail(t *testing.T, dir, contact string, msg sessioncollab.MailMessage) {
	t.Helper()
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, contact+".inbox.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestNineDuplicatesFoldToOneAndBatchMarkRead(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, nil)
	mail := sessioncollab.NewMailStore(dir)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	// 事故形态：同一内容 9 次、每次新 id（修复前每次 Deliver 都落新行）。
	var dupIDs []string
	for i := 0; i < 9; i++ {
		id := "msg_dup" + string(rune('a'+i))
		dupIDs = append(dupIDs, id)
		seedInboxMail(t, dir, "sc_b", sessioncollab.MailMessage{
			ID: id, ThreadID: id, From: "sc_heartbeat", To: "sc_b",
			Body: "心跳确认：任务仍在运行", At: now - int64(9-i)*60000,
		})
	}
	// 一封真实对话邮件（必须不受折叠/批量已读误伤）。
	deliver(t, mail, sessioncollab.MailMessage{From: "sc_peer", To: "sc_b", Body: "正经派活：修一下登录页"})

	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 2 {
		t.Fatalf("want 2 entries after fold (duplicates+real), got %d: %+v", snap.Total, snap.Entries)
	}
	var folded *Entry
	for i := range snap.Entries {
		if snap.Entries[i].DuplicateCount > 0 {
			folded = &snap.Entries[i]
		}
	}
	if folded == nil {
		t.Fatal("folded entry missing")
	}
	if folded.DuplicateCount != 9 {
		t.Fatalf("duplicateCount = %d, want 9", folded.DuplicateCount)
	}
	// 折叠簇内任一副本未消费 → 整簇仍算未读。
	unread, err := store.List(ctx, Query{Unread: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if unread.Total != 2 {
		t.Fatalf("unread before mark-read = %d, want 2 (folded cluster + real mail)", unread.Total)
	}

	// 验收②：一键批量已读——只给折叠条目的 primary id，整簇 9 条全部结算。
	if _, err := store.MarkRead(ctx, []string{folded.ID}); err != nil {
		t.Fatal(err)
	}
	after, err := store.List(ctx, Query{Unread: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != 1 {
		t.Fatalf("unread after mark-read = %d, want 1 (only the real mail)", after.Total)
	}
	if after.Entries[0].ID == folded.ID {
		t.Fatal("the folded cluster must read as consumed")
	}
	// 传输层 seen 游标真实推进：9 条副本全部 seen，收件箱未读只剩真消息 1 条。
	if got, _ := mail.InboxStatus("sc_b"); got != 1 {
		t.Fatalf("transport unread = %d, want 1 (all 9 duplicates settled at the cursor)", got)
	}
}

func TestFoldWindowKeepsOldCopiesSeparate(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, nil)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	for i, age := range []int64{0, 26 * 3600 * 1000} { // 相差 26h：超出折叠窗
		seedInboxMail(t, dir, "sc_b", sessioncollab.MailMessage{
			ID: string(rune('a'+i)) + "_id", ThreadID: string(rune('a'+i)) + "_id",
			From: "sc_a", To: "sc_b", Body: "weekly report", At: now - age,
		})
	}
	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 2 {
		t.Fatalf("copies beyond the fold window must stay separate, got %d entries", snap.Total)
	}
	for _, e := range snap.Entries {
		if e.DuplicateCount > 1 {
			t.Fatalf("no cluster expected across the window boundary, got count %d", e.DuplicateCount)
		}
	}
}
