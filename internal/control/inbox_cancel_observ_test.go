package control

// tl1747：投递取消打点接线测试。
//
// 验收锚点：cancel 路径每条被丢弃条目出一条结构化记录，字段与 desktop
// delivery-decision 契约同款（delivery_decision/decision_reason/session/topic）
// 外加 cancel_source（取消发起方）与 msg_id。session 携带条目的 CollabMailTo
// （本地条目为空）。全部 t.Fatal 断言，无静默 SKIP。

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// captureCancelLog swaps slog's default for a buffer and restores it.
func captureCancelLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// ① 取消 collab 邮件条目：cancelled 记录带 CollabMailTo + cancel_source。
// 注意（既有语义）：CancelWithInboxItems 的 source 参数既是 cancel_source
// 也是条目归属过滤——collab 条目 item.Source="collab:<contact>"，须以同值
// 传参才会被丢弃（TestCancelWithInboxItemsDiscardsOnlyOwnedPendingItems 钉过）。
func TestCancelWithInboxItemsEmitsCancelDecision(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	_ = os.WriteFile(session, []byte("{}\n"), 0o644)
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	buf := captureCancelLog(t)

	collabItem, err := c.EnqueueInbox(InboxRequest{
		Submit:       "collab mail pending cancel",
		Source:       "collab:sc_sender",
		CollabMsgID:  "msg_c1",
		CollabMailTo: "sc_recipient",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CancelWithInboxItems([]string{collabItem.ItemID}, "collab:sc_sender"); err != nil {
		t.Fatal(err)
	}

	logText := buf.String()
	for _, want := range []string{
		"delivery_decision=cancelled",
		"decision_reason=inbox_cancel",
		`session=sc_recipient`,
		"msg_id=" + collabItem.ItemID,
		"cancel_source=collab:sc_sender",
		"source=collab:sc_sender",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("cancel record must carry %s, got:\n%s", want, logText)
		}
	}
}

// ② 本地条目取消：session 为空如实记录（本地条目无 CollabMailTo）。
func TestCancelLocalItemCarriesEmptySession(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	_ = os.WriteFile(session, []byte("{}\n"), 0o644)
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	buf := captureCancelLog(t)

	localItem, err := c.EnqueueInbox(InboxRequest{Submit: "local only", Source: "desktop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CancelWithInboxItems([]string{localItem.ItemID}, "desktop"); err != nil {
		t.Fatal(err)
	}

	logText := buf.String()
	if !strings.Contains(logText, "delivery_decision=cancelled") {
		t.Fatalf("local cancel must still emit, got:\n%s", logText)
	}
	if !strings.Contains(logText, `session="" topic="" msg_id=`+localItem.ItemID) {
		t.Fatalf("local item must carry empty session, got:\n%s", logText)
	}
}

// ③ helper 字段契约直测（edit_resend 等来源走同一 helper，cancel_source 各自命名）。
func TestLogInboxCancelDecisionSourceDistribution(t *testing.T) {
	buf := captureCancelLog(t)
	logInboxCancelDecision("edit_resend", nil) // 空批次：零记录
	if buf.String() != "" {
		t.Fatalf("empty batch must emit nothing, got:\n%s", buf.String())
	}
	buf.Reset()
	logInboxCancelDecision("edit_resend", mustMeta(t))
	logText := buf.String()
	if !strings.Contains(logText, "cancel_source=edit_resend") {
		t.Fatalf("edit_resend source must be named, got:\n%s", logText)
	}
}

// mustMeta 造一条最小元数据（helper 直测不需落盘）。
func mustMeta(t *testing.T) []sessioninbox.InboxItemMeta {
	t.Helper()
	return []sessioninbox.InboxItemMeta{{ID: "item_e1", CollabMailTo: "sc_r", Source: "collab:sc_s"}}
}
