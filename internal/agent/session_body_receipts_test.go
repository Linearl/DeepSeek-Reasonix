package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/sessioncollab"
)

// 任务866: plain user turns are the application receipt for follow-up rows —
// the twin face the steer receipts deliberately do not carry. A consumed
// follow-up (e.g. a degraded collab mail) persists its body as a plain user
// message, so the residue matcher can compare row bodies against this set.
func TestAppliedTurnBodyTexts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "plain follow-up body"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: "<current-time>2026-10-13T14:17:51+08:00 Mon — host clock when this turn started</current-time>\n\nwrapped in transient blocks"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: midTurnSteerMessage("steer body stays out")})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "ok"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	set := AppliedTurnBodyTexts(path)
	if _, ok := set["plain follow-up body"]; !ok {
		t.Fatalf("body set = %v, want the plain user turn", set)
	}
	if _, ok := set["wrapped in transient blocks"]; !ok {
		t.Fatalf("body set = %v, want the transient framing stripped", set)
	}
	if _, ok := set["steer body stays out"]; ok {
		t.Fatalf("body set = %v, steer messages belong to the steer-receipt face", set)
	}
}

// Missing or empty inputs report no receipts: no receipt, no auto-settlement.
func TestAppliedTurnBodyTextsEmptyInputs(t *testing.T) {
	if set := AppliedTurnBodyTexts(""); set != nil {
		t.Fatalf("empty path = %v, want nil", set)
	}
	if set := AppliedTurnBodyTexts(filepath.Join(t.TempDir(), "missing.jsonl")); set != nil {
		t.Fatalf("missing transcript = %v, want nil", set)
	}
}

// CollabSentTimeText renders the current-time shape; zero timestamps render
// empty so malformed records keep their shape.
func TestCollabSentTimeText(t *testing.T) {
	if got := CollabSentTimeText(0); got != "" {
		t.Fatalf("zero timestamp = %q, want empty", got)
	}
	const at = int64(1791872271776)
	want := time.UnixMilli(at).Format("2006-01-02T15:04:00Z07:00 Mon")
	if got := CollabSentTimeText(at); got != want {
		t.Fatalf("sent time = %q, want the current-time shape %q", got, want)
	}
}

// 任务866/1553: the drain rendering carries the send timestamp on its head
// line, mirroring the desktop pump's injection text.
func TestDrainInboxRenderTextCarriesSentTime(t *testing.T) {
	const at = int64(1791872271776)
	msg := sessioncollab.MailMessage{
		ID:   "msg_test",
		From: "sc_sender",
		To:   "sc_target",
		Body: "正文",
		At:   at,
	}
	text := drainInboxRenderText(msg, 0)
	if !strings.Contains(text, "发送于 "+time.UnixMilli(at).Format("2006-01-02T15:04:00Z07:00 Mon")) {
		t.Fatalf("rendered text = %q, want the send timestamp on the head line", text)
	}

	msg.At = 0
	if text := drainInboxRenderText(msg, 0); strings.Contains(text, "发送于") {
		t.Fatalf("zero-timestamp text = %q, want no fabricated clock", text)
	}
}
