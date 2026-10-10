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
	started time.Time
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
// (success, abort, panic) closes the window.
func (m *compactionLiveMeter) begin(trigger string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = true
	m.trigger = trigger
	m.started = m.clock()
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
func (m *compactionLiveMeter) addOutput(text string) bool {
	if text == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active {
		return false
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
func (m *compactionLiveMeter) tryEmitLocked(respectInterval bool) bool {
	now := m.clock()
	changed := !m.hasLastEmit ||
		m.chars != m.lastEmitChars || m.done != m.lastEmitDone || m.total != m.lastEmitTotal
	if !changed {
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
		Tokens:       tokens,
		TokensPerSec: tps,
		Done:         m.done,
		Total:        m.total,
	}, true
}

// compactionLiveBegin opens the metering window at a CompactionStarted emit.
func (a *Agent) compactionLiveBegin(trigger string) { a.compactionLive.begin(trigger) }

// compactionLiveStop closes the window; callers defer it next to begin so
// Done, aborts, and panics all seal the pass.
func (a *Agent) compactionLiveStop() { a.compactionLive.stop() }

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
	a.svc.sink.Emit(event.Event{Kind: event.CompactionProgress, Compaction: event.Compaction{
		Trigger: readout.Trigger, Done: readout.Done, Total: readout.Total,
		Tokens: readout.Tokens, TokensPerSec: readout.TokensPerSec,
	}})
}
