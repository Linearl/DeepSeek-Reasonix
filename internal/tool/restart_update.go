package tool

import "context"

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

// RestartUpdaterFromContext returns the implementation bound to this tool call.
func RestartUpdaterFromContext(ctx context.Context) (RestartUpdater, bool) {
	updater, ok := ctx.Value(restartUpdaterKey{}).(RestartUpdater)
	return updater, ok && updater != nil
}
