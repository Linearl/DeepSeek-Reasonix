package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"reasonix/internal/agent"
)

// Node failure reasons (tl1454): a stable code per way a node can stop the
// run, so the panel can say what happened in plain words next to the
// engine's technical detail.
const (
	ReasonTimeout        = "timeout"
	ReasonCancelled      = "cancelled"
	ReasonBudget         = "budget"
	ReasonSubagentError  = "subagent_error"
	ReasonResultContract = "result_contract"
	ReasonGateFailed     = "gate_failed"
	ReasonGateError      = "gate_error"
	ReasonGateDenied     = "gate_denied"
	ReasonInternal       = "internal"
)

// NodeError is the run error when a node aborted the run: the node and the
// classified reason travel with the original error. Error() and the Unwrap
// chain are the pre-tl1454 error, so errors.Is(err, context.Canceled) and
// every message reader keep working.
type NodeError struct {
	NodeID string
	Reason string
	Err    error
}

func (e *NodeError) Error() string { return e.Err.Error() }
func (e *NodeError) Unwrap() error { return e.Err }

// reasonError tags an error with its failure reason on its way to abortNode.
type reasonError struct {
	reason string
	err    error
}

func (e *reasonError) Error() string { return e.err.Error() }
func (e *reasonError) Unwrap() error { return e.err }

func withReason(reason string, err error) error {
	if err == nil {
		return nil
	}
	return &reasonError{reason: reason, err: err}
}

// reasonOf resolves the failure reason of an abort error.
func reasonOf(err error) string {
	var tagged *reasonError
	var budget *BudgetExceededError
	var timeout *NodeTimeoutError
	var denied *GateDeniedError
	switch {
	case errors.As(err, &tagged):
		return tagged.reason
	case errors.As(err, &budget):
		return ReasonBudget
	case errors.As(err, &timeout):
		return ReasonTimeout
	case errors.As(err, &denied):
		return ReasonGateDenied
	case errors.Is(err, context.Canceled):
		return ReasonCancelled
	default:
		return ReasonSubagentError
	}
}

// NodeTimeoutError is the cancel cause of a node whose declared timeout
// elapsed (tl1454).
type NodeTimeoutError struct {
	NodeID  string
	Timeout time.Duration
}

func (e *NodeTimeoutError) Error() string {
	return fmt.Sprintf("node %q timed out: no result within %s (declared timeout)", e.NodeID, e.Timeout)
}

// GateDeniedError is returned by a guarded gate runner when the user's
// permission rules explicitly deny the literal command (tl1454 panel runs).
type GateDeniedError struct {
	Command string
	Rule    string
}

func (e *GateDeniedError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("gate command %q is denied by your permission rules (%s)", e.Command, e.Rule)
	}
	return fmt.Sprintf("gate command %q is denied by your permission rules", e.Command)
}

// BudgetExceededError is the run error when a run-level budget axis is spent
// (tl1454). After names the node whose completion crossed the line ("" when
// the wall clock ran out mid-flight).
type BudgetExceededError struct {
	Axis  string // "tokens" | "rounds" | "wall"
	Used  int64  // tokens / rounds, or wall milliseconds
	Limit int64
	After string
}

func (e *BudgetExceededError) Error() string {
	switch e.Axis {
	case "wall":
		return fmt.Sprintf("workflow budget exceeded: wall clock %s >= %s budget",
			(time.Duration(e.Used) * time.Millisecond).Round(time.Second), time.Duration(e.Limit)*time.Millisecond)
	default:
		if e.After != "" {
			return fmt.Sprintf("workflow budget exceeded: %s %d >= %d budget (crossed after node %q)", e.Axis, e.Used, e.Limit, e.After)
		}
		return fmt.Sprintf("workflow budget exceeded: %s %d >= %d budget", e.Axis, e.Used, e.Limit)
	}
}

// NodeObservation is what the engine reports per node execution through
// Engine.Observe (tl1454): the spend of that execution, the failure reason
// when it aborted, and a non-fatal contract note. The 686 event contract is
// untouched — observations are a host-side channel like Checkpoint.
type NodeObservation struct {
	NodeID   string
	Tokens   int
	Requests int
	Reason   string
	Contract string
}

// budgetMeter accumulates sub-agent spend for one run. It is shared with the
// concurrent dispatch segment, so it carries its own lock (runState stays
// single-writer). A nil meter (no budget declared) is a no-op everywhere.
type budgetMeter struct {
	mu        sync.Mutex
	tokens    int
	requests  int
	maxTokens int
	maxRounds int
	after     string
}

func newBudgetMeter(b *Budget) *budgetMeter {
	if b == nil || (b.Tokens <= 0 && b.Rounds <= 0) {
		return nil
	}
	return &budgetMeter{maxTokens: b.Tokens, maxRounds: b.Rounds}
}

func (m *budgetMeter) add(nodeID string, tokens, requests int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.overLocked() != nil
	m.tokens += tokens
	m.requests += requests
	if !before && m.overLocked() != nil {
		m.after = nodeID
	}
}

func (m *budgetMeter) exceeded() *BudgetExceededError {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.overLocked()
}

func (m *budgetMeter) overLocked() *BudgetExceededError {
	if m.maxTokens > 0 && m.tokens >= m.maxTokens {
		return &BudgetExceededError{Axis: "tokens", Used: int64(m.tokens), Limit: int64(m.maxTokens), After: m.after}
	}
	if m.maxRounds > 0 && m.requests >= m.maxRounds {
		return &BudgetExceededError{Axis: "rounds", Used: int64(m.requests), Limit: int64(m.maxRounds), After: m.after}
	}
	return nil
}

// remainingTokens is the token headroom handed to a child as its own task
// budget (soft landing via agent.WithTaskBudget); ok is false when the run
// declares no token axis.
func (m *budgetMeter) remainingTokens() (int, bool) {
	if m == nil || m.maxTokens <= 0 {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxTokens - m.tokens, true
}

// wallBudgetCause is the cancel cause of a run whose budget.wall elapsed.
type wallBudgetCause struct {
	limit   time.Duration
	started time.Time
}

func (c *wallBudgetCause) Error() string { return "workflow wall-clock budget elapsed" }

func (c *wallBudgetCause) budgetError() *BudgetExceededError {
	return &BudgetExceededError{Axis: "wall", Used: time.Since(c.started).Milliseconds(), Limit: c.limit.Milliseconds()}
}

// withWallBudget bounds ctx by the run's wall budget (no-op when unset).
func withWallBudget(ctx context.Context, b *Budget) (context.Context, context.CancelFunc) {
	wall := b.WallDuration()
	if wall <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeoutCause(ctx, wall, &wallBudgetCause{limit: wall, started: time.Now()})
}

// stopCause converts a stopped context into the error the run reports: the
// wall budget becomes a BudgetExceededError, a node timeout its own error,
// anything else (user cancel) the context's plain error.
func stopCause(ctx context.Context) error {
	cause := context.Cause(ctx)
	var wall *wallBudgetCause
	if errors.As(cause, &wall) {
		return wall.budgetError()
	}
	var timeout *NodeTimeoutError
	if errors.As(cause, &timeout) {
		return timeout
	}
	return ctx.Err()
}

// meteredCall dispatches one sub-agent execution with a usage sink on its ctx
// and folds the recorded spend into the meter. Usage is returned so the
// single writer can attribute it to the node.
func (e *Engine) meteredCall(ctx context.Context, meter *budgetMeter, req SubagentRequest) (string, spend, error) {
	if rem, ok := meter.remainingTokens(); ok {
		if rem <= 0 {
			return "", spend{}, meter.exceeded()
		}
		ctx = agent.WithTaskBudget(ctx, agent.TaskBudget{Tokens: rem})
	}
	ctx = agent.WithSubagentUsageSink(ctx)
	answer, err := e.runSubagent(ctx, req)
	usage, _ := agent.SubagentUsage(ctx)
	meter.add(req.NodeID, usage.Tokens, usage.Requests)
	return answer, spend{tokens: usage.Tokens, requests: usage.Requests}, err
}

// spend is one or more executions' sub-agent usage.
type spend struct {
	tokens   int
	requests int
}

func (s *spend) plus(o spend) {
	s.tokens += o.tokens
	s.requests += o.requests
}

// ReportSubagentUsage records one execution's spend from inside a
// SubagentRunner (tl1454). The production runner never needs it — the task
// substrate records through the same ctx sink — but simulated runners use it
// to exercise the budget through the production-shaped path.
func ReportSubagentUsage(ctx context.Context, tokens, requests int) {
	agent.RecordSubagentUsage(ctx, agent.SubagentUsageSummary{Tokens: tokens, Requests: requests})
}

// classifyCallError settles a dispatched execution's error against the
// contexts it ran under: the node's own timeout or the run's wall budget turn
// into their typed errors — a deadline of ours that fired is a failure even
// when some text came back (the timeout is the node's contract). Anything
// else (a user cancel racing a finished answer) keeps the pre-tl1454 outcome.
func classifyCallError(callCtx context.Context, err error) error {
	if callCtx.Err() == nil {
		return err
	}
	cause := context.Cause(callCtx)
	var timeout *NodeTimeoutError
	var wall *wallBudgetCause
	switch {
	case errors.As(cause, &timeout):
		return timeout
	case errors.As(cause, &wall):
		return wall.budgetError()
	default:
		return err
	}
}

// missingResultFields lists declared typed-result fields absent from the
// parsed answer — a non-fatal contract note (tl1454): control flow stays
// exactly as before, the panel just stops hiding the gap.
func missingResultFields(n *Node, obj map[string]any) []string {
	var missing []string
	for _, f := range n.Result {
		if _, ok := obj[f.Name]; !ok {
			missing = append(missing, f.Name)
		}
	}
	return missing
}

// gateWorldError tags a gate world failure (the command never produced an
// exit code) with its reason: denied by the guard, stopped by the run's wall
// budget or a cancel, or a launch/timeout failure of the command itself.
func gateWorldError(ctx context.Context, err error) error {
	var denied *GateDeniedError
	if errors.As(err, &denied) {
		return err
	}
	if ctx.Err() != nil {
		if stopped := stopCause(ctx); stopped != nil {
			var budget *BudgetExceededError
			if errors.As(stopped, &budget) {
				return budget
			}
		}
		return err
	}
	return withReason(ReasonGateError, err)
}
