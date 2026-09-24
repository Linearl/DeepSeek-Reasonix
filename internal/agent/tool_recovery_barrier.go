package agent

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func (a *Agent) emitToolStarted(c provider.ToolCall) error {
	readOnly := false
	if t, _, ambiguous := a.svc.tools.ResolveCall(c.Name); t != nil && len(ambiguous) == 0 {
		readOnly = t.ReadOnly()
	}
	ev := event.Tool{ID: c.ID, Name: c.Name, ReadOnly: readOnly, RunState: provider.ToolRunStarted}
	if c.Recovery != nil {
		ev.ReadOnly = c.Recovery.ReadOnly
		ev.AttemptID = c.Recovery.Identity.AttemptID
	}
	return event.EmitChecked(a.svc.sink, event.Event{Kind: event.ToolStarted, Tool: ev})
}

// finishRunRecovery keeps an unresolved effect from silently closing the run:
// it joins the barrier error so callers see the turn ended on an unconfirmed
// external effect. Task 299: exempt turns (auto/yolo/unattended — task 107
// P0-0) must not take that error either. Joining here would end every turn of
// an autopilot run with recovery_required even though the write fence already
// exempts it, so the goal loop would stall on a barrier nobody can press.
// The pending record itself is untouched: PendingToolRecovery still lists it
// for the panel and for after-the-fact review.
func (a *Agent) finishRunRecovery(ctx context.Context, err *error) {
	if toolRecoveryExempt(ctx) {
		return
	}
	for _, r := range a.PendingToolRecovery() {
		if !r.ReadOnly {
			*err = errors.Join(*err, ErrToolRecoveryRequired)
			return
		}
	}
}
func (a *Agent) checkToolRecoveryStart(ctx context.Context, p *toolCallPlan) (toolOutcome, bool) {
	if err := a.beginToolRecovery(ctx, p); err != nil {
		return toolOutcome{runState: provider.ToolRunNotStarted, blocked: true, output: "blocked: " + err.Error(), errMsg: err.Error()}, true
	}
	return toolOutcome{}, false
}
func uncertainToolError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
func recoveryFailureState(err error) provider.ToolRunState {
	if uncertainToolError(err) {
		return provider.ToolRunUnknown
	}
	return provider.ToolRunFailed
}
func assignRecoveryCallIDs(calls []provider.ToolCall) error {
	seen := map[string]bool{}
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = "call_" + rand.Text()
		}
		if seen[calls[i].ID] {
			return fmt.Errorf("provider returned duplicate tool call IDs")
		}
		seen[calls[i].ID] = true
	}
	return nil
}
