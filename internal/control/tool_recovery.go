package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reasonix/internal/agent"

	"reasonix/internal/provider"
)

type ToolRecoverySnapshot struct {
	Silent       bool                         `json:"silent"`
	Statistics   agent.ToolRecoveryStatistics `json:"statistics"`
	SessionPath  string                       `json:"sessionPath"`
	RuntimeEpoch string                       `json:"runtimeEpoch"`
	Revision     string                       `json:"revision"`
	Calls        []provider.ToolCallRecord    `json:"calls"`
	RetryEnabled bool                         `json:"retryEnabled"`
}

type ToolRecoveryRequest struct {
	SessionPath  string `json:"sessionPath"`
	RuntimeEpoch string `json:"runtimeEpoch"`
	Revision     string `json:"revision"`
	AttemptID    string `json:"attemptId"`
	InspectionID string `json:"inspectionId"`
	Action       string `json:"action"` // inspect | confirm | reject | retry
}

func (c *Controller) ToolRecoverySnapshot() ToolRecoverySnapshot {
	view := ToolRecoverySnapshot{SessionPath: c.SessionPath(), RuntimeEpoch: c.RuntimeStateSnapshot().RuntimeEpoch, Calls: []provider.ToolCallRecord{}, RetryEnabled: os.Getenv("REASONIX_TOOL_RECOVERY_RETRY") == "1"}
	if c.executor != nil {
		view.Calls = c.executor.PendingToolRecovery()
		view.Statistics = c.executor.ToolRecoveryStatistics()
		view.Silent = c.executor.SilentToolRecovery()
	}
	// Raw parameters stay in the session. Frontends get immutable identities
	// and inspection facts, never an executable payload supplied by the UI.
	for i := range view.Calls {
		view.Calls[i].Arguments = nil
	}
	bytes, _ := json.Marshal(view)
	sum := sha256.Sum256(bytes)
	view.Revision = hex.EncodeToString(sum[:])
	return view
}

// HasPendingToolRecovery reports whether the session still carries unresolved
// effect records (任务461-P2 兜底②): the session's last step was a tool call
// whose outcome is unknown — the exact shape an auto-resume must not blindly
// replay. The desktop reads this through a narrow type assertion
// (restartInterruptedToolProbe), so older fakes without the method simply
// never trigger the backstop.
func (c *Controller) HasPendingToolRecovery() bool {
	if c == nil || c.executor == nil {
		return false
	}
	return len(c.executor.PendingToolRecovery()) > 0
}

// SettleRestartInterruptedEffects hands the session's pending effect records
// to the restart resume chain (task 435): a session staged in the task-254
// roster was interrupted by OUR planned restart, so when its restore point
// resumes it, the leftover unknown-outcome records are settled host-side
// instead of lighting the 「中断的工具需要核实」 review panel. A session not in
// the roster — a genuine crash interruption — never reaches this method and
// keeps the manual review. Returns the number of records settled.
func (c *Controller) SettleRestartInterruptedEffects() int {
	if c == nil || c.executor == nil {
		return 0
	}
	return c.executor.ResolveInterruptedByRestart()
}

// ResolveToolRecovery uses the same admission exclusion and session write
// authority as model turns. No stale tab may resolve a replacement session.
//
// X3 guard split: the blanket ErrTurnRunning here used to swallow three very
// different states behind one misleading message — a live turn (transient),
// a session rotation (transient), and a closed controller (permanent for that
// surface) — so a stuck review card reported "turn already running" even when
// no turn existed. Each state now reports itself. The one deliberate gate
// relaxation is the "dismiss" action: settling a leftover record is a
// metadata-only mutation on the same setToolRecoveryRecord path the run loop
// itself uses mid-turn, so it stays available while a turn runs (that is the
// stuck-card escape hatch) and still refuses during rotation/teardown, where
// the executor session may be swapped underneath the attempt id.
func (c *Controller) ResolveToolRecovery(ctx context.Context, req ToolRecoveryRequest) (ToolRecoverySnapshot, error) {
	if err := c.ensureWriteAuthorityReady(); err != nil {
		return ToolRecoverySnapshot{}, err
	}
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return ToolRecoverySnapshot{}, fmt.Errorf("tool recovery unavailable: session is closed — switch tabs or reopen the session")
	case c.rotating:
		c.mu.Unlock()
		return ToolRecoverySnapshot{}, fmt.Errorf("session is switching — retry the review panel action in a moment")
	case (c.running || c.finishing) && req.Action != "dismiss":
		c.mu.Unlock()
		return ToolRecoverySnapshot{}, fmt.Errorf("%w — stop the running turn or wait for it to finish, then resolve the interrupted tool", ErrTurnRunning)
	}
	c.rotating = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.rotating = false; c.mu.Unlock() }()
	view := c.ToolRecoverySnapshot()
	if req.SessionPath != view.SessionPath || req.RuntimeEpoch == "" || req.RuntimeEpoch != view.RuntimeEpoch || req.Revision == "" || req.Revision != view.Revision {
		return view, fmt.Errorf("recovery snapshot changed; refresh before resolving")
	}
	if c.executor == nil {
		return view, fmt.Errorf("tool recovery unavailable")
	}
	var err error
	switch req.Action {
	case "inspect":
		_, err = c.executor.InspectToolRecovery(ctx, req.AttemptID)
	case "confirm", "reject":
		err = c.executor.ResolveToolRecovery(req.AttemptID, req.InspectionID, req.Action)
	case "dismiss":
		err = c.executor.ResolveToolRecoveryDismissed(req.AttemptID)
	case "retry":
		if !view.RetryEnabled {
			return view, fmt.Errorf("tool recovery retry is disabled")
		}
		err = c.executor.RetryToolRecovery(ctx, req.AttemptID, req.InspectionID)
	default:
		err = fmt.Errorf("unsupported recovery action")
	}
	result := c.ToolRecoverySnapshot()
	if err == nil && req.Action == "inspect" {
		for _, r := range c.executor.PendingToolRecovery() {
			if r.Identity.AttemptID == req.AttemptID {
				for i := range result.Calls {
					if result.Calls[i].Identity.AttemptID == req.AttemptID {
						result.Calls[i].Arguments = r.Arguments
					}
				}
			}
		}
	}
	return result, err
}
