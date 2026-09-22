package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/sessioninbox"
)

// minor-2 (audit-2): the inbox-layer two-phase Claim→Ack shape of drain_inbox,
// pinned directly instead of behind the mutual-exclusion exemption.
// ClaimItem (phase 1: queued→running) and AckDequeue (phase 2: remove after
// settle) are separate transactions; the window between them is the crash
// window. The tests pin the two safety properties of that window:
//
//   - exclusivity: a claimed item cannot be claimed again (exactly one
//     consumer per item);
//   - no silent redelivery: an item claimed but never acked stays running and
//     is skipped by later drains — never consumed twice (a manual retry is
//     the only way back to queued).
func newTwoPhaseInboxFixture(t *testing.T) string {
	t.Helper()
	sessionPath := filepath.Join(t.TempDir(), "sess.jsonl")
	store, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("open inbox: %v", err)
	}
	defer store.Close()
	if _, err := store.Enqueue(sessioninbox.EnqueueRequest{
		Envelope:    sessioninbox.PromptEnvelope{DisplayText: "hello", RawText: "hello", SubmitText: "hello"},
		Source:      "sc_creator",
		Idempotency: "two-phase-1",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return sessionPath
}

func openTwoPhaseInbox(t *testing.T, sessionPath string) *sessioninbox.Store {
	t.Helper()
	store, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("reopen inbox: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestDrainInboxTwoPhaseSettleConsumesExactlyOnce(t *testing.T) {
	sessionPath := newTwoPhaseInboxFixture(t)

	msgs, settled := drainInboxFromSessionInbox(sessionPath, "", true, 10)
	if len(msgs) != 1 || len(settled) != 1 {
		t.Fatalf("settle drain: msgs=%d settled=%d, want 1/1", len(msgs), len(settled))
	}

	// Phase 2 ran (AckDequeue): a second drain sees nothing.
	msgs2, settled2 := drainInboxFromSessionInbox(sessionPath, "", true, 10)
	if len(msgs2) != 0 || len(settled2) != 0 {
		t.Fatalf("post-settle drain: msgs=%d settled=%d, want 0/0 (ack must dequeue)", len(msgs2), len(settled2))
	}
}

func TestDrainInboxTwoPhaseClaimExclusive(t *testing.T) {
	sessionPath := newTwoPhaseInboxFixture(t)
	store := openTwoPhaseInbox(t, sessionPath)

	snap := store.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("fixture items=%d, want 1", len(snap.Items))
	}
	itemID := snap.Items[0].ID

	if err := store.ClaimItem(itemID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.ClaimItem(itemID); err == nil {
		t.Fatal("second claim of a running item must fail (exactly-one consumer per item)")
	}
}

func TestDrainInboxTwoPhaseClaimedItemNotRedelivered(t *testing.T) {
	sessionPath := newTwoPhaseInboxFixture(t)

	// Phase 1 only: a consumer claimed the item and "crashed" before acking.
	crashed := openTwoPhaseInbox(t, sessionPath)
	snap := crashed.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("fixture items=%d, want 1", len(snap.Items))
	}
	if err := crashed.ClaimItem(snap.Items[0].ID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	crashed.Close()

	// A later drain must not hand it out again: running items are skipped,
	// never consumed twice.
	msgs, settled := drainInboxFromSessionInbox(sessionPath, "", true, 10)
	if len(msgs) != 0 || len(settled) != 0 {
		t.Fatalf("claimed-but-unacked item redelivered: msgs=%d settled=%d, want 0/0", len(msgs), len(settled))
	}
}
