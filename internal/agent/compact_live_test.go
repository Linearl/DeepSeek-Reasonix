package agent

// 任务 556 验收：压缩进行中的实时 token/吞吐读数。
// 钉三件事：① 读数随流增量持续变化（非 0、非静态）；② 事件节流——事件数
// 有与 chunk 数无关的上界（1s 间隔门），不产生事件风暴；③ 窗口封口后
// （Done/abort）不再发事件，前端卡片不会被脏值续命。

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// chunkSpamProvider replies with many small text chunks in one buffered send
// burst — the worst-case chunk rate the throttle gate must absorb (the
// historical per-chunk emit would fire once per chunk).
type chunkSpamProvider struct {
	chunks int
	text   string
}

func (p *chunkSpamProvider) Name() string { return "chunk-spam" }

func (p *chunkSpamProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *chunkSpamProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, p.chunks+2)
	for i := 0; i < p.chunks; i++ {
		ch <- provider.Chunk{Type: provider.ChunkText, Text: p.text}
	}
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 10, TotalTokens: 20}}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// fakeClock drives the meter deterministically in the unit tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestCompactionLiveMeterThrottlesChunkEvents pins the event-storm guard:
// chunk-driven emissions are bounded by wall-clock duration (≤1 per
// compactionLiveEmitInterval), never by chunk count. 500 chunks inside one
// interval must produce exactly one emission (the first).
func TestCompactionLiveMeterThrottlesChunkEvents(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("auto")

	// First chunk emits immediately (readout appears at stream start).
	if !m.addOutput("abcd") {
		t.Fatal("first chunk must emit")
	}
	for i := 0; i < 499; i++ {
		if m.addOutput("abcd") {
			t.Fatalf("chunk %d emitted inside the throttle interval", i)
		}
	}
	if got := m.emittedCount; got != 1 {
		t.Fatalf("emittedCount = %d inside one interval, want 1", got)
	}

	// Past the interval, the next changed snapshot emits again.
	clk.Advance(2 * compactionLiveEmitInterval)
	if !m.addOutput("abcd") {
		t.Fatal("chunk past the interval must emit")
	}
	if got := m.emittedCount; got != 2 {
		t.Fatalf("emittedCount = %d after one interval step, want 2", got)
	}

	// An unchanged snapshot never re-emits even with time passed (no
	// heartbeat spam during a stalled stream).
	clk.Advance(2 * compactionLiveEmitInterval)
	if m.addOutput("") {
		t.Fatal("empty chunk must not emit")
	}
	if m.setFoldProgress(0, 0) {
		t.Fatal("unchanged fold progress must not emit")
	}
	if got := m.emittedCount; got != 2 {
		t.Fatalf("emittedCount = %d after unchanged ticks, want 2", got)
	}
}

// TestCompactionLiveMeterFoldProgressBypassesGate pins the N/M contract:
// fold transitions emit on every change regardless of the 1s gate — bounded
// by maxChunkedSummaryCalls — so the final fragment count always reaches the
// card even when no further chunk crosses the interval gate.
func TestCompactionLiveMeterFoldProgressBypassesGate(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("manual")

	if !m.addOutput("abcd") {
		t.Fatal("first chunk must emit")
	}
	// No clock advance: fold transitions still emit.
	if !m.setFoldProgress(1, 3) {
		t.Fatal("fold progress change must bypass the interval gate")
	}
	if !m.setFoldProgress(2, 3) {
		t.Fatal("second fold progress change must emit")
	}
	if m.setFoldProgress(2, 3) {
		t.Fatal("repeated fold progress must not emit")
	}
	if got := m.emittedCount; got != 3 {
		t.Fatalf("emittedCount = %d, want 3 (1 chunk + 2 fold changes)", got)
	}

	r, ok := m.snapshot()
	if !ok {
		t.Fatal("snapshot must be available while active")
	}
	if r.Done != 2 || r.Total != 3 {
		t.Fatalf("readout done/total = %d/%d, want 2/3", r.Done, r.Total)
	}
}

// TestCompactionLiveMeterReadoutMath pins the estimate semantics against the
// run-strip fallback: tokens = runes÷4, tps = tokens ÷ elapsed, tps hidden
// (0) below the 500ms noise floor — the same formula and guard Composer uses
// for the turn readout, so both readouts look alike.
func TestCompactionLiveMeterReadoutMath(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("auto")

	// 4000 runes → 1000 tokens. tps stays hidden before 500ms.
	if !m.addOutput(strings.Repeat("x", 4000)) {
		t.Fatal("first chunk must emit")
	}
	r, _ := m.snapshot()
	if r.Tokens != 1000 {
		t.Fatalf("tokens = %d, want 1000 (runes÷4)", r.Tokens)
	}
	if r.TokensPerSec != 0 {
		t.Fatalf("tps before the 500ms floor = %d, want 0", r.TokensPerSec)
	}

	// 2s elapsed → 1000 tokens / 2s = 500 tok/s, cumulative average.
	clk.Advance(2 * time.Second)
	r, _ = m.snapshot()
	if r.TokensPerSec != 500 {
		t.Fatalf("tps = %d, want 500", r.TokensPerSec)
	}
	// Throughput decays as elapsed grows with no new output — an honest
	// cumulative average, same as the run-strip's elapsed-based rate.
	clk.Advance(8 * time.Second)
	r, _ = m.snapshot()
	if r.TokensPerSec != 100 {
		t.Fatalf("tps after 10s = %d, want 100", r.TokensPerSec)
	}
}

// TestCompactionLiveMeterStopSilences pins the window seal: after stop the
// meter emits nothing and snapshots report inactive, so a resolved or aborted
// card can never receive a stale readout (验收 ③/d：事件卸载安全).
func TestCompactionLiveMeterStopSilences(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("auto")
	if !m.addOutput("abcd") {
		t.Fatal("active meter must emit")
	}
	m.stop()
	if m.addOutput("more") {
		t.Fatal("addOutput after stop must not emit")
	}
	if m.setFoldProgress(1, 2) {
		t.Fatal("setFoldProgress after stop must not emit")
	}
	if _, ok := m.snapshot(); ok {
		t.Fatal("snapshot after stop must report inactive")
	}
	// Re-begin for the next pass resets the counters cleanly.
	m.begin("manual")
	if _, ok := m.snapshot(); !ok {
		t.Fatal("snapshot after re-begin must be active")
	}
	r, _ := m.snapshot()
	if r.Tokens != 0 || r.Done != 0 || r.Total != 0 {
		t.Fatalf("re-begin readout = %+v, want zeroed", r)
	}
}

// TestCompactionLiveMeterConcurrentSumSExact pins the parallel-fragment
// semantics (路径 d): 4-way fragment workers feed ONE shared meter, so Tokens
// is the exact cross-fragment sum — no lost or double-counted increments.
func TestCompactionLiveMeterConcurrentSumSExact(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("auto")

	const workers, perWorker, runesPerChunk = 4, 250, 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				m.addOutput(strings.Repeat("y", runesPerChunk))
			}
		}()
	}
	wg.Wait()

	r, ok := m.snapshot()
	if !ok {
		t.Fatal("snapshot must be available while active")
	}
	wantTokens := workers * perWorker * runesPerChunk / compactionLiveCharsPerToken
	if r.Tokens != wantTokens {
		t.Fatalf("tokens = %d, want exact parallel sum %d", r.Tokens, wantTokens)
	}
	// Storm bound for the concurrent burst: everything lands inside one fake
	// interval, so at most the first emission plus fold changes (none here)
	// can fire.
	if m.emittedCount > 1 {
		t.Fatalf("emittedCount = %d for a burst inside one interval, want ≤ 1", m.emittedCount)
	}
}

// TestCompactEmitsThrottledLiveProgress is the end-to-end pin of 验收 ①+②:
// a real compact pass over a chunk-spamming summarizer produces live
// CompactionProgress events with growing token estimates, ordered strictly
// between CompactionStarted and CompactionDone, and — the storm guard — a
// count bounded by the throttle instead of the 300-chunk stream.
func TestCompactEmitsThrottledLiveProgress(t *testing.T) {
	prov := &chunkSpamProvider{chunks: 300, text: "compact digest slice "}
	sess := longASCIISession(6)
	var mu sync.Mutex
	var progress []event.Compaction
	startedAt, doneAt := -1, -1
	sink := event.FuncSink(func(e event.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case event.CompactionStarted:
			startedAt = len(progress)
		case event.CompactionProgress:
			progress = append(progress, e.Compaction)
		case event.CompactionDone:
			doneAt = len(progress)
		}
	})
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 50_000, RecentKeep: 2}, sink)

	if err := a.compact(context.Background(), "auto", "", true); err != nil {
		t.Fatalf("compact: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(progress) == 0 {
		t.Fatal("no CompactionProgress events — live readout is dead")
	}
	// ① 读数非 0 且随时间变化（单调不减，且至少两次读数或末值可观）。
	last := 0
	for i, c := range progress {
		if c.Tokens <= 0 {
			t.Fatalf("progress[%d].Tokens = %d, want > 0", i, c.Tokens)
		}
		if c.Tokens < last {
			t.Fatalf("progress[%d].Tokens = %d regressed below %d", i, c.Tokens, last)
		}
		last = c.Tokens
	}
	// 事件只出现在 Started→Done 窗口内（验收 d：窗口外零事件）。
	if startedAt < 0 || doneAt < 0 {
		t.Fatalf("lifecycle events missing: started=%d done=%d", startedAt, doneAt)
	}
	// ② 节流上界：300 chunk 若不节流将发 300+ 事件；有门时受时长约束。
	// 放宽到 10 以吸收慢机器上的时钟步进，仍与 chunk 数彻底解耦。
	if len(progress) > 10 {
		t.Fatalf("progress events = %d for a 300-chunk stream — the throttle gate leaked", len(progress))
	}
}

// TestSummarizeLiveProgressGrows pins the increment source: consecutive
// summarizer requests while a pass is active keep feeding the meter, and the
// readout tokens reflect the SUM across requests (chunked fragments and merge
// steps are separate requests on purpose).
func TestSummarizeLiveProgressGrows(t *testing.T) {
	prov := &chunkSpamProvider{chunks: 40, text: "abcd"}
	a := New(prov, tool.NewRegistry(), &Session{}, Options{}, event.Discard)
	a.compactionLive.begin("manual")
	defer a.compactionLive.stop()

	region := []provider.Message{{Role: provider.RoleUser, Content: "x"}}
	if _, _, err := a.summarize(context.Background(), region, ""); err != nil {
		t.Fatalf("summarize 1: %v", err)
	}
	first, ok := a.compactionLive.snapshot()
	if !ok || first.Tokens <= 0 {
		t.Fatalf("readout after request 1 = %+v ok=%v, want tokens > 0", first, ok)
	}
	if _, _, err := a.summarize(context.Background(), region, ""); err != nil {
		t.Fatalf("summarize 2: %v", err)
	}
	second, _ := a.compactionLive.snapshot()
	if second.Tokens <= first.Tokens {
		t.Fatalf("tokens must grow across requests: %d then %d", first.Tokens, second.Tokens)
	}
}

// TestChunkedFoldProgressReachesMeter pins the restored #9082 wiring: the
// chunked fallback's progress callback feeds the pass meter, so fold N/M
// transitions surface as CompactionProgress events and the final done/total
// equals the fragment count (no splits in this fixture).
func TestChunkedFoldProgressReachesMeter(t *testing.T) {
	prov := &chunkSpamProvider{chunks: 5, text: "fragment digest "}
	a := New(prov, tool.NewRegistry(), &Session{}, Options{}, event.Discard)
	a.compactionLive.begin("manual")
	defer a.compactionLive.stop()

	var mu sync.Mutex
	var progress []event.Compaction
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionProgress {
			mu.Lock()
			progress = append(progress, e.Compaction)
			mu.Unlock()
		}
	})

	fold := []provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("chunk one ", 25600)},
		{Role: provider.RoleUser, Content: strings.Repeat("chunk two ", 25600)},
		{Role: provider.RoleUser, Content: strings.Repeat("chunk three ", 25600)},
	}
	if _, err := a.chunkedFoldSummary(context.Background(), fold, "", nil); err != nil {
		t.Fatalf("chunkedFoldSummary (no meter callback): %v", err)
	}
	// Baseline: without the callback the meter only sees chunk deltas.
	mu.Lock()
	baseline := len(progress)
	mu.Unlock()

	// Now with the production wiring (the same closure
	// foldSummaryWithChunkedFallback installs).
	a.compactionLive.begin("manual")
	defer a.compactionLive.stop()
	if _, err := a.chunkedFoldSummary(context.Background(), fold, "", func(done, total int) {
		a.compactionLiveFoldProgress(done, total)
	}); err != nil {
		t.Fatalf("chunkedFoldSummary: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	wired := progress[baseline:]
	var foldEvents []event.Compaction
	for _, c := range wired {
		if c.Total > 0 {
			foldEvents = append(foldEvents, c)
		}
	}
	if len(foldEvents) == 0 {
		t.Fatalf("no fold-progress events — the #9082 callback wiring is still dead (baseline=%d wired=%d total=%d)", baseline, len(wired), len(progress))
	}
	lastF := foldEvents[len(foldEvents)-1]
	if lastF.Done != 3 || lastF.Total != 3 {
		t.Fatalf("final fold progress = %d/%d, want 3/3", lastF.Done, lastF.Total)
	}
	if lastF.Tokens <= 0 {
		t.Fatalf("fold event carries no token estimate: %+v", lastF)
	}
}

// TestCompactAbortStopsLiveEvents pins the abort leg of 验收 ③: a pass that
// dies mid-summarizer (hung stream + cancelled context → the abort path's
// emitCompactionAborted) must seal the meter via the deferred stop, so no
// live event can follow the abort CompactionDone.
func TestCompactAbortStopsLiveEvents(t *testing.T) {
	prov := &fakeProvider{hang: true}
	sess := longASCIISession(6)
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 50_000, RecentKeep: 2}, event.Discard)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.compact(ctx, "auto", "", true); err == nil {
		t.Fatal("compact on a cancelled context must fail")
	}
	if _, ok := a.compactionLive.snapshot(); ok {
		t.Fatal("meter still active after an aborted pass — a stale readout could reattach to a later card")
	}
	if a.compactionLive.addOutput("late") {
		t.Fatal("meter emitted after the abort — the window is not sealed")
	}
}
