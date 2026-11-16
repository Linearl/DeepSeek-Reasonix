package proc

import (
	"context"
	"time"
)

// SleepCtx waits for d or until ctx is cancelled, whichever comes first. It
// returns true if the full duration elapsed and false if ctx was cancelled (or
// was already cancelled on entry). A non-positive d returns true immediately.
//
// tl1620 promoted this from internal/bot/connloop.go (which keeps the
// reconnect-loop family) so packages outside bot stop hand-rolling
// time.Sleep — which ignores ctx and leaves a Stop() blocked for the
// remaining delay, the counterexample the connloop header comment names
// (desktoplauncher/watchdog.go was the last bare site and now uses this).
func SleepCtx(ctx context.Context, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
