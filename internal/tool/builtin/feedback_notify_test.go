package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 任务 1016 双写采集层：submit_feedback 落盘 md（权威源）后调用通知钩子。
// 四条契约：①无钩子（CLI/测试默认）= 行为与 1016 之前逐字节一致（返回体无
// notified 键）；②钩子收到的 note 携带 md 文件名（幂等键）+ ≤3 行摘要 + 标题；
// ③钩子失败不影响提交结果，返回体如实标注 notified=false + notifyError；
// ④md 在钩子失败时依然完整落盘（权威源语义）。
func TestSubmitFeedbackDualWriteNotify(t *testing.T) {
	dir := t.TempDir()
	SetFeedbackHome(dir)
	t.Cleanup(func() { SetFeedbackHome("") })

	var got []FeedbackNote
	SetFeedbackNotify(func(n FeedbackNote) error {
		got = append(got, n)
		return nil
	})
	t.Cleanup(func() { SetFeedbackNotify(nil) })

	tool := submitFeedback{}
	args, _ := json.Marshal(map[string]any{
		"kind": "bug",
		"text": "Panel does not open on Ctrl+P.\nSecond line of detail.\nThird line.\nFourth line beyond the cap.",
		"title": "快捷键冲突",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(got))
	}
	note := got[0]
	if !strings.HasPrefix(note.FileName, "feedback-") || !strings.HasSuffix(note.FileName, ".md") {
		t.Fatalf("note file name must be the md base name: %q", note.FileName)
	}
	if _, statErr := os.Stat(filepath.Join(dir, note.FileName)); statErr != nil {
		t.Fatalf("referenced md must exist on disk: %v", statErr)
	}
	if note.Kind != "bug" || note.Title != "快捷键冲突" {
		t.Fatalf("note kind/title: %+v", note)
	}
	if lines := strings.Split(note.Summary, "\n"); len(lines) != 3 {
		t.Fatalf("summary must fold to 3 lines, got %d: %q", len(lines), note.Summary)
	}
	var payload map[string]any
	if jerr := json.Unmarshal([]byte(result), &payload); jerr != nil {
		t.Fatalf("result JSON: %v (%s)", jerr, result)
	}
	if payload["notified"] != true {
		t.Fatalf("result must report notified=true: %s", result)
	}
}

// 钩子失败：md 仍完整落盘（权威源），返回体标注失败原因，Execute 不报错。
func TestSubmitFeedbackNotifyFailureKeepsMD(t *testing.T) {
	dir := t.TempDir()
	SetFeedbackHome(dir)
	t.Cleanup(func() { SetFeedbackHome("") })

	SetFeedbackNotify(func(FeedbackNote) error { return errors.New("mailbox busy") })
	t.Cleanup(func() { SetFeedbackNotify(nil) })

	tool := submitFeedback{}
	args, _ := json.Marshal(map[string]any{"kind": "idea", "text": "Add a dark theme."})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute must not fail on notify error: %v", err)
	}
	entries, lerr := ListFeedbackEntries(0)
	if lerr != nil || len(entries) != 1 || entries[0].Text != "Add a dark theme." {
		t.Fatalf("md must survive a failed notification: entries=%+v err=%v", entries, lerr)
	}
	var payload map[string]any
	if jerr := json.Unmarshal([]byte(result), &payload); jerr != nil {
		t.Fatalf("result JSON: %v", jerr)
	}
	if payload["notified"] != false {
		t.Fatalf("result must report notified=false: %s", result)
	}
	if msg, _ := payload["notifyError"].(string); !strings.Contains(msg, "mailbox busy") {
		t.Fatalf("notifyError must carry the cause: %s", result)
	}
}

// 无钩子时返回体保持旧形状（既有消费者零回归：path/kind 恒在，notified 键不出现）。
func TestSubmitFeedbackNoHookKeepsLegacyShape(t *testing.T) {
	dir := t.TempDir()
	SetFeedbackHome(dir)
	t.Cleanup(func() { SetFeedbackHome("") })
	SetFeedbackNotify(nil)

	tool := submitFeedback{}
	args, _ := json.Marshal(map[string]any{"kind": "other", "text": "note"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if jerr := json.Unmarshal([]byte(result), &payload); jerr != nil {
		t.Fatalf("result JSON: %v", jerr)
	}
	if _, present := payload["notified"]; present {
		t.Fatalf("without a hook the result must not carry notified: %s", result)
	}
	if payload["path"] == nil || payload["kind"] != "other" {
		t.Fatalf("legacy shape broken: %s", result)
	}
}
