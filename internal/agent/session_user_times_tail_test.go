package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// writeTailEventLog writes a schema-2 event log beside sessionPath. Each entry
// is encoded as one JSONL record, matching the on-disk shape the reader scans.
func writeTailEventLog(t *testing.T, sessionPath string, entries []map[string]any) {
	t.Helper()
	var sb strings.Builder
	for _, entry := range entries {
		b, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(store.SessionEventLog(sessionPath), []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write event log: %v", err)
	}
}

func tailMessageEntry(id, role string, at int64, content string) map[string]any {
	return map[string]any{
		"schema_version": 2,
		"type":           "message",
		"id":             id,
		"at":             "2026-09-18T00:00:00Z",
		"msgs": []map[string]any{
			{"role": role, "created_at": at, "createdAt": at, "content": content},
		},
	}
}

func tailPatchEntry(target string, at int64) map[string]any {
	return map[string]any{
		"schema_version": 2,
		"type":           "patch",
		"target":         target,
		"msgs": []map[string]any{
			{"role": "user", "createdAt": at, "content": "patched"},
		},
	}
}

func TestLoadSessionUserTimesTailMapsUserMessagesOnly(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	writeTailEventLog(t, sessionPath, []map[string]any{
		tailMessageEntry("m1", "user", 1000, "first"),
		tailMessageEntry("m2", "assistant", 2000, "reply"),
		tailMessageEntry("m3", "user", 3000, "second"),
	})

	times, ok := LoadSessionUserTimesTail(sessionPath, 0)
	if !ok {
		t.Fatalf("scan should reach the start of a small log")
	}
	if len(times) != 2 {
		t.Fatalf("expected only the two user messages, got %v", times)
	}
	if times["m1"] != 1000 || times["m3"] != 3000 {
		t.Fatalf("unexpected times: %v", times)
	}
	if _, found := times["m2"]; found {
		t.Fatalf("assistant message must not contribute a time: %v", times)
	}
}

func TestLoadSessionUserTimesTailNewestRecordWins(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	writeTailEventLog(t, sessionPath, []map[string]any{
		tailMessageEntry("m1", "user", 1000, "first"),
		tailPatchEntry("m1", 4000),
	})

	times, _ := LoadSessionUserTimesTail(sessionPath, 0)
	if times["m1"] != 4000 {
		t.Fatalf("a later patch must supersede the message entry, got %v", times)
	}
}

func TestLoadSessionUserTimesTailBudgetReturnsSuffixOnly(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	entries := make([]map[string]any, 0, 40)
	for i := range 40 {
		entries = append(entries, tailMessageEntry(fmt.Sprintf("m%02d", i), "user", int64(1000+i), strings.Repeat("x", 200)))
	}
	writeTailEventLog(t, sessionPath, entries)

	times, ok := LoadSessionUserTimesTail(sessionPath, 1024)
	if ok {
		t.Fatalf("a 1 KiB budget cannot reach the start of a ~9 KiB log")
	}
	if len(times) == 0 {
		t.Fatalf("budget-limited scan should still return the tail it read")
	}
	if _, found := times["m39"]; !found {
		t.Fatalf("newest record must be included: %v", times)
	}
	if _, found := times["m00"]; found {
		t.Fatalf("oldest record must be beyond the budget: %v", times)
	}
}

func TestLoadSessionUserTimesTailSpansChunks(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	writeTailEventLog(t, sessionPath, []map[string]any{
		tailMessageEntry("m1", "user", 111, "a"),
		tailMessageEntry("m2", "user", 222, strings.Repeat("y", 300)),
		tailMessageEntry("m3", "user", 333, "c"),
	})

	// A chunk far smaller than a single record forces the cross-chunk line
	// reassembly path to run.
	times, ok := loadSessionUserTimesTail(sessionPath, 1<<20, 64)
	if !ok {
		t.Fatalf("small chunk with a large budget should still complete")
	}
	for id, want := range map[string]int64{"m1": 111, "m2": 222, "m3": 333} {
		if times[id] != want {
			t.Fatalf("id %s: got %v want %v (all=%v)", id, times[id], want, times)
		}
	}
}

func TestLoadSessionUserTimesTailMissingLog(t *testing.T) {
	times, ok := LoadSessionUserTimesTail(filepath.Join(t.TempDir(), "absent.jsonl"), 0)
	if ok || len(times) != 0 {
		t.Fatalf("missing log must report an incomplete empty scan, got %v ok=%v", times, ok)
	}
}
