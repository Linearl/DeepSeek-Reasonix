package agent

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// openerProbeTool records whether the executing context carries the task-128
// host project opener, and which instance.
type openerProbeTool struct {
	seen  *tool.WorktreeProjectOpener
	found *bool
}

func (openerProbeTool) Name() string            { return "opener_probe" }
func (openerProbeTool) Description() string     { return "probe" }
func (openerProbeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (openerProbeTool) ReadOnly() bool          { return true }
func (p openerProbeTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	opener, ok := tool.WorktreeProjectOpenerFromContext(ctx)
	*p.found = ok
	if ok {
		*p.seen = opener
	}
	return "probed", nil
}

// Task 128: a host with project registration stamps the opener onto every
// tool-call context in executeOne, so open_isolated_worktree_project (and any
// sub-agent inheriting the parent call context) can register the new project.
func TestExecuteOneStampsWorktreeProjectOpener(t *testing.T) {
	opener := &recordingAgentOpener{}
	found := false
	var seen tool.WorktreeProjectOpener
	reg := tool.NewRegistry()
	reg.Add(openerProbeTool{seen: &seen, found: &found})
	gate := &recordingPermissionGate{allow: true}
	a := New(nil, reg, NewSession("sys"), Options{Gate: gate, WorktreeProjectOpener: opener}, event.Discard)

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{ID: "probe", Name: "opener_probe", Arguments: `{}`})
	if out.blocked {
		t.Fatalf("probe call blocked: %+v", out)
	}
	if !found {
		t.Fatal("executeOne did not stamp the worktree project opener onto the call context")
	}
	if _, ok := seen.(*recordingAgentOpener); !ok {
		t.Fatalf("stamped opener = %T, want the host instance", seen)
	}
}

// Task 128 boundary: a host without project registration (CLI) must leave the
// context clean — the tool degrades to path-only instead of calling a nil hook.
func TestExecuteOneWithoutWorktreeProjectOpenerLeavesContextClean(t *testing.T) {
	found := false
	var seen tool.WorktreeProjectOpener
	reg := tool.NewRegistry()
	reg.Add(openerProbeTool{seen: &seen, found: &found})
	gate := &recordingPermissionGate{allow: true}
	a := New(nil, reg, NewSession("sys"), Options{Gate: gate}, event.Discard)

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{ID: "probe", Name: "opener_probe", Arguments: `{}`})
	if out.blocked {
		t.Fatalf("probe call blocked: %+v", out)
	}
	if found {
		t.Fatal("context must not carry an opener when the host wires none")
	}
}

// recordingAgentOpener is a stand-in host registration capability.
type recordingAgentOpener struct {
	roots []string
}

func (r *recordingAgentOpener) OpenIsolatedWorktreeProject(_ context.Context, worktreeRoot string) error {
	r.roots = append(r.roots, worktreeRoot)
	return nil
}
