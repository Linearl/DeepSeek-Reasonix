package agent

// tl1833 unit tests for the session outline renderer. The fixtures model real
// transcript shapes dumped from live sessions in this workspace (the tl1833
// survey corpus): provider.ToolCall with a JSON-encoded `arguments` string,
// provider.ToolCallRecord under `tool_recovery` carrying the parsed arguments
// and the ToolRunState — plus the legacy shapes accepted by scanSessionJSONL.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const outlineFixtureDesc = `{"role":"assistant","id":"a1","content":"","tool_calls":[{"id":"call_1","name":"bash","resolved_name":"bash","arguments":"{\"command\":\"ls -d \\\"D:/日志/\\\"20* 2>/dev/null\",\"description\":\"列出工作日志季度目录\"}","tool_recovery":{"identity":{"session_id":"s1","turn_id":"t1","attempt_id":"x1","call_id":"call_1","canonical_tool":"bash"},"arguments":{"command":"ls -d \"D:/日志/\"20* 2>/dev/null","description":"列出工作日志季度目录"},"state":"completed","read_only":true}}]}`

// No description: the key-argument ladder answers. pattern is the first hit
// for a grep that carries only it.
const outlineFixtureArgFallback = `{"role":"assistant","id":"a2","content":"","tool_calls":[{"id":"call_2","name":"grep","resolved_name":"grep","arguments":"{\"pattern\":\"sidecar\",\"case_insensitive\":true}","tool_recovery":{"identity":{"call_id":"call_2","canonical_tool":"grep"},"arguments":{"pattern":"sidecar"},"state":"failed"}}]}`

// A bookkeeping call: only non-string arguments → bare tool name.
const outlineFixtureNoIntent = `{"role":"assistant","id":"a3","content":"","tool_calls":[{"id":"call_3","name":"todo_write","arguments":"{\"todos\":[{\"content\":\"写单测\",\"status\":\"in_progress\"}]}","tool_recovery":{"arguments":{"todos":[{"content":"写单测","status":"in_progress"}]},"state":"completed"}}]}`

// Acceptance ①: the three intent forms — model-authored description wins over
// the command; a key argument backs the call without a description; a
// bookkeeping call with only non-string arguments renders the bare tool name.
func TestOutlineIntentThreeForms(t *testing.T) {
	in := strings.Join([]string{outlineFixtureDesc, outlineFixtureArgFallback, outlineFixtureNoIntent}, "\n") + "\n"
	text, turns, truncated, err := RenderSessionOutline(strings.NewReader(in), 10*1024, OutlineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatalf("a three-turn transcript fits the budget, truncated=%v", truncated)
	}
	if turns != 3 {
		t.Fatalf("want 3 turns, got %d (%s)", turns, text)
	}
	// Form 1 — description intent, NOT the command, with the real run state.
	if !strings.Contains(text, "  - bash(completed): 列出工作日志季度目录") {
		t.Fatalf("description intent line missing:\n%s", text)
	}
	if strings.Contains(text, "ls -d") {
		t.Fatalf("the command must not leak past the description:\n%s", text)
	}
	// Form 2 — key-argument fallback, state carried through.
	if !strings.Contains(text, "  - grep(failed): sidecar") {
		t.Fatalf("key-argument fallback line missing:\n%s", text)
	}
	// Form 3 — no intent: bare tool name, no colon tail.
	if !strings.Contains(text, "  - todo_write(completed)") {
		t.Fatalf("bare tool line missing:\n%s", text)
	}
	if strings.Contains(text, "todo_write(completed):") {
		t.Fatalf("a no-intent call must not render an intent tail:\n%s", text)
	}
	if !utf8.ValidString(text) {
		t.Fatalf("outline is not valid UTF-8:\n%s", text)
	}
}

// Assistant narration and user lines frame the tool lines, cut at the survey
// caps.
func TestOutlineRendersTurnText(t *testing.T) {
	long := strings.Repeat("结论先行。", 100) // 500 runes > 240 cap
	in := `{"role":"user","content":"查一下 sidecar 的加载顺序"}
{"role":"assistant","id":"a1","content":"` + long + `","tool_calls":[{"id":"c1","name":"read_file","arguments":"{\"path\":\"internal/agent/save.go\",\"description\":\"核对 CAS 装配点\"}","tool_recovery":{"state":"completed"}}]}
{"role":"tool","tool_call_id":"c1","name":"read_file","content":"` + strings.Repeat("x", 4096) + `"}
{"role":"system","content":"You are Reasonix."}`
	text, turns, _, err := RenderSessionOutline(strings.NewReader(in), 10*1024, OutlineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if turns != 2 {
		t.Fatalf("system and tool rows are not outline turns, want 2 got %d:\n%s", turns, text)
	}
	if !strings.HasPrefix(text, "[user] 查一下 sidecar 的加载顺序\n") {
		t.Fatalf("user line missing or misplaced:\n%s", text)
	}
	if !strings.Contains(text, "[asst] ") {
		t.Fatalf("assistant line missing:\n%s", text)
	}
	// The narration is cut to 240 runes (+1 ellipsis) and never swallows the
	// tool line below it.
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "[asst] ") {
			continue
		}
		body := strings.TrimPrefix(line, "[asst] ")
		max := 240
		if strings.HasSuffix(body, "…") {
			max = 241
		}
		if utf8.RuneCountInString(body) > max {
			t.Fatalf("assistant head exceeds the cap: %d runes", utf8.RuneCountInString(body))
		}
	}
	if !strings.Contains(text, "  - read_file(completed): 核对 CAS 装配点") {
		t.Fatalf("tool intent line missing:\n%s", text)
	}
	if strings.Contains(text, "You are Reasonix") || strings.Contains(text, "xxxx") {
		t.Fatalf("system/tool rows leaked into the outline:\n%s", text)
	}
}

// Legacy shapes the scanner already tolerates (scanSessionJSONL): OpenAI
// function nesting, transcripts without a recovery receipt, and recovery-only
// arguments.
func TestOutlineLegacyShapes(t *testing.T) {
	in := `{"role":"assistant","tool_calls":[{"function":{"name":"bash","arguments":"{\"command\":\"go test ./...\"}"}}]}
{"id":"a2","content":"续前会","tool_calls":[{"id":"c2","name":"use_capability","capability_id":"mcp-tool:exa/web_search_exa","arguments":"{\"query\":\"reasonix fork\"}","tool_recovery":{"arguments":{"query":"reasonix fork"},"state":"completed"}}]}
{"id":"a3","content":"","tool_calls":[{"id":"c3","name":"read_file","arguments":"","tool_recovery":{"arguments":{"path":"save.go"},"state":"running"}}]}`
	text, turns, _, err := RenderSessionOutline(strings.NewReader(in), 10*1024, OutlineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if turns != 3 {
		t.Fatalf("want 3 turns, got %d:\n%s", turns, text)
	}
	// Legacy function shape, no recovery receipt → "done" state, command fallback.
	if !strings.Contains(text, "  - bash(done): go test ./...") {
		t.Fatalf("legacy function-name shape not rendered:\n%s", text)
	}
	// Proxied dispatch renders under the resolved capability, not the proxy.
	if !strings.Contains(text, "mcp-tool:exa/web_search_exa(completed): reasonix fork") {
		t.Fatalf("capability dispatch not resolved:\n%s", text)
	}
	if strings.Contains(text, "use_capability(") {
		t.Fatalf("raw proxy name leaked:\n%s", text)
	}
	// Recovery-only arguments backfill an empty wire string; in-flight state passes through.
	if !strings.Contains(text, "  - read_file(running): save.go") {
		t.Fatalf("recovery-argument fallback missing:\n%s", text)
	}
}

// Rune safety: CJK content cut at the caps stays valid UTF-8, and a single
// turn larger than the whole budget is hard-capped at the byte budget with
// truncated reported.
func TestOutlineRuneAndBudgetCaps(t *testing.T) {
	cjk := strings.Repeat("跨会话尾部", 100) // 500 runes, 1500 bytes
	in := `{"role":"assistant","content":"` + cjk + `","tool_calls":[{"id":"c1","name":"bash","arguments":"{\"command\":\"` + cjk + `\"}"}]}`
	text, turns, truncated, err := RenderSessionOutline(strings.NewReader(in), 256, OutlineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if turns != 1 || !truncated {
		t.Fatalf("one oversized turn: turns=%d truncated=%v", turns, truncated)
	}
	if !utf8.ValidString(text) {
		t.Fatalf("budget cap split a rune:\n%q", text)
	}
	if len(text) > 256 {
		t.Fatalf("outline exceeds the hard budget: %d > 256 bytes", len(text))
	}
}

// Budget walk: newest turns win, and the count of what did not fit is
// reported as truncated.
func TestOutlineBudgetWalkNewestFirst(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString(`{"role":"user","content":"第` + strconv.Itoa(i) + `轮"}`)
		b.WriteString("\n")
	}
	text, turns, truncated, err := RenderSessionOutline(strings.NewReader(b.String()), 10*1024, OutlineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if turns != 50 || truncated {
		t.Fatalf("tiny turns must all fit: turns=%d truncated=%v", turns, truncated)
	}
	if !strings.Contains(text, "第0轮") || !strings.Contains(text, "第1轮") {
		t.Fatalf("early turns missing: %s", text[:120])
	}

	// Same transcript at a tiny budget: the newest turns fit, older ones do
	// not, and the walk reports the cut.
	text, turns, truncated, err = RenderSessionOutline(strings.NewReader(b.String()), 40, OutlineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if turns < 1 || turns == 50 {
		t.Fatalf("a 40-byte budget must include a bounded prefix of the newest turns, got %d", turns)
	}
	if !truncated {
		t.Fatal("a budget walk that stopped early must report truncated")
	}
	if !strings.Contains(text, "第49轮") {
		t.Fatalf("the newest turn must win the walk:\n%s", text)
	}
	if strings.Contains(text, "第0轮") {
		t.Fatalf("an old turn must not ride along:\n%s", text)
	}
}

// A torn trailing record (crash mid-append) keeps everything before it; a
// file that never decodes is an error, not a silent empty outline.
func TestOutlineTornTailAndGarbage(t *testing.T) {
	good := `{"role":"user","content":"完整的一轮"}`
	torn := `{"role":"assistant","content":"被截断的`
	text, turns, _, err := RenderSessionOutline(strings.NewReader(good+"\n"+torn), 10*1024, OutlineOptions{})
	if err != nil {
		t.Fatalf("a torn tail must not fail the render: %v", err)
	}
	if turns != 1 || !strings.Contains(text, "完整的一轮") {
		t.Fatalf("the torn tail lost the good prefix: turns=%d %q", turns, text)
	}
	if _, _, _, err := RenderSessionOutline(strings.NewReader("这不是会话文件\n第二行也是垃圾"), 10*1024, OutlineOptions{}); err == nil {
		t.Fatal("a non-transcript file must surface an error, not an empty outline")
	}
}

// Streaming discipline: a multi-MB tool OUTPUT row is skipped without being
// retained — the outline of a huge transcript stays bounded and correct.
func TestOutlineSkipsHugeToolRows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "huge.jsonl")
	var b strings.Builder
	b.WriteString(`{"role":"user","content":"跑一次全量构建"}` + "\n")
	for i := 0; i < 8; i++ {
		// 4 MB of tool output per row, ~32 MB total: whole-file loading would
		// be visible here; the renderer must stream past it.
		b.WriteString(`{"role":"tool","tool_call_id":"c","name":"bash","content":"` + strings.Repeat("0", 4*1024*1024) + `"}` + "\n")
	}
	b.WriteString(`{"role":"assistant","content":"","tool_calls":[{"id":"c","name":"bash","arguments":"{\"command\":\"go build ./...\",\"description\":\"全量构建\"}","tool_recovery":{"state":"completed"}}]}` + "\n")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	text, turns, _, err := RenderSessionOutline(f, 10*1024, OutlineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if turns != 2 || !strings.Contains(text, "  - bash(completed): 全量构建") {
		t.Fatalf("huge tool rows must vanish from the outline: turns=%d\n%s", turns, text)
	}
	if !strings.Contains(text, "跑一次全量构建") {
		t.Fatalf("the framing user turn is missing:\n%s", text)
	}
}
