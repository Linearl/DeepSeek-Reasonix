package control

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/sessioninbox"
)

// Task 243 A5 (sub-report 01-④5): "宁可留队列不写空消息" — both drain paths
// must refuse empty renders. The single-item defence already exists
// (sessioninbox.Enqueue rejects an envelope with no body); this pins it and
// adds its merge-side twin: an all-empty merge group leaves the queue
// untouched instead of consuming members into a header-only shell.

// TestInboxMergeAllEmptyBodiesLeavesQueue pins the merge-side twin: invocation
// envelopes can legally enqueue with empty text, so a same-source group of
// them would merge into scaffolding only — nothing may be written or deleted.
func TestInboxMergeAllEmptyBodiesLeavesQueue(t *testing.T) {
	armInboxMerge(t, CollabInboxMergeSameSender)
	c, _, _ := newInboxDispatchController(t)

	// Invocation-only items: legal to enqueue (hasInvocation exempts the
	// empty-body rejection) but their merged render would be headers with no
	// content underneath.
	rec1, err := c.EnqueueInbox(InboxRequest{
		Intent:      sessioninbox.IntentFollowup,
		Source:      "collab:sc_empty",
		Invocations: []InvocationRequest{{Name: "legacy"}},
	})
	if err != nil {
		t.Fatalf("first invocation-only enqueue: %v", err)
	}
	if _, err := c.EnqueueInbox(InboxRequest{
		Intent:      sessioninbox.IntentFollowup,
		Source:      "collab:sc_empty",
		Invocations: []InvocationRequest{{Name: "legacy2"}},
	}); err != nil {
		t.Fatalf("second invocation-only enqueue: %v", err)
	}

	var first sessioninbox.InboxItemMeta
	for _, it := range c.InboxSnapshot().Items {
		if it.ID == rec1.ItemID {
			first = it
		}
	}
	if first.ID == "" {
		t.Fatal("enqueued item missing from snapshot")
	}

	got := c.maybeMergeInboxDispatchGroup(first)
	if got.ID != first.ID {
		t.Fatalf("carrier must stay itself when the group renders empty: %s -> %s", first.ID, got.ID)
	}
	if ids := queuedIDs(c); len(ids) != 2 {
		t.Fatalf("empty group must leave BOTH items queued (宁可留队列), queued = %v", ids)
	}
	// The carrier must not have been rewritten into a header-only shell.
	for _, it := range c.InboxSnapshot().Items {
		if it.ID == first.ID {
			if it.State != sessioninbox.StateQueued {
				t.Fatalf("carrier state = %s, want queued", it.State)
			}
		}
	}
}

// TestSingleEmptyEnvelopeRejectedAtEnqueue pins the single-item twin that the
// merge guard documents: Enqueue refuses an envelope with no body, no
// invocation — the "渲染后空则丢" direction on the non-merge path.
func TestSingleEmptyEnvelopeRejectedAtEnqueue(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := sessioninbox.Open(session, sessioninbox.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.Enqueue(sessioninbox.EnqueueRequest{
		Intent:   sessioninbox.IntentFollowup,
		Source:   "test",
		Envelope: sessioninbox.PromptEnvelope{},
	})
	if !errors.Is(err, sessioninbox.ErrEmpty) {
		t.Fatalf("empty render must be rejected at enqueue with ErrEmpty, got %v", err)
	}
}
