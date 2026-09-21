package control

import (
	"strings"
	"testing"

	"reasonix/internal/sessioninbox"
)

func armInboxMerge(t *testing.T, mode string) {
	t.Helper()
	SetCollabInboxMergeMode(mode)
	t.Cleanup(func() { SetCollabInboxMergeMode(CollabInboxMergeOff) })
}

func enqueueMergeItem(t *testing.T, c *Controller, source, text string) sessioninbox.InboxItemMeta {
	t.Helper()
	rec, err := c.EnqueueInbox(InboxRequest{
		Intent: sessioninbox.IntentFollowup,
		Submit: text,
		Source: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range c.InboxSnapshot().Items {
		if it.ID == rec.ItemID {
			return it
		}
	}
	t.Fatalf("enqueued item %s missing from snapshot", rec.ItemID)
	return sessioninbox.InboxItemMeta{}
}

func queuedIDs(c *Controller) []string {
	var ids []string
	for _, it := range c.InboxSnapshot().Items {
		if it.State == sessioninbox.StateQueued {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

func TestInboxMergeOffKeepsFIFO(t *testing.T) {
	armInboxMerge(t, CollabInboxMergeOff)
	c, _, _ := newInboxDispatchController(t)
	first := enqueueMergeItem(t, c, "collab:sc_a", "first")
	enqueueMergeItem(t, c, "collab:sc_a", "second")

	got := c.maybeMergeInboxDispatchGroup(first)
	if got.ID != first.ID {
		t.Fatalf("carrier changed: %s -> %s", first.ID, got.ID)
	}
	if ids := queuedIDs(c); len(ids) != 2 {
		t.Fatalf("off mode must not touch the queue, queued = %v", ids)
	}
}

func TestInboxMergeSingleItemNeverMerges(t *testing.T) {
	armInboxMerge(t, CollabInboxMergeAll)
	c, _, _ := newInboxDispatchController(t)
	first := enqueueMergeItem(t, c, "collab:sc_a", "only")

	got := c.maybeMergeInboxDispatchGroup(first)
	if got.ID != first.ID {
		t.Fatalf("carrier changed: %s -> %s", first.ID, got.ID)
	}
	if ids := queuedIDs(c); len(ids) != 1 {
		t.Fatalf("single item queue must stay intact, queued = %v", ids)
	}
}

func TestInboxMergeSameSenderGroupsBySource(t *testing.T) {
	armInboxMerge(t, CollabInboxMergeSameSender)
	c, _, _ := newInboxDispatchController(t)
	first := enqueueMergeItem(t, c, "collab:sc_a", "hello from A")
	enqueueMergeItem(t, c, "collab:sc_a", "second from A")
	enqueueMergeItem(t, c, "collab:sc_b", "from B stays")

	got := c.maybeMergeInboxDispatchGroup(first)
	if got.ID != first.ID {
		t.Fatalf("carrier changed: %s -> %s", first.ID, got.ID)
	}
	ids := queuedIDs(c)
	// The two A items merged into the carrier; B keeps its own queue slot and
	// merges in its own drain.
	if len(ids) != 2 {
		t.Fatalf("same_sender merge should leave the carrier plus B queued, queued = %v", ids)
	}
	_, env, err := c.ReadInboxItem(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hello from A", "second from A", first.ID} {
		if !strings.Contains(env.SubmitText, want) {
			t.Fatalf("merged body missing %q:\n%s", want, env.SubmitText)
		}
	}
	if strings.Contains(env.SubmitText, "from B stays") {
		t.Fatal("same_sender merge must not include another sender's body")
	}
	if env.Extra["mergeMode"] != CollabInboxMergeSameSender {
		t.Fatalf("mergeMode extra = %q", env.Extra["mergeMode"])
	}
}

func TestInboxMergeAllMergesEverythingQueued(t *testing.T) {
	armInboxMerge(t, CollabInboxMergeAll)
	c, _, _ := newInboxDispatchController(t)
	first := enqueueMergeItem(t, c, "collab:sc_a", "from A")
	second := enqueueMergeItem(t, c, "collab:sc_b", "from B")
	enqueueMergeItem(t, c, "desktop", "typed by hand")

	got := c.maybeMergeInboxDispatchGroup(first)
	if got.ID != first.ID {
		t.Fatalf("carrier changed: %s -> %s", first.ID, got.ID)
	}
	if ids := queuedIDs(c); len(ids) != 1 {
		t.Fatalf("all merge should leave exactly one queued item, queued = %v", ids)
	}
	_, env, err := c.ReadInboxItem(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"from A", "from B", "typed by hand"} {
		if !strings.Contains(env.SubmitText, want) {
			t.Fatalf("merged body missing %q:\n%s", want, env.SubmitText)
		}
	}
	if !strings.Contains(env.Extra["mergedFrom"], second.ID) {
		t.Fatalf("mergedFrom extra missing member ids: %q", env.Extra["mergedFrom"])
	}
}

func TestInboxMergeNormalizesUnknownMode(t *testing.T) {
	SetCollabInboxMergeMode("aggressive")
	defer SetCollabInboxMergeMode(CollabInboxMergeOff)
	if got := CollabInboxMergeMode(); got != CollabInboxMergeOff {
		t.Fatalf("unknown mode = %q, want off", got)
	}
	SetCollabInboxMergeMode(" same_sender ")
	if got := CollabInboxMergeMode(); got != CollabInboxMergeOff {
		t.Fatalf("untrimmed mode must normalize to off, got %q", got)
	}
}
