package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1675 验收钉：分块压缩的三条静默/判据缺陷。现场=20261117 20:34 pressure
// 压缩（src=709323）：start 后 0.4s 直进 chunked summary run chunks=3，全程零
// err/fail 行，done 全绿但 cache miss=65%。取证根因=18:30 的 stale-context
// 失败武装了 tl1587 跨 pass 分段闩且从未复位 → pass 2 跳过单发 rung 直进分
// 段道；两条入口（闩直进 / pass1 尺寸失败 fallback）在当时都不落任何日志。
//
// 本文件钉四件事：
//  1. 闩武装分类（segmentLatchArmable）：只有尺寸/空响应/传输类请求失败可武
//     装；stale-context、取消、checkpoint/model 拒绝、限流、死钥不武装；
//  2. 闩生效预算门：闩武装但折叠段预估装得进单次请求时，先跑单发 rung；
//  3. 闩直进的分块入口必须落日志+Notice（不再静默）；
//  4. pass1 尺寸失败的 chunked fallback 必须落日志+Notice（不再静默）。

// tl1675 验收①（单元）：闩武装分类表。
func TestSegmentLatchArmingClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// 20261117 20:34 事故的武装类：stale-context 与单发 rung 的可行性无关。
		{"stale-context", errCompressStaleContext, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"checkpoint-rejected", errCheckpointRejected, false},
		{"model-rejected-400", &provider.APIError{Status: 400, Body: "model refused"}, false},
		{"rate-limit", errors.New("status 429: rate limited"), false},
		{"auth-expired", errors.New("status 401: invalid api key"), false},
		// 请求失败类：单发 rung 确实失败了，分段道才接管记忆。
		{"output-truncated", fmt.Errorf("%w: provider reached the output token limit", errSummaryOutputTruncated), true},
		{"local-admission", ErrCompactionRequired, true},
		{"provider-overflow", &provider.ContextLimitError{WindowTokens: 10, PromptTokens: 20}, true},
		{"empty-output", errors.New("summarizer returned empty output"), true},
		{"transport", errors.New("read tcp: connection reset"), true},
	}
	for _, tc := range cases {
		if got := segmentLatchArmable(tc.err); got != tc.want {
			t.Fatalf("%s: segmentLatchArmable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// tl1675 验收①（行为）：stale-context 失败不武装闩——18:30 的失败不得让
// 20:34 的 pass 白白跳过单发 rung。尺寸/空响应失败仍武装（tl1587 主契约）。
func TestStaleContextFailureDoesNotArmSegmentLatch(t *testing.T) {
	a := agentOverForce(t, &fakeProvider{reply: "- kept: alpha constraint"}, foldableSessionOverForce(6))

	a.emitCompactionFailed(CompactionTriggerPressure, errCompressStaleContext)
	if a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("a stale-context failure armed the segment latch — it says nothing about the single rung's viability")
	}
	a.emitCompactionFailed(CompactionTriggerPressure, context.Canceled)
	if a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("a cancellation armed the segment latch")
	}
	a.emitCompactionFailed(CompactionTriggerPressure, errors.New("status 429: rate limited"))
	if a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("a rate limit armed the segment latch")
	}
	a.emitCompactionFailed(CompactionTriggerPressure, errors.New("summarizer returned empty output"))
	if !a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("an empty-output request failure did not arm the segment latch (tl1587 core)")
	}
	a.emitCompactionSucceeded(CompactionTriggerPressure, 3, "digest", "")
	if a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("a successful pass left the segment latch armed")
	}
}

// tl1675 验收②：闩武装但折叠段预估装得进单次请求 → 先跑单发 rung。这是
// 20:34 现场的直修面（111K 折叠段在 ~1.2M 窗口上本该一次落地）。
func TestLatchedPassRetriesSingleRungWhenFoldFitsBudget(t *testing.T) {
	const window = 1_000_000
	trail := captureSlog(t)
	prov := &segmentStubProvider{window: window, bigThreshold: 900_000}
	sess := bigWindowSegmentSession(1600)
	a := segmentAgent(t, prov, sess, window)
	a.sess.compaction.segmentSummaryLatched.Store(true) // 上一轮失败留下的闩

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	reqs := prov.recorded()
	if len(reqs) == 0 {
		t.Fatal("no summary requests reached the provider")
	}
	if fragmentRequestCount(reqs) != 0 {
		t.Fatalf("fragment requests = %d, want 0: a fitting fold must ride the single rung first, not the chunked path", fragmentRequestCount(reqs))
	}
	if first := reqs[0]; requestContains(first, "This is fragment") {
		t.Fatal("the first summary request was already a fragment — the single rung was skipped")
	}
	bypass := tl1562Lines(trail.String(), "fold fits one summary request")
	if len(bypass) != 1 {
		t.Fatalf("latch-bypass lines = %d, want exactly 1:\n%s", len(bypass), trail.String())
	}
	requireLineFields(t, bypass[0], "trigger=pressure", "fold_tokens=", "single_budget=")
	if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want the single-rung digest applied", r)
	}
	if a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("the bypassed latch survived a successful pass")
	}
}

// tl1675 验收②的对偶面：闩武装且折叠段预估真的装不进单次请求（预估超单次
// 上限）→ 闩照旧跳过单发 rung 直进分段道（tl1587 的原契约），但这次跳过
// 必须落日志+Notice（验收③）。
func TestLatchedOversizedFoldSkipsSingleRungObservably(t *testing.T) {
	const window = 1_000_000
	trail := captureSlog(t)
	prov := &segmentStubProvider{window: window, bigThreshold: 900_000}
	sess := bigWindowSegmentSession(2400) // ≈1.2M tokens, over the single-request budget
	a := segmentAgent(t, prov, sess, window)
	a.sess.compaction.segmentSummaryLatched.Store(true)

	var notices []event.Event
	a.SetSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeCompactionChunkedFallback {
			notices = append(notices, e)
		}
	}))

	fold := sess.Snapshot()
	res, _, err := a.summarizeFold(context.Background(), CompactionTriggerPressure, fold, "", a.guardedSummaryInputTokens(fold), SummaryInputCachePrefix, foldRequest{allowChunked: true})
	if err != nil {
		t.Fatalf("summarizeFold = %v", err)
	}
	if strings.TrimSpace(res.Text) == "" {
		t.Fatal("empty digest from the segmented path")
	}
	skip := tl1562Lines(trail.String(), "cross-pass latch")
	if len(skip) != 1 {
		t.Fatalf("latch-skip lines = %d, want exactly 1:\n%s", len(skip), trail.String())
	}
	requireLineFields(t, skip[0], "trigger=pressure", "fold_tokens=", "segment_budget=250000", "source_tokens=")
	if len(notices) != 1 {
		t.Fatalf("chunked-degradation notices = %d, want exactly 1", len(notices))
	}
	if !strings.Contains(notices[0].Detail, "trigger=pressure") || !strings.Contains(notices[0].Detail, "latch") {
		t.Fatalf("notice detail = %q, want the trigger and the latch reason", notices[0].Detail)
	}
	// 跳过的直接读数：第一个到达 provider 的摘要请求已经是 fragment。
	reqs := prov.recorded()
	if len(reqs) == 0 || !requestContains(reqs[0], "This is fragment") {
		t.Fatalf("first request was not a fragment — the single rung ran despite the oversized fold (%d requests)", len(reqs))
	}
}

// tl1675 验收④：pass1 尺寸失败走 chunked fallback 时，失败原因必须落日志
// （pass 绿了也留痕）并发出 Notice——不再是静默通道。
func TestChunkedFallbackFailureIsObservable(t *testing.T) {
	trail := captureSlog(t)
	prov := &extractStubProvider{failFirst: 2, reply: "digest"} // 单发+加倍重试都截断 → fallback
	a := New(prov, tool.NewRegistry(), extractStubSession(), Options{}, event.Discard)
	var notices []event.Event
	a.SetSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeCompactionChunkedFallback {
			notices = append(notices, e)
		}
	}))

	res, _, err := a.foldSummaryWithChunkedFallback(context.Background(), CompactionTriggerPressure, a.Session().Snapshot(), "focus", 321, SummaryInputCachePrefix)
	if err != nil {
		t.Fatalf("foldSummaryWithChunkedFallback = %v", err)
	}
	if strings.TrimSpace(res.Text) == "" {
		t.Fatal("empty digest after the chunked fallback")
	}
	fallbacks := tl1562Lines(trail.String(), "falling back to the chunked fragment path")
	if len(fallbacks) != 1 {
		t.Fatalf("fallback lines = %d, want exactly 1:\n%s", len(fallbacks), trail.String())
	}
	requireLineFields(t, fallbacks[0], "trigger=pressure", "reason=", "err=")
	if !strings.Contains(fallbacks[0], "output token limit") {
		t.Fatalf("fallback line must name the pass-1 failure shape:\n%s", fallbacks[0])
	}
	if len(notices) != 1 {
		t.Fatalf("chunked-degradation notices = %d, want exactly 1", len(notices))
	}
	if !strings.Contains(notices[0].Detail, "trigger=pressure") || !strings.Contains(notices[0].Detail, "source_tokens=321") {
		t.Fatalf("notice detail = %q, want trigger and source tokens", notices[0].Detail)
	}
}
