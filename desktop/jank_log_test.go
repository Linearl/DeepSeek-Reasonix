package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Task 360: the jank sink must land valid JSONL under logs/perf/ with a server
// timestamp, reject non-objects, and rotate at the file ceiling.
func TestReportJankRecordWritesJSONL(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ReportJankRecord(`{"label":"long task 120ms","reason":"long task 120ms","snapshot":{"uptimeMs":4200},"breadcrumbs":[{"t":1,"cat":"performance","msg":"x"}]}`)

	path := jankDayPath(time.Now())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("jank file missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("line is not json: %v", err)
	}
	if record["ts"] == nil || record["ts"] == "" {
		t.Fatal("server-side ts missing")
	}
	if record["reason"] != "long task 120ms" {
		t.Fatalf("reason = %v", record["reason"])
	}
	snapshot, ok := record["snapshot"].(map[string]any)
	if !ok || snapshot["uptimeMs"] != 4200.0 {
		t.Fatalf("snapshot = %v", record["snapshot"])
	}
	// The jank file lives next to the perf samples so timelines align by day.
	if want := filepath.Join(perfMonitorDir(), "jank-"); !strings.HasPrefix(path, want) {
		t.Fatalf("path = %q, want prefix %q", path, want)
	}
}

func TestReportJankRecordRejectsInvalidAndEmpty(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ReportJankRecord("")
	app.ReportJankRecord("not json at all")
	app.ReportJankRecord(`[1,2,3]`) // valid json, not an object
	entries, err := os.ReadDir(perfMonitorDir())
	if err == nil && len(entries) != 0 {
		t.Fatalf("expected no jank files, got %d", len(entries))
	}
}

func TestReportJankRecordCapsOversizePayload(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	big := make([]byte, 80<<10)
	for i := range big {
		big[i] = 'x'
	}
	app.ReportJankRecord(`{"reason":"huge","blob":"` + string(big) + `"}`)
	raw, err := os.ReadFile(jankDayPath(time.Now()))
	if err != nil {
		t.Fatalf("jank file missing: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("truncated record is not json: %v", err)
	}
	if record["truncated"] != true {
		t.Fatal("oversize record not marked truncated")
	}
	if record["reason"] != "huge" {
		t.Fatalf("reason = %v", record["reason"])
	}
}

func TestAppendJankLineRotatesAtCeiling(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := filepath.Join(perfMonitorDir(), "jank-20000101.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, jankMaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendJankLine(path, []byte(`{"reason":"after-rotation"}`)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotation backup missing: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("rotated file is empty")
	}
	if !strings.Contains(scanner.Text(), "after-rotation") {
		t.Fatalf("rotated file first line = %q", scanner.Text())
	}
}
