package agent

// tl1833 integration tests: read_session_tail's outline mode against the real
// tool face — coverage gain over full mode at the same budget, intent lines in
// the output, and the zero-change pin on the historical full path.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// tailFixtureSession writes a tool-heavy transcript: each turn is a small user
// line + a small assistant dispatch (description armed, tl1613 style) + a ~4 KiB
// tool OUTPUT row — the shape that makes full mode burn its whole budget on two
// turns of output bytes.
func tailFixtureSession(t *testing.T, turns int) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "peer.jsonl")
	var b strings.Builder
	for i := 0; i < turns; i++ {
		b.WriteString(`{"role":"user","content":"第` + strconv.Itoa(i) + `轮 派活请求"}` + "\n")
		b.WriteString(`{"role":"assistant","id":"a` + strconv.Itoa(i) + `","content":"","tool_calls":[{"id":"c` + strconv.Itoa(i) + `","name":"bash","resolved_name":"bash","arguments":"{\"command\":\"go test -run Case` + strconv.Itoa(i) + ` ./internal/agent/\",\"description\":\"验证第` + strconv.Itoa(i) + `轮改动\"}","tool_recovery":{"state":"completed"}}]}` + "\n")
		b.WriteString(`{"role":"tool","tool_call_id":"c` + strconv.Itoa(i) + `","name":"bash","content":"` + strings.Repeat("x", 4096) + `"}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func runTail(t *testing.T, dir, args string) string {
	t.Helper()
	tl := NewReadSessionTailTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir})
	out, err := tl.Execute(context.Background(), []byte(args))
	if err != nil {
		t.Fatalf("Execute(%s): %v", args, err)
	}
	return out
}

func countTurnMarkers(s string) int { return strings.Count(s, `{"role":"user"`) }

// Acceptance ②: at the same 10 KiB budget the outline covers ≥10x the turns of
// the full mode, and its output carries the intent lines.
func TestReadSessionTailOutlineCoversMoreTurns(t *testing.T) {
	dir, _ := tailFixtureSession(t, 40)

	fullRaw := runTail(t, dir, `{"target":"peer"}`)
	var full struct {
		Bytes     int    `json:"bytes"`
		Truncated bool   `json:"truncated"`
		Tail      string `json:"tail"`
	}
	if err := json.Unmarshal([]byte(fullRaw), &full); err != nil {
		t.Fatalf("full envelope: %v (%s)", err, fullRaw[:120])
	}
	fullTurns := countTurnMarkers(full.Tail)
	if fullTurns < 1 {
		t.Fatalf("full mode must still cover at least one turn: %s", fullRaw[:200])
	}

	outRaw := runTail(t, dir, `{"target":"peer","mode":"outline"}`)
	var outline struct {
		Mode      string `json:"mode"`
		Turns     int    `json:"turns"`
		Bytes     int    `json:"bytes"`
		Truncated bool   `json:"truncated"`
		Tail      string `json:"tail"`
	}
	if err := json.Unmarshal([]byte(outRaw), &outline); err != nil {
		t.Fatalf("outline envelope: %v (%s)", err, outRaw[:120])
	}
	// Turns counts rendered records: 40 user + 40 assistant dispatches = 80.
	if outline.Mode != "outline" || outline.Turns != 80 {
		t.Fatalf("outline envelope must report mode=outline and all 80 rendered records: %+v", outline)
	}
	if outline.Bytes > 10*1024 {
		t.Fatalf("outline exceeds the default budget: %d bytes", outline.Bytes)
	}
	if outline.Truncated {
		t.Fatal("40 small outline turns fit the 10 KiB budget; truncated must be false")
	}

	// Intent lines: the newest dispatch is visible with its description, and
	// the covered window spans the whole session.
	if !strings.Contains(outline.Tail, "  - bash(completed): 验证第39轮改动") {
		t.Fatalf("intent line for the newest dispatch missing:\n%s", outline.Tail)
	}
	if !strings.Contains(outline.Tail, "[user] 第0轮 派活请求") {
		t.Fatalf("the oldest covered turn is missing:\n%s", outline.Tail)
	}

	// The acceptance ratio, measured on the tool outputs themselves, on one
	// shared unit: user turns covered within the budget.
	outlineUserTurns := strings.Count(outline.Tail, "[user] ")
	if outlineUserTurns != 40 {
		t.Fatalf("all 40 user turns must be covered, got %d", outlineUserTurns)
	}
	if outlineUserTurns < 10*fullTurns {
		t.Fatalf("coverage gain too small: outline=%d turns vs full=%d turns (want ≥10x)",
			outlineUserTurns, fullTurns)
	}
	t.Logf("coverage at 10 KiB: full=%d user turns, outline=%d user turns (%.1fx)",
		fullTurns, outlineUserTurns, float64(outlineUserTurns)/float64(fullTurns))
}

// Acceptance ③: the full path is byte-for-byte unchanged. Omitting `mode` and
// passing mode="full" must produce IDENTICAL outputs, and the envelope must
// still carry the historical keys with the historical semantics (raw trailing
// bytes cut at a line boundary).
func TestReadSessionTailFullModeUnchanged(t *testing.T) {
	dir, path := tailFixtureSession(t, 6)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	omitted := runTail(t, dir, `{"target":"peer"}`)
	explicit := runTail(t, dir, `{"target":"peer","mode":"full"}`)
	if omitted != explicit {
		t.Fatal("omitted mode and mode=full must be byte-identical")
	}

	var env struct {
		Title     string `json:"title"`
		Purpose   string `json:"purpose"`
		ContactID string `json:"contactId"`
		TopicID   string `json:"topicId"`
		Bytes     int    `json:"bytes"`
		Truncated bool   `json:"truncated"`
		Tail      string `json:"tail"`
		Mode      string `json:"mode"`
		Turns     *int   `json:"turns"`
	}
	if err := json.Unmarshal([]byte(explicit), &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	// 6 turns ≈ 26 KiB > 10 KiB budget → the historical line-boundary cut.
	if env.Bytes > 10*1024 {
		t.Fatalf("full mode must cut at the budget: bytes=%d", env.Bytes)
	}
	if env.Truncated != (env.Bytes == 10*1024) {
		t.Fatalf("the historical truncated formula (bytes == budget) must hold: bytes=%d truncated=%v", env.Bytes, env.Truncated)
	}
	if env.Title != "peer" {
		t.Fatalf("directory title must still be the stem fallback, got %q", env.Title)
	}
	if !strings.Contains(env.Tail, `{"role":"user"`) || strings.Contains(env.Tail, "[asst] ") {
		t.Fatal("full tail must stay raw transcript bytes")
	}
	// The tail is the file's trailing bytes ending on a line boundary.
	if !strings.HasSuffix(env.Tail, "\n") {
		t.Fatal("the full-mode cut must end on a line boundary")
	}
	if !strings.HasSuffix(string(raw), env.Tail) {
		t.Fatal("the full tail must be a suffix of the transcript file")
	}
	// The zero-change pin also covers the envelope: no outline keys leak into
	// the full output.
	if env.Mode != "" || env.Turns != nil {
		t.Fatalf("full mode must not grow outline-only keys: mode=%q turns=%v", env.Mode, env.Turns)
	}

	// Below the budget the historical path returns the file verbatim.
	smallDir, smallPath := tailFixtureSession(t, 2)
	smallRaw, err := os.ReadFile(smallPath)
	if err != nil {
		t.Fatal(err)
	}
	small := runTail(t, smallDir, `{"target":"peer"}`)
	var smallEnv struct {
		Bytes     int    `json:"bytes"`
		Truncated bool   `json:"truncated"`
		Tail      string `json:"tail"`
	}
	if err := json.Unmarshal([]byte(small), &smallEnv); err != nil {
		t.Fatal(err)
	}
	if smallEnv.Truncated || smallEnv.Bytes != len(smallRaw) || smallEnv.Tail != string(smallRaw) {
		t.Fatalf("a sub-budget session must come back verbatim: bytes=%d/%d truncated=%v",
			smallEnv.Bytes, len(smallRaw), smallEnv.Truncated)
	}
}

// An unknown mode is refused with the valid values named, not silently
// treated as full.
func TestReadSessionTailUnknownModeRefused(t *testing.T) {
	dir, _ := tailFixtureSession(t, 2)
	tl := NewReadSessionTailTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir})
	_, err := tl.Execute(context.Background(), []byte(`{"target":"peer","mode":"brief"}`))
	if err == nil || !strings.Contains(err.Error(), `"full"`) || !strings.Contains(err.Error(), `"outline"`) {
		t.Fatalf("an unknown mode must name the valid values, got %v", err)
	}
}

// Acceptance ④ (consumer walkthrough, in-process form): the same outline a
// coordinating session reads must answer "what did this session do, with what
// outcome, what is it doing now" — description intents, argument fallbacks and
// bare bookkeeping lines all in one read.
func TestReadSessionTailOutlineSemanticsForCoordination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.jsonl")
	var b strings.Builder
	// A real-ish work slice: dispatch with description, an unlabeled grep, and
	// a todo bookkeeping call — the three intent forms behind the survey's
	// 91.7% lenient rate.
	b.WriteString(`{"role":"user","content":"把反馈池过一遍"}` + "\n")
	b.WriteString(`{"role":"assistant","id":"a0","content":"反馈分诊线开工","tool_calls":[{"id":"c0","name":"grep","resolved_name":"grep","arguments":"{\"pattern\":\"C-20261126\"}","tool_recovery":{"state":"completed"}}]}` + "\n")
	b.WriteString(`{"role":"assistant","id":"a1","content":"","tool_calls":[{"id":"c1","name":"edit_file","resolved_name":"edit_file","arguments":"{\"path\":\"feedback/pool.md\",\"description\":\"归档反馈 C-20261126-01\"}","tool_recovery":{"state":"failed"}}]}` + "\n")
	b.WriteString(`{"role":"assistant","id":"a2","content":"","tool_calls":[{"id":"c2","name":"todo_write","arguments":"{\"todos\":[]}","tool_recovery":{"state":"completed"}}]}` + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out := runTail(t, dir, `{"target":"worker","mode":"outline"}`)
	var env struct {
		Tail string `json:"tail"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[user] 把反馈池过一遍",
		"[asst] 反馈分诊线开工",
		"  - grep(completed): C-20261126",           // argument fallback
		"  - edit_file(failed): 归档反馈 C-20261126-01", // description + real state
		"  - todo_write(completed)",                 // bare bookkeeping name
	} {
		if !strings.Contains(env.Tail, want) {
			t.Fatalf("coordination read is missing %q:\n%s", want, env.Tail)
		}
	}
}
