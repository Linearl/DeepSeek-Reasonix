package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/memory"
)

func writeSession(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force mtime "now" so listRecentSessionFiles accepts the file.
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanSessionJSONLExtractsUsersAndTools(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s1.jsonl",
		`{"role":"user","content":"prefer tabs over spaces"}`,
		`{"role":"assistant","kind":"tool","name":"read_file"}`,
		`{"role":"assistant","kind":"tool","name":"bash"}`,
		`not-json`,
		`{"role":"assistant","content":[{"type":"text","text":"ok"}]}`,
	)
	users, tools := scanSessionJSONL(filepath.Join(dir, "s1.jsonl"), time.Now().Add(-time.Hour))
	if len(users) != 1 || !strings.Contains(users[0].Text, "prefer tabs") {
		t.Fatalf("users = %+v", users)
	}
	if len(tools) != 2 || tools[0].Name != "read_file" || tools[1].Name != "bash" {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestRunDreamSavesNewPreferencesAndSkipsDupes(t *testing.T) {
	sessions := t.TempDir()
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	writeSession(t, sessions, "a.jsonl",
		`{"role":"user","content":"prefer concise commit messages"}`,
		`{"role":"user","content":"always run go test before committing"}`,
		`{"role":"user","content":"hello"}`,
	)
	cfg := DreamDistillConfig{
		Enabled:       true,
		SessionDir:    sessions,
		Memory:        store,
		WorkspaceRoot: t.TempDir(),
	}
	report, err := RunDream(cfg)
	if err != nil {
		t.Fatalf("RunDream: %v", err)
	}
	if report.Sessions != 1 || report.Candidates < 2 || report.Saved < 2 {
		t.Fatalf("report = %+v", report)
	}
	saved := store.List()
	if len(saved) < 2 {
		t.Fatalf("saved memories = %d", len(saved))
	}
	// Second run must not duplicate.
	report2, err := RunDream(cfg)
	if err != nil {
		t.Fatalf("second RunDream: %v", err)
	}
	if report2.Saved != 0 || report2.SkippedDupe == 0 {
		t.Fatalf("second run should skip duplicates: %+v", report2)
	}
}

func TestRunDreamDisabled(t *testing.T) {
	if _, err := RunDream(DreamDistillConfig{Enabled: false}); err == nil {
		t.Fatal("disabled dream must error")
	}
}

func TestDreamToolDryRunDoesNotWrite(t *testing.T) {
	sessions := t.TempDir()
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	writeSession(t, sessions, "a.jsonl",
		`{"role":"user","content":"prefer dark theme"}`,
	)
	tool := NewDreamTool(DreamDistillConfig{Enabled: true, SessionDir: sessions, Memory: store})
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out, "candidates") && !strings.Contains(out, "Candidates") {
		t.Fatalf("dry run output = %s", out)
	}
	if n := len(store.List()); n != 0 {
		t.Fatalf("dry run wrote %d memories", n)
	}
	// Confirm writes.
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"confirm":true}`)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if n := len(store.List()); n == 0 {
		t.Fatal("confirm should have written at least one memory")
	}
}

func TestRunDistillNominatesRepeatedWorkflows(t *testing.T) {
	sessions := t.TempDir()
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	lineA := `{"role":"assistant","kind":"tool","name":"read_file"}`
	lineB := `{"role":"assistant","kind":"tool","name":"bash"}`
	// Same 2-tool sequence three times across two files.
	writeSession(t, sessions, "s1.jsonl", lineA, lineB, lineA, lineB)
	writeSession(t, sessions, "s2.jsonl", lineA, lineB)
	cfg := DreamDistillConfig{
		Enabled:         true,
		SessionDir:      sessions,
		Memory:          store,
		WorkspaceRoot:   t.TempDir(),
		DistillMinHits:  3,
	}
	report, err := RunDistill(cfg)
	if err != nil {
		t.Fatalf("RunDistill: %v", err)
	}
	if len(report.Nominations) == 0 {
		t.Fatalf("expected nominations, got %+v", report)
	}
	found := false
	for _, n := range report.Nominations {
		if len(n.Tools) == 2 && n.Tools[0] == "read_file" && n.Tools[1] == "bash" && n.Hits >= 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("read_file→bash×3 not nominated: %+v", report.Nominations)
	}
	if report.OutputPath == "" {
		t.Fatal("nominations file must be written")
	}
	if _, err := os.Stat(report.OutputPath); err != nil {
		t.Fatalf("nominations file missing: %v", err)
	}
}

func TestRunDistillDisabled(t *testing.T) {
	if _, err := RunDistill(DreamDistillConfig{Enabled: false}); err == nil {
		t.Fatal("disabled distill must error")
	}
}

func TestLooksLikePreferenceMarkers(t *testing.T) {
	if !looksLikePreference("I prefer pnpm over npm") {
		t.Fatal("prefer marker missed")
	}
	if !looksLikePreference("以后都用中文回复") {
		t.Fatal("Chinese marker missed")
	}
	if looksLikePreference("fix the login bug") {
		t.Fatal("ordinary request should not match")
	}
}
