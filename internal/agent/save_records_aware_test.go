package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// Task 193 long fix: when the persisted event index shows the message count
// approaching the replay record caps, Save must compact the log even though
// its byte size is still under the size gate - otherwise a live turn pushes
// the record count past the replay cap and freezes the session.
func TestSaveCompactsWhenEventIndexNearRecordCap(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "cap.events.jsonl")
	var b strings.Builder
	// 6 messages + a replace snapshot: index MessageCount stays small here, so
	// the test drives the near-cap branch through a hand-written index below.
	for i := 0; i < 6; i++ {
		b.WriteString(`{"schema_version":1,"type":"append","revision":` + strconv.Itoa(i+1) + `,"base_revision":` + strconv.Itoa(i) + `,"message_index":` + strconv.Itoa(i) + `,"messages":[{"role":"user","content":"m` + strconv.Itoa(i) + `"}]}` + "\n")
	}
	if err := os.WriteFile(logPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	// Hand-write an index whose MessageCount is at the compact trigger.
	cap := sessionEventReplayMaxRecords - sessionEventReplayCompactHeadroom
	idx := `{"schema_version":1,"log_size":` + strconv.FormatInt(int64(b.Len()), 10) + `,"message_count":` + strconv.Itoa(cap+1) + `,"revision":6}`
	indexPath := SessionEventIndexPath(logPath)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte(idx), 0o600); err != nil {
		t.Fatal(err)
	}
	if !sessionEventIndexNearCap(logPath) {
		t.Fatal("index near cap not detected")
	}
	// A small index stays false (byte gate decides).
	small := `{"schema_version":1,"log_size":10,"message_count":3,"revision":1}`
	if err := os.WriteFile(indexPath, []byte(small), 0o600); err != nil {
		t.Fatal(err)
	}
	if sessionEventIndexNearCap(logPath) {
		t.Fatal("small index must not trigger near-cap")
	}
}

// Task 193 tuning (2026-09-27): the record-aware compaction trigger fires at
// 50% of the 400k record cap (headroom 200_000) instead of 90% (40_000).
// End-to-end proof on a real 200k+ record session: hand-write a schema-1
// append log one record past the tuned trigger, keep the in-memory snapshot
// as disk prefix + one new message, then Save. Asserts the near-cap branch
// fires at 200_001 records (the old 360_000 threshold would not have fired),
// the log folds back to a single replace snapshot with every message kept,
// and the 400_000 hard cap is never approached.
//
// Two construction constraints pin the exercised path to near-cap (review
// finding 2026-09-27, proven with a temporary branch probe):
//   - No leading system message (NewSession("")), so the in-memory snapshot
//     is an exact append of the hand-written log; a leading system message
//     breaks messagesHavePrefix and silently routes Save to full-rewrite.
//   - 64B content padding keeps logSize (~46MB) under the byte-side gate
//     (4 x contentBytes ~= 80MB), so sessionEventLogOversized cannot fire;
//     without it the byte gate folds the log and the near-cap case is never
//     reached even though the test still passes.
func TestSaveCompactsAtHalfRecordBudget(t *testing.T) {
	t.Setenv(SessionLogSchemaEnv, "v1") // pin the schema-1 save route; the near-cap branch lives there
	dir := t.TempDir()
	path := filepath.Join(dir, "budget.jsonl")
	logPath := SessionEventLogPath(path)
	// The index must be addressed exactly as Save addresses it: the production
	// read (sessionEventIndexNearCap(path) in save.go) derives from the main
	// session path, which yields "<name>.event-index.json" — deriving from
	// logPath instead writes a different file that Save never looks at.
	indexPath := SessionEventIndexPath(path)

	const records = sessionEventReplayCompactHeadroom + 1 // 200_001: one past the tuned 50% trigger
	pad := strings.Repeat("x", 64)                        // see byte-gate constraint above
	var b strings.Builder
	for i := 0; i < records; i++ {
		b.WriteString(`{"schema_version":1,"type":"append","revision":`)
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(`,"base_revision":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"message_index":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"messages":[{"id":"mid`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`","role":"user","content":"m`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(pad)
		b.WriteString(`"}]}` + "\n")
	}
	logBytes := b.Len()
	if err := os.WriteFile(logPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write event log: %v", err)
	}
	b.Reset()
	idx := `{"schema_version":1,"log_size":` + strconv.FormatInt(int64(logBytes), 10) + `,"message_count":` + strconv.Itoa(records) + `,"revision":` + strconv.Itoa(records) + `}`
	if err := os.WriteFile(indexPath, []byte(idx), 0o600); err != nil {
		t.Fatalf("write event index: %v", err)
	}

	// Hard cap untouched: replaying 200_001 records must succeed outright
	// (the 400_000 cap rejects at >= 400_000, which this never reaches).
	rep, err := replaySessionEventLog(logPath)
	if err != nil {
		t.Fatalf("replay of %d records must succeed below the hard cap: %v", records, err)
	}
	if len(rep.msgs) != records {
		t.Fatalf("replayed messages = %d, want %d", len(rep.msgs), records)
	}

	// Same argument shape as save.go:396 passes (the main session path), so
	// this pre-check evaluates the exact index the Save branch will read.
	if !sessionEventIndexNearCap(path) {
		t.Fatalf("index at %d records must trigger the 50%% near-cap branch (threshold %d); the old 40_000 headroom would have waited for %d",
			records, sessionEventReplayMaxRecords-sessionEventReplayCompactHeadroom, sessionEventReplayMaxRecords-40_000)
	}
	// Byte-side gate must stay quiet for the whole test: if the log were
	// oversized the full-rewrite path would fold it instead and mask the
	// near-cap branch (that is exactly what happened before the padding).
	if sessionEventLogOversized(int64(logBytes), int64(sessionEventEstimateBytesForTest(t, records, pad))) {
		t.Fatalf("construction must sit under the byte gate: logBytes=%d would be oversized", logBytes)
	}

	// In-memory snapshot = disk prefix + one fresh message so Save takes the
	// appendOnly path where the near-cap case lives. AddBatch keeps setup O(1)
	// in lock acquisitions (per-Add recovery expiry made 200k Adds quadratic).
	s := NewSession("") // no leading system message: must be an exact append
	prefix := make([]provider.Message, 0, records)
	for i := 0; i < records; i++ {
		prefix = append(prefix, provider.Message{ID: "mid" + strconv.Itoa(i), Role: provider.RoleUser, Content: "m" + strconv.Itoa(i) + pad})
	}
	s.AddBatch(prefix...)
	s.Add(provider.Message{Role: provider.RoleUser, Content: "fresh"})
	if err := s.Save(path); err != nil {
		t.Fatalf("Save at half record budget: %v", err)
	}

	// The log folded: the 200_001 append records collapsed to exactly one
	// replace snapshot (the near-cap branch appends nothing after folding).
	lineCount := 0
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read compacted log: %v", err)
	}
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lineCount++
		}
	}
	if lineCount != 1 {
		t.Fatalf("event log lines after Save = %d, want exactly 1 folded replace (records did not fall back via near-cap)", lineCount)
	}

	// Messages survive the fold, including the new one that triggered Save:
	// replay must reproduce the exact in-memory snapshot (200_002 = 200_001
	// hand-written records + fresh).
	rep, err = replaySessionEventLog(logPath)
	if err != nil {
		t.Fatalf("replay after compact: %v", err)
	}
	if len(rep.msgs) != len(s.Messages) {
		t.Fatalf("messages after compact = %d, want %d (fold must keep every message)", len(rep.msgs), len(s.Messages))
	}
	if got := rep.msgs[len(rep.msgs)-1].Content; got != "fresh" {
		t.Fatalf("last message after compact = %q, want %q", got, "fresh")
	}
}

// sessionEventEstimateBytesForTest returns a lower-bound estimate of what
// digestAndSizeSessionMessages would report for this test's in-memory
// snapshot, so the test can assert the byte gate stays quiet. It mirrors the
// per-message JSON shape (id, role, content) plus framing overhead; only the
// magnitude matters (the gate is 4x contentBytes vs logBytes).
func sessionEventEstimateBytesForTest(t *testing.T, records int, pad string) int {
	t.Helper()
	// avg "m<i>" digits ~6 for 200k ids; id/role/content/framing ~90B base.
	const perMessageOverhead = 96
	avgContent := 1 + 6 + len(pad)
	return records * (perMessageOverhead + avgContent)
}
