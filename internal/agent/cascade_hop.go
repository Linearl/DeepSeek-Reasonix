package agent

import "context"

// Task 225: cascade delegation may chain (a task source that is itself a
// dispatched session forwards again). Each hop is stamped on the delegate's
// context so a cycle — mutual dispatches granting each other — terminates at
// maxCascadeHops instead of stacking Ask frames until the process dies.
// Reaching the limit is not an error: the prompt simply stays local, which is
// the pre-225 behavior.
const maxCascadeHops = 3

// MaxCascadeHops exposes the chain bound for log lines and tests.
func MaxCascadeHops() int { return maxCascadeHops }

type cascadeHopContextKey struct{}

// WithCascadeHop increments the delegation depth carried by ctx.
func WithCascadeHop(ctx context.Context) context.Context {
	depth, _ := CascadeHop(ctx)
	return context.WithValue(ctx, cascadeHopContextKey{}, depth+1)
}

// CascadeHop reports how many delegation hops ctx has already taken.
func CascadeHop(ctx context.Context) (int, bool) {
	depth, ok := ctx.Value(cascadeHopContextKey{}).(int)
	return depth, ok
}

// CascadeHopExhausted reports whether one more delegation would exceed the
// chain bound.
func CascadeHopExhausted(ctx context.Context) bool {
	depth, _ := CascadeHop(ctx)
	return depth >= maxCascadeHops
}
