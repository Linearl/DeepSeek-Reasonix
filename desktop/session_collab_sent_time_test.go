package main

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

// 任务866/1553: the pump's injection text carries the send timestamp on its
// head line (same shape as the current-time block, same as the drain path's
// rendering), so the recipient can tell a fresh message from one that waited
// overnight. Storage metadata, the injected text and the UI card carried no
// time at all before this — three layers, no clock.
func TestSessionCollabDeliveryTextCarriesSentTime(t *testing.T) {
	const at = int64(1791872271776)
	msg := sessioncollab.MailMessage{
		ID:   "msg_test",
		From: "sc_sender",
		To:   "sc_target",
		Body: "正文",
		At:   at,
	}
	text := sessionCollabDeliveryText(msg, 0)
	if !strings.Contains(text, "发送于 "+time.UnixMilli(at).Format("2006-01-02T15:04:00Z07:00 Mon")) {
		t.Fatalf("delivery text = %q, want the send timestamp on the head line", text)
	}

	msg.At = 0
	if text := sessionCollabDeliveryText(msg, 0); strings.Contains(text, "发送于") {
		t.Fatalf("zero-timestamp text = %q, want no fabricated clock", text)
	}
}
