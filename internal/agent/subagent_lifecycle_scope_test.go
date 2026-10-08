package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// lifecycleCaptureSink records subagent lifecycle telemetry so tests can
// assert the content-free payload (phases, background ownership) end to end.
type lifecycleCaptureSink struct {
	entries []event.SubagentLifecycleInfo
}

func (s *lifecycleCaptureSink) RecordSubagentLifecycle(info event.SubagentLifecycleInfo) {
	s.entries = append(s.entries, info)
}

func (s *lifecycleCaptureSink) Emit(event.Event) {}

func TestEmitSubagentLifecycleCarriesBackgroundFlag(t *testing.T) {
	run := &SubagentRun{Ref: "sa_flag", Meta: subagentMetaForRefTest("sa_flag")}
	sink := &lifecycleCaptureSink{}

	emitSubagentLifecycle(sink, "child_running", "call-1", "task", "m", "", run, nil, true)
	if len(sink.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(sink.entries))
	}
	if !sink.entries[0].Background {
		t.Fatalf("background-owned emit must carry Background=true: %+v", sink.entries[0])
	}

	emitSubagentLifecycle(sink, "child_completed", "call-1", "task", "m", "", run, &SubagentOutcome{Status: SubagentOutcomeCompleted}, false)
	if sink.entries[1].Background {
		t.Fatalf("foreground emit must carry Background=false: %+v", sink.entries[1])
	}
}

// subagentMetaForRefTest builds a minimal meta so the emit guard (non-empty
// ref) passes without a full store round-trip.
func subagentMetaForRefTest(ref string) (meta SubagentMeta) {
	meta.Ref = ref
	return meta
}

func TestBackgroundOwnedLifecycleMarker(t *testing.T) {
	ctx := context.Background()
	if BackgroundOwnedLifecycle(ctx) {
		t.Fatal("plain context must not be background-owned")
	}
	marked := withBackgroundOwnedLifecycle(ctx)
	if !BackgroundOwnedLifecycle(marked) {
		t.Fatal("marked context must be background-owned")
	}
	// The marker must survive the claim-id layering the foreground path adds.
	if !BackgroundOwnedLifecycle(WithSubagentClaimID(marked, 7)) {
		t.Fatal("marker must survive context layering")
	}
}

// TestTaskToolForegroundLifecycleIsTurnOwned pins the foreground contract:
// every lifecycle transition of a synchronous task sub-agent carries
// Background=false, so the desktop registry counts it for the capsule.
func TestTaskToolForegroundLifecycleIsTurnOwned(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "foreground answer"},
		{Type: provider.ChunkDone},
	}}
	task := newTestTaskTool(t, sub, tool.NewRegistry(), "sys", "", "", nil)
	sink := &lifecycleCaptureSink{}
	ctx := withCallContext(testTaskContext(), "call-fg", sink, nil, false)

	out, err := task.Execute(ctx, []byte(`{"prompt":"foreground task"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "foreground answer") {
		t.Fatalf("output = %q, want the final answer", out)
	}
	if len(sink.entries) < 3 {
		t.Fatalf("lifecycle entries = %d, want at least created/running/completed: %+v", len(sink.entries), sink.entries)
	}
	wantPhases := map[string]bool{"child_created": false, "child_running": false, "child_completed": false}
	for _, info := range sink.entries {
		if seen, ok := wantPhases[info.Phase]; ok {
			if seen {
				t.Fatalf("phase %s observed twice: %+v", info.Phase, info)
			}
			wantPhases[info.Phase] = true
		}
		if info.Background {
			t.Fatalf("foreground phase %s must not be background-owned: %+v", info.Phase, info)
		}
		if info.Ref == "" {
			t.Fatalf("phase %s must carry the child ref: %+v", info.Phase, info)
		}
	}
	for phase, seen := range wantPhases {
		if !seen {
			t.Fatalf("missing foreground lifecycle phase %s: %+v", phase, sink.entries)
		}
	}
}

// TestTaskToolBackgroundLifecycleIsJobOwned pins the background contract:
// every lifecycle transition of a run_in_background task carries
// Background=true, so the desktop registry never counts it next to its job
// row (the job+lifecycle dual-source dedupe, task 557).
func TestTaskToolBackgroundLifecycleIsJobOwned(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "background answer"},
		{Type: provider.ChunkDone},
	}}
	task := newTestTaskTool(t, sub, tool.NewRegistry(), "sys", "", "", nil)
	sink := &lifecycleCaptureSink{}

	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	ctx := testTaskContext()
	ctx = jobs.WithSession(ctx, "parent-session")
	ctx = jobs.WithManager(ctx, jm)
	ctx = withCallContext(ctx, "call-bg", sink, nil, false)

	out, err := task.Execute(ctx, []byte(`{"prompt":"background task","run_in_background":true}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	jobID := extractJobID(out)
	if jobID == "" {
		t.Fatalf("no background job id in output:\n%s", out)
	}
	res := jm.WaitForSession(context.Background(), "parent-session", []string{jobID}, 5)
	if len(res) != 1 || res[0].Status != jobs.Done {
		t.Fatalf("background job result = %+v, want done", res)
	}
	if len(sink.entries) < 3 {
		t.Fatalf("lifecycle entries = %d, want at least created/running/completed: %+v", len(sink.entries), sink.entries)
	}
	phases := map[string]bool{}
	for _, info := range sink.entries {
		phases[info.Phase] = true
		if !info.Background {
			t.Fatalf("background-owned phase %s must carry Background=true: %+v", info.Phase, info)
		}
		if info.Ref == "" {
			t.Fatalf("phase %s must carry the child ref: %+v", info.Phase, info)
		}
	}
	for _, phase := range []string{"child_created", "child_running", "child_completed"} {
		if !phases[phase] {
			t.Fatalf("missing background lifecycle phase %s: %+v", phase, sink.entries)
		}
	}
}
