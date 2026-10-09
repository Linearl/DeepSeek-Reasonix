package sessioninbox

import (
	"encoding/json"
	"os"
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
	orphanedConsumed := enqueue("orphaned consumed")
	orphanedRunning := enqueue("orphaned running")
	if err := s.SetState(owned, StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(orphanedConsumed, StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimItem(orphanedRunning); err != nil {
		t.Fatal(err)
	}

	// P15: the unowned consumed steer is applied residue — the durable consume
	// boundary committed before the restart — so recovery drops it instead of
	// rewriting it to Uncertain. Only the orphaned running item is recovered.
	recovered, err := s.RecoverOrphanedInFlight([]string{owned})
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1 (consumed residue is dropped, not recovered)", recovered)
	}
	snap := s.Snapshot()
	// Task 300: RecoveredN covers every surviving unowned pending item — the
	// running orphan (now Uncertain) plus the live Queued one. The dropped
	// consumed residue is uncounted.
	if !snap.Paused || !snap.Recovered || snap.RecoveredN != 2 {
		t.Fatalf("recovery metadata = %+v, want N=2 (running orphan + live pending)", snap)
	}
	states := make(map[string]InboxState, len(snap.Items))
	for _, item := range snap.Items {
		states[item.ID] = item.State
	}
	if states[queued] != StateQueued || states[owned] != StateSteerAccepted ||
		states[orphanedRunning] != StateUncertain {
		t.Fatalf("recovered states = %+v", states)
	}
	if _, ok := states[orphanedConsumed]; ok {
		t.Fatal("orphaned consumed steer must be dropped as applied residue, not replayed onto the shelf")
	}

	if again, err := s.RecoverOrphanedInFlight([]string{owned}); err != nil || again != 0 {
		t.Fatalf("idempotent recovery = %d, err=%v", again, err)
	}
	// The recount is idempotent too: the second pass finds nothing new and
	// leaves RecoveredN at the same value.
	if n := s.Snapshot().RecoveredN; n != 2 {
		t.Fatalf("RecoveredN after idempotent pass = %d, want 2", n)
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

// Task 300, semantics corrected by task 641: loadOrInit's cross-process pass
// rewrites in-flight items to Uncertain (so the 263 in-flight gate can never
// see them again) — that Uncertain face must settle when the source mail was
// delivered. Queued/Blocked must NOT settle: the mail cursor and the
// delivery receipt record a DELIVERY (acked the moment the message lands in
// this inbox), not a consumption into a turn. A queued row was never
// admitted — settling it deleted real pending work, the exact loss the 641
// acceptance forbids ("未消费消息正确保留并可在恢复后继续处理").
func TestRecoverSettlesAdmittedResidueOnly(t *testing.T) {
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
	// Settled delivery in each state a restart can leave behind: Uncertain
	// (was in-flight — residue), Queued and Blocked (never admitted — real
	// work). The fourth item is live work with an unsettled delivery.
	settledUncertain := enqueue("processed before restart", "collab:msg_done_a")
	settledQueued := enqueue("delivered but never admitted", "collab:msg_done_b")
	settledBlocked := enqueue("blocked before admission", "collab:msg_done_c")
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
	if _, ok := states[settledUncertain]; ok {
		t.Fatal("settled Uncertain (admitted before the restart) must be dropped, not replayed onto the shelf")
	}
	// Task 641: a settled delivery is not a consumption — rows that were
	// never admitted survive with their work intact.
	if states[settledQueued] != StateQueued {
		t.Fatalf("settled Queued state = %q, want queued (delivered-but-unadmitted work survives)", states[settledQueued])
	}
	if states[settledBlocked] != StateBlocked {
		t.Fatalf("settled Blocked state = %q, want blocked (user decision, not residue)", states[settledBlocked])
	}
	if states[liveQueued] != StateQueued {
		t.Fatalf("live pending state = %q, want queued (kept for /queue review)", states[liveQueued])
	}
	if snap.RecoveredN != 3 {
		t.Fatalf("RecoveredN = %d, want 3 (every surviving pending row counts)", snap.RecoveredN)
	}
	if !snap.Paused {
		t.Fatalf("live pending work must keep the inbox paused for review: %+v", snap)
	}
}

// Task 585/641: review parks survive the settled drop. Their content did not
// durably reach the transcript (unapplied steer / snapshot failed), so a
// delivered mail is not application evidence — dropping them would silently
// delete the work the parks exist to surface. The ack-failed park drops: the
// transcript IS durable there, so the content has its application receipt.
func TestRecoverKeepsReviewParksFromSettledDrop(t *testing.T) {
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
	unappliedSteer := enqueue("steer accepted, never injected", "collab:msg_unapplied")
	snapshotFailed := enqueue("turn done, transcript lost", "collab:msg_snapshot")
	ackFailed := enqueue("turn done, ack lost", "collab:msg_ackfail")
	if err := s.SetState(unappliedSteer, StateUncertain, BlockReasonSteerUnapplied); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(snapshotFailed, StateUncertain, BlockReasonTranscriptNotDurable); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(ackFailed, StateUncertain, "turn completed but inbox acknowledgement failed"); err != nil {
		t.Fatal(err)
	}

	probe := func(m InboxItemMeta) bool {
		_, ok := strings.CutPrefix(m.Idempotency, "collab:")
		return ok // every source mail was delivered before the restart
	}
	if _, err := s.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, probe); err != nil {
		t.Fatal(err)
	}
	states := make(map[string]InboxState)
	reasons := make(map[string]string)
	for _, item := range s.Snapshot().Items {
		states[item.ID] = item.State
		reasons[item.ID] = item.BlockReason
	}
	if states[unappliedSteer] != StateUncertain || reasons[unappliedSteer] != BlockReasonSteerUnapplied {
		t.Fatalf("unapplied-steer park = (%v, %q), want kept uncertain for inspection", states[unappliedSteer], reasons[unappliedSteer])
	}
	if states[snapshotFailed] != StateUncertain || reasons[snapshotFailed] != BlockReasonTranscriptNotDurable {
		t.Fatalf("snapshot-failed park = (%v, %q), want kept uncertain for inspection", states[snapshotFailed], reasons[snapshotFailed])
	}
	if _, ok := states[ackFailed]; ok {
		t.Fatal("ack-failed park must drop: the durable transcript is the application receipt")
	}
}

// Task 589b: a steer accepted into the agent queue but not yet consumed sits a
// tool-call away from its injection point (the loader's MarkSteerConsumed at
// the next tool-round gap). A kill inside that window leaves an admitted row
// whose 570 delivery receipt was written at pump delivery — long BEFORE the
// accept — so the settled probe answers true and cannot stand in for "reached
// a turn". The accepted state must never settle: the row parks as unapplied-
// steer review work, and the park survives every later settled pass.
func TestRecoverKeepsAcceptedUninjectedSteerFromSettledDrop(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rec, err := s.Enqueue(EnqueueRequest{
		Envelope:    PromptEnvelope{SubmitText: "steer killed between accept and inject"},
		Source:      "collab:contact-x",
		Idempotency: "collab:msg_accepted_kill",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(rec.ItemID, StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}

	probe := func(m InboxItemMeta) bool {
		_, ok := strings.CutPrefix(m.Idempotency, "collab:")
		return ok // the mail was delivered (and receipted) before the accept
	}
	first, err := s.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, probe)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 {
		t.Fatalf("first pass recovered = %d, want 1 (the accepted steer parks, not drops)", first)
	}
	snap := s.Snapshot()
	var item *InboxItemMeta
	for i := range snap.Items {
		if snap.Items[i].ID == rec.ItemID {
			item = &snap.Items[i]
		}
	}
	if item == nil {
		t.Fatal("accepted-uninjected steer was dropped by the settled pass — the 589b loss face")
	}
	if item.State != StateUncertain || item.BlockReason != BlockReasonSteerUnapplied {
		t.Fatalf("parked steer = (%v, %q), want (uncertain, %q)", item.State, item.BlockReason, BlockReasonSteerUnapplied)
	}

	// A later pass (the controller re-runs recovery on every open and every
	// orphaned-steer read) must keep the park via the review-park exemption.
	if _, err := s.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, probe); err != nil {
		t.Fatal(err)
	}
	snap = s.Snapshot()
	if len(snap.Items) != 1 || snap.Items[0].ID != rec.ItemID ||
		snap.Items[0].State != StateUncertain || snap.Items[0].BlockReason != BlockReasonSteerUnapplied {
		t.Fatalf("second pass lost the park: %+v", snap.Items)
	}
	if !snap.Paused || snap.RecoveredN != 1 {
		t.Fatalf("recovery metadata = %+v, want paused with the park counted as live pending work", snap)
	}
}

// Task 641 acceptance, end to end at the store boundary: after a
// cross-process restart (loadOrInit rewrote in-flight rows to Uncertain), a
// collab row whose source mail was delivered drops as residue, while a row
// that was still queued keeps its work and the banner counts only what
// survives — consumed cross-session messages must not replay onto the
// guidance shelf, undelivered/unadmitted ones must resume.
func TestRecoverAfterRestartDropsDeliveredAdmittedKeepsQueued(t *testing.T) {
	session := filepath.Join(t.TempDir(), "s.jsonl")
	s, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	enqueueCollab := func(text, idem string) string {
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
	// Mid-turn when the restart hit: the message was delivered and its row
	// admitted into the running turn.
	midTurn := enqueueCollab("merge-line receipt", "collab:msg_midturn")
	if err := s.ClaimItem(midTurn); err != nil {
		t.Fatal(err)
	}
	// Delivered but never admitted: waiting for an idle dispatch when the
	// restart hit.
	awaiting := enqueueCollab("coordinator dispatch", "collab:msg_awaiting")
	inboxDir := s.Dir()
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
	// The pump settled both deliveries before the restart.
	delivered := map[string]bool{"msg_midturn": true, "msg_awaiting": true}
	probe := func(m InboxItemMeta) bool {
		id, ok := strings.CutPrefix(m.Idempotency, "collab:")
		if !ok || id == "" {
			return false
		}
		return delivered[id]
	}
	if _, err := reopened.RecoverOrphanedInFlightOwnedBy(func(string) bool { return false }, probe); err != nil {
		t.Fatal(err)
	}
	states := make(map[string]InboxState)
	for _, item := range reopened.Snapshot().Items {
		states[item.ID] = item.State
	}
	if _, ok := states[midTurn]; ok {
		t.Fatal("delivered row admitted mid-turn must drop after the restart, not replay onto the shelf")
	}
	if states[awaiting] != StateQueued {
		t.Fatalf("delivered row never admitted = %v, want queued (work survives and resumes)", states[awaiting])
	}
}
