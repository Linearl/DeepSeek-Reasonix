package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl-1166 acceptance suite: a reasoning-only final (thinking streamed, visible
// answer empty) gets exactly ONE continuation reminder (B); a follow-up that
// is STILL reasoning-only is displayed as a synthesized body carrying BOTH
// thinking segments in chronological order (C); tool rounds and normal
// answers are untouched; every injection leaves a grep-able slog trail.

func reasoningOnlyTurn(reasoning string) []provider.Chunk {
	return []provider.Chunk{
		{Type: provider.ChunkReasoning, Text: reasoning},
		{Type: provider.ChunkDone},
	}
}

func countUserMessagesContaining(s *Session, needle string) int {
	count := 0
	for _, m := range s.Messages {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, needle) {
			count++
		}
	}
	return count
}

// 验收 1: 「只有 reasoning、text 空」→ B 被触发恰好 1 次，续写轮给出正文，
// 第一轮 reasoning 作为合法历史保留在 assistant 消息上。
func TestReasoningOnlyFinalInjectsExactlyOneContinuation(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		reasoningOnlyTurn("the answer is 42 because"),
		textTurn("The answer is 42."),
	}}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{ReasoningOnlyContinuation: true}, event.Discard)

	if err := a.Run(context.Background(), "answer me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want original round + one continuation", prov.call)
	}
	if got := countUserMessagesContaining(a.sess.conversation, "thinking process"); got != 1 {
		t.Fatalf("continuation reminders = %d, want exactly 1", got)
	}
	msgs := a.sess.conversation.Snapshot()
	if len(msgs) < 2 || msgs[1].Role != provider.RoleAssistant || msgs[1].ReasoningContent != "the answer is 42 because" {
		t.Fatalf("original reasoning not preserved on the committed assistant turn: %+v", msgs[1])
	}
	if got := lastAssistantContent(a.sess.conversation); got != "The answer is 42." {
		t.Fatalf("final answer = %q, want the continuation round's text", got)
	}
}

// 验收 2: B 后仍只有 reasoning → C 生效，正文含两段思考、顺序正确、都不丢；
// 且不再注入第二次提醒。
func TestReasoningOnlyFallbackBodyCarriesBothThinkingSegments(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		reasoningOnlyTurn("FIRST-THINKING original turn"),
		reasoningOnlyTurn("SECOND-THINKING after reminder"),
	}}
	sink := &recordSink{}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{ReasoningOnlyContinuation: true}, sink)

	if err := a.Run(context.Background(), "answer me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want original + one continuation (no further injection)", prov.call)
	}
	if got := countUserMessagesContaining(a.sess.conversation, "thinking process"); got != 1 {
		t.Fatalf("continuation reminders = %d, want exactly 1 even when the fallback fires", got)
	}
	body := lastAssistantContent(a.sess.conversation)
	if !strings.Contains(body, i18n.M.ReasoningOnlyFinalBody) {
		t.Fatalf("fallback body missing the host marker: %q", body)
	}
	first, second := strings.Index(body, "FIRST-THINKING original turn"), strings.Index(body, "SECOND-THINKING after reminder")
	if first < 0 || second < 0 {
		t.Fatalf("fallback body must carry BOTH thinking segments, got %q", body)
	}
	if first > second {
		t.Fatalf("thinking segments out of chronological order: %q", body)
	}
	if !strings.Contains(body, i18n.M.ReasoningOnlyThinking1) || !strings.Contains(body, i18n.M.ReasoningOnlyThinking2) {
		t.Fatalf("segment labels missing from the fallback body: %q", body)
	}
	notices := 0
	for _, ev := range sink.kinds(event.Notice) {
		if ev.Code == event.NoticeCodeReasoningOnlyFinal {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("reasoning_only_final notices = %d, want 1", notices)
	}
}

// 验收 3: 只有 reasoning + 有 tool call → 正常工具轮，不触发 B/C。
func TestReasoningOnlyWithToolCallSkipsContinuation(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			{Type: provider.ChunkReasoning, Text: "I should call the tool first."},
			toolCallChunk("c1", "echo", `{"text":"hi"}`),
			{Type: provider.ChunkDone},
		},
		textTurn("Tool done, here is the answer."),
	}}
	a := New(prov, echoRegistry(), NewSession(""), Options{ReasoningOnlyContinuation: true}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "use the tool"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want tool round + final", prov.call)
	}
	if got := countUserMessagesContaining(a.sess.conversation, "thinking process"); got != 0 {
		t.Fatalf("continuation reminders = %d, want 0 for a normal tool round", got)
	}
	if got := lastAssistantContent(a.sess.conversation); got != "Tool done, here is the answer." {
		t.Fatalf("final answer = %q, want the post-tool text untouched", got)
	}
}

// 验收 4（回归）: 正常有正文 turn 行为完全不变——一次请求、无注入。
func TestNormalFinalUnchangedByReasoningOnlyContinuation(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		textTurn("plain answer"),
	}}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{ReasoningOnlyContinuation: true}, event.Discard)

	if err := a.Run(context.Background(), "answer me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want exactly one clean request", prov.call)
	}
	msgs := a.sess.conversation.Snapshot()
	for _, m := range msgs {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "thinking process") {
			t.Fatalf("normal turn must not receive a continuation reminder: %q", m.Content)
		}
	}
	if len(msgs) != 2 || msgs[0].Role != provider.RoleUser || msgs[1].Role != provider.RoleAssistant {
		t.Fatalf("session shape changed for a normal turn: %+v", msgs)
	}
}

// 开关关闭（Options 缺省 false；生产由 config 缺省开解析）→ 保持历史行为：
// reasoning-only 轮静默接受，正文为空，不注入。
func TestReasoningOnlyContinuationSwitchOffKeepsHistoricalBehavior(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		reasoningOnlyTurn("thinking only"),
	}}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)

	if err := a.Run(context.Background(), "answer me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want 1 (no continuation while off)", prov.call)
	}
	if got := countUserMessagesContaining(a.sess.conversation, "thinking process"); got != 0 {
		t.Fatalf("continuation reminders = %d, want 0 while the switch is off", got)
	}
	if got := lastAssistantContent(a.sess.conversation); got != "" {
		t.Fatalf("last assistant content = %q, want the historical empty body", got)
	}
}

// 验收 5（留痕）: 注入点与保底合成点都留 slog，可 grep 统计。
func TestReasoningOnlyContinuationLeavesSlogTrail(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		reasoningOnlyTurn("first"),
		reasoningOnlyTurn("second"),
	}}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{ReasoningOnlyContinuation: true}, event.Discard)

	if err := a.Run(context.Background(), "answer me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	trail := buf.String()
	if !strings.Contains(trail, "injecting one continuation reminder") || !strings.Contains(trail, "tl-1166") {
		t.Fatalf("injection slog trail missing: %q", trail)
	}
	if !strings.Contains(trail, "synthesizing fallback body") {
		t.Fatalf("fallback slog trail missing: %q", trail)
	}
	if strings.Count(trail, "injecting one continuation reminder") != 1 {
		t.Fatalf("injection trail must appear exactly once: %q", trail)
	}
}

// B 的 notice（info）发一次，带稳定码供前端本地化。
func TestReasoningOnlyContinuationEmitsInfoNotice(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		reasoningOnlyTurn("thinking"),
		textTurn("visible answer"),
	}}
	sink := &recordSink{}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{ReasoningOnlyContinuation: true}, sink)

	if err := a.Run(context.Background(), "answer me"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	notices := 0
	for _, ev := range sink.kinds(event.Notice) {
		if ev.Code == event.NoticeCodeReasoningOnlyContinuation {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("reasoning_only_continuation notices = %d, want 1", notices)
	}
}
