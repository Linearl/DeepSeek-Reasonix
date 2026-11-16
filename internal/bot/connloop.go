package bot

import (
	"context"
	"log/slog"
	"time"

	"reasonix/internal/proc"
)

// This file holds the shared connection-lifecycle primitives for the platform
// adapters (qq / feishu / weixin). Each adapter previously hand-rolled the same
// "loop forever, attempt a connection, sleep a fixed 5s on failure" shape; two of
// them used time.Sleep, which ignores ctx and so left a Stop() blocked for the
// remaining delay. The cancellation-aware sleep fixes that; it lived here as
// SleepCtx until tl1620 promoted it to proc.SleepCtx (the shared home — the
// desktoplauncher watchdog was the last bare time.Sleep site). RunWithRetry
// folds the persistent-connection reconnect loop (qq / feishu) into one
// cancellation-aware, exponentially-backing-off helper.

const (
	defaultInitialDelay = 1 * time.Second
	defaultMaxDelay     = 30 * time.Second
	defaultResetAfter   = 60 * time.Second
)

// RetryConfig controls the reconnect/error backoff for RunWithRetry. The zero
// value uses sane defaults (1s → 30s exponential, reset after 60s up).
type RetryConfig struct {
	// InitialDelay is the wait after the first failed/closed attempt.
	InitialDelay time.Duration
	// MaxDelay caps the exponential backoff.
	MaxDelay time.Duration
	// ResetAfter is the minimum time an attempt must stay connected for the
	// backoff to reset to InitialDelay on its next failure — so a flaky reconnect
	// escalates while a long-healthy connection that finally drops retries fast.
	ResetAfter time.Duration
}

func (c RetryConfig) withDefaults() RetryConfig {
	if c.InitialDelay <= 0 {
		c.InitialDelay = defaultInitialDelay
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = defaultMaxDelay
	}
	if c.MaxDelay < c.InitialDelay {
		c.MaxDelay = c.InitialDelay
	}
	if c.ResetAfter <= 0 {
		c.ResetAfter = defaultResetAfter
	}
	return c
}

// nextDelay doubles cur, capped at max. The <= 0 guard catches overflow on very
// large durations.
func nextDelay(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next <= 0 || next > max {
		next = max
	}
	return next
}

// RunWithRetry drives a persistent-connection adapter. It calls attempt(ctx) —
// one full connection lifetime that blocks until the connection drops or errors —
// then waits a cancellation-aware exponential backoff and reconnects, repeating
// until ctx is cancelled. attempt MUST honor ctx and clean up its own connection
// before returning; it returns nil for a clean close and an error otherwise (both
// trigger a reconnect — only ctx cancellation stops the loop).
func RunWithRetry(ctx context.Context, log *slog.Logger, name string, cfg RetryConfig, attempt func(context.Context) error) {
	if log == nil {
		log = slog.Default()
	}
	cfg = cfg.withDefaults()
	delay := cfg.InitialDelay
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := attempt(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error(name+" connection failed", "err", err)
		} else {
			log.Warn(name + " connection closed")
		}
		if time.Since(start) >= cfg.ResetAfter {
			delay = cfg.InitialDelay
		}
		if !proc.SleepCtx(ctx, delay) {
			return
		}
		delay = nextDelay(delay, cfg.MaxDelay)
	}
}
