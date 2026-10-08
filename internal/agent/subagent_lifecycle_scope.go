package agent

import "context"

// backgroundOwnedKey marks a child-run context whose lifecycle transitions are
// owned by a background job — a task started with run_in_background, or the
// children of a backgrounded fleet — rather than by the parent turn.
//
// The distinction is what keeps the desktop foreground-subagent registry
// (task 557) free of double counting: job-owned children are already visible
// through their job row in the running-work surfaces, so their lifecycle
// events must not create a second countable entry. Fleet children are the
// subtle case: their specs say foreground (the fleet owns backgrounding), so
// the flag cannot be derived from the spec alone and rides the context the
// job closure installs.
type backgroundOwnedKey struct{}

func withBackgroundOwnedLifecycle(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundOwnedKey{}, true)
}

// BackgroundOwnedLifecycle reports whether subagent lifecycle events emitted
// for runs under ctx belong to a background job instead of the parent turn.
func BackgroundOwnedLifecycle(ctx context.Context) bool {
	owned, _ := ctx.Value(backgroundOwnedKey{}).(bool)
	return owned
}
