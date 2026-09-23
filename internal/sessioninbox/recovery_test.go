package sessioninbox

import (
	"path/filepath"
	"testing"
)

func TestRecoverOrphanedInFlightPreservesOwnedAndPendingItems(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	enqueue := func(text string) string {
		rec, enqueueErr := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: text}})
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		return rec.ItemID
	}
	queued := enqueue("queued")
	owned := enqueue("owned accepted")
	orphanedAccepted := enqueue("orphaned accepted")
	orphanedRunning := enqueue("orphaned running")
	if err := s.SetState(owned, StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(orphanedAccepted, StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimItem(orphanedRunning); err != nil {
		t.Fatal(err)
	}

	recovered, err := s.RecoverOrphanedInFlight([]string{owned})
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 2 {
		t.Fatalf("recovered = %d, want 2", recovered)
	}
	snap := s.Snapshot()
	if !snap.Paused || !snap.Recovered || snap.RecoveredN != 2 {
		t.Fatalf("recovery metadata = %+v", snap)
	}
	states := make(map[string]InboxState, len(snap.Items))
	for _, item := range snap.Items {
		states[item.ID] = item.State
	}
	if states[queued] != StateQueued || states[owned] != StateSteerAccepted ||
		states[orphanedAccepted] != StateUncertain || states[orphanedRunning] != StateUncertain {
		t.Fatalf("recovered states = %+v", states)
	}

	if again, err := s.RecoverOrphanedInFlight([]string{owned}); err != nil || again != 0 {
		t.Fatalf("idempotent recovery = %d, err=%v", again, err)
	}
}

// Task 263 sample 2: after an update restart the shelf replayed eight already
// processed collaboration replies — in-flight items whose source message was
// consumed (the seen cursor) before the restart. The settled probe must drop
// them along the completion path: only genuine orphans become uncertain work,
// and the recovered banner counts them alone.
func TestRecoverOrphanedInFlightDropsSettledItems(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	enqueue := func(text string, idem string) string {
		rec, enqueueErr := s.Enqueue(EnqueueRequest{
			Envelope:     PromptEnvelope{SubmitText: text},
			Source:       "collab:contact-x",
			Idempotency:  idem,
		})
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		return rec.ItemID
	}
	settled1 := enqueue("265 dev delivery", "collab:msg_settled_one")
	settled2 := enqueue("research delivery", "collab:msg_settled_two")
	genuine := enqueue("never consumed", "collab:msg_live")
	unrelated := enqueue("still queued", "")
	// All three collab items were mid-flight when the process restarted.
	for _, id := range []string{settled1, settled2, genuine} {
		if err := s.ClaimItem(id); err != nil {
			t.Fatal(err)
		}
	}

	// The probe mirrors the boot closure: idempotency "collab:<id>" consulted
	// against the mailbox cursor (only msg_settled_* was Acked pre-restart).
	settledSeen := map[string]bool{"msg_settled_one": true, "msg_settled_two": true}
	probe := func(m InboxItemMeta) bool {
		return settledSeen[m.Idempotency[len("collab:"):]]
	}
	recovered, err := s.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, probe)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1 (only the genuinely unsettled item)", recovered)
	}
	snap := s.Snapshot()
	states := make(map[string]InboxState, len(snap.Items))
	for _, item := range snap.Items {
		states[item.ID] = item.State
	}
	if _, ok := states[settled1]; ok {
		t.Fatal("settled item 1 must be dropped, not replayed onto the shelf")
	}
	if _, ok := states[settled2]; ok {
		t.Fatal("settled item 2 must be dropped, not replayed onto the shelf")
	}
	if states[genuine] != StateUncertain {
		t.Fatalf("genuine orphan state = %q, want uncertain", states[genuine])
	}
	if states[unrelated] != StateQueued {
		t.Fatalf("queued item state = %q, want queued", states[unrelated])
	}
	if !snap.Paused || !snap.Recovered || snap.RecoveredN != 1 {
		t.Fatalf("recovery metadata = %+v, want Paused+Recovered N=1 (settled items uncounted)", snap)
	}
}

// nil probe keeps the pre-263 behaviour: every orphaned in-flight item is
// recovered, hosts that never inject one see no change.
func TestRecoverOrphanedInFlightNilProbeKeepsLegacyBehaviour(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "orphan"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimItem(rec.ItemID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("nil probe recovered = %d, want 1", recovered)
	}
	if states := s.Snapshot().Items; len(states) != 1 || states[0].State != StateUncertain {
		t.Fatalf("nil probe items = %+v, want one uncertain item", states)
	}
}
