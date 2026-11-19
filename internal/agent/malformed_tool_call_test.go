package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1707 (external issue #103): the malformed-tool-call detector must catch the
// wild shape (bare <bash>/<command>/<arg_key> markup in the visible text of a
// zero-tool-call round) while never flagging markup shown as an EXAMPLE inside
// fenced code blocks or inline code spans. The false-negative face is the
// silent autopilot stop the issue reports; the false-positive face costs at
// most one notice plus one cheap re-ask round — so the fence/span stripping is
// the load-bearing half and every rule below is pinned.
func TestDetectMalformedToolCallText(t *testing.T) {
	// The issue-#103 field shape: normal-sounding progress prose followed by
	// spilled tool-call markup the model believed it had emitted properly.
	const wildShape = "本轮结果 改动统计不完整/未记录检查 3 工具\n" +
		"<bash>\n<command>git status</command>\n<arg_key>content</arg_key>\n</bash>"
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "wild shape from issue #103",
			text: wildShape,
			want: []string{"bash", "command", "arg_key"},
		},
		{
			name: "attribute-bearing open tag",
			text: `working on it <bash -c "ls">`,
			want: []string{"bash"},
		},
		{
			name: "close tag alone (open tag truncated away)",
			text: "running now </command>",
			want: []string{"command"},
		},
		{
			name: "example inside a fenced code block is not flagged",
			text: "The tool call format looks like this:\n\n```xml\n<bash>\n<command>ls</command>\n<arg_key>x</arg_key>\n</bash>\n```\n\nTell me if you need changes.",
			want: nil,
		},
		{
			name: "example inside an inline code span is not flagged",
			text: "the tag `</command>` closes the element",
			want: nil,
		},
		{
			name: "prose markup outside the fence still counts when a fence follows",
			text: "first <bash> bare\n\n```python\nprint(1)\n```",
			want: []string{"bash"},
		},
		{
			name: "ordinary words never match",
			text: "Run the bash command to build. The command output is below.",
			want: nil,
		},
		{
			name: "near-miss tag names do not match",
			text: "<bashism> and <commander> are not tool-call markup",
			want: nil,
		},
		{
			name: "unrelated angle-bracket tags do not match",
			text: "see <system> and </thinking> for details",
			want: nil,
		},
		{
			name: "empty text",
			text: "",
			want: nil,
		},
	}
	for _, tc := range cases {
		got := detectMalformedToolCallText(tc.text)
		if len(got) != len(tc.want) {
			t.Errorf("%s: detectMalformedToolCallText = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: detectMalformedToolCallText[%d] = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// Guard pins (mirrors TestEmptyFinalNotice): the user-facing line stays short
// and free of internal diagnostics; the facts (tags, provider) live in the
// expandable detail.
func TestMalformedToolCallNoticeWiring(t *testing.T) {
	msg := malformedToolCallNotice()
	for _, hidden := range []string{"malformed tool-call markup detected", "provider ", "recovery "} {
		if strings.Contains(msg, hidden) {
			t.Errorf("notice %q should not expose internal diagnostic %q", msg, hidden)
		}
	}
	detail := malformedToolCallDetail([]string{"bash", "command"}, "deepseek-flash")
	for _, want := range []string{"bash, command", "deepseek-flash", "not executed"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q missing %q", detail, want)
		}
	}
	if got := malformedToolCallStageDetail(1); got != "recovery 1/1: re-ask for structured tool_calls" {
		t.Errorf("stage detail = %q", got)
	}
}

// countingSink extends incompleteReadEventSink with a per-code counter (the
// budget-exhaustion pin must count detections, not just see one).
type countingSink struct {
	incompleteReadEventSink
}

func (s *countingSink) countCode(code string) int {
	n := 0
	for _, e := range s.events {
		if e.Code == code {
			n++
		}
	}
	return n
}

// malformedNoticeLevels pins the level split: detection = Warn, re-ask = Info.
func malformedNoticeLevels(s *countingSink, code string) []event.Level {
	var levels []event.Level
	for _, e := range s.events {
		if e.Code == code {
			levels = append(levels, e.Level)
		}
	}
	return levels
}

// End-to-end nail (recovery succeeds): round 1 carries the wild shape, the
// re-ask round answers with a REAL structured tool call, and the turn closes
// with a normal final. The detection notice and the single re-ask notice must
// both be on the transcript, exactly one host instruction injected, the tool
// actually executed, and the provider called exactly three times.
func TestMalformedToolCallDetectedAndOneReAskRecovers(t *testing.T) {
	const wildShape = "本轮结果 改动统计不完整/未记录检查 3 工具\n<bash><command>git status</command><arg_key>content</arg_key></bash>"
	sink := &countingSink{}
	prov := &scriptedProvider{name: "deepseek-flash", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: wildShape}, {Type: provider.ChunkDone}},
		{toolCallChunk("r1", "read_file", `{"path":"a.go"}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "All checks passed: 3 tools recorded."}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	reg.Add(fakeReadFileTool{})
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "check the tools"); err != nil {
		t.Fatalf("run with malformed-tool-call recovery failed: %v", err)
	}
	if prov.call != 3 {
		t.Fatalf("provider calls = %d, want exactly 3 (malformed round + re-ask round + final)", prov.call)
	}
	if sink.countCode(event.NoticeCodeMalformedToolCall) != 1 {
		t.Fatal("no (or multiple) malformed_tool_call detection notice — the broken shape was silent or duplicated")
	}
	if sink.countCode(event.NoticeCodeMalformedToolCallRetry) != 1 {
		t.Fatal("no (or multiple) malformed_tool_call_retry notice — the single re-ask round never fired or fired twice")
	}
	levels := malformedNoticeLevels(sink, event.NoticeCodeMalformedToolCall)
	if len(levels) != 1 || levels[0] != event.LevelWarn {
		t.Fatalf("detection notice level = %v, want exactly one Warn", levels)
	}
	asked := userInstructions(t, a, "re-send them NOW as real structured tool calls")
	if len(asked) != 1 {
		t.Fatalf("host re-ask instructions = %d, want exactly 1 (bounded recovery)", len(asked))
	}
	if toolResult(a.Session(), "read_file") == "" {
		t.Fatal("the re-ask round's structured tool call never executed")
	}
}

// End-to-end nail (budget stops): every further malformed round recovers
// nothing. The run still ends cleanly — the detection notice is the visible
// face of the stop, never silent — with exactly one re-ask spent, no tool
// executed, and no run error surfaced.
func TestMalformedToolCallBudgetStopsAfterOneReAsk(t *testing.T) {
	// Neutral shape on purpose: after the budget is spent the round falls
	// through to the ordinary nudge chain, so the text must not trip any of
	// those detectors — this pin isolates the malformed-call budget alone.
	const wildShape = "checking now <command>git status</command>"
	sink := &countingSink{}
	prov := &scriptedProvider{name: "deepseek-flash", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: wildShape}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: wildShape}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "check the tools"); err != nil {
		t.Fatalf("second malformed round must end the turn cleanly with the notice on the transcript, got error: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want exactly 2 (one re-ask spent, budget stops further replay)", prov.call)
	}
	if got := sink.countCode(event.NoticeCodeMalformedToolCall); got != 2 {
		t.Fatalf("detection notices = %d, want one per malformed round (2)", got)
	}
	if got := sink.countCode(event.NoticeCodeMalformedToolCallRetry); got != 1 {
		t.Fatalf("re-ask notices = %d, want exactly 1 (hard ceiling maxMalformedToolCallRecoveries)", got)
	}
	if asked := userInstructions(t, a, "re-send them NOW as real structured tool calls"); len(asked) != 1 {
		t.Fatalf("host re-ask instructions = %d, want exactly 1", len(asked))
	}
}

// Zero-false-positive nail: an ordinary answer that TEACHES the markup format
// (examples inside fenced code blocks) must pass through untouched — no
// notice, no re-ask, one provider call, clean end.
func TestNormalAnswerWithMarkupExampleFiresNoNotice(t *testing.T) {
	const teaching = "工具调用格式如下：\n\n```xml\n<bash>\n<command>git status</command>\n<arg_key>content</arg_key>\n</bash>\n```\n\n以上格式说明完毕，如需调整告诉我。\n行内形式写作 `</command>`。"
	sink := &countingSink{}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: teaching}, {Type: provider.ChunkDone}},
	}}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession("sys"), Options{}, sink)

	if err := a.Run(context.Background(), "explain the format"); err != nil {
		t.Fatalf("normal run failed: %v", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 (no re-ask round injected)", prov.call)
	}
	if sink.countCode(event.NoticeCodeMalformedToolCall) != 0 {
		t.Fatal("a fenced code-block example fired the malformed_tool_call notice — false positive")
	}
	if sink.countCode(event.NoticeCodeMalformedToolCallRetry) != 0 {
		t.Fatal("a fenced code-block example fired the re-ask notice — false positive")
	}
	if asked := userInstructions(t, a, "re-send them NOW as real structured tool calls"); len(asked) != 0 {
		t.Fatalf("host re-ask instructions = %d, want 0 — the teaching answer was nagged", len(asked))
	}
}
