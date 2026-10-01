package agent

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// Task 339 (upstream #10970 integration): an event log already past the replay
// caps used to freeze the session on the write path as well as the read path -
// Save classifies the write shape by replaying the log, so the refusal failed
// every later save. The rescue folds the log from the in-memory snapshot when
// the disk state is provably this runtime's own last write, and the save
// proceeds. Upstream #10970 behavior对照: an over-budget session gets a way out
// on its next save instead of staying bricked.

// writeReplayLimitedLogOverwrites overwrites the session's event log with a
// schema-1 stream that replays to exactly msgs and then carries enough
// empty-message appends to push the record count one step past the replay cap
// (400_001 records: the refusal fires when the 400_001st record is about to be
// applied). The stream has no trailing replace, so the trailing-replace
// salvage cannot rescue it and the refusal is the honest outcome.
func writeReplayLimitedLogOverwrites(t *testing.T, path string, msgs []provider.Message) {
	t.Helper()
	logPath := SessionEventLogPath(path)
	var b strings.Builder
	b.WriteString(`{"schema_version":1,"type":"replace","revision":1,"base_revision":0,"message_index":0,"messages":[`)
	for i, m := range msgs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":"` + m.ID + `","role":"user","content":` + strconv.Quote(m.Content) + `}`)
	}
	b.WriteString(`],"content_digest":"stale"}` + "\n")
	for i := 0; i < sessionEventReplayMaxRecords; i++ {
		b.WriteString(`{"schema_version":1,"type":"append","revision":` + strconv.Itoa(i+2) + `,"base_revision":` + strconv.Itoa(i+1) + `,"message_index":` + strconv.Itoa(len(msgs)) + `,"messages":[]}` + "\n")
	}
	if err := os.WriteFile(logPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write replay-limited log: %v", err)
	}
}

// TestSaveRescuesEventLogPastRecordCap pins the end-to-end rescue on the
// records dimension: baseline established by a real save, the log then
// overgrown past the 400k record cap behind that baseline's back, one fresh
// in-memory message, Save. The log folds from memory, the save lands, every
// message survives, and the ledger advances.
func TestSaveRescuesEventLogPastRecordCap(t *testing.T) {
	t.Setenv(SessionLogSchemaEnv, "v1") // the rescue targets the schema-1 save route
	dir := t.TempDir()
	path := filepath.Join(dir, "rescue.jsonl")

	seed := provider.Message{ID: "m0", Role: provider.RoleUser, Content: "seed"}
	s := NewSession("")
	s.Add(seed)
	if err := s.Save(path); err != nil {
		t.Fatalf("baseline save: %v", err)
	}
	baseRevision, baseDigest, err := sessionContentRevision(path)
	if err != nil || baseRevision <= 0 {
		t.Fatalf("baseline ledger: revision=%d err=%v", baseRevision, err)
	}

	// Overgrow the log behind the runtime's back; the ledger still describes
	// exactly the baseline this runtime persisted - the rescue's ownership
	// proof. Memory grows by one fresh message.
	writeReplayLimitedLogOverwrites(t, path, []provider.Message{seed})
	fresh := provider.Message{ID: "m1", Role: provider.RoleUser, Content: "fresh"}
	s.Add(fresh)

	// Precondition: the log really is refused under the replay caps.
	logPath := SessionEventLogPath(path)
	if _, err := replaySessionEventLog(logPath); !errors.Is(err, ErrSessionReplayLimitExceeded) {
		t.Fatalf("fixture log must be replay-refused, got err=%v", err)
	}

	if err := s.Save(path); err != nil {
		t.Fatalf("Save must rescue a replay-limited log it owns: %v", err)
	}

	// The log folded to a single replace snapshot carrying the full in-memory
	// snapshot, and the ledger advanced past the stale digest.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read rescued log: %v", err)
	}
	lines := 0
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines++
		}
	}
	if lines != 1 {
		t.Fatalf("event log lines after rescue = %d, want 1 folded replace", lines)
	}
	rep, err := replaySessionEventLog(logPath)
	if err != nil {
		t.Fatalf("replay after rescue: %v", err)
	}
	if len(rep.msgs) != 2 || rep.msgs[0].Content != "seed" || rep.msgs[1].Content != "fresh" {
		t.Fatalf("messages after rescue = %d (%q, %q), want [seed fresh]",
			len(rep.msgs), rep.msgs[0].Content, rep.msgs[1].Content)
	}
	revision, digest, err := sessionContentRevision(path)
	if err != nil || revision <= baseRevision {
		t.Fatalf("ledger after rescue: revision=%d base=%d err=%v", revision, baseRevision, err)
	}
	if digest == baseDigest {
		t.Fatal("ledger digest must describe the rescued content, not the stale baseline")
	}
}

// TestSaveRescueDeclinesForeignDiskState pins the honest boundary: when the
// ledger no longer describes this runtime's baseline (another writer committed
// since), the fold could discard turns it cannot see, so the rescue must
// decline and the replay refusal must surface unchanged, with the over-limit
// log left untouched.
func TestSaveRescueDeclinesForeignDiskState(t *testing.T) {
	t.Setenv(SessionLogSchemaEnv, "v1")
	dir := t.TempDir()
	path := filepath.Join(dir, "foreign.jsonl")

	seed := provider.Message{ID: "m0", Role: provider.RoleUser, Content: "seed"}
	s := NewSession("")
	s.Add(seed)
	if err := s.Save(path); err != nil {
		t.Fatalf("baseline save: %v", err)
	}

	writeReplayLimitedLogOverwrites(t, path, []provider.Message{seed})
	fresh := provider.Message{ID: "m1", Role: provider.RoleUser, Content: "fresh"}
	s.Add(fresh)

	// Another writer advances the ledger after the baseline was persisted;
	// the disk state may now hold turns this runtime has never seen.
	seedDigest, err := digestSessionMessages([]provider.Message{seed})
	if err != nil {
		t.Fatalf("digest seed: %v", err)
	}
	if _, err := recordSessionContentRevision(path, seedDigest, 1, 0); err != nil {
		t.Fatalf("simulate foreign writer: %v", err)
	}

	if err := s.Save(path); !errors.Is(err, ErrSessionReplayLimitExceeded) {
		t.Fatalf("Save must surface the replay refusal for a foreign disk state, got err=%v", err)
	}
	info, statErr := os.Stat(SessionEventLogPath(path))
	if statErr != nil {
		t.Fatalf("stat refused log: %v", statErr)
	}
	// The folded log would be a few hundred bytes; the untouched one is ~40 MB.
	if info.Size() < int64(sessionEventReplayMaxRecords) {
		t.Fatalf("refused log must be left untouched, size=%d", info.Size())
	}
}

// TestRescueDeclinesWithoutOwnershipProof covers the guard rails at unit
// scale: a runtime without a persisted baseline, a non-limit error, and a
// schema-2 DAG log each leave the log and the error untouched.
func TestRescueDeclinesWithoutOwnershipProof(t *testing.T) {
	t.Setenv(SessionLogSchemaEnv, "v1")
	dir := t.TempDir()
	path := filepath.Join(dir, "nobaseline.jsonl")

	// A tiny log whose only job is to probe as native: the guard rails below
	// fire before any replay, so it does not need to be over the caps.
	logPath := SessionEventLogPath(path)
	if err := os.WriteFile(logPath, []byte(`{"schema_version":1,"type":"replace","revision":1,"messages":[]}`+"\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if probe, err := probeSessionEventLog(path); err != nil || !probe.native {
		t.Fatalf("fixture must probe native (probe=%+v err=%v)", probe, err)
	}
	limitErr := &SessionReplayLimitError{Resource: "event_records", Value: 400_001, Limit: 400_000}

	// A runtime without a persisted baseline cannot prove ownership.
	s := NewSession("")
	if s.rescueReplayLimitedEventLog(path, nil, [sha256.Size]byte{}, limitErr) {
		t.Fatal("rescue must decline without a persisted baseline")
	}

	// A non-limit error is none of the rescue's business, whatever the
	// baseline state is.
	if s.rescueReplayLimitedEventLog(path, nil, [sha256.Size]byte{}, errors.New("plain failure")) {
		t.Fatal("rescue must decline a non-limit error")
	}

	// A schema-2 DAG log must not be folded into a whole-transcript replace:
	// its other heads would not survive the fold.
	dagPath := filepath.Join(dir, "dag.jsonl")
	if err := os.WriteFile(SessionEventLogPath(dagPath), []byte("{\"schema_version\":2,\"type\":\"heads\"}\n"), 0o600); err != nil {
		t.Fatalf("write dag log: %v", err)
	}
	if probe, err := probeSessionEventLog(dagPath); err != nil || !probe.dag {
		t.Fatalf("dag fixture must probe as dag (probe=%+v err=%v)", probe, err)
	}
	if s.rescueReplayLimitedEventLog(dagPath, nil, [sha256.Size]byte{}, limitErr) {
		t.Fatal("rescue must decline a DAG log")
	}
}

// TestEventsRotationAutoCapFoldsOnlyWhenItShrinks ports the upstream #10970
// fold discipline onto the auto-mode cap: over the cap the fold still fires
// only when it actually shrinks the log; an already-compact log over the cap
// is left in place with a greppable WARN instead of being rewritten on every
// save. The factor branch and manual mode stay byte-for-byte as before.
func TestEventsRotationAutoCapFoldsOnlyWhenItShrinks(t *testing.T) {
	resetEventsRotation(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	SetEventsAutoRotation("auto", 16, 1)
	const content int64 = 900 << 10 // 900 KiB live content
	// Over the cap and the fold at least halves the log: fold fires.
	if !gateJudges(3<<20, content) {
		t.Fatal("auto cap must fold when the fold halves the log")
	}
	// Over the cap but the folded log would land at or above half the size it
	// replaces: leave it, with a greppable WARN naming the session path.
	if gateJudges((1500<<10)+1, content) {
		t.Fatal("auto cap must not rewrite a log the fold cannot shrink")
	}
	if !strings.Contains(buf.String(), "fold would not shrink it") {
		t.Fatalf("shrink-guard skip must leave a greppable WARN, log=%q", buf.String())
	}
	if !strings.Contains(buf.String(), "path=test.events.jsonl") {
		t.Fatalf("shrink-guard WARN must attribute the session path, log=%q", buf.String())
	}
	// The factor branch keeps firing regardless of the shrink guard.
	SetEventsAutoRotation("auto", 2, 0)
	if !gateJudges((2<<20)+1, 1<<20) {
		t.Fatal("auto factor branch must keep firing")
	}
	// Manual mode stays legacy-identical over the same shapes.
	SetEventsAutoRotation("manual", 16, 1)
	if gateJudges(3<<20, content) != legacyOversized(3<<20, content) {
		t.Fatal("manual mode must keep the legacy judgment")
	}
}
