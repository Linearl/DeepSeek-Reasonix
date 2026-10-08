package control

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// RunInboxTurn synchronously claims and executes one durable item. Bot and ACP
// use this path so their blocking response sink remains attached through every
// queued follow-up while Controller still owns durable state and ack semantics.
func (c *Controller) RunInboxTurn(ctx context.Context, id string) error {
	st, err := c.ensureInbox()
	if err != nil {
		return err
	}
	meta, env, err := st.ReadItem(id)
	if err != nil {
		return err
	}
	if meta.State != sessioninbox.StateQueued {
		return sessioninbox.ErrInvalidState
	}
	run, block, err := c.prepareInboxRun(env)
	if err != nil {
		return err
	}
	if block != "" {
		_ = st.SetState(id, sessioninbox.StateBlocked, block)
		_ = st.SetPaused(true)
		return fmt.Errorf("%w: %s", sessioninbox.ErrInvalidState, block)
	}
	return c.runSynchronousTurn(ctx, func() error {
		c.inbox.admissionMu.Lock()
		defer c.inbox.admissionMu.Unlock()
		c.inbox.trackAdmission(id)
		defer c.inbox.untrackAdmission(id)
		if err := st.ClaimItem(id); err != nil {
			return err
		}
		c.inbox.mu.Lock()
		c.inbox.trackActive(id)
		c.inbox.mu.Unlock()
		c.emitInboxUserInput(id, firstNonEmptyStr(env.DisplayText, env.SubmitText))
		return nil
	}, run)
}

func (c *Controller) prepareInboxRun(env sessioninbox.PromptEnvelope) (func(context.Context) error, string, error) {
	submit, frozenImages, block, err := applyInboxReferences(env)
	if err != nil || block != "" {
		return nil, block, err
	}
	display := firstNonEmptyStr(env.DisplayText, submit)
	raw := firstNonEmptyStr(env.RawText, submit)
	requests := controlInvocationsFromInbox(env)
	if len(requests) == 0 {
		return func(ctx context.Context) error {
			return c.runGoalLoopWithFrozenImagesRawDisplay(c.withTurnFormat(ctx, strings.TrimSpace(env.Format)), submit, raw, display, frozenImages)
		}, "", nil
	}
	prepared, err := c.prepareInvocationTurn(submit, requests)
	if err != nil {
		return nil, err.Error(), nil
	}
	return func(ctx context.Context) error {
		return c.runPreparedInvocationTurn(c.withTurnFormat(ctx, strings.TrimSpace(env.Format)), prepared, submit, raw, display, frozenImages)
	}, "", nil
}

// emitInboxUserInput announces a consumed inbox item as the input of a newly
// admitted turn (task 580). The wire protocol has no other user-message
// channel — composer submissions render their row optimistically and history
// reloads only happen on hydrate — so without this event an externally
// triggered turn (collab mail, idle-turn bridge, bot/ACP) starts with no user
// row on the transcript. Best-effort by contract: a nil/empty sink or blank
// display must never fail the admission that is already reserved.
func (c *Controller) emitInboxUserInput(itemID, display string) {
	if c == nil || strings.TrimSpace(display) == "" {
		return
	}
	c.sink.Emit(event.Event{Kind: event.UserInput, Text: display, ItemID: itemID})
}

// submitPreparedInboxTurn starts an already-classified inbox envelope without
// interpreting slash commands, shell shortcuts, or @references a second time.
// display is the envelope's user-visible text (task 580): once admission
// succeeds it is announced to frontends as a UserInput event so the consumed
// message renders as a user row immediately instead of waiting for a history
// reload.
func (c *Controller) submitPreparedInboxTurn(itemID, display string, run func(context.Context) error) admissionResult {
	return c.runGuardedInbox(run, func() {
		c.inbox.mu.Lock()
		c.inbox.trackActive(itemID)
		c.inbox.mu.Unlock()
		c.emitInboxUserInput(itemID, display)
	})
}

func sessionInboxInvocations(requests []InvocationRequest) []sessioninbox.StructuredInvocation {
	if len(requests) == 0 {
		return nil
	}
	out := make([]sessioninbox.StructuredInvocation, 0, len(requests))
	for _, request := range requests {
		out = append(out, sessioninbox.StructuredInvocation{Name: request.Name, Kind: request.Kind, Offset: request.Offset})
	}
	return out
}

func controlInvocationsFromInbox(env sessioninbox.PromptEnvelope) []InvocationRequest {
	stored := env.Invocations
	if len(stored) == 0 && env.Invocation != nil {
		stored = []sessioninbox.StructuredInvocation{*env.Invocation}
	}
	out := make([]InvocationRequest, 0, len(stored))
	for _, invocation := range stored {
		out = append(out, InvocationRequest{Name: invocation.Name, Kind: invocation.Kind, Offset: invocation.Offset})
	}
	return out
}
