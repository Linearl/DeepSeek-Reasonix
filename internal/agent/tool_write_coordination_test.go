package agent

import (
	"testing"
)

// #9592: under mutating hooks, read-only tools and meta/delegation tools
// (wait, kill_shell, task) must not take the whole-workspace hold — that hold
// froze every read/wait/kill in the parent while a background writer ran.
// Write tools (bash/MCP/path-bound writers) keep the hold.
func TestReserveCoordinatedParentWriteExemptsReadOnlyAndMetaTools(t *testing.T) {
	root := t.TempDir()
	sched := NewSubagentScheduler(4, 2)
	a := &Agent{agentConfig: agentConfig{writeWorkspaceRoot: root, subagentDepth: 0}, svc: agentServices{writeScheduler: sched}}

	// Read-only tool: effects.WorkspaceMutation=false and not a guard target.
	readPlan := &toolCallPlan{hooksMayMutateWorkspace: true}
	readPlan.runTool = &recordingWriter{name: "read_file"}
	release, err := a.reserveCoordinatedParentWrite(readPlan)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if n := len(sched.ActiveWriterClaims()); n != 0 {
		t.Fatalf("read-only claims = %d, want 0", n)
	}

	// Meta/delegation tool (wait): excluded from parentWriteGuardTarget.
	metaPlan := &toolCallPlan{hooksMayMutateWorkspace: true}
	metaPlan.runTool = &recordingWriter{name: "wait"}
	release, err = a.reserveCoordinatedParentWrite(metaPlan)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if n := len(sched.ActiveWriterClaims()); n != 0 {
		t.Fatalf("meta-tool claims = %d, want 0", n)
	}

	// Write tool under mutating hooks keeps the whole-workspace hold.
	writePlan := &toolCallPlan{hooksMayMutateWorkspace: true}
	writePlan.runTool = &recordingWriter{name: "bash"}
	writePlan.effects.WorkspaceMutation = true
	release, err = a.reserveCoordinatedParentWrite(writePlan)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(sched.ActiveWriterClaims()); n != 1 {
		t.Fatalf("write-tool claims = %d, want 1", n)
	}
	release()
	if n := len(sched.ActiveWriterClaims()); n != 0 {
		t.Fatalf("claims after release = %d", n)
	}
}
