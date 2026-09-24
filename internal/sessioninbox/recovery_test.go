package sessioninbox

import (
	"path/filepath"
	"strings"
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
	// Task 300: RecoveredN covers every surviving unowned pending item — the
	// two in-flight orphans plus the live Queued one — so the banner agrees
	// with loadOrInit's cross-process count and with what /queue shows.
	if !snap.Paused || !snap.Recovered || snap.RecoveredN != 3 {
		t.Fatalf("recovery metadata = %+v, want N=3 (2 orphans + 1 live pending)", snap)
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
	// The recount is idempotent too: the second pass finds nothing new and
	// leaves RecoveredN at the same value.
	if n := s.Snapshot().RecoveredN; n != 3 {
		t.Fatalf("RecoveredN after idempotent pass = %d, want 3", n)
	}
}

// Task 263 sample 2: after an update restart the shelf replayed eight already
// processed collaboration replies — in-flight items whose source message was
// consumed (the seen cursor) before the restart. The settled probe must drop
// them along the completion path: only genuine orphans become uncertain work,
// and the recovered banner counts live pending work alone.
func TestRecoverOrphanedInFlightDropsSettledItems(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	enqueue := func(text string, idem string) string {
		rec, enqueueErr := s.Enqueue(EnqueueRequest{
			Envelope:    PromptEnvelope{SubmitText: text},
			Source:      "collab:contact-x",
			Idempotency: idem,
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
	// Task 300: recovery now probes pending items too, including non-collab
	// ones with an empty idempotency — guard the prefix like the boot closure.
	settledSeen := map[string]bool{"msg_settled_one": true, "msg_settled_two": true}
	probe := func(m InboxItemMeta) bool {
		id, ok := strings.CutPrefix(m.Idempotency, "collab:")
		if !ok || id == "" {
			return false
		}
		return settledSeen[id]
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
	// Task 300: N counts surviving pending work — the genuine orphan (Uncertain
	// after recovery) plus the live Queued item; settled residue is uncounted.
	if !snap.Paused || !snap.Recovered || snap.RecoveredN != 2 {
		t.Fatalf("recovery metadata = %+v, want Paused+Recovered N=2 (settled uncounted, live pending counted)", snap)
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

// Task 300 core: loadOrInit's cross-process pass rewrites in-flight items to
// Uncertain (so the 263 in-flight gate can never see them again) and counts
// Queued/Blocked leftovers without any settled check — the exact shape that
// replayed processed guidance onto the shelf. Recovery must drop settled
// residue in EVERY pending state, keep live pending work, and recount N from
// what actually survives.
func TestRecoverDropsSettledPendingAcrossAllStates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	enqueue := func(text string, idem string) string {
		rec, enqueueErr := s.Enqueue(EnqueueRequest{
			Envelope:    PromptEnvelope{SubmitText: text},
			Source:      "collab:contact-x",
			Idempotency: idem,
		})
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		return rec.ItemID
	}
	// Settled residue in each state loadOrInit leaves behind: Uncertain (was
	// in-flight), Queued, and Blocked. The fourth item is live work.
	settledUncertain := enqueue("processed before restart", "collab:msg_done_a")
	settledQueued := enqueue("queued but source acked", "collab:msg_done_b")
	settledBlocked := enqueue("blocked but source acked", "collab:msg_done_c")
	liveQueued := enqueue("still awaiting review", "collab:msg_live")
	if err := s.SetState(settledUncertain, StateUncertain, "in-flight owner is no longer active"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(settledBlocked, StateBlocked, "waiting on approval"); err != nil {
		t.Fatal(err)
	}

	settledSeen := map[string]bool{"msg_done_a": true, "msg_done_b": true, "msg_done_c": true}
	probe := func(m InboxItemMeta) bool {
		id, ok := strings.CutPrefix(m.Idempotency, "collab:")
		if !ok || id == "" {
			return false
		}
		return settledSeen[id]
	}
	recovered, err := s.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, probe)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 {
		t.Fatalf("recovered = %d, want 0 (settled residue is dropped, not recovered)", recovered)
	}
	snap := s.Snapshot()
	states := make(map[string]InboxState, len(snap.Items))
	for _, item := range snap.Items {
		states[item.ID] = item.State
	}
	for id, name := range map[string]string{
		settledUncertain: "settled Uncertain",
		settledQueued:    "settled Queued",
		settledBlocked:   "settled Blocked",
	} {
		if _, ok := states[id]; ok {
			t.Fatalf("%s item must be dropped after the restart, not replayed onto the shelf: %+v", name, states)
		}
	}
	if states[liveQueued] != StateQueued {
		t.Fatalf("live pending state = %q, want queued (kept for /queue review)", states[liveQueued])
	}
	if snap.RecoveredN != 1 {
		t.Fatalf("RecoveredN = %d, want 1 (only the live pending item counts)", snap.RecoveredN)
	}
	if !snap.Paused {
		t.Fatalf("live pending work must keep the inbox paused for review: %+v", snap)
	}
}
