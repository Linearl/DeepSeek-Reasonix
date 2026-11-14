package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1587 验收钉：满窗（≈1M）会话触发压缩时，摘要请求输入有界（分段/降载，
// 不整窗喂）；单发摘要失败≤1 次即进分段道；分段失败兜底不直接裁剪最早历史；
// 分段道命中在 tl1562 风格结构化日志可观测。现场=20261114 包92 的 31 行失败链
// （输出截断→空响应→传输中断→fail-streak→Diet→投影回退全量→裁剪最早历史）。

// segmentStubProvider 是 tl1587 的窗口共享 stub：按请求输入字节数阈值区分
// 「大请求失败（空响应形态，包92 之②）/小请求成功」，并记录每个请求。
// 阈值用字节（wire 尺寸）而非 census 估算——后者对 ASCII 按 1 rune=1 token
// 计价偏高 4 倍，无法稳定区分「单发段请求」与「分片请求」两档。
type segmentStubProvider struct {
	mu           sync.Mutex
	window       int
	bigThreshold int  // request bytes above this = a "big" request
	failBig      bool // big requests fail with the empty-response shape
	allEmpty     bool // every request fails with the empty-response shape
	streamErr    error
	requests     []provider.Request
}

func (p *segmentStubProvider) Name() string { return "segment-stub" }

func (p *segmentStubProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{
		WindowMode: provider.ContextWindowShared, AutoOutputTokens: 8192,
		MaxOutputTokens: 8192, LimitMode: provider.OutputLimitOmitWhenSafe,
	}
}

func segmentRequestBytes(req provider.Request) int {
	n := 0
	for _, m := range req.Messages {
		n += len(m.Content)
	}
	return n
}

func (p *segmentStubProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	cp := req
	cp.Messages = append([]provider.Message(nil), req.Messages...)
	p.requests = append(p.requests, cp)
	big := segmentRequestBytes(req) > p.bigThreshold
	fail := p.streamErr != nil || p.allEmpty || (p.failBig && big)
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 4)
	if fail {
		if p.streamErr != nil {
			ch <- provider.Chunk{Type: provider.ChunkError, Err: p.streamErr}
		}
		// else: the stream closes with no content — the empty provider response
		// shape (包92 ②) that runSummaryRequest reports as empty output.
		close(ch)
		return ch, nil
	}
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "- goal: keep going\n- pending: continue"}
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, RequestCount: 1}}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func (p *segmentStubProvider) recorded() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.requests...)
}

func (p *segmentStubProvider) setAllEmpty(v bool) {
	p.mu.Lock()
	p.allEmpty = v
	p.mu.Unlock()
}

// bigWindowSegmentSession builds a session whose fold region dwarfs the segment
// bound: `turns` assistant turns of ~500 tokens each (2000 ASCII chars at the
// cold 4-chars-per-token estimate).
func bigWindowSegmentSession(turns int) *Session {
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "standing constraint: keep the public API stable"})
	big := strings.Repeat("word ", 400)
	for i := 0; i < turns; i++ {
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: big})
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	}
	return sess
}

func segmentAgent(t *testing.T, prov provider.Provider, sess *Session, window int) *Agent {
	t.Helper()
	return New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: window,
		CompactRatio:  0.8,
		RecentKeep:    2,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
}

// bigRequestShapes counts DISTINCT big request shapes (by wire bytes). The
// provider layer may retry one failed request several times on its own
// (包92 现场「空响应 4 次 retry 全空」正是这层的行为，不在本件范围）；agent
// 层的契约是「失败≤1 次」——同一个大请求形态只允许一个，不得出现第二种
// 大形态（原尺寸重放/重规划）。
func bigRequestShapes(reqs []provider.Request, threshold int) int {
	seen := map[int]bool{}
	for _, req := range reqs {
		if n := segmentRequestBytes(req); n > threshold {
			seen[n] = true
		}
	}
	return len(seen)
}

func fragmentRequestCount(reqs []provider.Request) int {
	n := 0
	for _, req := range reqs {
		if requestContains(req, "This is fragment") {
			n++
		}
	}
	return n
}

// 验收①：满窗(≈1M)会话触发压缩，单发摘要请求输入被分段上限约束（≈窗口的
// 四分之一），不再把近整窗（#9572 safe 上限 ≈94% 窗口）一次喂给摘要。
func TestFullWindowSummaryInputStaysWithinSegmentBound(t *testing.T) {
	const window = 1_000_000
	prov := &segmentStubProvider{window: window, bigThreshold: 2_000_000}
	sess := bigWindowSegmentSession(1600) // ≈813K tokens, over the 800K trigger
	a := segmentAgent(t, prov, sess, window)
	if seg, enforced := a.summarySegmentInputBudget(); !enforced || seg != window/4 {
		t.Fatalf("segment budget = %d enforced=%v, want %d", seg, enforced, window/4)
	}
	if est, fold := a.contextManager().currentPrepared().InputTokens, a.compactTrigger(); est < fold {
		t.Fatalf("fixture estimates %d tokens, below fold %d", est, fold)
	}

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	reqs := prov.recorded()
	if len(reqs) == 0 {
		t.Fatal("no summary requests reached the provider")
	}
	budget, _ := a.summarySegmentInputBudget()
	for i, req := range reqs {
		if got := a.estimatedRequestTokens(req); got > budget {
			t.Fatalf("summary request %d carried %d tokens, over the segment bound %d", i, got, budget)
		}
	}
	if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" || latestDigest(a.sess.compactionState.Projection.Messages) == "" {
		t.Fatalf("receipt = %+v, want an applied summary with a digest", r)
	}
}

// 验收②（pass 内）：单发摘要请求失败一次（空响应形态）即换分段道，分段道
// 落地；大请求形态只出现一次，不按原尺寸重试刷屏。字节阈值 900KB 落在
// 「单发段请求（≈996KB）失败、最大分片（≈780KB）成功」之间。
func TestSingleSummaryFailureEscalatesToSegmentedInPass(t *testing.T) {
	const window = 1_000_000
	const bigBytes = 900_000
	prov := &segmentStubProvider{window: window, bigThreshold: bigBytes, failBig: true}
	sess := bigWindowSegmentSession(1600)
	a := segmentAgent(t, prov, sess, window)

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	reqs := prov.recorded()
	if got := bigRequestShapes(reqs, bigBytes); got != 1 {
		t.Fatalf("distinct big summary request shapes = %d, want exactly 1 failure before the segmented path", got)
	}
	if fragmentRequestCount(reqs) == 0 {
		t.Fatal("the segmented fragment path never engaged after the single failure")
	}
	if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" || latestDigest(a.sess.compactionState.Projection.Messages) == "" {
		t.Fatalf("receipt = %+v, want the segmented path to land the digest", r)
	}
	if a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("a successful segmented install left the segment latch armed")
	}
}

// 验收②（跨 pass）：pass 1 全形态失败后，下一个 pass 直进分段道（闩），
// 不再重放单发大请求。
func TestFailedPassLatchesSegmentedPathForNextPass(t *testing.T) {
	const window = 1_000_000
	const bigBytes = 900_000
	prov := &segmentStubProvider{window: window, bigThreshold: bigBytes, failBig: true, allEmpty: true}
	sess := bigWindowSegmentSession(1600)
	a := segmentAgent(t, prov, sess, window)

	// pass 1: 单发 + 分段道全空响应 → 失败落账，闩武装。
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	if r := a.sess.compactionState.LastReceipt; r == nil || (r.Status != "blocked" && r.Status != "failed") {
		t.Fatalf("receipt = %+v, want the failure recorded", r)
	}
	if !a.sess.compaction.segmentSummaryLatched.Load() {
		t.Fatal("a failed pass did not arm the segment latch")
	}

	// provider 恢复（小请求能落地），视图增长换 hash，pass 2 直进分段道。
	prov.setAllEmpty(false)
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "follow-up turn"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "acknowledged"})
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("second prepare = %v", err)
	}
	if got := bigRequestShapes(prov.recorded(), bigBytes); got != 1 {
		t.Fatalf("distinct big summary request shapes across both passes = %d, want only pass 1's single attempt (no replay)", got)
	}
	if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" || latestDigest(a.sess.compactionState.Projection.Messages) == "" {
		t.Fatalf("receipt = %+v, want pass 2 to land via the segmented path", r)
	}
}

// 验收③：分段道也失败后兜底才裁剪——溢出道上截断救援前，分段道必须已尝试
// （fragment 请求在案）；截断是最后手段而非第一反应。
func TestTruncationRescueOnlyAfterSegmentedPathAttempted(t *testing.T) {
	const window = 1_000_000
	prov := &segmentStubProvider{window: window, bigThreshold: 900_000, streamErr: errors.New("provider transport failed")}
	sess := bigWindowSegmentSession(1600)
	a := segmentAgent(t, prov, sess, window)

	if err := prepareContext(context.Background(), a, CompactionTriggerOverflow); err != nil {
		t.Fatalf("prepare = %v, want the truncation rescue after the segmented path failed", err)
	}
	reqs := prov.recorded()
	// 分段道先于裁剪：fragment 请求必须已经发生。
	if fragmentRequestCount(reqs) == 0 {
		t.Fatal("the segmented path was never attempted before the truncation rescue")
	}
	if !truncatedRescue(a) {
		t.Fatalf("receipt = %+v, want the truncation rescue as the last resort", a.sess.compactionState.LastReceipt)
	}
}

// 验收③（硬顶之下）：分段失败后不裁剪最早历史——视图原样保留、失败落账、
// 等下一个视图再走分段重试。
func TestSegmentedFailureBelowCeilingKeepsHistoryIntact(t *testing.T) {
	const window = 1_000_000
	prov := &segmentStubProvider{window: window, bigThreshold: 900_000, streamErr: errors.New("provider transport failed")}
	sess := bigWindowSegmentSession(1600)
	a := segmentAgent(t, prov, sess, window)
	if est, hard := a.contextManager().currentPrepared().InputTokens, a.hardInputCeiling(); est >= hard {
		t.Fatalf("fixture estimates %d tokens, not below the ceiling %d", est, hard)
	}

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v, want the failure absorbed below the ceiling", err)
	}
	if truncatedRescue(a) {
		t.Fatal("a below-ceiling segmented failure trimmed the earliest history")
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || (r.Status != "blocked" && r.Status != "failed") {
		t.Fatalf("receipt = %+v, want the failure recorded", r)
	}
	if fragmentRequestCount(prov.recorded()) == 0 {
		t.Fatal("the segmented path was never attempted below the ceiling")
	}
}

// 验收钉（单元）：分段预算只在「窗口共享 + 窗口大过地板四倍」时生效——
// 小窗口会话保持既有单发路径，独立窗口 provider 保持全量折叠。
func TestSummarySegmentInputBudgetEngagement(t *testing.T) {
	newWith := func(window int, prov provider.Provider) *Agent {
		return segmentAgent(t, prov, NewSession("sys"), window)
	}
	prov := &segmentStubProvider{window: 1 << 20}
	if budget, enforced := newWith(1_000_000, prov).summarySegmentInputBudget(); !enforced || budget != 250_000 {
		t.Fatalf("1M window: budget=%d enforced=%v, want 250000/true", budget, enforced)
	}
	if _, enforced := newWith(64_000, prov).summarySegmentInputBudget(); enforced {
		t.Fatal("64K window must stay on today's single-cap path (segment regime off)")
	}
	if _, enforced := newWith(30_000, prov).summarySegmentInputBudget(); enforced {
		t.Fatal("30K window must stay on today's single-cap path (segment regime off)")
	}
	// 独立窗口 provider：不设段上界（沿用 #9572 的独立窗口全量折叠契约）。
	independent := &fakeProvider{}
	if _, enforced := newWith(1_000_000, independent).summarySegmentInputBudget(); enforced {
		t.Fatal("a window-independent provider must keep the full fold (no segment cap)")
	}
}

// 验收钉（单元）：分段升级分类器——溢出归 replan→slim 阶梯、尺寸失败归既有
// chunked fallback、限流/鉴权归专用道；空响应与传输/未知形态升级分段。
func TestSummarySegmentEscalatableClassification(t *testing.T) {
	overflow := &provider.ContextLimitError{WindowTokens: 10, PromptTokens: 20}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"provider-overflow", overflow, false},
		{"output-truncated", fmt.Errorf("%w: provider reached the output token limit", errSummaryOutputTruncated), false},
		{"empty-output", errors.New("summarizer returned empty output"), true},
		{"transport", errors.New("provider transport failed"), true},
		{"rate-limit", errors.New("status 429: rate limited"), false},
		{"auth", errors.New("status 401: invalid api key"), false},
	}
	for _, tc := range cases {
		if got := summarySegmentEscalatable(tc.err); got != tc.want {
			t.Fatalf("%s: escalatable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 验收④：分段路径命中必须在 tl1562 风格结构化日志可观测——升级行带会话
// 归属、fold/预算读数与失败形态（三形态 grep 之锚），成功行点名 segmented。
func TestSegmentedPathHitIsObservableInStructuredLog(t *testing.T) {
	const window = 1_000_000
	trail := captureSlog(t)
	prov := &segmentStubProvider{window: window, bigThreshold: 900_000, failBig: true}
	sess := bigWindowSegmentSession(1600)
	a := segmentAgent(t, prov, sess, window)

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	escalations := tl1562Lines(trail.String(), "segmenting the fold")
	if len(escalations) != 1 {
		t.Fatalf("segmenting lines = %d, want exactly 1:\n%s", len(escalations), trail.String())
	}
	requireLineFields(t, escalations[0],
		"trigger=pressure",
		"session=reasonix-",
		"fold_tokens=",
		"segment_budget=250000",
		"source_tokens=")
	if !strings.Contains(escalations[0], "err=") {
		t.Fatalf("segmenting line must carry the failure shape (err=):\n%s", escalations[0])
	}
	successes := tl1562Lines(trail.String(), "succeeded on the segmented path")
	if len(successes) != 1 {
		t.Fatalf("segmented success lines = %d, want exactly 1:\n%s", len(successes), trail.String())
	}
	requireLineFields(t, successes[0], "session=reasonix-", "spans=", "summary_input_mode=chunked")
}
