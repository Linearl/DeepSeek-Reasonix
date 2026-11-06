package control

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1459 stage 3 (P0-b): does a recovery fork, a disk adoption or the deferred
// promotion change the Goal's business binding (scopeID / continuationEpoch /
// goal / status)? CONSTITUTION D2 refinement: repair facilities may move
// physical pointers (the session path, the goal-state sidecar location) but
// must not write business state. These pins answer with the real code paths.

type goalBinding struct {
	goal    string
	status  string
	scopeID string
	epoch   uint64
}

func captureGoalBinding(c *Controller) goalBinding {
	c.goals.mu.Lock()
	defer c.goals.mu.Unlock()
	return goalBinding{goal: c.goals.goal, status: c.goals.status, scopeID: c.goals.scopeID, epoch: c.goals.continuationEpoch}
}

// divergedGoalControllers is divergedControllers1352 with a running Goal on
// the controller that will lose the race: two controllers append their own
// turn to one file, then A (holding the Goal) saves into a true divergence.
func divergedGoalControllers(t *testing.T, dir, name string) (*Controller, string, goalBinding) {
	t.Helper()
	path := filepath.Join(dir, name)
	const systemPrompt = "SYS"
	provA := &recordingProvider{streams: [][]provider.Chunk{replyChunks1352("a1"), replyChunks1352("a2")}}
	execA := agent.New(provA, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlA := New(Options{Runner: execA, Executor: execA, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "a", Sink: event.Discard})
	if err := ctrlA.RunTurn(context.Background(), "first from A"); err != nil {
		t.Fatalf("A first turn: %v", err)
	}
	if err := ctrlA.Snapshot(); err != nil {
		t.Fatalf("A snapshot 1: %v", err)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	provB := &recordingProvider{streams: [][]provider.Chunk{replyChunks1352("b2")}}
	execB := agent.New(provB, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlB := New(Options{Runner: execB, Executor: execB, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "b", Sink: event.Discard})
	ctrlB.Resume(loaded, path)
	if err := ctrlB.RunTurn(context.Background(), "second from B"); err != nil {
		t.Fatalf("B second turn: %v", err)
	}
	if err := ctrlA.RunTurn(context.Background(), "second from A"); err != nil {
		t.Fatalf("A second turn: %v", err)
	}
	// The Goal is installed between the turns and the conflicting save, so
	// the FSM itself never runs: any binding change below is the recovery
	// machinery's doing.
	ctrlA.SetGoal("finish the constitution")
	before := captureGoalBinding(ctrlA)
	if before.status != GoalStatusRunning || before.scopeID == "" {
		t.Fatalf("setup: goal binding = %+v", before)
	}
	if err := ctrlA.Snapshot(); err != nil {
		t.Fatalf("A snapshot 2 (conflict handled inside): %v", err)
	}
	return ctrlA, path, before
}

func readGoalSidecar(t *testing.T, sessionPath string) goalState {
	t.Helper()
	raw, err := os.ReadFile(goalStatePath(sessionPath))
	if err != nil {
		t.Fatalf("read goal sidecar for %s: %v", filepath.Base(sessionPath), err)
	}
	var state goalState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestRecoveryForkAndPromotionLeaveGoalBindingUntouched(t *testing.T) {
	dir := t.TempDir()
	ctrlA, path, before := divergedGoalControllers(t, dir, "goal-shared.jsonl")

	// Fork: the controller moved to the recovery copy.
	copyPath := ctrlA.SessionPath()
	if copyPath == path {
		t.Fatal("setup: the conflicting save did not fork")
	}
	if got := captureGoalBinding(ctrlA); got != before {
		t.Fatalf("recovery fork changed the Goal binding:\n before %+v\n after  %+v", before, got)
	}

	// A Goal write after the fork lands on the copy's sidecar (physical
	// pointer moved with the path) — the promotion must carry it back.
	ctrlA.GoalStrict(true)
	if st := readGoalSidecar(t, copyPath); st.ScopeID != before.scopeID || !st.Strict {
		t.Fatalf("post-fork goal write on the copy = %+v", st)
	}

	// Deferred promotion back onto the quiet main file.
	for range 6 {
		if err := ctrlA.SnapshotDurable(); err != nil {
			t.Fatalf("snapshot durable: %v", err)
		}
		if ctrlA.SessionPath() == path {
			break
		}
	}
	if got := ctrlA.SessionPath(); got != path {
		t.Fatalf("setup: promotion did not settle (path %s)", got)
	}
	if got := captureGoalBinding(ctrlA); got != before {
		t.Fatalf("recovery promotion changed the Goal binding:\n before %+v\n after  %+v", before, got)
	}
	// The persisted business state followed the promotion: the main sidecar
	// carries the same Goal, scope and the post-fork write.
	st := readGoalSidecar(t, path)
	if st.Goal != before.goal || st.Status != GoalStatusRunning || st.ScopeID != before.scopeID || !st.Strict {
		t.Fatalf("main goal sidecar after promotion = %+v, want goal/scope %q/%q running strict", st, before.goal, before.scopeID)
	}
	// And a fresh runtime resuming the main file restores the same scope.
	fresh := &goalMachine{}
	fresh.restoreFromState(path)
	if fresh.goal != before.goal || fresh.scopeID != before.scopeID || fresh.status != GoalStatusRunning {
		t.Fatalf("resume after promotion restored %q/%q/%q", fresh.goal, fresh.scopeID, fresh.status)
	}
}

func TestRecoveryDiskAdoptionLeavesGoalBindingUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adopt.jsonl")
	s := agent.NewSession("SYS")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "question"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("SYS"), agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SessionDir: dir, SessionPath: path, Sink: event.Discard})
	c.SetGoal("finish the constitution")
	before := captureGoalBinding(c)
	if !c.adoptDiskSession(path) {
		t.Fatal("adoptDiskSession failed")
	}
	if got := captureGoalBinding(c); got != before {
		t.Fatalf("disk adoption changed the Goal binding:\n before %+v\n after  %+v", before, got)
	}
}

// TestRecoveryFacilitiesOnlyReadGoalState is the structural half of the D2
// boundary: the recovery/promotion functions may move the goal sidecar's
// physical pointer (setStatePath) and read Goal state for logs, but never call
// a Goal mutator. A future "just bump the epoch on promote" fails here.
func TestRecoveryFacilitiesOnlyReadGoalState(t *testing.T) {
	facilities := map[string]bool{
		"applyPendingRecoveryPromotion": true, "armPendingRecoveryPromotion": true,
		"commitRecoveredSession": true, "adoptDiskSession": true, "rebindCheckpoints": true,
		"recoverSnapshotConflict": true, "recoverSnapshotConflictLadder": true,
		"recoverExternallyRemovedSession": true, "recoverExternallyRemovedSessionTraced": true,
		"recoverShutdownSnapshot": true, "recoverShutdownSnapshotTraced": true, "recoverShutdownSave": true,
		"takeoverDivergedRevLag": true, "reconcileWrongGenerationMetaOnRepeat": true,
		"transplantInFlightTurnMarker": true, "finishForkTrace": true, "beginForkTrace": true,
	}
	readOnly := map[string]bool{
		"setStatePath": true, "statusForDisplay": true, "continuationToken": true,
		"runtimeView": true, "active": true, "snapshot": true, "goalText": true, "deliveryState": true,
	}
	goalMutators := map[string]bool{
		"SetGoal": true, "SetGoalDurable": true, "SetGoalWithResearchMode": true, "ClearGoal": true,
		"stopGoal": true, "stopGoalForCancelledTurn": true, "ResumeGoal": true, "PauseGoal": true,
		"GoalStrict": true, "persistGoalState": true, "persistGoalStateAtEpoch": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	var offenders []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !facilities[fn.Name.Name] {
				continue
			}
			seen[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := sel.Sel.Name
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "goals" && !readOnly[name] {
					offenders = append(offenders, fset.Position(call.Pos()).String()+": "+fn.Name.Name+" calls goals."+name)
				}
				if goalMutators[name] {
					offenders = append(offenders, fset.Position(call.Pos()).String()+": "+fn.Name.Name+" calls "+name)
				}
				return true
			})
		}
	}
	var missing []string
	for name := range facilities {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("facility functions not found (renamed? update the list): %v", missing)
	}
	if len(offenders) > 0 {
		t.Fatalf("recovery facilities must not write Goal business state (CONSTITUTION D2):\n%s", strings.Join(offenders, "\n"))
	}
}
