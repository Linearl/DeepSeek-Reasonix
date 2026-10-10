package control

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
)

// midTurnSnapshotInterval is atomic (nanoseconds) so a test shrinking it
// cannot race a previous test's still-parking autosave goroutine.
var midTurnSnapshotInterval atomic.Int64

func init() { midTurnSnapshotInterval.Store(int64(30 * time.Second)) }

// 任务 710 backoff knobs. The mid-turn autosave is a crash-window nicety; on
// a session whose event log makes one save cost seconds (fork开发-新5: 96MB
// log, 0.7-1.3s per save, 4431 saves over 7.4h saturating the disk), a fixed
// cadence multiplies the most expensive operation the session has. The tick
// interval therefore follows the measured per-save cost: each save slower
// than midTurnSaveBackoffFloorMs widens the interval by the same ratio, up to
// midTurnSaveBackoffCap.
const (
	midTurnSaveBackoffFloorMs   = int64(750)
	midTurnSaveBackoffFactorMax = int64(10)
	midTurnSaveBackoffCap       = 5 * time.Minute
)

// midTurnEffectiveInterval resolves this tick's interval: the base cadence,
// scaled by how long the session's last save actually took. A cheap append
// save keeps the base interval exactly; an expensive one backs off
// proportionally, bounded by the cap.
func midTurnEffectiveInterval(base time.Duration, lastSaveMs int64) time.Duration {
	if lastSaveMs <= midTurnSaveBackoffFloorMs || base <= 0 {
		return base
	}
	factor := lastSaveMs / midTurnSaveBackoffFloorMs
	if factor < 1 {
		factor = 1
	}
	if factor > midTurnSaveBackoffFactorMax {
		factor = midTurnSaveBackoffFactorMax
	}
	interval := base * time.Duration(factor)
	if interval > midTurnSaveBackoffCap {
		interval = midTurnSaveBackoffCap
	}
	return interval
}

// autosaveWhileRunning snapshots the session periodically while a turn runs,
// so an abrupt kill (SSH drop, force-quit) loses at most one interval of a
// long turn instead of all of it (#3772). Session.Save copies under the lock
// and replaces the file atomically, so racing the turn's appends is safe.
// The same tick drives the stall watchdog, so silence is checked as often as
// progress is persisted.
//
// P18-R3 (2026-10-04): the tick is non-blocking. The blocking form queued on
// the save-path mutex, and on a large active session those waits fed the very
// queue saturation the Q2 report measured (save-path lock waits of 19-40s) —
// a background durability nicety must not add queue pressure behind the
// turn's own synchronous persistence. A busy path skips the tick: the
// crash-recovery window widens by the skipped intervals only, while the
// turn-end snapshot (finishInFlightTurn) stays blocking and durable.
//
// 任务 710 (2026-10-10): the tick interval adapts to the session's measured
// save cost, so a multi-minute-per-save session cannot turn the nicety into a
// continuous IO storm; and the stall watchdog keeps its own cadence below.
func (c *Controller) autosaveWhileRunning(ctx context.Context) {
	base := time.Duration(midTurnSnapshotInterval.Load())
	timer := time.NewTimer(base)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if s := c.executorSession(); s != nil {
				timer.Reset(midTurnEffectiveInterval(base, s.LastSaveDurationMs()))
			} else {
				timer.Reset(base)
			}
			if _, err := c.snapshotWithDurability(false, false, false, true); err != nil && !errors.Is(err, errSavePathBusy) {
				slog.Warn("controller: mid-turn snapshot", "err", err)
			}
			c.warnIfTurnStalled(time.Now())
		}
	}
}

// executorSession returns the live session, or nil while the executor is
// absent (fresh/closed controller). The tick loop reads it per tick because
// session swaps (new/fork/switch) replace the executor.
func (c *Controller) executorSession() *agent.Session {
	if c.executor == nil {
		return nil
	}
	return c.executor.Session()
}
