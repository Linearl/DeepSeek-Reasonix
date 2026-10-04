package control

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
)

// midTurnSnapshotInterval is atomic (nanoseconds) so a test shrinking it
// cannot race a previous test's still-parking autosave goroutine.
var midTurnSnapshotInterval atomic.Int64

func init() { midTurnSnapshotInterval.Store(int64(30 * time.Second)) }

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
func (c *Controller) autosaveWhileRunning(ctx context.Context) {
	t := time.NewTicker(time.Duration(midTurnSnapshotInterval.Load()))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := c.snapshotWithDurability(false, false, false, true); err != nil && !errors.Is(err, errSavePathBusy) {
				slog.Warn("controller: mid-turn snapshot", "err", err)
			}
			c.warnIfTurnStalled(time.Now())
		}
	}
}
