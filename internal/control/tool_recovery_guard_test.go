package control

import (
	"errors"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// recoveryGuardFixture builds a controller whose executor session carries one
// unresolved write record, the exact shape the review panel resolves.
func recoveryGuardFixture(t *testing.T) *Controller {
	t.Helper()
	record := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "a1", CallID: "call-w", CanonicalTool: "write_file"}, State: provider.ToolRunUnknown, ReadOnly: false}
	s := agent.NewSession("")
	s.Messages = []provider.Message{{Role: provider.RoleAssistant, ID: "turn-1", ToolCalls: []provider.ToolCall{{ID: "call-w", Name: "write_file", Recovery: &record}}}}
	a := agent.New(nil, tool.NewRegistry(), s, agent.Options{}, event.Discard)
	c := &Controller{executor: a, sink: event.Discard}
	// Production controllers get a runtime epoch at construction; the revision
	// guard rejects an empty one, so initialize the snapshot the same way.
	c.runtimeState.sink = event.Discard
	c.refreshRuntimeState(event.Event{})
	return c
}

// X3 guard 拆分: a closed controller must not report "turn already running" —
// that state never heals by waiting, and the old blanket error sent users
// clicking a card no button could ever clear.
func TestResolveToolRecoveryClosedReportsClosedNotTurnRunning(t *testing.T) {
	c := recoveryGuardFixture(t)
	c.closed = true
	snap := c.ToolRecoverySnapshot()
	_, err := c.ResolveToolRecovery(nil, ToolRecoveryRequest{SessionPath: snap.SessionPath, RuntimeEpoch: snap.RuntimeEpoch, Revision: snap.Revision, AttemptID: "a1", Action: "dismiss"})
	if err == nil {
		t.Fatal("closed controller accepted a resolve")
	}
	if errors.Is(err, ErrTurnRunning) {
		t.Fatalf("closed controller misreported as turn running: %v", err)
	}
	if !strings.Contains(err.Error(), "session is closed") {
		t.Fatalf("error %q does not name the closed state", err)
	}
}

// X3 guard 拆分: a rotating controller names the rotation instead of the turn.
func TestResolveToolRecoveryRotatingReportsRotation(t *testing.T) {
	c := recoveryGuardFixture(t)
	c.rotating = true
	snap := c.ToolRecoverySnapshot()
	_, err := c.ResolveToolRecovery(nil, ToolRecoveryRequest{SessionPath: snap.SessionPath, RuntimeEpoch: snap.RuntimeEpoch, Revision: snap.Revision, AttemptID: "a1", Action: "dismiss"})
	if err == nil {
		t.Fatal("rotating controller accepted a resolve")
	}
	if errors.Is(err, ErrTurnRunning) {
		t.Fatalf("rotating controller misreported as turn running: %v", err)
	}
	if !strings.Contains(err.Error(), "switching") {
		t.Fatalf("error %q does not name the rotation state", err)
	}
}

// X3 核心: while a turn runs, the panel's inspect/confirm/reject/retry keep the
// turn-running rejection (a live turn owns the fence), but the explicit
// dismiss action goes through — it is the metadata-only escape hatch that
// makes the card clearable without first killing the turn.
func TestResolveToolRecoveryTurnRunningBlocksActionsButAllowsDismiss(t *testing.T) {
	c := recoveryGuardFixture(t)
	c.running = true
	snap := c.ToolRecoverySnapshot()
	base := ToolRecoveryRequest{SessionPath: snap.SessionPath, RuntimeEpoch: snap.RuntimeEpoch, Revision: snap.Revision, AttemptID: "a1"}

	_, err := c.ResolveToolRecovery(nil, func() ToolRecoveryRequest { r := base; r.Action = "inspect"; return r }())
	if !errors.Is(err, ErrTurnRunning) {
		t.Fatalf("inspect during a turn: err=%v, want ErrTurnRunning", err)
	}
	_, err = c.ResolveToolRecovery(nil, func() ToolRecoveryRequest { r := base; r.Action = "confirm"; return r }())
	if !errors.Is(err, ErrTurnRunning) {
		t.Fatalf("confirm during a turn: err=%v, want ErrTurnRunning", err)
	}
	// The wrapped error must still tell the user what unblocks it.
	if !strings.Contains(err.Error(), "stop the running turn") {
		t.Fatalf("error %q lost the actionable half", err)
	}

	r := base
	r.Action = "dismiss"
	snapAfter, err := c.ResolveToolRecovery(nil, r)
	if err != nil {
		t.Fatalf("dismiss during a running turn: %v", err)
	}
	if len(snapAfter.Calls) != 0 {
		t.Fatalf("dismiss left %d pending calls", len(snapAfter.Calls))
	}
	if c.rotating {
		t.Fatal("dismiss leaked the rotating exclusion flag")
	}
}
