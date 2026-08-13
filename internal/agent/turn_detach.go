package agent

import (
	"context"
	"errors"
)

// ErrTurnDetached is returned by the run loop when the host requested the
// remaining work of the current turn to continue as a background job (#8170).
// Controllers distinguish it from cancellation: the turn is NOT treated as
// interrupted; it finished normally up to the detach boundary (the current
// tool round already committed), and the host snapshots the session and
// hands the remainder to a job.
var ErrTurnDetached = errors.New("turn detached to background job")

type turnDetachSignalKey struct{}

// WithTurnDetachSignal attaches a host-provided predicate that reports whether
// a detach was requested. The run loop consults it at turn boundaries.
func WithTurnDetachSignal(ctx context.Context, fn func() bool) context.Context {
	return context.WithValue(ctx, turnDetachSignalKey{}, fn)
}

func turnDetachSignal(ctx context.Context) func() bool {
	fn, _ := ctx.Value(turnDetachSignalKey{}).(func() bool)
	return fn
}
