package sessioninbox

import (
	"errors"
	"path/filepath"
	"testing"
)

// Task 309: mailbox idempotency defaults ON — a retried send (no explicit
// key) must dedup onto the original delivery instead of enqueueing a
// duplicate, and differing content must never be swallowed.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "sessions", "s1"), Limits{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func collabEnvelope(body string) PromptEnvelope {
	return PromptEnvelope{
		DisplayText:      body,
		RawText:          body,
		SubmitText:       body,
		Source:           "collab:senderA",
		ReceiptRequested: true,
		CollabMsgID:      "msg_" + body,
		CollabMailTo:     "sc_recipient",
	}
}

func TestMailboxIdempotencyDefaultsOn(t *testing.T) {
	store := openTestStore(t)
	first, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: collabEnvelope("hello"), Source: "collab:senderA"})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if first.Idempotent {
		t.Fatal("first delivery reported idempotent")
	}
	again, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: collabEnvelope("hello"), Source: "collab:senderA"})
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if !again.Idempotent {
		t.Fatal("same-content redelivery was not deduped (idempotency default failed)")
	}
	if again.ItemID != first.ItemID {
		t.Fatalf("redelivery returned item %s, want original %s", again.ItemID, first.ItemID)
	}
	snap := store.Snapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("queue holds %d items after redelivery, want 1 (no duplicate enqueue)", len(snap.Items))
	}
}

func TestMailboxIdempotencyContentConflict(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: collabEnvelope("hello"), Source: "collab:senderA", Idempotency: "explicit-key"}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if _, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: collabEnvelope("different body"), Source: "collab:senderA", Idempotency: "explicit-key"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key with different content: err=%v, want ErrIdempotencyConflict (content must not be swallowed)", err)
	}
	// Auto keys are content-derived, so different content never collides.
	if _, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: collabEnvelope("different body"), Source: "collab:senderA"}); err != nil {
		t.Fatalf("different content under auto keys must enqueue as a new mail: %v", err)
	}
}

// Task 309 × 221: after a merge folds B into A, a redelivery of B's original
// content must NOT enqueue a duplicate — it resolves to the surviving merged
// row — and the surviving row must carry B's receipt bookkeeping so the
// consume-side sends B's sender a receipt too.
func TestMailboxMergeFoldedReceiptBookkeeping(t *testing.T) {
	store := openTestStore(t)
	envA := collabEnvelope("body A")
	envA.CollabMsgID = "msg_A"
	first, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: envA, Source: "collab:senderA"})
	if err != nil {
		t.Fatalf("enqueue A: %v", err)
	}
	envB := collabEnvelope("body B")
	envB.CollabMsgID = "msg_B"
	keyB := "collab:senderA:thread1:bbbbbbbbbbbbbbbb"
	if _, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: envB, Source: "collab:senderA", Idempotency: keyB}); err != nil {
		t.Fatalf("enqueue B: %v", err)
	}

	// Merge B into A: the surviving row keeps A's id, B's key aliases onto it.
	mergedEnv := collabEnvelope("body A\nbody B")
	mergedEnv.CollabMsgID = "msg_A"
	if _, err := store.UpdateItemWithIdempotency(first.ItemID, mergedEnv, keyB, envB); err != nil {
		t.Fatalf("merge update: %v", err)
	}

	// Folded bookkeeping landed on the surviving row.
	meta, _, err := store.ReadItem(first.ItemID)
	if err != nil {
		t.Fatalf("read merged item: %v", err)
	}
	foundFolded := false
	for _, ref := range meta.FoldedReceipts {
		if ref.MsgID == "msg_B" && ref.MailTo == "sc_recipient" {
			foundFolded = true
		}
	}
	if !foundFolded {
		t.Fatalf("merged row missing folded receipt for msg_B: %+v", meta.FoldedReceipts)
	}

	// Redelivery of B's original content under B's key: deduped onto the
	// merged row (the merge destination), not enqueued again.
	redelivered, err := store.Enqueue(EnqueueRequest{Intent: IntentFollowup, Envelope: envB, Source: "collab:senderA", Idempotency: keyB})
	if err != nil {
		t.Fatalf("redelivery after merge: %v", err)
	}
	if !redelivered.Idempotent {
		t.Fatal("post-merge redelivery was not deduped")
	}
	if redelivered.ItemID != first.ItemID {
		t.Fatalf("post-merge redelivery resolved to %s, want the merged row %s (merge destination)", redelivered.ItemID, first.ItemID)
	}
	snap := store.Snapshot()
	if len(snap.Items) != 2 {
		t.Fatalf("queue holds %d items after post-merge redelivery, want 2 (A merged + untouched third)", len(snap.Items))
	}
}
