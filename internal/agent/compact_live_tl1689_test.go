package agent

// tl1689 验收（后端半）：快速压缩的过程帧保证。
// 现场（20261118 08:50）：26s 压缩只发了一帧 progress（tokens=1 tps=0）——
// 整个 summarizer 流落在 1s 节流窗口内，后续帧全被门挡掉，卡片始终没有
// 吞吐读数。修复：pass 收口（成功/失败 Done 前）发一帧 seal 帧——绕过
// 1s 间隔门与 tps 500ms 实时噪声底，携带 pass 的最终 tokens/tps 读数；
// 全程零可见输出的 pass 不 seal（0 值抑制契约不变）。
//
// 钉三件事：
// ① sealReadout 单元语义：终帧 tps 绕过 500ms 底、窗口不可测时 tps=0、
//    不可见 pass / 停窗后不 seal；
// ② 事件序：seal 帧在 Done 之前、且携带最终读数（interval 门内被挡的
//    尾部 chunk 不再丢失）；
// ③ 节流契约不回归：interval 门内 chunk 依旧不 emit（seal 不改门状态）。

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// TestCompactionLiveSealFrame pins ①: the seal readout bypasses the 500ms
// live tps floor (final frame states the real average), stays silent for an
// invisible pass, and is impossible after the window seals.
func TestCompactionLiveSealFrame(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("auto", 1)

	// First chunk emits the regular first frame (visible, tps=0 below floor).
	if !m.addOutput("abcd") {
		t.Fatal("first chunk must emit")
	}
	// 200ms later, more output — the 1s interval gate throttles it (and the
	// 500ms tps floor would still hide tps on a regular snapshot).
	clk.Advance(200 * time.Millisecond)
	if m.addOutput("abcdef") {
		t.Fatal("chunk inside the interval must stay throttled")
	}
	// Seal bypasses BOTH gates: 2 tokens over 0.2s → 10 tok/s on the final
	// frame — the figure the card would otherwise never show.
	r, ok := m.sealReadout()
	if !ok {
		t.Fatal("seal must report the final readout while the pass is active")
	}
	if r.Tokens != 2 {
		t.Fatalf("seal tokens = %d, want 2 (10 runes ÷ 4)", r.Tokens)
	}
	if r.TokensPerSec != 10 {
		t.Fatalf("seal tps = %d, want 10 (2 tokens / 0.2s — 500ms floor does not apply)", r.TokensPerSec)
	}
	if r.Pass != 1 {
		t.Fatalf("seal pass = %d, want 1", r.Pass)
	}

	// After the window seals, no seal frame is possible.
	m.stop()
	if _, ok := m.sealReadout(); ok {
		t.Fatal("seal after stop must be impossible")
	}

	// An all-invisible pass (chars below the ÷4 floor) seals nothing.
	var m2 compactionLiveMeter
	m2.now = clk.Now
	m2.begin("auto", 0)
	if m2.addOutput("abc") {
		t.Fatal("sub-threshold chunk must not emit")
	}
	if _, ok := m2.sealReadout(); ok {
		t.Fatal("invisible pass must not seal (zero-suppression contract)")
	}
}

// TestCompactSealFrameCarriesFinalReadout pins ② over a real compact pass:
// the LAST CompactionProgress (the seal frame) arrives BEFORE CompactionDone
// and carries the pass's final token estimate, even though every chunk after
// the first was interval-throttled (buffered burst fixture).
func TestCompactSealFrameCarriesFinalReadout(t *testing.T) {
	prov := &chunkSpamProvider{chunks: 300, text: "compact digest slice "} // 21 runes ×300 → 1575 tokens
	sess := longASCIISession(6)
	var mu sync.Mutex
	var progress []event.Compaction
	doneAt := -1
	var donePayload *event.Compaction
	sink := event.FuncSink(func(e event.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case event.CompactionProgress:
			progress = append(progress, e.Compaction)
		case event.CompactionDone:
			doneAt = len(progress)
			payload := e.Compaction
			donePayload = &payload
		}
	})
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 50_000, RecentKeep: 2}, sink)

	if err := a.compact(context.Background(), "auto", "", true); err != nil {
		t.Fatalf("compact: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(progress) < 2 {
		t.Fatalf("progress frames = %d, want >=2 (first frame + seal frame)", len(progress))
	}
	if doneAt != len(progress) {
		t.Fatalf("Done must follow the seal frame: doneAt=%d progress=%d", doneAt, len(progress))
	}
	last := progress[len(progress)-1]
	if last.Tokens <= progress[0].Tokens {
		t.Fatalf("seal frame must carry the final readout: last=%d first=%d", last.Tokens, progress[0].Tokens)
	}
	if last.Tokens < 1500 {
		t.Fatalf("seal tokens = %d, want the full pass estimate (>=1500 of 1575)", last.Tokens)
	}
	// tl1689 补强: the ok Done carries the release metrics the hover card
	// states — source > projection (a release actually happened), the
	// summarizer input cache split, and the pass duration.
	if donePayload == nil {
		t.Fatal("ok CompactionDone missing")
	}
	if donePayload.SourceTokens <= 0 || donePayload.ProjectionTokens <= 0 {
		t.Fatalf("Done release tokens = src %d / proj %d, want both > 0", donePayload.SourceTokens, donePayload.ProjectionTokens)
	}
	if donePayload.ProjectionTokens >= donePayload.SourceTokens {
		t.Fatalf("projection %d must be smaller than source %d — the pass released nothing", donePayload.ProjectionTokens, donePayload.SourceTokens)
	}
	if donePayload.SummaryHitTokens+donePayload.SummaryMissTokens <= 0 {
		t.Fatalf("summary input split = hit %d / miss %d, want a measurable request", donePayload.SummaryHitTokens, donePayload.SummaryMissTokens)
	}
	if donePayload.DurationMs < 0 {
		t.Fatalf("DurationMs = %d, want >= 0", donePayload.DurationMs)
	}
}
