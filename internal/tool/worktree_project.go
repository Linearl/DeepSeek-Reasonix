package tool

import "context"

// WorktreeProjectOpener is an optional host hook that registers a newly created
// isolated worktree as a visible project (for example opening a desktop tab).
// Task 128: agents can create a worktree project without a host; hosts that
// want UI registration bind this on the tool-call context.
type WorktreeProjectOpener interface {
	OpenIsolatedWorktreeProject(ctx context.Context, worktreeRoot string) error
}

type worktreeProjectOpenerKey struct{}

// WithWorktreeProjectOpener binds a host implementation onto the call context.
func WithWorktreeProjectOpener(ctx context.Context, opener WorktreeProjectOpener) context.Context {
	if opener == nil {
		return ctx
	}
	return context.WithValue(ctx, worktreeProjectOpenerKey{}, opener)
}

// WorktreeProjectOpenerFromContext returns the host hook when present.
func WorktreeProjectOpenerFromContext(ctx context.Context) (WorktreeProjectOpener, bool) {
	opener, ok := ctx.Value(worktreeProjectOpenerKey{}).(WorktreeProjectOpener)
	return opener, ok && opener != nil
}
