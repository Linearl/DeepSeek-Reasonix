package jobs

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

// tl1479 — the UI's snapshot read must be invisible to the agent's
// incremental bash_output reader. This is the load-bearing direction: if the
// snapshot ever consumed from the shared cursor, a polling UI would silently
// rob the agent of output it has not read yet.

// TestSnapshotDoesNotConsumeIncrementalCursor pins both directions:
// a snapshot before the agent reads leaves the agent's first delta intact,
// and the agent's read leaves the next snapshot intact.
func TestSnapshotDoesNotConsumeIncrementalCursor(t *testing.T) {
	m := NewManager(event.Discard)
	release := make(chan struct{})
	j := m.Start("bash", "tail demo", func(ctx context.Context, out io.Writer) (string, error) {
		if _, err := out.Write([]byte("line-1\n")); err != nil {
			return "", err
		}
		<-release
		if _, err := out.Write([]byte("line-2\n")); err != nil {
			return "", err
		}
		return "", nil
	})

	// Wait until line-1 is visible in the streamed buffer (the run goroutine
	// blocks on release right after writing it, so a bounded spin is enough).
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, _, ok := m.SnapshotOutputForSession("", j.ID)
		if ok && strings.Contains(snapshot, "line-1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("line-1 never reached the buffer; last snapshot %q ok=%v", snapshot, ok)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// UI snapshot: idempotent, and must not touch the cursor.
	for i := 0; i < 3; i++ {
		snapshot, status, ok := m.SnapshotOutputForSession("", j.ID)
		if !ok {
			t.Fatal("snapshot: job not found")
		}
		if status != Running {
			t.Fatalf("snapshot status = %q, want running", status)
		}
		if !strings.Contains(snapshot, "line-1") {
			t.Fatalf("snapshot %d lost line-1: %q", i, snapshot)
		}
	}

	// Agent's first bash_output: must still see the full delta including
	// line-1 — the snapshots above consumed nothing.
	text, status, ok := m.OutputForSession("", j.ID)
	if !ok {
		t.Fatal("bash_output: job not found")
	}
	if status != Running {
		t.Fatalf("bash_output status = %q, want running", status)
	}
	if !strings.Contains(text, "line-1") {
		t.Fatalf("UI snapshots stole the agent's delta: bash_output got %q, want line-1", text)
	}

	// Produce line-2 and finish the job.
	close(release)
	<-j.done

	// Agent's second bash_output: only the line-2 delta remains.
	text, status, _ = m.OutputForSession("", j.ID)
	if status != Done || !strings.Contains(text, "line-2") || strings.Contains(text, "line-1") {
		t.Fatalf("second bash_output = (%q, %s), want the line-2 delta only", text, status)
	}

	// Snapshot after the agent drained: still shows everything (cursor-free
	// read of the buffer), and repeated calls stay stable.
	first, status, ok := m.SnapshotOutputForSession("", j.ID)
	if !ok || status != Done {
		t.Fatalf("terminal snapshot ok=%v status=%s, want true/done", ok, status)
	}
	if !strings.Contains(first, "line-1") || !strings.Contains(first, "line-2") {
		t.Fatalf("terminal snapshot lost history: %q", first)
	}
	second, _, _ := m.SnapshotOutputForSession("", j.ID)
	if second != first {
		t.Fatalf("snapshot is not idempotent: %q vs %q", first, second)
	}
}

// TestSnapshotBoundsLargeOutput pins the server-side cap: a job that produced
// far more than the cap never ships more than snapshotOutputTailBytes to the
// UI, whatever storage the run used (streamed buffer or artifact file).
func TestSnapshotBoundsLargeOutput(t *testing.T) {
	m := NewManager(event.Discard)
	big := strings.Repeat("x", 200*1024) + "\nTAIL-MARKER\n"
	j := m.Start("bash", "big output", func(ctx context.Context, out io.Writer) (string, error) {
		if _, err := out.Write([]byte(big)); err != nil {
			return "", err
		}
		return "", nil
	})
	<-j.done

	text, status, ok := m.SnapshotOutputForSession("", j.ID)
	if !ok {
		t.Fatal("job not found")
	}
	if status != Done {
		t.Fatalf("status = %q, want done", status)
	}
	if len(text) > snapshotOutputTailBytes {
		t.Fatalf("snapshot shipped %d bytes, cap is %d", len(text), snapshotOutputTailBytes)
	}
	if !strings.Contains(text, "TAIL-MARKER") {
		t.Fatal("bounded snapshot dropped the newest output (tail)")
	}
}

// TestSnapshotSessionScopeAndUnknownID pins the routing contract: unknown ids
// report ok=false and a job scoped to another session is not visible.
func TestSnapshotSessionScopeAndUnknownID(t *testing.T) {
	m := NewManager(event.Discard)
	j := m.StartForSession("sess-a", "bash", "scoped", func(ctx context.Context, out io.Writer) (string, error) {
		if _, err := out.Write([]byte("scoped-output\n")); err != nil {
			return "", err
		}
		return "", nil
	})
	<-j.done

	if _, _, ok := m.SnapshotOutputForSession("", "no-such-job"); ok {
		t.Fatal("unknown id must report ok=false")
	}
	if _, _, ok := m.SnapshotOutputForSession("sess-b", j.ID); ok {
		t.Fatal("a foreign session must not see the job")
	}
	text, status, ok := m.SnapshotOutputForSession("sess-a", j.ID)
	if !ok {
		t.Fatal("owner session lost the job")
	}
	if status != Done || !strings.Contains(text, "scoped-output") {
		t.Fatalf("owner snapshot = (%q, %s), want scoped-output/done", text, status)
	}
}

// TestSnapshotSurfacesResultForBufferlessTask pins the task-job branch: the
// snapshot surfaces the terminal answer without consuming it, so the agent's
// bash_output still receives it afterwards.
func TestSnapshotSurfacesResultForBufferlessTask(t *testing.T) {
	m := NewManager(event.Discard)
	j := m.Start("task", "demo", func(ctx context.Context, _ io.Writer) (string, error) {
		return "THE-ANSWER", nil
	})
	<-j.done

	text, status, ok := m.SnapshotOutputForSession("", j.ID)
	if !ok || status != Done || !strings.Contains(text, "THE-ANSWER") {
		t.Fatalf("snapshot = (%q, %s, %v), want THE-ANSWER/done/true", text, status, ok)
	}

	// Non-consumption: the agent's bash_output still sees the answer.
	agentText, _, ok := m.OutputForSession("", j.ID)
	if !ok || !strings.Contains(agentText, "THE-ANSWER") {
		t.Fatalf("snapshot consumed the task answer: bash_output got %q", agentText)
	}
}
