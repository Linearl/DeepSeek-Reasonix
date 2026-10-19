package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// 任务999: a >4 MiB instruction earns one UI-layer notice and nothing else —
// admitted, never blocked, never injected into the model (任务894 no-action
// tier). These tests pin both halves: the notice fires exactly when the
// encoded item crosses SoftNoticeItemBytes, and the enqueue outcome is
// identical to a small instruction.

func newTask999Controller(t *testing.T, sink event.Sink) *Controller {
	t.Helper()
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(Options{SessionPath: session, SessionDir: dir, Sink: sink})
}

func task999NoticeCodes(sink *recordingSink) []string {
	var codes []string
	for _, e := range sink.all() {
		if e.Kind == event.Notice && e.Code != "" {
			codes = append(codes, e.Code)
		}
	}
	return codes
}

// imageSubmit mirrors the real multi-image send: short text, the byte bulk in
// one FrozenImages data URL (that is where pasted screenshots actually live).
func task999ImageSubmit(payloadBytes int) InboxRequest {
	return InboxRequest{
		Intent:  sessioninbox.IntentFollowup,
		Display: "look at these screenshots",
		Submit:  "look at these screenshots",
		Source:  "test",
		Extra:   map[string]string{"task999-payload": strings.Repeat("p", payloadBytes)},
	}
}

func TestTask999LargeItemSoftNoticeFiresWithoutBlocking(t *testing.T) {
	sink := &recordingSink{}
	c := newTask999Controller(t, sink)
	// 6 MiB > the 4 MiB advisory threshold, well under the 16 MiB gate.
	rec, err := c.EnqueueInbox(task999ImageSubmit(6 << 20))
	if err != nil {
		t.Fatalf("large enqueue failed: %v", err)
	}
	if rec.ItemID == "" || rec.ByteSize <= sessioninbox.SoftNoticeItemBytes {
		t.Fatalf("receipt = %+v, want admitted with ByteSize > %d", rec, sessioninbox.SoftNoticeItemBytes)
	}
	found := false
	for _, e := range sink.all() {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeInboxLargeItem {
			found = true
			if e.Level != event.LevelInfo {
				t.Fatalf("soft notice level = %v, want info", e.Level)
			}
		}
	}
	if !found {
		t.Fatalf("codes = %v, want one %s notice", task999NoticeCodes(sink), event.NoticeCodeInboxLargeItem)
	}
	// Non-blocking contract: the item is queued and visible, nothing paused.
	snap := c.InboxSnapshot()
	if snap.Paused || len(snap.Items) != 1 {
		t.Fatalf("snapshot after large enqueue: paused=%v items=%d — the soft notice must not gate admission", snap.Paused, len(snap.Items))
	}
}

func TestTask999SmallItemStaysSilent(t *testing.T) {
	sink := &recordingSink{}
	c := newTask999Controller(t, sink)
	if _, err := c.EnqueueInbox(InboxRequest{
		Intent: sessioninbox.IntentFollowup, Display: "tiny", Submit: "tiny", Source: "test",
	}); err != nil {
		t.Fatal(err)
	}
	for _, code := range task999NoticeCodes(sink) {
		if code == event.NoticeCodeInboxLargeItem {
			t.Fatalf("small enqueue emitted %s — the threshold must not fire below 4 MiB", code)
		}
	}
}

// Idempotent replays stay silent: the first delivery already said it once.
func TestTask999IdempotentReplayStaysSilent(t *testing.T) {
	sink := &recordingSink{}
	c := newTask999Controller(t, sink)
	req := task999ImageSubmit(6 << 20)
	req.Idempotency = "task999-replay"
	if _, err := c.EnqueueInbox(req); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnqueueInbox(req); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	n := 0
	for _, code := range task999NoticeCodes(sink) {
		if code == event.NoticeCodeInboxLargeItem {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%s notices = %d, want exactly 1 (replays silent)", event.NoticeCodeInboxLargeItem, n)
	}
}
