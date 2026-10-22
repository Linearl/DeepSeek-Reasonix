package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 1071：修复日志侧车不再被误当会话。
//
// 现场（瘦身诊断 2026-10-22 附带发现）：<stem>.repairlog.jsonl 旁生成了
// .repairlog.display-index.json / .repairlog.jsonl.meta / .repairlog.jsonl.lock，
// 且修复日志本身在 14:25 被清成两行 {"role":""}——取证时先读到完整记录行，
// 其后文件被改写。根因：IsSessionTranscriptName 把 .repairlog.jsonl 判成转录，
// catalog 修复队列把它当会话投影修复（读入→writeSessionMessagesContext 原子
// 重写），审计内容即被清空。
//
// 本文件三断言，覆盖修复日志的完整生命周期：写入→分类→投影修复尝试→读回。

// writeHealRepairRecordFixture writes one real repair-log record through the
// production path, then returns the log path.
func writeHealRepairRecordFixture(t *testing.T, dir string) (sessionPath, logPath string) {
	t.Helper()
	sessionPath = filepath.Join(dir, "sess.jsonl")
	writeRepairLogRecord(sessionPath, RepairRecord{
		At:      "2026-10-22T05:27:07Z",
		Path:    sessionPath,
		Actions: []SelfHealAction{{Action: HealActionStripImages, Status: "done"}},
		Before:  "projection_overflow",
		After:   "healthy",
	})
	logPath = sessionSiblingPath(sessionPath, selfHealRepairLogSuffix)
	if logPath == "" {
		t.Fatal("sessionSiblingPath returned empty for a repair log")
	}
	return sessionPath, logPath
}

// Test1071RepairLogNotClassifiedAsTranscript asserts the classification fix
// at the point of use: the production repair-log path must never classify as
// a transcript, so no downstream scan derives display-index/meta/lock beside
// it or rewrites it through the transcript save path.
func Test1071RepairLogNotClassifiedAsTranscript(t *testing.T) {
	dir := t.TempDir()
	sessionPath, logPath := writeHealRepairRecordFixture(t, dir)
	_ = sessionPath

	if store.IsSessionTranscriptName(filepath.Base(logPath)) {
		t.Fatalf("repair log %q classified as a transcript — this was the bug (task 1071)", filepath.Base(logPath))
	}

	// A directory scan over the session dir must not surface the repair log as
	// a session: no sidecar derivation, no phantom row.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sawRepairLog := false
	for _, e := range entries {
		if e.Name() == filepath.Base(logPath) {
			sawRepairLog = true
			continue
		}
		if store.IsSessionTranscriptName(e.Name()) {
			t.Fatalf("scan surfaced %q as a transcript — only the transcript itself should classify", e.Name())
		}
	}
	if !sawRepairLog {
		t.Fatal("fixture repair log missing from directory")
	}
}

// Test1071RepairLogSurvivesListingRepairAttempt simulates the catalog repair
// queue processing the repair-log path (as it did before the classification
// fix, or via a stale queue entry): the repair must refuse to rewrite the
// file, leaving the record byte-intact.
func Test1071RepairLogSurvivesListingRepairAttempt(t *testing.T) {
	dir := t.TempDir()
	sessionPath, logPath := writeHealRepairRecordFixture(t, dir)

	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("fixture repair log is empty before the repair attempt")
	}

	// Run the listing repair directly on the repair-log path — the exact call
	// the catalog repair queue makes. The classification gate must refuse.
	result, err := RepairSessionListingProjection(context.Background(), logPath)
	if err != nil {
		t.Fatalf("listing repair on a sidecar path must not error (refusal is a status, not an error): %v", err)
	}
	if result.Status != SessionListingRepairUnsupported {
		t.Fatalf("listing repair on repairlog status = %q, want unsupported — a sidecar must never be rewritten as a transcript", result.Status)
	}

	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("repair-log bytes changed after a listing-repair attempt:\nbefore=%q\nafter=%q", before, after)
	}

	// The record must still decode as a real RepairRecord — not {"role":""}.
	records, err := ListSessionRepairRecordsFor(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("repair records = %d, want 1 — the record was lost", len(records))
	}
	if records[0].Before != "projection_overflow" || records[0].After != "healthy" {
		t.Fatalf("repair record fields lost: %+v", records[0])
	}
}

// Test1071RepairLogLoadSessionRefuses asserts the LoadSession gate: a sidecar
// path must be refused before any lock is taken, so no downstream save path
// can ever rewrite it.
func Test1071RepairLogLoadSessionRefuses(t *testing.T) {
	dir := t.TempDir()
	sessionPath, logPath := writeHealRepairRecordFixture(t, dir)
	_ = sessionPath

	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := LoadSession(logPath); err == nil {
		t.Fatal("LoadSession on a repair-log sidecar must refuse")
	}

	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("repair-log bytes changed after a LoadSession attempt")
	}

	// The transcript itself must still load normally — the gate must not
	// over-reject a real session path.
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	if err := s.Save(sessionPath); err != nil {
		t.Fatalf("Save transcript: %v", err)
	}
	if _, err := LoadSession(sessionPath); err != nil {
		t.Fatalf("LoadSession on a real transcript must still succeed: %v", err)
	}
}

// Test1071RepairLogAppendPreservesPriorRecords asserts the append semantics:
// a second repair record must not clobber the first, and the combined log
// must still decode cleanly.
func Test1071RepairLogAppendPreservesPriorRecords(t *testing.T) {
	dir := t.TempDir()
	sessionPath, logPath := writeHealRepairRecordFixture(t, dir)

	writeRepairLogRecord(sessionPath, RepairRecord{
		At:      "2026-10-22T06:00:00Z",
		Path:    sessionPath,
		Actions: []SelfHealAction{{Action: HealActionFold, Status: "done"}},
		Before:  "projection_overflow",
		After:   "healthy",
	})

	records, err := ListSessionRepairRecordsFor(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("repair records = %d, want 2 — an append was lost", len(records))
	}

	// Both records must decode as real JSON lines, never {"role":""}.
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var probe map[string]any
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("repair-log line %d is not valid JSON: %v", i, err)
		}
		if role, ok := probe["role"]; ok {
			t.Fatalf("repair-log line %d decoded as a message (role=%v) — the record was rewritten as a transcript", i, role)
		}
		if _, ok := probe["at"]; !ok {
			t.Fatalf("repair-log line %d lost its repair fields: %s", i, line)
		}
	}
}
