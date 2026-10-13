package collabinbox

// 任务 856 ② — the physical-delete API contracts:
//
//   Delete:     archive-only semantics (the mail was already injected into
//               conversations at delivery time; deleting the archive must not
//               re-inject or mutate anything else), duplicate-cluster
//               expansion, dead dismissed/decided state keys GC'd, fresh
//               snapshot returned for one-round-trip convergence.
//   DeleteAll:  empties every bucket including dismissed rows, reports the
//               physical removal count (the 620 clean-now shape), resets the
//               view state, and the follow-up List reads empty.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

func TestDeleteRemovesArchiveOnly(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_keep", From: "sc_a", To: "sc_b", Body: "keep me", At: store.now() - dayMs})
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_drop", From: "sc_a", To: "sc_b", Body: "drop me", At: store.now() - 2*dayMs})

	snap, err := store.Delete(ctx, []string{"m_drop"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 1 {
		t.Fatalf("after delete total = %d, want 1", snap.Total)
	}
	if snap.Entries[0].ID != "m_keep" {
		t.Fatalf("survivor = %s, want m_keep", snap.Entries[0].ID)
	}
	// The physical line is gone from the recipient inbox file (archive-only
	// means the REMOVED copy is gone; nothing else is rewritten).
	if _, found := mail.ParentThread("sc_b", "m_drop"); found {
		t.Fatal("the deleted mail still sits in the recipient inbox")
	}
	if _, found := mail.ParentThread("sc_b", "m_keep"); !found {
		t.Fatal("the delete touched a mail it was not asked to")
	}
}

func TestDeleteExpandsDuplicateCluster(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	// Three byte-identical copies already on disk within the fold window → one
	// folded entry. Written straight into the inbox file: the delivery layer's
	// resend-dedup (the primary duplicate blocker) would refuse copies 2+3,
	// and the fold is exactly the consumer-side backstop for copies that got
	// there before that layer existed (the 9-duplicates incident).
	inbox := mail.InboxPath("sc_b")
	var lines []string
	for i, id := range []string{"m_d0", "m_d1", "m_d2"} {
		line, err := json.Marshal(sessioncollab.MailMessage{ID: id, From: "sc_a", To: "sc_b", Body: "same body", At: base - int64(i)*60_000})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(line))
	}
	joiner := "\n"
	if err := os.WriteFile(inbox, []byte(strings.Join(lines, joiner)+joiner), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Entries) != 1 || snap.Entries[0].DuplicateCount != 3 {
		t.Fatalf("fixture: want 1 folded entry ×3, got %d entries", len(snap.Entries))
	}
	if _, err := store.Delete(ctx, []string{snap.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m_d0", "m_d1", "m_d2"} {
		if _, found := mail.ParentThread("sc_b", id); found {
			t.Fatalf("cluster member %s survived a folded-entry delete", id)
		}
	}
}

func TestDeleteGarbageCollectsDismissedAndDecided(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_x", From: "sc_a", To: "sc_b", Body: "approve?", At: store.now(), Approver: "sc_b"})
	if _, err := store.Dismiss(ctx, []string{"m_x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Decide(ctx, "m_x", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Delete(ctx, []string{"m_x"}); err != nil {
		t.Fatal(err)
	}
	st := store.loadState()
	if len(st.Dismissed) != 0 || len(st.Decided) != 0 {
		t.Fatalf("dead state keys survived: dismissed=%d decided=%d", len(st.Dismissed), len(st.Decided))
	}
}

func TestDeleteAllEmptiesEveryBucketIncludingDismissed(t *testing.T) {
	store, mail := fixtureStore(t)
	ctx := context.Background()
	base := store.now()
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_1", From: "sc_a", To: "sc_b", Body: "one", At: base})
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_2", From: "sc_c", To: "sc_d", Body: "two", At: base - dayMs})
	deliver(t, mail, sessioncollab.MailMessage{ID: "m_3", From: "sc_e", To: "sc_f", Body: "three", At: base - 2*dayMs})
	if _, err := store.Dismiss(ctx, []string{"m_3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRetention(ctx, RetentionForever); err != nil {
		t.Fatal(err)
	}

	removed, snap, err := store.DeleteAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Fatalf("removed = %d, want 3 (dismissed rows are part of the archive)", removed)
	}
	if snap.Total != 0 || len(snap.Entries) != 0 {
		t.Fatalf("the post-clear snapshot shows %d entries, want an honest empty", snap.Total)
	}
	// The view state resets with the archive (stale dismissed keys would be
	// invisible garbage forever).
	st := store.loadState()
	if len(st.Dismissed) != 0 || len(st.Decided) != 0 {
		t.Fatalf("view state not reset: dismissed=%d decided=%d", len(st.Dismissed), len(st.Decided))
	}
	// A later panel read (applyRetention=true would sweep; keep it honest with
	// a plain read) still shows empty, and the mail dir holds no parsable rows.
	after, err := store.List(ctx, Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != 0 {
		t.Fatalf("a follow-up read shows %d entries, want 0", after.Total)
	}
}
