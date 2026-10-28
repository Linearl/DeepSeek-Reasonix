package agent

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl1209: the foreground sub-agent cancel registry. A synchronous child blocks
// the parent turn on the turn ctx; the registry gives each run its own derived
// cancel so one child can be terminated without tearing down the turn.

func TestCancelSubagentRunCancelsRegisteredRun(t *testing.T) {
	tool := &TaskTool{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tool.registerForegroundRun("sa_reg", cancel)
	if !tool.CancelSubagentRun("sa_reg") {
		t.Fatal("CancelSubagentRun = false, want true for a registered running ref")
	}
	if ctx.Err() == nil {
		t.Fatal("derived context was not cancelled by CancelSubagentRun")
	}
	// The registry entry stays armed until the run unregisters (the run's own
	// defer); a second cancel on the same ref is still a hit — cancelling an
	// already-cancelled context is a no-op.
	if !tool.CancelSubagentRun("sa_reg") {
		t.Fatal("second CancelSubagentRun = false, want true while the entry is still registered")
	}
}

func TestCancelSubagentRunMissesUnregistered(t *testing.T) {
	tool := &TaskTool{}
	if tool.CancelSubagentRun("sa_never") {
		t.Fatal("CancelSubagentRun = true for an unknown ref, want false")
	}
	if tool.CancelSubagentRun("") {
		t.Fatal("CancelSubagentRun = true for an empty ref, want false")
	}
	var nilTool *TaskTool
	if nilTool.CancelSubagentRun("sa_nil") {
		t.Fatal("nil tool CancelSubagentRun = true, want false")
	}
}

func TestUnregisterForegroundRunReleasesAndDisarms(t *testing.T) {
	tool := &TaskTool{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tool.registerForegroundRun("sa_arm", cancel)
	tool.unregisterForegroundRun("sa_arm")
	if ctx.Err() == nil {
		t.Fatal("unregister must release the derived context (context leak guard)")
	}
	if tool.CancelSubagentRun("sa_arm") {
		t.Fatal("CancelSubagentRun = true after unregister, want false")
	}
	// Double unregister is the explicit+defer shape: must stay safe.
	tool.unregisterForegroundRun("sa_arm")
}

func TestSubagentMetaCarriesDispatchDescription(t *testing.T) {
	store := NewSubagentStore(t.TempDir())
	run, err := store.PrepareFresh(SubagentSpec{
		Kind: "skill", Name: "ask", Description: "核对凭据文件的读取链",
		WorkspaceRoot: t.TempDir(), ParentSession: "parent-desc",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Release()
	if run.Meta.Description != "核对凭据文件的读取链" {
		t.Fatalf("meta.Description = %q, want the dispatch line", run.Meta.Description)
	}
}

// The dispatch description is display-only: a continue_from whose spec now
// carries a different description must still be accepted — description is
// deliberately outside the continuation identity (validateMeta).
func TestValidateMetaIgnoresDescription(t *testing.T) {
	store := NewSubagentStore(t.TempDir())
	run, err := store.PrepareFresh(SubagentSpec{
		Kind: "skill", Name: "ask", Description: "旧描述",
		WorkspaceRoot: t.TempDir(), ParentSession: "parent-vd",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Release()
	meta := run.Meta
	meta.Status = SubagentCompleted
	newSpec := SubagentSpec{
		Kind: "skill", Name: "ask", Description: "换一条新描述",
		WorkspaceRoot: meta.WorkspaceRoot, ParentSession: "parent-vd",
		SystemPrompt: "sys",
	}
	if err := validateMeta(meta, newSpec); err != nil {
		t.Fatalf("validateMeta with a changed description = %v, want accepted", err)
	}
}

// The dispatch description rides the lifecycle telemetry via the run's own
// meta, so the desktop running-work panels can label rows without a signature
// change on any emit site.
func TestEmitSubagentLifecycleCarriesDescription(t *testing.T) {
	sink := &lifecycleCaptureSink{}
	run := &SubagentRun{
		Ref: "sa_desc",
		Meta: SubagentMeta{
			Ref: "sa_desc", Name: "ask", Description: "翻译校对第二段",
			CreatedAt: time.Now().UTC(),
		},
	}
	emitSubagentLifecycle(sink, "child_created", "call-1", "ask", "m", "high", run, nil, false)
	if len(sink.entries) != 1 {
		t.Fatalf("lifecycle entries = %d, want 1", len(sink.entries))
	}
	info := sink.entries[0]
	if info.Description != "翻译校对第二段" {
		t.Fatalf("lifecycle Description = %q, want the dispatch line", info.Description)
	}
	if info.Skill != "ask" {
		t.Fatalf("lifecycle Skill = %q, want the worker name", info.Skill)
	}
}

// A foreground child stopped through its own run-level cancel finishes with a
// cancelled SubagentRunError while the parent ctx stays live — the progress
// card must read cancelled, not failed (tl1209).
func TestSubagentProgressCancelledOutcomeReadsCancelled(t *testing.T) {
	clock := newFakeProgressClock(time.Unix(0, 0))
	ch := make(chan event.Event, 64)
	trk := newTestTracker(t, clock, chanSink{ch: ch}, "child-cancel")
	trk.running()
	cancelled := NewSubagentRunError(nil, context.Canceled)
	if cancelled.Outcome.Status != SubagentOutcomeCancelled {
		t.Fatalf("outcome status = %q, want cancelled for a context.Canceled cause", cancelled.Outcome.Status)
	}
	trk.finish(nil, cancelled)

	terminals := 0
	for _, e := range collectFor(t, ch, 100*time.Millisecond) {
		if progressName(e) != event.SubagentProgressStatusName {
			continue
		}
		if progressOutput(e) == string(subagentPhaseCancelled) {
			terminals++
		}
		if progressOutput(e) == string(subagentPhaseFailed) {
			t.Fatal("a cancelled SubagentRunError must not surface as failed")
		}
	}
	if terminals != 1 {
		t.Fatalf("cancelled terminals = %d, want exactly one", terminals)
	}
}

// The agent-level cancel reaches the task tool through the registry lookup, so
// the CancelSubagentRun interface discovery must not be fooled by a registry
// without the task tool.
func TestAgentCancelSubagentRunWithoutTaskToolIsMiss(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	if a.CancelSubagentRun("sa_any") {
		t.Fatal("CancelSubagentRun = true without a task tool, want false")
	}
}
