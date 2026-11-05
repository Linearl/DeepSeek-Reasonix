package jobs

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

// tl1387 二轮: the dock panel's registry job rows project the background
// child's live work-process stream by the DISPATCHING call id (nested_sink.go
// re-parents the child's tool events under it). The View therefore carries
// ParentToolCallID, stamped once at Start by StartForSessionDetailCall — the
// same channel shape the tl1033 Detail field uses (Start variant → running
// View), with the same legacy-path-stays-empty guarantee.

func TestStartForSessionDetailCallStampsParentToolCallID(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	j := m.StartForSessionDetailCall("session-a", "task", "verify refs", "", "call-abc-123", func(context.Context, io.Writer) (string, error) {
		time.Sleep(30 * time.Millisecond)
		return "", nil
	})
	views := m.RunningForSession("session-a")
	if len(views) != 1 || views[0].ID != j.ID {
		t.Fatalf("running views = %+v, want exactly job %s", views, j.ID)
	}
	if views[0].ParentToolCallID != "call-abc-123" {
		t.Fatalf("View.ParentToolCallID = %q, want the dispatch call id", views[0].ParentToolCallID)
	}
}

func TestStartForSessionDetailCallTrimsParentToolCallID(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	m.StartForSessionDetailCall("session-trim", "task", "whitespace id", "", "   ", func(context.Context, io.Writer) (string, error) {
		return "", nil
	})
	views := m.RunningForSession("session-trim")
	if len(views) != 1 {
		t.Fatalf("running views = %+v, want exactly one", views)
	}
	if views[0].ParentToolCallID != "" {
		t.Fatalf("View.ParentToolCallID = %q, want empty for a whitespace-only stamp", views[0].ParentToolCallID)
	}
}

func TestLegacyStartPathsKeepParentToolCallIDEmpty(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	// Block the runs so every job stays on the running surface for the whole
	// assertion — a job that returns early races the snapshot.
	release := make(chan struct{})
	run := func(context.Context, io.Writer) (string, error) {
		<-release
		return "", nil
	}
	m.Start("unscoped-task", "bash", run)
	m.StartForSession("session-legacy", "task", "task via legacy", run)
	m.StartForSessionDetail("session-legacy", "bash", "bash via detail", "echo hi", run)
	defer func() { close(release) }()
	seen := 0
	for _, session := range []string{"", "session-legacy"} {
		for _, view := range m.RunningForSession(session) {
			seen++
			if view.ParentToolCallID != "" {
				t.Fatalf("legacy job %s (%s) carries ParentToolCallID %q, want empty", view.ID, session, view.ParentToolCallID)
			}
		}
	}
	// RunningForSession("") is the unscoped view: it lists every session's
	// jobs, so the three legacy jobs appear there plus the two session-legacy
	// jobs again under their own session.
	if seen != 5 {
		t.Fatalf("running views across sessions = %d, want 5", seen)
	}
}

func TestParentToolCallIDOmittedWhenEmpty(t *testing.T) {
	// bash jobs (and any legacy-start job) must not grow a new JSON key: the
	// remote-tab job projection and the FE JobView treat absence as "no
	// dispatch linkage".
	empty, err := json.Marshal(View{ID: "bash-1", Kind: "bash", Label: "echo", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "parentToolCallId") {
		t.Fatalf("empty ParentToolCallID serialized: %s", empty)
	}
	stamped, err := json.Marshal(View{ID: "task-2", Kind: "task", Label: "verify", Status: "running", ParentToolCallID: "call-9"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stamped), "parentToolCallId") {
		t.Fatalf("stamped ParentToolCallID missing from JSON: %s", stamped)
	}
}
