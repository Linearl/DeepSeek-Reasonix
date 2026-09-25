package agent

import (
	"reflect"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 244 B6: the admission-table documentation in execute_batch.go is only
// as true as these pins. guards_test.go already covers complete_step and
// compress; these add the rest of the barrier enum, the nested-entry (task)
// claim, and the hook-flattening row of the table.

// TestPartitionToolCallsBarrierEnumSerial pins every remaining barrier member.
// The switch-level barriers are deliberately registered ReadOnly: even a tool
// that claims read-only is refused the parallel path, because receipts must
// land in provider order. task is registered as a writer — it is not in the
// switch; its serial entry comes from ReadOnly()==false, which is exactly why
// nested dispatch needs no #2456-style deadlock exemption (table comment).
func TestPartitionToolCallsBarrierEnumSerial(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "ro", readOnly: true})
	reg.Add(fakeTool{name: "task", readOnly: false})
	for _, barrier := range []string{"todo_write", "wait", "bash_output"} {
		reg.Add(fakeTool{name: barrier, readOnly: true})
	}

	for _, barrier := range []string{"task", "todo_write", "wait", "bash_output"} {
		calls := []provider.ToolCall{{Name: "ro"}, {Name: barrier}, {Name: "ro"}}
		got := partitionToolCalls(reg, calls)
		want := []toolCallBatch{
			{start: 0, end: 1, parallel: true},
			{start: 1, end: 2},
			{start: 2, end: 3, parallel: true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("barrier %q partition = %+v, want %+v", barrier, got, want)
		}
	}
}

// TestToolCallBatchesHookForcesProviderOrder pins the hook row of the table:
// with a workspace-mutating PreToolUse hook present (stubHooks implements no
// capability report, so the conservative branch treats it as mutating), the
// whole-workspace claim the hook holds must not race sibling calls — even
// pure read-only neighbours flatten to serial provider order. Hooks absent:
// the same calls keep the parallel fan-out.
func TestToolCallBatchesHookForcesProviderOrder(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "ro1", readOnly: true})
	reg.Add(fakeTool{name: "ro2", readOnly: true})
	calls := []provider.ToolCall{{Name: "ro1"}, {Name: "ro2"}}

	a := &Agent{svc: agentServices{tools: reg, hooks: &stubHooks{}}}
	got := a.toolCallBatches(calls)
	// Flattening flips the parallel bit in place; the segmentation itself is
	// unchanged (one batch for the contiguous read-only run).
	want := []toolCallBatch{{start: 0, end: 2, parallel: false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hook-on batches = %+v, want flattened %+v", got, want)
	}

	a.svc.hooks = nil
	got = a.toolCallBatches(calls)
	want = []toolCallBatch{{start: 0, end: 2, parallel: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hook-off batches = %+v, want parallel %+v", got, want)
	}
}
