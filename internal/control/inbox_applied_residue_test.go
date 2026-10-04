package control

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// seedInboxItem opens the store directly and plants one row with the given
// text, idempotency key and state — the shape an older fork's recovery left
// behind. The store is closed again so the controller under test opens it
// fresh.
func seedInboxItem(t *testing.T, session string, text, idem string, state sessioninbox.InboxState) {
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
		if err := st.SetState(rec.ItemID, state, "in-flight owner is no longer active"); err != nil {
			t.Fatal(err)
		}
	}
}

// P15 end-to-end: a pre-P15 leftover (uncertain row whose guidance the
// transcript already applied) settles on the first inbox open, while a queued
// row with the identical text survives — repeating a finished instruction is
// live user intent, not residue. The same fixture without the receipt loader
// keeps the legacy behaviour (nothing is auto-settled).
func TestReopenSettlesAppliedResidue(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedInboxItem(t, session, "guidance applied long ago", "k-residue", sessioninbox.StateUncertain)
	seedInboxItem(t, session, "guidance applied long ago", "k-live", sessioninbox.StateQueued)

	withReceipts := New(Options{
		SessionPath: session,
		SessionDir:  dir,
		Sink:        event.Discard,
		InboxAppliedReceipts: func(string) map[string]struct{} {
			return map[string]struct{}{"guidance applied long ago": {}}
		},
	})
	snap := withReceipts.InboxSnapshot()
	if len(snap.Items) != 1 {
		t.Fatalf("items after settle = %+v, want only the live queued row", snap.Items)
	}
	if snap.Items[0].State != sessioninbox.StateQueued {
		t.Fatalf("surviving row state = %q, want queued", snap.Items[0].State)
	}

	// Without the receipt loader nothing is auto-settled: both rows stay for
	// review, exactly as before P15.
	legacyDir := t.TempDir()
	legacySession := filepath.Join(legacyDir, "s.jsonl")
	if err := os.WriteFile(legacySession, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedInboxItem(t, legacySession, "guidance applied long ago", "k-residue", sessioninbox.StateUncertain)
	seedInboxItem(t, legacySession, "guidance applied long ago", "k-live", sessioninbox.StateQueued)
	legacy := New(Options{SessionPath: legacySession, SessionDir: legacyDir, Sink: event.Discard})
	legacySnap := legacy.InboxSnapshot()
	if len(legacySnap.Items) != 2 {
		t.Fatalf("legacy items = %+v, want both rows kept", legacySnap.Items)
	}
}

// P15 end-to-end: an orphaned consumed steer — the row shape an in-flight
// exit leaves behind — is dropped on the first inbox open with no probe at
// all, instead of being recovered as pending guidance.
func TestReopenDropsOrphanedConsumedSteer(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedInboxItem(t, session, "consumed before the restart", "k-consumed", sessioninbox.StateSteerConsumed)

	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	snap := c.InboxSnapshot()
	if len(snap.Items) != 0 {
		t.Fatalf("items after reopen = %+v, want the consumed steer dropped", snap.Items)
	}
	if snap.Paused || snap.Recovered || snap.RecoveredN != 0 {
		t.Fatalf("metadata = %+v, want no pause/banner for an applied-residue drop", snap)
	}
}
