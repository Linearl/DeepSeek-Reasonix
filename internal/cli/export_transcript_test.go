package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// exportTestSession builds a session covering every wire shape the 任务 229 G6
// contract promises: plain user/assistant text, a tool call pair with one
// failed (durable recovery state) call, and distinct tool names.
func exportTestSession(t *testing.T, path string) {
	t.Helper()
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "check the file"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "reading", ToolCalls: []provider.ToolCall{
		{ID: "call-1", Name: "read_file", Recovery: &provider.ToolCallRecord{State: provider.ToolRunFailed}},
		{ID: "call-2", Name: "grep"},
	}})
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "boom", ToolRunState: provider.ToolRunFailed})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}

func TestRunExportTranscriptNormalizedJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	exportTestSession(t, path)

	var out string
	var rc int
	out = captureStdout(t, func() {
		rc = runExportTranscript([]string{"--session", path})
	})
	if rc != verdictExitConfirmed {
		t.Fatalf("exit=%d want 0", rc)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// 8 = system, user, assistant, 2 tool_calls, real tool result, loader-repair
	// placeholder for the interrupted call-2, final assistant. The repair rows
	// are the single-source payoff: export shows exactly what a reload feeds
	// the model, placeholder included.
	if len(lines) != 8 {
		t.Fatalf("lines=%d want 8: %q", len(lines), out)
	}
	records := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d not JSON: %v", i, err)
		}
		if want, ok := rec["seq"].(float64); !ok || want != float64(i+1) {
			t.Fatalf("line %d seq=%v want %d", i, rec["seq"], i+1)
		}
		records = append(records, rec)
	}
	if records[0]["role"] != "system" {
		t.Fatalf("record 0 = %v", records[0])
	}
	if records[1]["role"] != "user" || records[1]["content"] != "check the file" {
		t.Fatalf("record 1 = %v", records[1])
	}
	if records[2]["role"] != "assistant" || records[2]["content"] != "reading" {
		t.Fatalf("record 2 = %v", records[2])
	}
	// assistant tool_calls expand to one record each, in call order.
	if records[3]["role"] != "tool_call" || records[3]["tool_name"] != "read_file" || records[3]["tool_call_id"] != "call-1" {
		t.Fatalf("record 3 = %v", records[3])
	}
	if records[3]["is_error"] != true {
		t.Fatalf("failed call must carry is_error: %v", records[3])
	}
	if records[4]["role"] != "tool_call" || records[4]["tool_name"] != "grep" {
		t.Fatalf("record 4 = %v", records[4])
	}
	if _, ok := records[4]["is_error"]; ok {
		t.Fatalf("healthy call must omit is_error: %v", records[4])
	}
	// tool results keep name/call linkage and the failed state.
	if records[5]["role"] != "tool" || records[5]["tool_call_id"] != "call-1" || records[5]["content"] != "boom" || records[5]["is_error"] != true {
		t.Fatalf("record 5 = %v", records[5])
	}
	if records[7]["role"] != "assistant" || records[7]["content"] != "done" {
		t.Fatalf("record 7 = %v", records[7])
	}
	// no integrity markers on a clean log.
	if strings.Contains(out, "_damaged") || strings.Contains(out, "_tail_truncated") {
		t.Fatalf("clean log must not carry markers: %q", out)
	}
}

func TestRunExportTranscriptOutFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	exportTestSession(t, path)
	outPath := filepath.Join(dir, "out.jsonl")
	if rc := runExportTranscript([]string{"--session", path, "--out", outPath}); rc != verdictExitConfirmed {
		t.Fatalf("exit=%d want 0", rc)
	}
	blob, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(blob)), "\n")); got != 8 {
		t.Fatalf("out lines=%d want 8", got)
	}
}

func TestRunExportTranscriptUsageErrors(t *testing.T) {
	if rc := runExportTranscript(nil); rc != verdictExitUsage {
		t.Fatalf("missing --session exit=%d want 2", rc)
	}
	if rc := runExportTranscript([]string{"--session", filepath.Join(t.TempDir(), "ghost.jsonl")}); rc != verdictExitUsage {
		t.Fatalf("missing transcript exit=%d want 2", rc)
	}
}

func TestRunExportTranscriptDamagedMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	exportTestSession(t, path)
	// Append a torn record the way an interrupted writer would leave it; the
	// loader salvages the bytes and reports the damage without a healing save
	// (export is read-only).
	logPath := store.SessionEventLog(path)
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"schema_version":1,"type":"ap`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var out string
	out = captureStdout(t, func() {
		if rc := runExportTranscript([]string{"--session", path}); rc != verdictExitConfirmed {
			t.Errorf("exit=%d want 0", rc)
		}
	})
	if !strings.Contains(out, `"_damaged"`) {
		t.Fatalf("damaged log must carry the terminal marker: %q", out)
	}
	// The marker comes after every real record (terminal position).
	if strings.LastIndex(out, `"role":"assistant"`) > strings.LastIndex(out, `"_damaged"`) {
		t.Fatalf("marker must be terminal: %q", out)
	}
}
