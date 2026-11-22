package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/store"
	"reasonix/internal/tool"
)

// tl1748 pins: the rev-lag takeover (agent.SaveAdoptOverStaleDisk) rewrites the
// transcript from the live snapshot, which used to silently destroy the disk
// side's unique messages — the 2026-11-21 field case lost a user decision
// message that way. Three obligations, one test each, none of which may change
// the takeover decision itself:
//
//  ① observability — the decision log carries the revision gap and the
//     abandoned messages (id/time/role, user ones flagged);
//  ② injection consistency — the abandoned line's user messages reach the
//     live conversation as one origin=host relay (persisted, model-visible,
//     non-protocol so the tl1602 interrupted-turn rebuild keeps it);
//  ③ non-destruction — the abandoned messages land in full in
//     <stem>.conflict-preserved.jsonl; a declined takeover writes nothing.

// newPreserveObservingController is newTakeoverLadderController plus a captured
// event slice, so tests can assert the recovery notice.
func newPreserveObservingController(t *testing.T, path string, live *agent.Session) (*Controller, *[]event.Event) {
	t.Helper()
	writer, err := agent.AcquireSessionWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	exec := agent.New(&streamlessProvider{}, reg, live, agent.Options{}, event.Discard)
	if err := writer.Bind(exec.Session(), agent.NextSessionWriteGeneration()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Release)
	sink, _, events := collectSink()
	c := New(Options{
		Runner:      exec,
		Executor:    exec,
		Sink:        sink,
		SessionDir:  filepath.Dir(path),
		SessionPath: path,
	})
	t.Cleanup(func() { c.Close(); c.autosaveWG.Wait() })
	return c, events
}

// findSlogLine returns the first captured slog line whose msg equals want,
// parsed into key=value fields.
func findSlogLine(t *testing.T, logs string, msg string) map[string]string {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(logs))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		fields := parseSlogTextLine(scanner.Text())
		if fields["msg"] == msg {
			return fields
		}
	}
	t.Fatalf("slog line %q not found in captured log:\n%s", msg, logs)
	return nil
}

// readPreserveRecords decodes <stem>.conflict-preserved.jsonl.
func readPreserveRecords(t *testing.T, path string) []takeoverPreserveRecord {
	t.Helper()
	file, err := os.Open(store.SessionConflictPreserveLog(path))
	if err != nil {
		t.Fatalf("open preserve log: %v", err)
	}
	defer file.Close()
	var out []takeoverPreserveRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for scanner.Scan() {
		var rec takeoverPreserveRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("decode preserve record: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

// TestCheckpointRevLagTakeoverLogsRevLagAndAbandonedDetail pins acceptance ①:
// the mid-turn checkpoint takeover logs the revision gap and the abandoned
// messages with user ones flagged, and the conflicts.jsonl record carries the
// preserve/relay accounting.
func TestCheckpointRevLagTakeoverLogsRevLagAndAbandonedDetail(t *testing.T) {
	logs := captureForkTraceLog(t)
	path := filepath.Join(t.TempDir(), "checkpoint-observe.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	c := newTakeoverLadderController(t, path, live)

	if err := c.checkpointToolTranscript(); err != nil {
		t.Fatalf("checkpoint after takeover escalation = %v, want nil", err)
	}

	// The main decision line now carries the rev-lag difference.
	main := findSlogLine(t, logs.String(), "controller: snapshot conflict; live transcript took over the dead disk writer's state")
	if main["rev_lag"] != "1" {
		t.Fatalf("rev_lag = %q, want 1 (disk %d - base %d)", main["rev_lag"], conflict.DiskRevision, conflict.BaseRevision)
	}
	// The abandoned-side detail line names the disk-only user message.
	detail := findSlogLine(t, logs.String(), "controller: rev-lag takeover committed; abandoned disk side had unique messages (preserved + relayed)")
	if detail["disk_only_messages"] != "1" || detail["disk_only_user_messages"] != "1" {
		t.Fatalf("disk_only counts = %s/%s, want 1/1", detail["disk_only_messages"], detail["disk_only_user_messages"])
	}
	summary := detail["disk_only_summary"]
	if !strings.Contains(summary, "role=USER") || !strings.Contains(summary, "dead-tail") {
		t.Fatalf("disk_only_summary = %q, want the USER-flagged dead-tail entry", summary)
	}
	if detail["injected"] != "true" || !strings.HasSuffix(detail["preserved_ref"], store.SessionConflictPreserveLogSuffix) {
		t.Fatalf("detail preserve/relay fields = ref %q injected %q, want preserved ref + injected", detail["preserved_ref"], detail["injected"])
	}
	// The conflicts.jsonl record carries the same accounting (content-free).
	rec := lastConflictDiagnostic(t, path)
	if rec.Outcome != "diverged_takeover_committed" {
		t.Fatalf("diagnostic outcome = %q, want diverged_takeover_committed", rec.Outcome)
	}
	if rec.TakeoverDiskOnly != 1 || rec.TakeoverDiskOnlyUser != 1 || rec.TakeoverPreserved != 1 {
		t.Fatalf("diagnostic takeover counts = %d/%d/%d, want 1/1/1", rec.TakeoverDiskOnly, rec.TakeoverDiskOnlyUser, rec.TakeoverPreserved)
	}
	if rec.TakeoverPreserveRef == "" || !rec.TakeoverInjected {
		t.Fatalf("diagnostic preserve ref %q injected %v, want both set", rec.TakeoverPreserveRef, rec.TakeoverInjected)
	}
}

// TestTakeoverPreservesAndRelaysAbandonedUserMessages pins acceptances ② and ③:
// the takeover preserves the abandoned line's user message in full and relays
// it into the live conversation as origin=host guidance.
func TestTakeoverPreservesAndRelaysAbandonedUserMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preserve-relay.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	c, events := newPreserveObservingController(t, path, live)

	recoveredPath, outcome, recoverErr := c.recoverSnapshotConflict(path, conflict, false)
	if recoverErr != nil || outcome != conflictTookOver || recoveredPath != path {
		t.Fatalf("recover = %v/%v/%v, want the takeover on the original path", recoveredPath, outcome, recoverErr)
	}

	// ③ Preservation, readable back in full.
	records := readPreserveRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("preserve records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.Reason != forkReasonRevLagTakeover || rec.DiskOnlyUser != 1 || len(rec.Messages) != 1 {
		t.Fatalf("preserve record = reason %q user %d msgs %d, want rev_lag_takeover/1/1", rec.Reason, rec.DiskOnlyUser, len(rec.Messages))
	}
	if got := rec.Messages[0]; got.Content != "dead-tail" || got.Role != provider.RoleUser {
		t.Fatalf("preserved message = %s/%q, want the full abandoned user message", got.Role, got.Content)
	}
	if rec.DiskRevision != conflict.DiskRevision {
		t.Fatalf("preserve record disk revision = %d, want %d", rec.DiskRevision, conflict.DiskRevision)
	}

	// ② Injection: one origin=host relay in the live conversation, carrying
	// the user's words, timestamped, non-protocol (tl1602 rebuild keeps it).
	snapshot := live.Snapshot()
	var relay *provider.Message
	for i := range snapshot {
		m := &snapshot[i]
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, TakeoverRecoveryRelayMarker) {
			relay = m
		}
	}
	if relay == nil {
		t.Fatalf("no relay message in live transcript (%d messages)", len(snapshot))
	}
	if !strings.Contains(relay.Content, "dead-tail") || !strings.Contains(relay.Content, "[sent at") {
		t.Fatalf("relay content = %q, want the abandoned message text with its timestamp", relay.Content)
	}
	if agent.IsHostProtocolMessage(*relay) {
		t.Fatal("relay classified as host protocol; the tl1602 interrupted-turn rebuild would drop it again")
	}

	// The user-facing notice names what happened to the forked-line messages.
	noticeFound := false
	for _, e := range *events {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeSessionRecoveryAdopted &&
			strings.Contains(e.Text, "other version line") && strings.Contains(e.Text, store.SessionConflictPreserveLogSuffix) {
			noticeFound = true
		}
	}
	if !noticeFound {
		t.Fatalf("recovery notice not found among %d events", len(*events))
	}
}

// TestTakeoverDeclinedWritesNoPreserveLog pins the discipline's other half: a
// declined takeover (the dead writer came back) lost nothing, so it must not
// manufacture preserve files or relay messages.
func TestTakeoverDeclinedWritesNoPreserveLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preserve-declined.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: provider.RoleUser, Content: "alive-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	c, events := newPreserveObservingController(t, path, live)

	_, outcome, recoverErr := c.recoverSnapshotConflict(path, conflict, false)
	if recoverErr != nil {
		t.Fatalf("recover: %v", recoverErr)
	}
	if outcome == conflictTookOver {
		t.Fatal("takeover committed while the disk writer was alive")
	}
	if _, statErr := os.Stat(store.SessionConflictPreserveLog(path)); !os.IsNotExist(statErr) {
		t.Fatalf("preserve log exists after a declined takeover (stat err %v)", statErr)
	}
	for _, m := range live.Snapshot() {
		if strings.Contains(m.Content, TakeoverRecoveryRelayMarker) {
			t.Fatal("relay message injected on a declined takeover")
		}
	}
	if len(*events) == 0 {
		t.Fatal("no events emitted; want the pre-existing conflict handling untouched")
	}
}

// TestTakeoverSupersetRewriteIsLossless pins the 22:59 field shape: the disk
// side's content is fully covered by the live line (same words, different
// writer-minted IDs), so the rewrite is lossless — logged once, no preserve
// file, no relay.
func TestTakeoverSupersetRewriteIsLossless(t *testing.T) {
	logs := captureForkTraceLog(t)
	path := filepath.Join(t.TempDir(), "preserve-superset.jsonl")
	live := agent.NewSession("sys")
	live.AddBatch(provider.Message{Role: "user", Content: "m1"}, provider.Message{Role: "assistant", Content: "m2"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: "user", Content: "shared-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	// The live line diverged (an extra message inserted) but still contains
	// the disk side's every message — superset by content, not by ID.
	live.AddBatch(provider.Message{Role: "user", Content: "live-only"},
		provider.Message{Role: "user", Content: "shared-tail"},
		provider.Message{Role: "assistant", Content: "live-final"})
	saveErr := live.SaveSnapshot(path)
	var conflict *agent.SessionSnapshotConflictError
	if !errors.As(saveErr, &conflict) || conflict == nil || conflict.Kind != agent.SessionSnapshotConflictDiverged {
		t.Fatalf("conflict = %v, want diverged", saveErr)
	}
	if conflict.SnapshotMessages <= conflict.ExistingMessages {
		t.Fatalf("fixture shape = snapshot %d disk %d, want the takeover order", conflict.SnapshotMessages, conflict.ExistingMessages)
	}
	c, _ := newPreserveObservingController(t, path, live)

	if _, outcome, recoverErr := c.recoverSnapshotConflict(path, conflict, false); recoverErr != nil || outcome != conflictTookOver {
		t.Fatalf("takeover outcome = %v err %v, want conflictTookOver", outcome, recoverErr)
	}
	lossless := findSlogLine(t, logs.String(), "controller: rev-lag takeover committed; abandoned disk side fully covered by the live transcript (lossless rewrite)")
	if lossless["disk_only_messages"] != "" || lossless["rev_lag"] != "1" {
		t.Fatalf("lossless line = %v, want empty disk_only and rev_lag 1", lossless)
	}
	if _, statErr := os.Stat(store.SessionConflictPreserveLog(path)); !os.IsNotExist(statErr) {
		t.Fatalf("preserve log written for a lossless rewrite (stat err %v)", statErr)
	}
	for _, m := range live.Snapshot() {
		if strings.Contains(m.Content, TakeoverRecoveryRelayMarker) {
			t.Fatal("relay injected for a lossless rewrite")
		}
	}
}

// TestAdmissionHealTakeoverPreservesAndRelays pins the third takeover site:
// the diverged turn-admission self-heal (the path the 2026-11-21 field case
// actually retried) carries the same preserve + relay + accounting as the
// ladder-side takeover.
func TestAdmissionHealTakeoverPreservesAndRelays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preserve-admission.jsonl")
	live := agent.NewSession("sys")
	live.AddBatch(provider.Message{Role: "user", Content: "m1"}, provider.Message{Role: "assistant", Content: "m2"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	loaded.Add(provider.Message{Role: "user", Content: "dead-tail"})
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("dead writer save: %v", err)
	}
	live.AddBatch(provider.Message{Role: "user", Content: "live3"}, provider.Message{Role: "assistant", Content: "live4"})
	saveErr := live.SaveSnapshot(path)
	var conflict *agent.SessionSnapshotConflictError
	if !errors.As(saveErr, &conflict) || conflict == nil || conflict.Kind != agent.SessionSnapshotConflictDiverged {
		t.Fatalf("live save err = %v, want diverged conflict", saveErr)
	}
	c, _ := newPreserveObservingController(t, path, live)

	if !c.healDivergedSnapshotConflict(conflict) {
		t.Fatal("admission self-heal declined the takeover")
	}
	records := readPreserveRecords(t, path)
	if len(records) != 1 || len(records[0].Messages) != 1 || records[0].Messages[0].Content != "dead-tail" {
		t.Fatalf("preserve records = %+v, want one record with the dead-tail message", records)
	}
	if records[0].Mode != "admission" {
		t.Fatalf("preserve record mode = %q, want admission", records[0].Mode)
	}
	found := false
	for _, m := range live.Snapshot() {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, TakeoverRecoveryRelayMarker) && strings.Contains(m.Content, "dead-tail") {
			found = true
		}
	}
	if !found {
		t.Fatal("no relay message after the admission-side takeover")
	}
	rec := lastConflictDiagnostic(t, path)
	if rec.Mode != "admission" || rec.TakeoverDiskOnly != 1 || rec.TakeoverPreserved != 1 || !rec.TakeoverInjected {
		t.Fatalf("admission diagnostic = mode %q only %d preserved %d injected %v, want admission/1/1/true", rec.Mode, rec.TakeoverDiskOnly, rec.TakeoverPreserved, rec.TakeoverInjected)
	}
}
