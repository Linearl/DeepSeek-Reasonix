package control

import (
	"context"
	"errors"
	"fmt"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// DetachSpec describes the continuation of a turn that the user moved to a
// background job (#8170). The snapshot file (the session transcript at the
// detach boundary) is owned by the caller; the runner copies it into the
// transcript store and deletes it again on completion.
type DetachSpec struct {
	ParentSession string
	WorkspaceRoot string
	SystemPrompt  string
	Label         string
}

// DetachRunner launches a background job that continues a detached turn from
// the given session-file snapshot and returns the job reference immediately
// (the job runs asynchronously). Implemented by the boot layer, which owns
// the provider/tool registry/subagent store.
type DetachRunner func(ctx context.Context, snapshotPath string, spec DetachSpec) (string, error)

// RequestDetach asks the running turn to hand its remaining work to a
// background job at the next tool-round boundary. It is a no-op unless a
// turn is currently running. The request self-clears: if the turn finishes
// before reaching a boundary, nothing is detached (best-effort).
func (c *Controller) RequestDetach() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return fmt.Errorf("no running turn to detach")
	}
	c.detachRequested = true
	return nil
}

// detachRequestedSignal is the predicate handed to the run loop; it reads
// under the controller mutex because the loop runs in its own goroutine.
func (c *Controller) detachRequestedSignal() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.detachRequested
}

func (c *Controller) clearDetachRequested() {
	c.mu.Lock()
	c.detachRequested = false
	c.mu.Unlock()
}

// performDetach executes the detach ritual: persist the transcript up to the
// boundary, then hand it to the background runner. The runner must return
// immediately (the job itself runs asynchronously).
func (c *Controller) performDetach(ctx context.Context, startMessages int) error {
	if c.detachRunner == nil {
		return fmt.Errorf("turn detach is not available in this session")
	}
	// Persist the transcript so the snapshot is complete (the current tool
	// round already committed in-memory).
	if durable, err := c.snapshotActivityIfChanged(startMessages); err != nil && !durable {
		return fmt.Errorf("detach: persist transcript: %w", err)
	}
	spec := DetachSpec{
		ParentSession: c.parentSessionID(),
		WorkspaceRoot: c.workspaceRoot,
		SystemPrompt:  systemPromptFromHistory(c.History()),
		Label:         "detached turn",
	}
	jobRef, err := c.detachRunner(ctx, c.SessionPath(), spec)
	if err != nil {
		return fmt.Errorf("detach: start background job: %w", err)
	}
	if jobRef != "" && c.sink != nil {
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Turn moved to background job " + jobRef})
	}
	return nil
}

// systemPromptFromHistory returns the first system-role message content.
func systemPromptFromHistory(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role == provider.RoleSystem {
			return m.Content
		}
	}
	return ""
}

// isTurnDetached reports whether err is the run-loop's detach sentinel.
func isTurnDetached(err error) bool {
	return errors.Is(err, agent.ErrTurnDetached)
}
