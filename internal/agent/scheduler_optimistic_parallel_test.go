package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Task 483 acceptance matrix (optimistic-parallel subagent-vs-subagent write
// gates). The parent-vs-subagent half is task 315
// (TestOptimisticWriteSkipsParentClaimGate); these tests pin the two remaining
// matrix cells — subagent/subagent and declared/undeclared — plus the
// conservative restore. Overlap safety under optimistic_write is delegated to
// the write-if-unchanged baseline checks inside path-bound tools
// (internal/tool/builtin/optimistic_expected.go, covered by
// optimistic_expected_test.go); bash/MCP opaque writes carry no such guard,
// which is the user's explicit opt-out.

// acquireWithTimeout runs a non-nested Acquire in a goroutine and fails the
// test if it does not return within 2s — a queued (serialized) writer would
// otherwise hang until the go-test timeout instead of failing the assertion.
func acquireWithTimeout(t *testing.T, s *SubagentScheduler, req AcquireRequest) (release func()) {
	t.Helper()
	type result struct {
		release func()
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		rel, err := s.Acquire(context.Background(), req)
		ch <- result{release: rel, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("acquire %q: %v", req.Label, r.err)
		}
		return r.release
	case <-time.After(2 * time.Second):
		t.Fatalf("acquire %q did not return within 2s — it queued behind a running writer instead of starting in parallel (task 483 regression)", req.Label)
		return func() {}
	}
}

// TestOptimisticUndeclaredSubagentsRunInParallel (V1, task 483 ①): with
// optimistic_write on, two writer subagents that both omit write_paths
// (whole-workspace claims) must start concurrently — no serialization, no
// queueing. Also covers the combined matrix cell: the parent writes
// concurrently and registers no gate at all.
func TestOptimisticUndeclaredSubagentsRunInParallel(t *testing.T) {
	s := NewSubagentScheduler(4, 3)
	root := t.TempDir()
	whole, err := WholeWorkspaceWriteClaim(root)
	if err != nil {
		t.Fatal(err)
	}
	s.SetOptimistic(true)

	// Parent write during the same turn must register no claim (task 315).
	parentRelease, err := s.ReserveParentWrite(whole)
	if err != nil {
		t.Fatalf("optimistic parent write must not be refused: %v", err)
	}
	defer parentRelease()

	release1 := acquireWithTimeout(t, s, AcquireRequest{Writer: true, WritePaths: whole, Label: "child-1"})
	release2 := acquireWithTimeout(t, s, AcquireRequest{Writer: true, WritePaths: whole, Label: "child-2"})
	defer release1()
	defer release2()

	claims := s.ActiveWriterClaims()
	if len(claims) != 2 {
		t.Fatalf("two undeclared writers must hold claims concurrently, got %d: %v", len(claims), claims)
	}
}

// TestConservativeUndeclaredSubagentsStillSerialize (V2, task 483 ③): the
// default conservative state keeps the fail-fast serialization for
// whole-workspace claims; toggling optimistic lifts it live; toggling back
// restores it. Off → on → off, one scheduler.
func TestConservativeUndeclaredSubagentsStillSerialize(t *testing.T) {
	s := NewSubagentScheduler(4, 3)
	root := t.TempDir()
	whole, err := WholeWorkspaceWriteClaim(root)
	if err != nil {
		t.Fatal(err)
	}

	// State 1 — conservative (default): the second undeclared writer fail-fasts.
	release1, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Label: "child-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	if _, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Nested: true, Label: "child-2"}); err == nil {
		t.Fatal("conservative state must serialize two undeclared writers")
	}

	// State 2 — optimistic (dynamic toggle): the same second writer starts now.
	s.SetOptimistic(true)
	release2 := acquireWithTimeout(t, s, AcquireRequest{Writer: true, WritePaths: whole, Nested: true, Label: "child-2"})
	release2()

	// State 3 — switched back: the conservative gate is restored.
	s.SetOptimistic(false)
	if _, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Nested: true, Label: "child-3"}); err == nil {
		t.Fatal("switching back to conservative must restore whole-workspace serialization")
	}
}

// TestOptimisticDeclaredUndeclaredPairRuns (V3, task 483 ④
// declared/undeclared cell): the conservative state gates a declared writer
// behind an undeclared (whole-workspace) one; optimistic_write lets the pair
// run concurrently.
func TestOptimisticDeclaredUndeclaredPairRuns(t *testing.T) {
	build := func(t *testing.T) (whole, declared WritePathSet) {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		w, err := WholeWorkspaceWriteClaim(root)
		if err != nil {
			t.Fatal(err)
		}
		d, err := NormalizeWritePaths(root, []string{"src/a.md"})
		if err != nil {
			t.Fatal(err)
		}
		return w, d
	}

	// Conservative control: the declared writer must not start behind the
	// undeclared one (fail-fast, never queue).
	whole, declared := build(t)
	sConservative := NewSubagentScheduler(4, 3)
	releaseWhole, err := sConservative.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Label: "undeclared"})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseWhole()
	if _, err := sConservative.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: declared, Nested: true, Label: "declared"}); err == nil {
		t.Fatal("conservative state must gate a declared writer behind an undeclared one")
	}

	// Optimistic: both run concurrently.
	whole, declared = build(t)
	s := NewSubagentScheduler(4, 3)
	s.SetOptimistic(true)
	releaseUndeclared := acquireWithTimeout(t, s, AcquireRequest{Writer: true, WritePaths: whole, Label: "undeclared"})
	releaseDeclared := acquireWithTimeout(t, s, AcquireRequest{Writer: true, WritePaths: declared, Label: "declared"})
	defer releaseUndeclared()
	defer releaseDeclared()

	if claims := s.ActiveWriterClaims(); len(claims) != 2 {
		t.Fatalf("declared and undeclared writers must hold claims concurrently, got %d: %v", len(claims), claims)
	}
}

// TestOptimisticRealizeRecordsWithoutRefusal (V5): under optimistic_write a
// same-file realize is recorded (diagnostics + conservative-restore gate) but
// refuses nothing; after switching back to conservative the recorded realized
// path blocks a new same-file sibling — conflict safety is restored, not lost.
func TestOptimisticRealizeRecordsWithoutRefusal(t *testing.T) {
	s := NewSubagentScheduler(6, 4)
	root := t.TempDir()
	whole, err := WholeWorkspaceWriteClaim(root)
	if err != nil {
		t.Fatal(err)
	}
	file, err := NormalizeWritePaths(root, []string{"a.md"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetOptimistic(true)

	release1, id1, err := s.AcquireWithID(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Label: "child-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	release2, id2, err := s.AcquireWithID(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Label: "child-2"})
	if err != nil {
		t.Fatalf("second undeclared writer must start in parallel: %v", err)
	}
	defer release2()

	// Both realize the SAME file: recorded, not refused (the tool-level
	// write-if-unchanged baseline is the concurrency guard here).
	if err := s.Realize(id1, file); err != nil {
		t.Fatalf("optimistic realize (child-1) must record without refusing: %v", err)
	}
	if err := s.Realize(id2, file); err != nil {
		t.Fatalf("optimistic realize (child-2) of the same file must record without refusing: %v", err)
	}

	// MarkOpaque (bash/MCP upgrade) likewise records without refusing.
	if err := s.MarkOpaque(id1); err != nil {
		t.Fatalf("optimistic opaque upgrade must record without refusing: %v", err)
	}
	if err := s.Realize(id2, file); err != nil {
		t.Fatalf("optimistic realize against an opaque sibling must not refuse: %v", err)
	}

	// Conservative restore: the recorded paths gate new dispatch again.
	s.SetOptimistic(false)
	release3, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: file, Nested: true, Label: "child-3"})
	if err == nil {
		release3()
		t.Fatal("after switching back to conservative, a new writer on a realized path must be refused")
	}
}

// TestConservativeQueuedWholeWriterStillBlocksLaterWriters (V6 regression):
// the queued-whole-writer priority rule stays intact in the conservative
// state — a later declared writer must not bypass a queued whole-workspace
// writer (scheduler_priority_test.go covers the same rule with directory
// claims; this pins it against single-file claims too).
func TestConservativeQueuedWholeWriterStillBlocksLaterWriters(t *testing.T) {
	s := NewSubagentScheduler(4, 4)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := NormalizeWritePaths(root, []string{"src/"})
	if err != nil {
		t.Fatal(err)
	}
	whole, err := WholeWorkspaceWriteClaim(root)
	if err != nil {
		t.Fatal(err)
	}
	file, err := NormalizeWritePaths(root, []string{"src/b.md"})
	if err != nil {
		t.Fatal(err)
	}
	release1, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: dir, Label: "dir-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	release2, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: dir, Label: "dir-2"})
	if err != nil {
		t.Fatal(err)
	}
	defer release2()

	// The undeclared writer queues behind the unrealized dir claims.
	wholeQueued := make(chan error, 1)
	go func() {
		rel, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Label: "queued-whole"})
		if err == nil {
			rel()
		}
		wholeQueued <- err
	}()
	waitForSchedulerWaiters(t, s, 1)

	// A later declared writer must not bypass the queued whole writer.
	if _, err := s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: file, Nested: true, Label: "late-declared"}); err == nil {
		t.Fatal("conservative state must keep the queued whole-workspace writer ahead of a later declared writer")
	}
}
