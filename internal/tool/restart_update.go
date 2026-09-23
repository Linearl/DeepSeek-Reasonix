package tool

import (
	"context"
	"sync/atomic"
)

// RestartUpdater is supplied by the host that owns the install layout. Binding it to
// the call context (rather than to the tool) keeps one stable tool schema across hosts
// that cannot restart anything, exactly as ContextCompressor does (task 81).
type RestartUpdater interface {
	RestartAndUpdate(ctx context.Context, sourceDir, version string) (string, error)
}

type restartUpdaterKey struct{}

// WithRestartUpdater binds the active host's restart-and-update implementation.
func WithRestartUpdater(ctx context.Context, updater RestartUpdater) context.Context {
	if updater == nil {
		return ctx
	}
	return context.WithValue(ctx, restartUpdaterKey{}, updater)
}

// RestartUpdaterFromContext returns the implementation bound to this tool
// call, falling back to the process-wide registration when the call path
// carries no context binding (capability-routed dispatch never crosses the
// agent's binding point — task 254 field report). Context bindings win.
func RestartUpdaterFromContext(ctx context.Context) (RestartUpdater, bool) {
	if updater, ok := ctx.Value(restartUpdaterKey{}).(RestartUpdater); ok && updater != nil {
		return updater, true
	}
	if updater := fallbackRestartUpdater.Load(); updater != nil {
		return *updater, true
	}
	return nil, false
}

// fallbackRestartUpdater mirrors fallbackAutonomousUpdate: one process-wide
// slot for call paths outside the agent context-binding point (task 254
// field report), registered by the desktop at startup and left nil in hosts
// that cannot restart anything.
var fallbackRestartUpdater atomic.Pointer[RestartUpdater]

// SetFallbackRestartUpdater registers the process-wide restart-and-update
// implementation. Later registrations replace earlier ones; nil is ignored.
func SetFallbackRestartUpdater(updater RestartUpdater) {
	if updater == nil {
		return
	}
	fallbackRestartUpdater.Store(&updater)
}
