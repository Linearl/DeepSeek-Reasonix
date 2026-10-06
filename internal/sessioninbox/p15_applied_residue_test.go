package sessioninbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// P15: an orphaned consumed steer is applied residue — the durable consume
// boundary committed before the restart. Recovery drops it even with no
// settled probe at all (nil keeps the pre-263 behaviour for every other
// state, but a consumed row is settled by definition), and the drop takes its
// idempotency bookkeeping with it so the same instruction can be enqueued
// fresh later.
func TestRecoverDropsOrphanedConsumedSteerWithoutProbe(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, err := s.Enqueue(EnqueueRequest{
		Envelope:    PromptEnvelope{SubmitText: "already applied guidance"},
		Idempotency: "desktop-key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(rec.ItemID, StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}

	recovered, err := s.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 {
		t.Fatalf("recovered = %d, want 0 (consumed residue is dropped, not recovered)", recovered)
	}
	snap := s.Snapshot()
	if len(snap.Items) != 0 {
		t.Fatalf("consumed steer survived recovery: %+v", snap.Items)
	}
	// A pure applied-residue drop is not recovery work: nothing pending
	// survives, so the inbox must not pause or raise the recovered banner.
	if snap.Paused || snap.Recovered || snap.RecoveredN != 0 {
		t.Fatalf("recovery metadata = %+v, want no pause/banner for residue-only drop", snap)
	}
	// The dropped row's idempotency key must be gone: re-sending the same
	// instruction enqueues a fresh item instead of deduplicating onto the
	// deleted one.
	again, err := s.Enqueue(EnqueueRequest{
		Envelope:    PromptEnvelope{SubmitText: "already applied guidance"},
		Idempotency: "desktop-key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Idempotent || again.ItemID == rec.ItemID {
		t.Fatalf("re-enqueue deduplicated onto the dropped row: %+v", again)
	}
	if again.Disposition != DispositionQueuedFollowup {
		t.Fatalf("re-enqueue disposition = %q, want %q", again.Disposition, DispositionQueuedFollowup)
	}
}

// P15: the cross-process pass in loadOrInit must leave consumed steers alone
// (they are applied, not pending) so the recovery drop can settle them;
// running/steer_accepted orphans still rewrite to Uncertain.
func TestCrossRunOpenKeepsConsumedSteerForResidueDrop(t *testing.T) {
	session := filepath.Join(t.TempDir(), "s.jsonl")
	s, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "consumed before restart"}})
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "running when restart hit"}})
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "still queued"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(consumed.ItemID, StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimItem(running.ItemID); err != nil {
		t.Fatal(err)
	}
	inboxDir := s.Dir()
	runID := s.runID
	s.Close()

	// Simulate the update restart: the previous run wrote the manifest.
	manifestPath := filepath.Join(inboxDir, manifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var man manifest
	if err := json.Unmarshal(data, &man); err != nil {
		t.Fatal(err)
	}
	if man.RunID != runID {
		t.Fatalf("manifest runId = %q, want the creating run %q", man.RunID, runID)
	}
	man.RunID = "previous-run"
	data, err = json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	states := make(map[string]InboxState)
	for _, item := range reopened.Snapshot().Items {
		states[item.ID] = item.State
	}
	if states[consumed.ItemID] != StateSteerConsumed {
		t.Fatalf("consumed steer state after cross-run open = %q, want steer_consumed (not resurrected as pending)", states[consumed.ItemID])
	}
	if states[running.ItemID] != StateUncertain {
		t.Fatalf("running orphan state after cross-run open = %q, want uncertain", states[running.ItemID])
	}
	if states[live.ItemID] != StateQueued {
		t.Fatalf("queued state after cross-run open = %q, want queued", states[live.ItemID])
	}

	// The controller-level recovery then settles the residue: the consumed
	// row drops, real pending work survives.
	if _, err := reopened.RecoverOrphanedInFlight(nil); err != nil {
		t.Fatal(err)
	}
	final := make(map[string]InboxState)
	for _, item := range reopened.Snapshot().Items {
		final[item.ID] = item.State
	}
	if _, ok := final[consumed.ItemID]; ok {
		t.Fatalf("consumed steer survived the recovery drop: %+v", final)
	}
	if final[running.ItemID] != StateUncertain || final[live.ItemID] != StateQueued {
		t.Fatalf("surviving states = %+v, want uncertain + queued", final)
	}
}

// P15: SettleAppliedResidue removes Uncertain rows whose body matches an
// applied steer receipt, keeps live intent (queued), blocked rows and
// non-matching residue, and unpauses when nothing is left to review.
func TestSettleAppliedResidueDropsMatchingUncertainOnly(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	envelope := func(text, idem string) EnqueueRequest {
		return EnqueueRequest{Envelope: PromptEnvelope{SubmitText: text}, Idempotency: idem}
	}
	applied := func(text string) func(InboxItemMeta, PromptEnvelope) bool {
		return func(_ InboxItemMeta, env PromptEnvelope) bool {
			return env.SubmitText == text
		}
	}
	residue, err := s.Enqueue(envelope("guidance applied long ago", "k-residue"))
	if err != nil {
		t.Fatal(err)
	}
	keepUncertain, err := s.Enqueue(envelope("genuine unresolved work", "k-keep"))
	if err != nil {
		t.Fatal(err)
	}
	liveQueued, err := s.Enqueue(envelope("guidance applied long ago", "k-live"))
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := s.Enqueue(envelope("guidance applied long ago", "k-blocked"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(residue.ItemID, StateUncertain, "in-flight owner is no longer active"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(keepUncertain.ItemID, StateUncertain, "in-flight owner is no longer active"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(blocked.ItemID, StateBlocked, "reference unavailable"); err != nil {
		t.Fatal(err)
	}
	// A queued live row with identical text must survive: repeating a finished
	// instruction is the user's call, not residue.
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}

	dropped, err := s.SettleAppliedResidue(applied("guidance applied long ago"))
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1 (only the matching uncertain row)", dropped)
	}
	states := make(map[string]InboxState)
	for _, item := range s.Snapshot().Items {
		states[item.ID] = item.State
	}
	if _, ok := states[residue.ItemID]; ok {
		t.Fatal("applied residue must be settled, not replayed onto the shelf")
	}
	if states[keepUncertain.ItemID] != StateUncertain || states[liveQueued.ItemID] != StateQueued || states[blocked.ItemID] != StateBlocked {
		t.Fatalf("surviving states = %+v, want uncertain + queued + blocked kept", states)
	}
	// Nothing about the surviving work changed, so the pause stays.
	if !s.Snapshot().Paused {
		t.Fatal("settling residue must not unpause surviving pending work")
	}
}

// An empty queue after the residue pass must clear the pause: there is
// nothing left to review, so the recovery banner state must not stick.
func TestSettleAppliedResidueUnpausesEmptyQueue(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "applied residue"}, Idempotency: "k-only"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(rec.ItemID, StateUncertain, "in-flight owner is no longer active"); err != nil {
		t.Fatal(err)
	}
	if err := s.ForcePause(true, 1); err != nil {
		t.Fatal(err)
	}
	dropped, err := s.SettleAppliedResidue(func(_ InboxItemMeta, env PromptEnvelope) bool { return env.SubmitText == "applied residue" })
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	snap := s.Snapshot()
	if len(snap.Items) != 0 {
		t.Fatalf("items = %+v, want empty", snap.Items)
	}
	if snap.Paused || snap.Recovered || snap.RecoveredN != 0 {
		t.Fatalf("metadata = %+v, want pause/banner cleared for an emptied queue", snap)
	}
	// The settled row's idempotency key is gone: the same instruction can be
	// enqueued fresh.
	again, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "applied residue"}, Idempotency: "k-only"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Idempotent || again.ItemID == rec.ItemID {
		t.Fatalf("re-enqueue deduplicated onto the settled row: %+v", again)
	}
}

// A body that cannot be read is never auto-dropped — the residue pass needs
// positive evidence of application.
func TestSettleAppliedResidueKeepsUnreadableBlob(t *testing.T) {
	session := filepath.Join(t.TempDir(), "s.jsonl")
	s, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "unreadable residue"}, Idempotency: "k-corrupt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(rec.ItemID, StateUncertain, "in-flight owner is no longer active"); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	var blobName string
	for _, item := range snap.Items {
		if item.ID == rec.ItemID {
			blobName = blobNameFor(item)
		}
	}
	if blobName == "" {
		t.Fatal("item not found in snapshot")
	}
	path, err := s.blobPath(blobName)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Corrupt the blob behind the store's back, then reopen.
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	dropped, err := reopened.SettleAppliedResidue(func(_ InboxItemMeta, env PromptEnvelope) bool {
		return env.SubmitText == "unreadable residue"
	})
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (unreadable bodies are kept)", dropped)
	}
	if items := reopened.Snapshot().Items; len(items) != 1 || items[0].ID != rec.ItemID {
		t.Fatalf("items = %+v, want the unreadable row kept", items)
	}
}

// A nil matcher settles nothing; a closed store reports ErrClosed.
func TestSettleAppliedResidueGuards(t *testing.T) {
	session := filepath.Join(t.TempDir(), "s.jsonl")
	s, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "x"}}); err != nil {
		t.Fatal(err)
	}
	dropped, err := s.SettleAppliedResidue(nil)
	if err != nil || dropped != 0 {
		t.Fatalf("nil matcher = (%d, %v), want (0, nil)", dropped, err)
	}
	s.Close()
	if _, err := s.SettleAppliedResidue(func(InboxItemMeta, PromptEnvelope) bool { return true }); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed store err = %v, want %v", err, ErrClosed)
	}
}
