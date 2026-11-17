package agent

// tl1657 验收矩阵（emit 数据源三态）：
// ① 等待态（begin → 首 chunk）：不 emit 全 0 读数（20261117 09:42:09 现场：
//    首 emit tokens=0/tps=0/fold=0/0，烧掉节流额度且前端全隐藏）；
// ② 流式态：tps 分母 = 首 chunk 起的流式窗口（同一现场 begin→首 chunk 相隔
//    101s，旧口径把排队算进分母，吞吐被永久读小）；
// ③ pass 贯通：Progress 事件与 CompactionLiveState 查询面携带 Started 的
//    pass 号（前端合成卡与真实事件挂同一张卡的前提）。

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// TestCompactionLiveMeterSuppressesZeroReadout pins 态①: chunks arriving
// under compactionLiveCharsPerToken runes produce NO emission while the
// readout is still all-zero — the meter stays armed and emits the moment the
// readout becomes visible.
func TestCompactionLiveMeterSuppressesZeroReadout(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("pressure", 3)

	if m.addOutput("abc") { // 3 runes → tokens=0 → invisible
		t.Fatal("sub-threshold chunk must not emit an all-zero readout")
	}
	if got := m.emittedCount; got != 0 {
		t.Fatalf("emittedCount = %d while readout was all-zero, want 0", got)
	}
	// The 4th rune crosses the ÷4 floor (tokens=1) — the FIRST emission lands
	// here, not on the earlier zero-shaped snapshot.
	if !m.addOutput("d") {
		t.Fatal("first visible snapshot must emit")
	}
	if got := m.emittedCount; got != 1 {
		t.Fatalf("emittedCount = %d after crossing the visibility floor, want 1", got)
	}
	r, ok := m.snapshot()
	if !ok || r.Tokens != 1 {
		t.Fatalf("snapshot = %+v ok=%v, want tokens=1", r, ok)
	}
}

// TestCompactionLiveMeterTpsExcludesQueueWindow pins 态②: the tps denominator
// starts at the FIRST CHUNK, not at begin() — a pass that queued 101s before
// its first token must not read its streaming throughput divided by the queue.
func TestCompactionLiveMeterTpsExcludesQueueWindow(t *testing.T) {
	clk := newFakeClock()
	var m compactionLiveMeter
	m.now = clk.Now
	m.begin("pressure", 1)

	// 101s of queue/admission/TTFT silence (the 20261117 incident window).
	clk.Advance(101 * time.Second)
	if !m.addOutput(strings.Repeat("x", 4000)) { // first chunk: 1000 tokens
		t.Fatal("first visible chunk must emit")
	}
	// 2s of streaming → 1000 tokens / 2s = 500 tok/s. The old begin-anchored
	// denominator would have read 1000/103s ≈ 9 tok/s.
	clk.Advance(2 * time.Second)
	r, _ := m.snapshot()
	if r.TokensPerSec != 500 {
		t.Fatalf("tps = %d, want 500 (streaming-window denominator, queue excluded)", r.TokensPerSec)
	}
}

// TestCompactionLiveProgressCarriesPass pins 态③ on the wire: the Progress
// event's Pass field equals the pass the meter was opened with, so a frontend
// card synthesized from CompactionLiveState attaches to the live stream.
func TestCompactionLiveProgressCarriesPass(t *testing.T) {
	prov := &chunkSpamProvider{chunks: 5, text: "abcd"}
	a := New(prov, tool.NewRegistry(), &Session{}, Options{}, event.Discard)
	var got event.Compaction
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionProgress {
			got = e.Compaction
		}
	})
	a.compactionLiveBegin("pressure", 7)
	defer a.compactionLiveStop()
	a.compactionLiveAddOutput("abcd")
	if got.Pass != 7 {
		t.Fatalf("Progress event pass = %d, want 7 (the Started pass)", got.Pass)
	}
	if got.Trigger != "pressure" {
		t.Fatalf("Progress event trigger = %q, want pressure", got.Trigger)
	}
}

// TestCompactionLiveStateQueryFace pins the desktop reconciliation read:
// Running=true with the meter's own readout while a pass is in flight;
// Running=false after the window seals (nothing to reconcile, no card).
func TestCompactionLiveStateQueryFace(t *testing.T) {
	a := New(&chunkSpamProvider{chunks: 1, text: "x"}, tool.NewRegistry(), &Session{}, Options{}, event.Discard)
	a.compactionLive.now = newFakeClock().Now
	a.compactionLiveBegin("pressure", 4)
	a.compactionLiveAddOutput(strings.Repeat("y", 400)) // 100 tokens, visible

	st := a.CompactionLiveState()
	if !st.Running {
		t.Fatal("query must report Running while a pass is in flight")
	}
	if st.Pass != 4 || st.Trigger != "pressure" {
		t.Fatalf("query = %+v, want pass 4 trigger pressure", st)
	}
	if st.Tokens != 100 {
		t.Fatalf("query tokens = %d, want 100", st.Tokens)
	}
	a.compactionLiveStop()
	st = a.CompactionLiveState()
	if st.Running {
		t.Fatal("query must report Running=false after the pass window seals — an ended pass is history, not a live card")
	}
}
