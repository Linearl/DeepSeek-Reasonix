package tool

import "context"

// RecallRequest names folded transcript positions to bring back, by the #n
// addresses a folded-work index carries, or a query that searches the folded
// region for something to read.
//
// The two forms are mutually exclusive: a request that gives both asks the
// host to read something the caller did not name, and one that gives neither
// names nothing.
type RecallRequest struct {
	// Positions are the #n addresses from the folded-work index, without the
	// leading '#'.
	Positions []int
	// Query searches the folded region instead of reading an address. The
	// search runs against the canonical transcript rather than the projection,
	// so the addresses it returns outlive the generations that lost them.
	Query string
	// Limit caps the number of search hits.
	Limit int
}

// RecallHit is one search result: what matched, and the address to read it
// at. The address always names a read unit, so reading it returns the call
// and the output it produced together.
type RecallHit struct {
	Position int
	Kind     string
	Tool     string
	Snippet  string
}

// RecallResult is one recall's content plus what it cost. Text is what the
// model reads; the counters let it see the generation's budget draining.
type RecallResult struct {
	Text       string
	Recalled   []int
	Missing    []int
	Hits       []RecallHit
	Searched   int // messages the query ran against
	Tokens     int
	BudgetLeft int
}

// ContextRecaller is supplied by the Agent that owns the active session, the
// same way ContextCompressor is: the canonical transcript never leaves the
// agent, so a tool asks it for a position rather than reading a session file.
type ContextRecaller interface {
	RecallContext(context.Context, RecallRequest) (RecallResult, error)
}

type contextRecallerKey struct{}

// WithContextRecaller binds the active session's recaller to a tool call.
func WithContextRecaller(ctx context.Context, recaller ContextRecaller) context.Context {
	if recaller == nil {
		return ctx
	}
	return context.WithValue(ctx, contextRecallerKey{}, recaller)
}

// ContextRecallerFromContext returns the recaller bound to this tool call.
func ContextRecallerFromContext(ctx context.Context) (ContextRecaller, bool) {
	recaller, ok := ctx.Value(contextRecallerKey{}).(ContextRecaller)
	return recaller, ok && recaller != nil
}
