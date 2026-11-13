package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1562 验收测试：20261112 实战（fork开发-新12）复盘出的三个日志缺口——
// 投影失效无差异细节、provider 空响应无本地对账与 会话归属、输出截断零打点。
// 每个形态一条可 grep 的结构化日志行，全部带 session 归属；验收④用一条
// 真实入口串出的单线程链（投影失效 → 空响应 → turn 失败），不依赖 UI。

// tl1562Lines extracts the incident-form lines from a captured slog trail,
// preserving order — the single-thread acceptance reads them in sequence.
func tl1562Lines(trail, marker string) []string {
	var out []string
	for _, line := range strings.Split(trail, "\n") {
		if strings.Contains(line, marker) {
			out = append(out, line)
		}
	}
	return out
}

// requireLineFields asserts every field key=value pair is present on a line.
func requireLineFields(t *testing.T, line string, fields ...string) {
	t.Helper()
	for _, f := range fields {
		if !strings.Contains(line, f) {
			t.Fatalf("log line missing %q:\n%s", f, line)
		}
	}
}

// installDriftedProjection installs a projection whose covered-prefix hash
// cannot match the current canonical transcript while the lineage key still
// does — the exact content_mismatch shape (not a lineage_key_mismatch).
func installDriftedProjection(t *testing.T, a *Agent) {
	t.Helper()
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{
		SchemaVersion:  compactionStateSchemaCurrent,
		PromptCacheKey: a.currentPromptCacheKeyLocked(),
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleUser, Content: "folded body"}},
			CoveredCount:      1,
			CoveredPrefixHash: "deadbeefdeadbeef",
		},
	}
	a.sess.compactionMu.Unlock()
}

// disarmBackgroundRebuilds pre-consumes the tl1203/549 rebuild singleflight
// latches so no background Prepare goroutine races the assertions: a stale
// projection makes modelVisibleMessages arm exactly one forced rebuild, and
// its maintenance check would land inside the warn cooldown, polluting the
// suppressed counter (and, against a scripted mock, stealing turns). These
// tests pin the WARN surfaces, not the rebuild.
func disarmBackgroundRebuilds(a *Agent) {
	a.sess.staleRebuildArmed.Store(true)
	a.sess.rebuildPending.Store(true)
}

// TestProjectionFallbackDriftDiffAndSuppression pins tl1562 G1: the fallback
// warning carries the drift evidence (before/after covered-prefix hash,
// covered vs canonical counts, system prefix bound, both transcript
// versions) plus the session attribution, and the cooldown flushes its
// swallowed-check count as suppressed=N on the next emitted line.
func TestProjectionFallbackDriftDiffAndSuppression(t *testing.T) {
	trail := captureSlog(t)
	a := New(&denseTokenizerProvider{window: 20_000}, tool.NewRegistry(), longASCIISession(2),
		Options{ContextWindow: 20_000, CompactRatio: 0.8}, event.Discard)
	disarmBackgroundRebuilds(a)
	installDriftedProjection(t, a)

	// 首次失效：立即告警，drift 证据齐备，suppressed=0。
	a.observeProjectionFallback(a.ContextUsedTokens())
	first := tl1562Lines(trail.String(), "context projection invalid")
	if len(first) != 1 {
		t.Fatalf("first check must warn exactly once, got %d lines:\n%s", len(first), trail.String())
	}
	requireLineFields(t, first[0],
		"reason=content_mismatch",
		"covered_count=1",
		"expected_prefix_hash=deadbeef",
		"actual_prefix_hash=",
		"system_prefix_len=",
		"projection_transcript_version=",
		"transcript_version=",
		"suppressed=0")
	if !strings.Contains(first[0], "session=reasonix-") {
		t.Fatalf("fallback warning missing the session attribution:\n%s", first[0])
	}

	// 冷却期内两次检查：静默，但吞掉计数累计。
	a.observeProjectionFallback(a.ContextUsedTokens())
	a.observeProjectionFallback(a.ContextUsedTokens())
	if got := a.sess.compaction.fallbackWarnSuppressed; got != 2 {
		t.Fatalf("suppressed counter = %d, want 2 (cooldown swallows counted)", got)
	}

	// 冷却过期：放行行回填 suppressed=2 并清零。
	a.sess.compaction.fallbackWarnAt = time.Now().Add(-projectionFallbackWarnCooldown - time.Minute)
	a.observeProjectionFallback(a.ContextUsedTokens())
	second := tl1562Lines(trail.String(), "context projection invalid")
	if len(second) != 2 {
		t.Fatalf("expired cooldown must emit again, got %d lines:\n%s", len(second), trail.String())
	}
	requireLineFields(t, second[1], "reason=content_mismatch", "suppressed=2")
	if got := a.sess.compaction.fallbackWarnSuppressed; got != 0 {
		t.Fatalf("suppressed counter = %d after flush, want 0", got)
	}

	// 投影修复：重置节流；再次漂移立即告警且 suppressed=0（新 episode）。
	a.sess.compactionMu.Lock()
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionState = CompactionState{
		SchemaVersion:  compactionStateSchemaCurrent,
		PromptCacheKey: a.currentPromptCacheKeyLocked(),
		Projection: ContextProjection{
			Messages:          []provider.Message{msgs[0]},
			CoveredCount:      1,
			CoveredPrefixHash: coveredPrefixHash(msgs, 1),
		},
	}
	a.sess.compactionMu.Unlock()
	a.observeProjectionFallback(a.ContextUsedTokens())
	if !a.sess.compaction.fallbackWarnAt.IsZero() {
		t.Fatal("a valid projection did not re-arm the throttle")
	}
	installDriftedProjection(t, a)
	a.observeProjectionFallback(a.ContextUsedTokens())
	third := tl1562Lines(trail.String(), "context projection invalid")
	if len(third) != 3 {
		t.Fatalf("re-armed throttle must warn immediately on the new episode:\n%s", trail.String())
	}
	requireLineFields(t, third[2], "reason=content_mismatch", "suppressed=0")
}

// emptyResponseUsage mirrors the 20261112 incident's provider side: a clean
// completion that produced nothing while billing a full cache-miss prefill.
func emptyResponseUsage() *provider.Usage {
	return &provider.Usage{PromptTokens: 503_383, CompletionTokens: 0, TotalTokens: 503_383, CacheMissTokens: 1_048_570}
}

// TestEmptyResponseTrailCarriesSessionAndLocalView pins tl1562 G2 end to
// end: a provider that keeps returning clean-but-empty completions retries
// out, and every failed-attempt line plus the turn-closing line must name
// the session, the local view tokens, and the provider usage side by side —
// so "local view vs provider cache_miss" is one grep, not an inference.
func TestEmptyResponseTrailCarriesSessionAndLocalView(t *testing.T) {
	trail := captureSlog(t)
	sink := &recordSink{}
	mp := testutil.NewMock("empty-provider",
		testutil.Turn{Usage: emptyResponseUsage()},
		testutil.Turn{Usage: emptyResponseUsage()},
		testutil.Turn{Usage: emptyResponseUsage()},
		testutil.Turn{Usage: emptyResponseUsage()},
	)
	a := New(mp, echoRegistry(), NewSession(""), Options{}, sink)
	disarmBackgroundRebuilds(a)
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	if err == nil {
		t.Fatal("a provider that never returns content must fail the turn")
	}
	if !strings.Contains(err.Error(), "empty provider response") {
		t.Fatalf("err = %v, want the empty provider response sentinel", err)
	}

	attempts := tl1562Lines(trail.String(), "sampling attempt failed")
	if len(attempts) < 2 {
		t.Fatalf("every failed attempt must leave a trail line, got %d:\n%s", len(attempts), trail.String())
	}
	session := a.sessionLogRef()
	if session == "" {
		t.Fatal("sessionLogRef resolved empty; the trail could not be attributed")
	}
	for _, line := range attempts {
		requireLineFields(t, line,
			"phase=empty",
			"usage_present=true",
			"session="+session,
			"view_tokens=",
			"prompt_tokens=503383",
			"total_tokens=503383",
			"cache_miss_tokens=1048570")
	}
	turns := tl1562Lines(trail.String(), "turn ended with provider failure")
	if len(turns) != 1 {
		t.Fatalf("exactly one turn-closing line expected, got %d:\n%s", len(turns), trail.String())
	}
	// turn 行的 usage 是多次 attempt 的 billable 聚合（mergeSamplingUsage），
	// 数值随 attempt 数放大；契约是字段在场且与本地视图并排，不是单次数值。
	requireLineFields(t, turns[0],
		"phase=empty",
		"session="+session,
		"view_tokens=",
		"prompt_tokens=",
		"total_tokens=",
		"completion_tokens=0",
		"cache_miss_tokens=")
}

// TestOutputTruncationFinishReasonLogged pins tl1562 G3 end to end: a turn
// the provider ends with finish_reason=length renders the existing UI notice
// AND leaves a structured log line carrying finish_reason, the output token
// count, the configured max_output_tokens, and the session — grep-able where
// the 20261112 incident had nothing.
func TestOutputTruncationFinishReasonLogged(t *testing.T) {
	trail := captureSlog(t)
	sink := &recordSink{}
	mp := testutil.NewMock("truncating-provider", testutil.Turn{
		Text: "partial answer cut by the cap",
		Usage: &provider.Usage{
			PromptTokens: 1_000, CompletionTokens: 128_000, TotalTokens: 129_000,
			FinishReason: "length",
		},
	})
	a := New(mp, echoRegistry(), NewSession(""), Options{}, sink)
	disarmBackgroundRebuilds(a)
	if err := a.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("finish_reason=length ends the stream cleanly, got: %v", err)
	}
	if len(sink.kinds(event.Notice)) == 0 {
		t.Fatal("the user-facing truncation notice must still be emitted")
	}
	lines := tl1562Lines(trail.String(), "turn output truncated by provider finish reason")
	if len(lines) != 1 {
		t.Fatalf("exactly one truncation log line expected, got %d:\n%s", len(lines), trail.String())
	}
	requireLineFields(t, lines[0],
		"finish_reason=length",
		"completion_tokens=128000",
		"max_output_tokens=",
		"session="+a.sessionLogRef())

	// 正常终止不产生该行：零行为变化红线。
	trail2 := captureSlog(t)
	mp2 := testutil.NewMock("clean-provider", testutil.Turn{Text: "done"})
	a2 := New(mp2, echoRegistry(), NewSession(""), Options{}, event.Discard)
	if err := a2.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("clean turn failed: %v", err)
	}
	if got := tl1562Lines(trail2.String(), "turn output truncated by provider finish reason"); len(got) != 0 {
		t.Fatalf("a clean stop must not log the truncation line:\n%s", trail2.String())
	}
}

// TestTl1562IncidentChainSingleLogThread is the acceptance-④ proof: one
// agent, one log stream — a projection drift warning, then a run against a
// provider that only returns empty completions. The three incident-form
// lines appear in order and all carry the SAME session value, so the whole
// chain closes from the log alone (no UI, no correlation guesses).
func TestTl1562IncidentChainSingleLogThread(t *testing.T) {
	trail := captureSlog(t)
	sink := &recordSink{}
	mp := testutil.NewMock("chain-empty-provider",
		testutil.Turn{Usage: emptyResponseUsage()},
		testutil.Turn{Usage: emptyResponseUsage()},
		testutil.Turn{Usage: emptyResponseUsage()},
		testutil.Turn{Usage: emptyResponseUsage()},
	)
	a := New(mp, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: t.TempDir()}, sink)
	disarmBackgroundRebuilds(a)

	// 形态一：人为构造投影失效（真实入口 observeProjectionFallback）。
	installDriftedProjection(t, a)
	a.observeProjectionFallback(a.ContextUsedTokens())

	// 形态二：真实 Run 打到连续空响应。
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	if err == nil || !strings.Contains(err.Error(), "empty provider response") {
		t.Fatalf("run err = %v, want the empty provider response sentinel", err)
	}

	out := trail.String()
	projection := tl1562Lines(out, "context projection invalid")
	attempts := tl1562Lines(out, "sampling attempt failed")
	turns := tl1562Lines(out, "turn ended with provider failure")
	if len(projection) < 1 || len(attempts) < 1 || len(turns) != 1 {
		t.Fatalf("chain incomplete: projection=%d attempts=%d turn=%d\n%s",
			len(projection), len(attempts), len(turns), out)
	}
	// 单线归属：三类行共用同一 session 值。
	session := a.sessionLogRef()
	for _, line := range append(append([]string{projection[0]}, attempts...), turns[0]) {
		if !strings.Contains(line, "session="+session) {
			t.Fatalf("chain line missing session=%s:\n%s", session, line)
		}
	}
	// 形态字段自证：投影行带 drift 摘要，空响应行带对账并排。
	requireLineFields(t, projection[0], "reason=content_mismatch", "expected_prefix_hash=", "actual_prefix_hash=")
	requireLineFields(t, attempts[0], "phase=empty", "view_tokens=", "prompt_tokens=503383", "cache_miss_tokens=1048570")
	requireLineFields(t, turns[0], "phase=empty", "view_tokens=")

	// 时间顺序：投影失效最早，turn 收尾最晚。
	projIdx := strings.Index(out, projection[0])
	turnIdx := strings.Index(out, turns[0])
	if projIdx < 0 || turnIdx < projIdx {
		t.Fatalf("chain order broken: projection@%d turn@%d", projIdx, turnIdx)
	}
}
