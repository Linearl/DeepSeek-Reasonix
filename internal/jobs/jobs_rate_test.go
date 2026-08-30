package jobs

import (
	"context"
	"io"
	"testing"

	"reasonix/internal/event"
)

// TestSetJobRateSurfacesInView covers the #9521 popover heartbeat: a task job's
// sampled streaming rate (tok/s) published by the sub-agent progress tracker
// through SetJobRate must surface in the running-jobs view, and unknown job
// ids must be a harmless no-op.
func TestSetJobRateSurfacesInView(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	release := make(chan struct{})
	defer close(release) // LIFO: runs before the deferred m.Close() below
	j := m.StartForSession("session-a", "task", "rateful", func(_ context.Context, _ io.Writer) (string, error) {
		<-release
		return "", nil
	})

	// Before any sample the view carries no rate.
	for _, v := range m.RunningForSession("session-a") {
		if v.ID == j.ID && v.Tps != 0 {
			t.Fatalf("fresh job view Tps = %d, want 0", v.Tps)
		}
	}

	m.SetJobRate(j.ID, 14)
	m.SetJobRate("task-does-not-exist", 99) // stale/unknown id: no-op, no panic

	found := false
	for _, v := range m.RunningForSession("session-a") {
		if v.ID != j.ID {
			continue
		}
		found = true
		if v.Tps != 14 {
			t.Fatalf("job view Tps = %d, want 14", v.Tps)
		}
	}
	if !found {
		t.Fatal("running job view not found after SetJobRate")
	}

	// Rate clamps: non-positive samples never clobber the last good value.
	m.SetJobRate(j.ID, 0)
	for _, v := range m.RunningForSession("session-a") {
		if v.ID == j.ID && v.Tps != 14 {
			t.Fatalf("job view Tps after zero sample = %d, want 14", v.Tps)
		}
	}
}

// TestSetJobRateAfterCompletionIsNoop ensures a tracker publishing just after
// its job left the running surface neither panics nor resurrects the job.
func TestSetJobRateAfterCompletionIsNoop(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.StartForSession("session-b", "task", "finished-fast", func(_ context.Context, _ io.Writer) (string, error) {
		return "done", nil
	})
	if res := m.WaitForSession(context.Background(), "session-b", []string{j.ID}, 5); len(res) != 1 || res[0].Status != Done {
		t.Fatalf("job result = %+v, want done", res)
	}
	m.SetJobRate(j.ID, 42) // job already left the table: ignored
}
