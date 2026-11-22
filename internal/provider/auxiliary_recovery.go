package provider

import (
	"context"
	"errors"
	"time"
)

// StreamAuxiliary applies the finite policy to search and summarization. It
// buffers attempts so failed partial summaries never leak into their caller.
//
// tl1751: the out channel stays atomicity-buffered (drained only after the
// terminal event), but callers that need a LIVE readout can attach
// WithStreamProgress: the callback fires the moment a text/reasoning chunk
// arrives, BEFORE it enters the retry buffer. The callback is a read-only
// bypass — it must not retain the text (the live compaction meter counts
// runes only) — so retry atomicity on the content channel is untouched and a
// failed attempt's partial text, while never sent on out, still shows up in
// the progress feed (which is the point: the readout mirrors real generation,
// not the post-retry drain burst).
func StreamAuxiliary(ctx context.Context, p Provider, req Request) (<-chan Chunk, error) {
	ctx = WithManagedRecovery(WithIndependentRequestAttemptCounter(ctx))
	out := make(chan Chunk)
	var aggregate Usage
	// Resolved once: the callback rides the caller's ctx and is fixed for the
	// whole call, so the per-chunk cost is a nil check, not a ctx walk.
	progress := streamProgressFrom(ctx)
	go func() {
		defer close(out)
		send := func(c Chunk) bool {
			select {
			case out <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for attempt := range 4 {
			attemptCtx, cancel := context.WithCancel(ctx)
			ch, err := p.Stream(attemptCtx, req)
			var latest *Usage
			var chunks []Chunk
			complete := false
			bytes := 0
			if err == nil {
			loop:
				for {
					select {
					case <-ctx.Done():
						cancel()
						return
					case c, ok := <-ch:
						if !ok {
							break loop
						}
						if c.Type == ChunkError {
							err = c.Err
							if err == nil {
								err = errors.New("auxiliary provider error")
							}
							break loop
						}
						bytes += len(c.Text)
						if bytes > 16*1024*1024 {
							err = errors.New("auxiliary response exceeds local limit")
							break loop
						}
						if c.Type == ChunkUsage {
							latest = c.Usage
							continue
						}
						// tl1751: live-readout bypass, strictly BEFORE the append —
						// text/reasoning only (same chunks the compaction meter
						// counted at drain time before this bypass existed). The
						// buffer below keeps its retry-atomicity semantics unchanged.
						if progress != nil && (c.Type == ChunkText || c.Type == ChunkReasoning) {
							progress(c.Text)
						}
						complete = complete || c.Type == ChunkDone
						chunks = append(chunks, c)
					}
				}
				if err == nil && !complete {
					err = StreamInterrupt(errors.New("auxiliary response ended before terminal event"), "unexpected_eof")
				}
			}
			cancel()
			if latest == nil {
				aggregate.Unknown = true
			}
			if latest != nil {
				aggregate.PromptTokens += latest.PromptTokens
				aggregate.CompletionTokens += latest.CompletionTokens
				aggregate.TotalTokens += latest.TotalTokens
				aggregate.CacheHitTokens += latest.CacheHitTokens
				aggregate.CacheMissTokens += latest.CacheMissTokens
				aggregate.ReasoningTokens += latest.ReasoningTokens
				aggregate.CacheWriteTokens += latest.CacheWriteTokens
				aggregate.CacheWriteBilledTokens += latest.CacheWriteBilledTokens
				aggregate.FinishReason = latest.FinishReason
				aggregate.Estimated = aggregate.Estimated || latest.Estimated
				aggregate.Unknown = aggregate.Unknown || latest.Unknown
			}
			aggregate.RequestCount = RequestAttemptCount(ctx)
			if aggregate.RequestCount == 0 {
				aggregate.RequestCount = attempt + 1
			}
			if err == nil {
				send(Chunk{Type: ChunkUsage, Usage: &aggregate})
				for _, c := range chunks {
					if !send(c) {
						return
					}
				}
				return
			}
			f := ClassifyRecovery(err)
			// 任务912 P0-b: a rate limit is surfaced, never absorbed here. This
			// lane's backoff (2s/4s/8s) cannot roll a TPM/RPM window over, and
			// every caller owns a window-aware lane that must SEE the failure —
			// summarizeFold's 429 wait (Retry-After), the fragment wait, and
			// session-extract's resume. Absorbing it here just burned three
			// fast attempts and returned a half-waited answer to a caller that
			// was ready to wait properly.
			if !f.Retryable || RateLimited(err) || attempt == 3 {
				send(Chunk{Type: ChunkUsage, Usage: &aggregate})
				send(Chunk{Type: ChunkError, Err: err})
				return
			}
			delay := time.Duration(1<<attempt) * 2 * time.Second
			delay = max(delay, f.RetryAfter)
			if !auxiliarySleep(ctx, delay) {
				return
			}
		}
	}()
	return out, nil
}

// streamProgressKey carries the tl1751 live-readout bypass (see
// StreamAuxiliary). Absent callback = the bypass costs nothing: callers that
// don't wire a meter (web search, draft rewrite) keep their exact baseline
// behavior.
type streamProgressKey struct{}

// WithStreamProgress attaches a live-readout callback that StreamAuxiliary
// invokes with each text/reasoning chunk as it ARRIVES, before the chunk
// enters the atomicity buffer. The callback must not retain the text — it is
// a metering bypass, not a second content channel; retaining content would
// defeat the very atomicity guarantee the buffer exists for. It runs on the
// StreamAuxiliary producer goroutine and must therefore be cheap and
// goroutine-safe (the compaction meter is mutex-guarded and its sink is
// event.Sync-wrapped).
func WithStreamProgress(ctx context.Context, onProgress func(text string)) context.Context {
	return context.WithValue(ctx, streamProgressKey{}, onProgress)
}

func streamProgressFrom(ctx context.Context) func(text string) {
	if fn, ok := ctx.Value(streamProgressKey{}).(func(text string)); ok {
		return fn
	}
	return nil
}

type recoverySleeperKey struct{}

// WithRecoverySleeper supplies the owner clock without changing the retry policy.
func WithRecoverySleeper(ctx context.Context, sleep func(context.Context, time.Duration) bool) context.Context {
	return context.WithValue(ctx, recoverySleeperKey{}, sleep)
}
func auxiliarySleep(ctx context.Context, d time.Duration) bool {
	if sleep, ok := ctx.Value(recoverySleeperKey{}).(func(context.Context, time.Duration) bool); ok {
		return sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
