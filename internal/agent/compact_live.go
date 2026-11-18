package agent

// 任务 556: live compaction readout. A compaction pass is an auxiliary
// streaming request whose increments previously had no path to the frontend —
// token counts landed in the post-hoc CompactionTelemetry slog only, so a
// minutes-long compaction rendered as a static "compacting…" placeholder and
// read as a hang. This meter accumulates summarizer output inside the pass
// window (CompactionStarted → Done/abort) and emits throttled
// CompactionProgress events carrying an estimated token count, the cumulative
// tokens/second, and the chunked-fold (done/total) progress.

import (
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"reasonix/internal/event"
)

const (
	// compactionLiveEmitInterval throttles chunk-driven CompactionProgress
	// emissions: at most one per second per pass. Chunks arrive at dozens to
	// hundreds per second, so the gate bounds events by wall-clock duration
	// (≤1/s), never by chunk count — the event-storm guard the acceptance
	// criteria require. Fold-progress changes (rare, bounded by
	// maxChunkedSummaryCalls) bypass this gate so the final N/M always lands.
	compactionLiveEmitInterval = time.Second
	// compactionLiveCharsPerToken is the chars→tokens estimate ratio. It is
	// the SAME ÷4 fallback the frontend run-strip uses for in-flight output
	// (Composer runMetrics: Math.round(inFlightChars / 4)); reusing it keeps
	// the compaction readout visually consistent with the turn readout and
	// avoids a second, divergent estimation algorithm. Rune count (not bytes)
	// mirrors the JS-side UTF-16 length the ratio was calibrated against.
	compactionLiveCharsPerToken = 4
	// compactionLiveTpsMinElapsedMs mirrors the run-strip's ≥500ms guard
	// before showing a tokens/s figure: sub-500ms averages read as noise.
	compactionLiveTpsMinElapsed = 500 * time.Millisecond
)

// compactionLiveMeter accumulates one in-flight compaction pass's live
// readout. Zero value is ready; all methods are safe for concurrent use —
// parallel chunked-fold fragments (compactionParallel, 4-way) stream chunks
// into the shared meter, so Tokens is the cross-fragment SUM and TokensPerSec
// the combined throughput of the whole pass, not a single lane.
//
// The meter is deliberately coarse: it never sees transcript content, only
// rune counts, so nothing sensitive rides the progress events.
type compactionLiveMeter struct {
	mu      sync.Mutex
	active  bool
	trigger string
	// tl1657: the pass number riding the CompactionStarted event (任务 1021),
	// so Progress emissions and the query face (CompactionLiveState) carry the
	// same pass the frontend card was opened with.
	pass    int
	started time.Time
	// tl1657: streamStarted latches the FIRST chunk. begin() records the pass
	// open (queue + admission + provider TTFT can sit on it for minutes — the
	// 20261117 09:40 incident: 101s from begin to the first chunk), while the
	// tokens/sec figure must measure the streaming window alone or a queued
	// pass reads as near-zero throughput forever.
	streamStarted bool
	// now is swappable for tests; real code never assigns it.
	now func() time.Time

	chars int // cumulative output runes (ChunkText + ChunkReasoning)

	// Chunked-fold progress, written by the chunkedFoldSummary callback.
	done, total int

	// Throttle bookkeeping: last emitted snapshot + wall clock.
	hasLastEmit   bool
	lastEmit      time.Time
	lastEmitChars int
	lastEmitDone  int
	lastEmitTotal int
	emittedCount  int // diagnostics/tests: total emissions this pass
}

func (m *compactionLiveMeter) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// begin opens the metering window for one compaction pass. Called right where
// CompactionStarted is emitted; callers defer stop so every exit path
// (success, abort, panic) closes the window. pass is the 任务 1021 pass number
// the Started event carried (0 = legacy caller without a tracked pass).
func (m *compactionLiveMeter) begin(trigger string, pass int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = true
	m.trigger = trigger
	m.pass = pass
	m.started = m.clock()
	m.streamStarted = false
	m.chars = 0
	m.done, m.total = 0, 0
	m.hasLastEmit = false
	m.lastEmit = time.Time{}
	m.lastEmitChars, m.lastEmitDone, m.lastEmitTotal = 0, 0, 0
	m.emittedCount = 0
}

// stop closes the metering window. Events after stop are impossible: the
// accumulation methods no-op on an inactive meter, so a resolved/aborted card
// can never receive a stale readout.
func (m *compactionLiveMeter) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = false
}

// addOutput accumulates one summarizer stream chunk and reports whether the
// throttle gate admits an emission now. No-op unless a pass is active.
// tl1657: the first chunk also latches the streaming window start — the tps
// denominator must exclude the queue/admission/TTFT stretch before the first
// token (the 20261117 101s-quiet incident).
func (m *compactionLiveMeter) addOutput(text string) bool {
	if text == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active {
		return false
	}
	if !m.streamStarted {
		m.streamStarted = true
		m.started = m.clock()
	}
	m.chars += utf8.RuneCountInString(text)
	return m.tryEmitLocked(true)
}

// setFoldProgress records chunked-fold (done/total) progress and reports
// whether an emission is admitted. Fold transitions are inherently bounded
// (≤ maxChunkedSummaryCalls leaves + ≤ one split each), so they bypass the
// 1s gate: the user sees N/M advance immediately and the FINAL N/M is
// guaranteed to be emitted even if no further chunk crosses the gate.
func (m *compactionLiveMeter) setFoldProgress(done, total int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active {
		return false
	}
	if done == m.done && total == m.total {
		return false
	}
	m.done, m.total = done, total
	return m.tryEmitLocked(false)
}

// tryEmitLocked applies the admission gate: only changed snapshots emit, and
// chunk-driven changes additionally respect compactionLiveEmitInterval (fold
// transitions pass respectInterval=false — bounded by maxChunkedSummaryCalls,
// they must reach the card immediately, including the final N/M after the
// last fragment). Callers hold m.mu.
//
// tl1657 zero-suppression: an all-invisible snapshot (tokens=0 — fewer than
// compactionLiveCharsPerToken runes so far — and no fold progress 0/0) never
// emits, including as the FIRST emission. The 20261117 incident's first and
// only emission was exactly that all-zero shape: it burned the throttled slot
// on a readout every frontend hides anyway (wire contract: 0 = "not emitted
// yet") and the queued pass then stayed silent for its whole streaming tail.
// Once visible, snapshots never regress (chars only grow), so the plain
// changed-check takes over.
func (m *compactionLiveMeter) tryEmitLocked(respectInterval bool) bool {
	now := m.clock()
	changed := m.chars != m.lastEmitChars || m.done != m.lastEmitDone || m.total != m.lastEmitTotal
	invisible := m.chars < compactionLiveCharsPerToken && m.done == 0 && m.total == 0
	if !changed || invisible {
		return false
	}
	if respectInterval && m.hasLastEmit && now.Sub(m.lastEmit) < compactionLiveEmitInterval {
		return false
	}
	m.hasLastEmit = true
	m.lastEmit = now
	m.lastEmitChars, m.lastEmitDone, m.lastEmitTotal = m.chars, m.done, m.total
	m.emittedCount++
	return true
}

// compactionLiveReadout is a wire-ready snapshot of the meter.
type compactionLiveReadout struct {
	Trigger      string
	Pass         int
	Tokens       int
	TokensPerSec int
	Done         int
	Total        int
}

// snapshot renders the current readout. ok is false when no pass is active.
func (m *compactionLiveMeter) snapshot() (compactionLiveReadout, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active {
		return compactionLiveReadout{}, false
	}
	tokens := m.chars / compactionLiveCharsPerToken
	var tps int
	if elapsed := m.clock().Sub(m.started); elapsed >= compactionLiveTpsMinElapsed && elapsed > 0 {
		tps = int(float64(tokens) / elapsed.Seconds())
	}
	return compactionLiveReadout{
		Trigger:      m.trigger,
		Pass:         m.pass,
		Tokens:       tokens,
		TokensPerSec: tps,
		Done:         m.done,
		Total:        m.total,
	}, true
}

// sealReadout renders the pass's FINAL readout for the seal frame (tl1689):
// the same shape snapshot() returns, with two deltas. ① The tps 500ms live
// noise floor does not apply — the final frame states the pass's real average
// whenever the streaming window is measurable (>0), because a fast pass that
// streamed entirely inside one throttle interval (the 20261118 08:50 shape:
// 26s pass, the ONLY progress frame read tokens=1 tps=0) would otherwise
// never show a throughput figure at all. ② A pass that accumulated nothing
// visible (chars below the ÷4 floor) seals nothing — the zero-suppression
// contract (wire 0 = "not emitted yet") stays intact. ok is false when no
// pass is active or nothing visible accumulated.
func (m *compactionLiveMeter) sealReadout() (compactionLiveReadout, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active || m.chars < compactionLiveCharsPerToken {
		return compactionLiveReadout{}, false
	}
	tokens := m.chars / compactionLiveCharsPerToken
	var tps int
	if elapsed := m.clock().Sub(m.started); elapsed > 0 {
		tps = int(float64(tokens) / elapsed.Seconds())
	}
	return compactionLiveReadout{
		Trigger:      m.trigger,
		Pass:         m.pass,
		Tokens:       tokens,
		TokensPerSec: tps,
		Done:         m.done,
		Total:        m.total,
	}, true
}

// compactionLiveBegin opens the metering window at a CompactionStarted emit.
func (a *Agent) compactionLiveBegin(trigger string, pass int) { a.compactionLive.begin(trigger, pass) }

// compactionLiveStop closes the window; callers defer it next to begin so
// Done, aborts, and panics all seal the pass.
func (a *Agent) compactionLiveStop() { a.compactionLive.stop() }

// compactionLiveSeal emits the pass's FINAL progress frame (tl1689) — one
// last CompactionProgress carrying the final tokens/tps readout, bypassing
// the 1s interval gate and the tps 500ms live floor, so a fast pass whose
// whole stream fit inside one throttle window still lands a visible frame
// before Done resolves the card (the 20261118 08:50 26s pass emitted only
// its first frame, tokens=1 tps=0, and the card never showed throughput).
// Callers invoke it immediately BEFORE the pass's CompactionDone emit so
// wire order stays first-frame → … → seal frame → Done; the deferred
// compactionLiveStop then makes any later emission impossible. No-op when
// the pass accumulated nothing visible.
func (a *Agent) compactionLiveSeal() {
	if readout, ok := a.compactionLive.sealReadout(); ok {
		a.emitCompactionLiveProgressFrom(readout)
	}
}

// CompactionRunning reports whether a compaction pass is in flight. 任务1147:
// the turn-stall watchdog consults it so a silent stretch inside a chunked
// compaction surfaces as a neutral "compaction running" notice instead of the
// "looks stuck, press Stop" warning (a pending summarizer request reads as a
// stall from the liveness tracker's seat, and the user was aborting healthy
// compactions because of it).
func (a *Agent) CompactionRunning() bool {
	if a == nil {
		return false
	}
	a.compactionLive.mu.Lock()
	defer a.compactionLive.mu.Unlock()
	return a.compactionLive.active
}

// CompactionLiveState is the query face for the desktop pending-card
// reconciliation (tl1657): a frontend that attached AFTER the pass opened
// (a resume-triggered compaction racing the webview's first subscription)
// missed CompactionStarted, so every CompactionProgress lands on no pending
// card and is dropped. The frontend polls this once per tab refresh and
// synthesizes the card when Running. All fields are the meter's own readout
// (same values the Progress wire carries).
type CompactionLiveState struct {
	Running      bool
	Trigger      string
	Pass         int
	Tokens       int
	TokensPerSec int
	Done         int
	Total        int
}

// CompactionLiveState snapshots the in-flight pass; Running=false (no pass)
// means there is nothing to reconcile and the caller builds no card — a pass
// that already ended is terminal history, not a live card.
func (a *Agent) CompactionLiveState() CompactionLiveState {
	if a == nil {
		return CompactionLiveState{}
	}
	readout, ok := a.compactionLive.snapshot()
	if !ok {
		return CompactionLiveState{}
	}
	return CompactionLiveState{
		Running:      true,
		Trigger:      readout.Trigger,
		Pass:         readout.Pass,
		Tokens:       readout.Tokens,
		TokensPerSec: readout.TokensPerSec,
		Done:         readout.Done,
		Total:        readout.Total,
	}
}

// CompactionLiveBeginForTest / CompactionLiveStopForTest drive the meter
// window directly, same convention as SetSessionReplayLimitsForTest — the
// turn-stall watchdog test (control) needs a live pass without staging a real
// compaction request.
func (a *Agent) CompactionLiveBeginForTest(trigger string) { a.compactionLiveBegin(trigger, 0) }

func (a *Agent) CompactionLiveStopForTest() { a.compactionLiveStop() }

// compactionLiveAddOutput feeds one summarizer chunk into the meter and emits
// a CompactionProgress event when the gate admits it. runSummaryRequest calls
// this per chunk; with no active pass it is a cheap mutex-guarded no-op, and
// runSummaryRequest only runs for compaction summarizer requests, so normal
// turns never reach this path at all.
func (a *Agent) compactionLiveAddOutput(text string) {
	if !a.compactionLive.addOutput(text) {
		return
	}
	a.emitCompactionLiveProgress()
}

// compactionLiveFoldProgress feeds chunked-fold N/M progress and emits when
// admitted (every change — see setFoldProgress).
func (a *Agent) compactionLiveFoldProgress(done, total int) {
	if !a.compactionLive.setFoldProgress(done, total) {
		return
	}
	a.emitCompactionLiveProgress()
}

// emitCompactionLiveProgress snapshots the meter and emits one
// CompactionProgress event.
func (a *Agent) emitCompactionLiveProgress() {
	readout, ok := a.compactionLive.snapshot()
	if !ok {
		return
	}
	a.emitCompactionLiveProgressFrom(readout)
}

// emitCompactionLiveProgressFrom emits one CompactionProgress event from a
// ready readout (shared by the chunk/fold-driven path and the tl1689 seal
// frame).
func (a *Agent) emitCompactionLiveProgressFrom(readout compactionLiveReadout) {
	// tl1637（tl556 复发诊断打点·证「发没发」）：Emit 前记一行实际下发的
	// 载荷。desktop.log 默认 Info 级，装机即可见；与前端 useController
	// compaction_progress 入口的 "progress received" 行构成三态判读——
	// Go 无本行=Go 侧断 / 有本行前端无 received=wire 断 / 两行都有而 UI 无
	// tokens=写入或渲染断。注意 Progress 事件 wire 上本就不携带 pass
	// （meter 不跟踪 pass），故以 trigger+fold N/M 关联两端。回收候裁：
	// 定位后转正式观测或随诊断件纪律删除。
	slog.Info("agent: compaction progress emit", "trigger", readout.Trigger,
		"pass", readout.Pass,
		"tokens", readout.Tokens, "tokens_per_sec", readout.TokensPerSec,
		"fold_done", readout.Done, "fold_total", readout.Total)
	a.svc.sink.Emit(event.Event{Kind: event.CompactionProgress, Compaction: event.Compaction{
		Trigger: readout.Trigger, Pass: readout.Pass, Done: readout.Done, Total: readout.Total,
		Tokens: readout.Tokens, TokensPerSec: readout.TokensPerSec,
	}})
}
