package control

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// seedInboxItemReason is seedInboxItem with an explicit park reason — the
// shape recovery / turn-exit paths actually leave behind (543's helper only
// writes the generic in-flight reason).
func seedInboxItemReason(t *testing.T, session, text, idem string, state sessioninbox.InboxState, reason string) {
	t.Helper()
	st, err := sessioninbox.Open(session, sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec, err := st.Enqueue(sessioninbox.EnqueueRequest{
		Envelope:    sessioninbox.PromptEnvelope{SubmitText: text},
		Idempotency: idem,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state != sessioninbox.StateQueued {
		if err := st.SetState(rec.ItemID, state, reason); err != nil {
			t.Fatal(err)
		}
	}
}

// bodyReceipts is a fake InboxAppliedBodyReceipts loader serving one body.
func bodyReceipts(bodies ...string) func(string) map[string]struct{} {
	set := make(map[string]struct{}, len(bodies))
	for _, b := range bodies {
		set[b] = struct{}{}
	}
	return func(string) map[string]struct{} { return set }
}

// 任务866 regression pin, degraded-follow-up face: a collab mail delivered as
// steer, degraded to a queued follow-up, consumed by a turn, then parked as
// TranscriptNotDurable by a failed turn snapshot (the 2026-10-13 实测: the
// transcript's autosave succeeded but the turn snapshot lost the write
// authority) replayed onto the shelf after every restart — the steer-receipt
// matcher has no mid-turn prefix to match, the recovery drops skip both park
// reasons by design, and the 840 settle only admits the two recovery park
// reasons. The body face is the missing settlement evidence: the turn's body
// persists as a plain user message, so the residue settles on the first
// inbox open.
func TestSettleDegradesFollowupBodyResidue(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const body = "tip 停 93 分钟需判「完成态静默」vs「卡住」——逐个核合并令七分支是否全进主线"
	seedInboxItemReason(t, session, body, "k-866-degraded",
		sessioninbox.StateUncertain, sessioninbox.BlockReasonTranscriptNotDurable)

	c := New(Options{
		SessionPath:              session,
		SessionDir:               dir,
		Sink:                     event.Discard,
		InboxAppliedBodyReceipts: bodyReceipts(body),
	})
	snap := c.InboxSnapshot()
	if len(snap.Items) != 0 {
		t.Fatalf("items after settle = %+v, want the TranscriptNotDurable follow-up residue dropped", snap.Items)
	}
	if snap.Paused || snap.Recovered || snap.RecoveredN != 0 {
		t.Fatalf("metadata = %+v, want no pause/banner for an applied-residue drop", snap)
	}
}

// The body face settles the same row shape without any park reason — the
// kill-in-running form loadOrInit rewrites to Uncertain with no reason at all
// (store.go's cross-process pass) once the turn actually consumed the body.
func TestSettleReasonlessUncertainBodyResidue(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedInboxItem(t, session, "fork 合并线请核对七分支", "k-866-running", sessioninbox.StateUncertain)

	c := New(Options{
		SessionPath:              session,
		SessionDir:               dir,
		Sink:                     event.Discard,
		InboxAppliedBodyReceipts: bodyReceipts("fork 合并线请核对七分支"),
	})
	snap := c.InboxSnapshot()
	if len(snap.Items) != 0 {
		t.Fatalf("items after settle = %+v, want the reasonless uncertain residue dropped", snap.Items)
	}
}

// Honesty faces: without the body in the transcript nothing settles. A
// TranscriptNotDurable park whose content never reached the transcript, a
// SteerUnapplied park (589b: acceptance is not injection) and a live queued
// repeat of the same instruction all stay for review.
func TestBodyFaceKeepsUnappliedRows(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const body = "未落到 transcript 的指令"
	seedInboxItemReason(t, session, body, "k-notdurable",
		sessioninbox.StateUncertain, sessioninbox.BlockReasonTranscriptNotDurable)
	seedInboxItemReason(t, session, "接受后未注入的 steer", "k-unapplied",
		sessioninbox.StateUncertain, sessioninbox.BlockReasonSteerUnapplied)
	seedInboxItemReason(t, session, body, "k-live-repeat", sessioninbox.StateQueued, "")

	c := New(Options{
		SessionPath:              session,
		SessionDir:               dir,
		Sink:                     event.Discard,
		InboxAppliedBodyReceipts: bodyReceipts("另一段毫不相干的对话正文"),
	})
	snap := c.InboxSnapshot()
	if len(snap.Items) != 3 {
		t.Fatalf("items = %+v, want all three rows kept (no body evidence, live repeat untouched)", snap.Items)
	}
}

// Loaders absent: both faces nil keeps the legacy behaviour — nothing is
// auto-settled, exactly as before this task.
func TestBodyFaceNilLoaderKeepsLegacy(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedInboxItemReason(t, session, "任何正文", "k-legacy",
		sessioninbox.StateUncertain, sessioninbox.BlockReasonTranscriptNotDurable)

	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	if snap := c.InboxSnapshot(); len(snap.Items) != 1 {
		t.Fatalf("items = %+v, want the row kept with no receipt loader at all", snap.Items)
	}
}

// Precedence: the steer faces still win. A row whose id appears in a merged
// receipt's segment header settles even when the body face serves nothing —
// the 543 contract is untouched.
func TestSteerFaceStillAuthoritative(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedInboxItem(t, session, "合并消费的成员行", "k-543-member", sessioninbox.StateUncertain)

	c := New(Options{
		SessionPath: session,
		SessionDir:  dir,
		Sink:        event.Discard,
		InboxAppliedReceipts: func(string) map[string]struct{} {
			return map[string]struct{}{
				"[合并消息 ×2]\n\n── 合并自 inbox 条目 k-543-member（来源 collab:x） ──\n合并消费的成员行\n\n── 合并自 inbox 条目 other（来源 collab:y） ──\n另一行": {},
			}
		},
	})
	if snap := c.InboxSnapshot(); len(snap.Items) != 0 {
		t.Fatalf("items = %+v, want the merged-member row settled by the steer id face", snap.Items)
	}
}
