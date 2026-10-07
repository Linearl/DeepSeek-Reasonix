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

// 验收⑤（任务 360）：本地档与弹窗隐私声明同口径——不写路径/密钥。breadcrumbs
// 的 console.error/bridge 文本是现实的路径与密钥载体，沉淀侧必须用上传路径
// （crash_app.go scrubSensitiveText）同一套清洗器处理，测试断言脱敏生效。
func TestReportJankRecordScrubsPathsAndSecrets(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	record := `{"label":"performance.longtask","reason":"long task 900ms",` +
		`"snapshot":{"reason":"long task 900ms",` +
		`"longTaskFrames":[{"label":"renderChat (C:\\Users\\yinji\\app\\assets\\index.js:1:2)","samples":5}]},` +
		`"breadcrumbs":[{"t":1,"cat":"console.error","msg":"read failed C:\\Users\\yinji\\notes.txt api_key=sk-proj-abcdefghijklmnopqrstu"}]}`
	app.ReportJankRecord(record)
	raw, err := os.ReadFile(jankDayPath(time.Now()))
	if err != nil {
		t.Fatalf("jank file missing: %v", err)
	}
	// 文件字节层面不得出现用户名与密钥明文（含 reason/label 等字段整体复核）。
	for _, secret := range []string{"yinji", "sk-proj-abcdefghijklmnopqrstu"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("jank file leaks %q: %s", secret, raw)
		}
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("line is not json: %v", err)
	}
	if decoded["reason"] != "long task 900ms" {
		t.Fatalf("reason must survive scrubbing, got %v", decoded["reason"])
	}
	snapshot := decoded["snapshot"].(map[string]any)
	frames := snapshot["longTaskFrames"].([]any)
	frame := frames[0].(map[string]any)
	if frame["label"] != `renderChat (C:\Users\_\app\assets\index.js:1:2)` {
		t.Fatalf("frame label path not scrubbed: %v", frame["label"])
	}
	breadcrumbs := decoded["breadcrumbs"].([]any)
	crumb := breadcrumbs[0].(map[string]any)
	want := `read failed C:\Users\_\notes.txt api_key=[redacted]`
	if crumb["msg"] != want {
		t.Fatalf("breadcrumb msg = %q, want %q", crumb["msg"], want)
	}
}

func TestReportJankRecordCapsOversizePayload(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	// Prose-like filler: the scrubber collapses base64/hex blobs to [redacted-*]
	// before the size check, so the cap is exercised with text that survives it.
	big := strings.Repeat("lorem ipsum dolor sit amet ", 4000)
	app.ReportJankRecord(`{"reason":"huge","blob":"` + big + `"}`)
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
