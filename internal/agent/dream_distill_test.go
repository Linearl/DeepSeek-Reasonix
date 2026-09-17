package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

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

// TestScanSessionJSONLExtractsUsersAndTools pins the legacy shape: older
// transcripts named the tool on the record itself. Live transcripts use the
// shapes covered by the two tests below.
// liveDispatchLine is an assistant record shaped exactly like the transcript
// this host writes: every tool_calls entry carries its name at the top level.
func liveDispatchLine(names ...string) string {
	calls := make([]string, 0, len(names))
	for i, name := range names {
		calls = append(calls, fmt.Sprintf(`{"id":"call_%d","name":%q,"arguments":{}}`, i, name))
	}
	return `{"role":"assistant","id":"msg_1","tool_calls":[` + strings.Join(calls, ",") + `]}`
}

// liveCapabilityDispatch is a use_capability record that resolved to a concrete
// capability, followed by any further calls the model made in the same batch.
func liveCapabilityDispatch(capabilityID string, names ...string) string {
	calls := []string{fmt.Sprintf(`{"id":"call_cap","name":"use_capability","capability_id":%q,"arguments":"{}"}`, capabilityID)}
	for i, name := range names {
		calls = append(calls, fmt.Sprintf(`{"id":"call_extra_%d","name":%q,"arguments":{}}`, i, name))
	}
	return `{"role":"assistant","id":"msg_cap","tool_calls":[` + strings.Join(calls, ",") + `]}`
}

// liveResultLine is one tool-result record: role="tool" with the name at the
// top level, repeating the call the assistant already dispatched.
func liveResultLine(name string) string {
	return fmt.Sprintf(`{"role":"tool","id":"res_1","name":%q,"tool_call_id":"call_0","content":"ok"}`, name)
}

// Task 150: a live transcript names every tool twice -- on the assistant
// dispatch and again on the role="tool" result row -- and the dispatch may also
// arrive in the OpenAI function.name shape. The scanner read neither, so every
// live session scanned to zero tool calls and distill nominated nothing at all.
func TestScanSessionJSONLReadsLiveToolCallShapes(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "live.jsonl",
		`{"role":"user","content":"整理这个仓库的 issue"}`,
		liveDispatchLine("use_capability", "bash"),
		liveResultLine("use_capability"),
		liveResultLine("bash"),
		`{"role":"assistant","tool_calls":[{"id":"call_9","type":"function","function":{"name":"task","arguments":"{}"}}]}`,
		liveCapabilityDispatch("mcp-tool:exa/web_search_exa"),
	)
	users, tools := scanSessionJSONL(filepath.Join(dir, "live.jsonl"), time.Now().Add(-time.Hour))
	if len(users) != 1 || !strings.Contains(users[0].Text, "issue") {
		t.Fatalf("users = %+v", users)
	}
	if len(tools) == 0 {
		t.Fatal("live transcript shapes produced no tool calls: this is the task-150 failure mode where distill nominates nothing at all")
	}
	var got []string
	for _, call := range tools {
		got = append(got, call.Name)
	}
	want := "use_capability,bash,task,mcp-tool:exa/web_search_exa"
	if strings.Join(got, ",") != want {
		t.Fatalf("tools = %v, want %s (dispatch order, result rows not double-counted)", got, want)
	}
}

// The result rows are the fallback for transcripts that never persisted the
// assistant dispatch array.
func TestScanSessionJSONLUsesResultRowsWhenDispatchIsAbsent(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "results-only.jsonl",
		liveResultLine("bash"),
		liveResultLine("read_file"),
	)
	_, tools := scanSessionJSONL(filepath.Join(dir, "results-only.jsonl"), time.Now().Add(-time.Hour))
	if len(tools) != 2 || tools[0].Name != "bash" || tools[1].Name != "read_file" {
		t.Fatalf("tools = %+v, want the two result rows as the fallback channel", tools)
	}
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
	// Live shapes, and a sequence the workflow filter keeps: a named capability
	// next to a generic host verb. Pure bash chains and the bare proxy are
	// covered by the filter tests below.
	lineA := liveCapabilityDispatch("mcp-tool:exa/web_search_exa", "bash")
	lineB := liveResultLine("use_capability")
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
		if len(n.Tools) == 2 && n.Tools[0] == "mcp-tool:exa/web_search_exa" && n.Tools[1] == "bash" && n.Hits >= 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("mcp-tool:exa/web_search_exa→bash×3 not nominated: %+v", report.Nominations)
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

// Task 150: the marker list only held pronouns of instruction, so real
// constraint-shaped preferences were missed.
func TestLooksLikePreferenceMatchesConstraints(t *testing.T) {
	for _, line := range []string{
		"必须统一提交信息的格式",
		"避免在日志里写 emoji",
		"以后用 pnpm，别用 npm",
	} {
		if !looksLikePreference(line) {
			t.Fatalf("constraint marker missed in %q", line)
		}
	}
}

// Task 150: generic tool loops are not workflows -- the unfiltered run scored
// 1877 of them, led by "bash→bash". They must be rejected while the count of
// what the filter dropped stays visible.
func TestRunDistillFiltersGenericToolLoops(t *testing.T) {
	sessions := t.TempDir()
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	bashBatch := liveDispatchLine("bash", "bash", "bash")
	result := liveResultLine("bash")
	writeSession(t, sessions, "loop1.jsonl", bashBatch, result, bashBatch, result)
	writeSession(t, sessions, "loop2.jsonl", bashBatch, result)
	report, err := RunDistill(DreamDistillConfig{
		Enabled:        true,
		SessionDir:     sessions,
		Memory:         store,
		WorkspaceRoot:  t.TempDir(),
		DistillMinHits: 3,
	})
	if err != nil {
		t.Fatalf("RunDistill: %v", err)
	}
	if len(report.Nominations) != 0 {
		t.Fatalf("generic tool loops must not be nominated: %+v", report.Nominations)
	}
	if report.GenericFiltered == 0 {
		t.Fatal("the report must say how many repeated sequences the workflow filter rejected")
	}
	if report.OutputPath != "" {
		t.Fatalf("no nominations means no draft file, got %s", report.OutputPath)
	}
}

// Task 150: an ASCII-only slug stripped CJK, so unrelated Chinese preferences
// collapsed onto one name and every later one was skipped as a duplicate.
func TestDreamFactNameKeepsChineseAndStaysDistinct(t *testing.T) {
	first := dreamFactName("以后回复都用中文，不要用英文")
	second := dreamFactName("必须统一提交信息的格式，禁止使用 emoji")
	if first == second {
		t.Fatalf("two different preferences must not share a name: %q", first)
	}
	for _, name := range []string{first, second} {
		if !strings.HasPrefix(name, "dream-") {
			t.Fatalf("name %q lost its prefix", name)
		}
		han := false
		for _, r := range name {
			if unicode.Is(unicode.Han, r) {
				han = true
			}
		}
		if !han {
			t.Fatalf("name %q lost its Chinese content", name)
		}
	}
	if dreamFactName("以后回复都用中文，不要用英文") != first {
		t.Fatal("the same text must slug to the same name so a re-run dedupes instead of inflating")
	}
}

func TestRunDreamSavesChinesePreferencesWithReadableNames(t *testing.T) {
	sessions := t.TempDir()
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	writeSession(t, sessions, "zh.jsonl",
		`{"role":"user","content":"以后回复都用中文，并且必须避免废话"}`,
		`{"role":"user","content":"统一用 pnpm，别用 npm"}`,
	)
	cfg := DreamDistillConfig{Enabled: true, SessionDir: sessions, Memory: store, WorkspaceRoot: t.TempDir()}
	report, err := RunDream(cfg)
	if err != nil {
		t.Fatalf("RunDream: %v", err)
	}
	if report.Saved < 2 {
		t.Fatalf("Chinese preferences were not saved: %+v", report)
	}
	for _, name := range report.SavedNames {
		han := false
		for _, r := range name {
			if unicode.Is(unicode.Han, r) {
				han = true
			}
		}
		if !han {
			t.Fatalf("saved name %q lost its Chinese content", name)
		}
	}
	second, err := RunDream(cfg)
	if err != nil {
		t.Fatalf("second RunDream: %v", err)
	}
	if second.Saved != 0 || second.SkippedDupe == 0 {
		t.Fatalf("re-run must dedupe on the real name: %+v", second)
	}
}

// Task 150: the workflow filter keeps sequences that name a step worth
// packaging (a skill, a memory write, an MCP tool) and drops the everyday verb
// chains -- including the bare capability proxy, whose own name carries no
// meaning.
func TestWorkflowFilterKeepsNamedCapabilities(t *testing.T) {
	keep := [][]string{
		{"bash", "mcp-tool:exa/web_search_exa"},
		{"memory:remember", "bash"},
		{"task", "wait"},
	}
	for _, seq := range keep {
		if !workflowLikeSequence(seq) {
			t.Fatalf("sequence %v should be a candidate", seq)
		}
	}
	drop := [][]string{
		{"bash", "bash"},
		{"bash", "use_capability"},
		{"bash", "tool:read_file"},
		{"read_file", "write_file"},
	}
	for _, seq := range drop {
		if workflowLikeSequence(seq) {
			t.Fatalf("sequence %v is generic and must be dropped", seq)
		}
	}
}
