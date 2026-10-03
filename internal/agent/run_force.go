package agent

import "context"

// 任务461-P7 三级终止 L3: the executor's force-abandon signal. The controller
// derives an independent force context for the running turn and stashes its
// Done channel on the turn context; the tool executor's watchdog abandons a
// wedged call (unknown effect, goroutine quarantined as a straggler) the
// moment the channel fires. L1 (normal cancel) fires only the turn context,
// so tools keep their graceful-exit window; L3 fires force alongside the turn
// cancel. The key lives here (not internal/control) because control imports
// agent, never the reverse.

type stopForceKey struct{}

// WithStopForce stamps the force-abandon signal onto the turn context. A nil
// channel means the host has no force half (older hosts / direct agent runs):
// the executor then keeps its pre-P7 wait-forever behavior.
func WithStopForce(ctx context.Context, forceDone <-chan struct{}) context.Context {
	if forceDone == nil {
		return ctx
	}
	return context.WithValue(ctx, stopForceKey{}, forceDone)
}

func stopForceDone(ctx context.Context) <-chan struct{} {
	if done, ok := ctx.Value(stopForceKey{}).(<-chan struct{}); ok {
		return done
	}
	return nil
}

// StopForceDone is the exported read side: hosts and tests observe whether a
// turn context carries the force signal without reaching into the value key.
func StopForceDone(ctx context.Context) <-chan struct{} {
	return stopForceDone(ctx)
}
