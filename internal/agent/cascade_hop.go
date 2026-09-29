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
//
// Task 367 C1 — the hop semantics, pinned in code shape so a future edit
// cannot re-introduce interception of the normal single hop. The hop table:
//
//	depth 0  the requesting session itself (no delegation yet) — never blocked
//	depth 1  one delegation, the normal parent<->child hop — never blocked
//	depth 2  a second delegation (dispatched source that is itself dispatched)
//	         — still allowed, one hop from the bound
//	depth >= 3 (maxCascadeHops) the chain is deep enough to smell a grant
//	         cycle — the prompt degrades to LOCAL (pre-225 behavior), which is
//	         the anti-loop guard, not an error
//
// The single-hop guarantee is unconditional: a direct parent<->child grant can
// never be intercepted by hop accounting, regardless of config or future
// changes to maxCascadeHops.
func CascadeHopExhausted(ctx context.Context) bool {
	depth, _ := CascadeHop(ctx)
	if depth <= 1 {
		return false // single-hop delegation is always allowed (task 367 C1)
	}
	return depth >= maxCascadeHops
}
