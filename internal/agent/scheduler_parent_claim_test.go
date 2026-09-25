package agent

import (
	"context"
	"strings"
	"testing"
)

// Task 315 acceptance matrix (nested subagent parent-write claim fail-fast).
// V1a/V2 pin the conservative behaviour that must NOT regress; V1b/V4 are the
// new contract (cause-specific message with a workaround; optimistic_write
// lifting the parent-claim gate entirely, read-only or not).

// TestReadOnlySubagentStartsWhileParentHoldsWrite (V1a): the slot-allocation
// chain promises that a read-only subagent never depends on the write slot —
// canStartLocked returns before the parentClaims loop for !Writer — so a
// parent-held write must not stop it. Pinned so the 315 changes cannot regress
// the direct pass-through.
func TestReadOnlySubagentStartsWhileParentHoldsWrite(t *testing.T) {
	s := NewSubagentScheduler(4, 2)
	root := t.TempDir()
	whole, err := WholeWorkspaceWriteClaim(root)
	if err != nil {
		t.Fatal(err)
	}
	parentRelease, err := s.ReserveParentWrite(whole)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRelease()

	release, err := s.Acquire(context.Background(), AcquireRequest{Writer: false, Label: "read-only task"})
	if err != nil {
		t.Fatalf("read-only subagent must start while the parent holds a write (slot chain promises this): %v", err)
	}
	release()
}

// TestParentHeldFailureExplainsCauseAndWorkaround (V1b + V2): a parent-held
// refusal must (a) fail fast — never queue for a claim only the parent can
// release (#9688 deadlock guard), (b) name the parent-held cause instead of
// masquerading as a generic concurrency limit, (c) offer the two concrete
// workarounds (wait out the in-flight write / dispatch read-only), and
// (d) leave a slog trail (this path was silent, so the "blocked with no write
// this turn" field reports could never be attributed — 304 error-path rule).
func TestParentHeldFailureExplainsCauseAndWorkaround(t *testing.T) {
	s := NewSubagentScheduler(4, 2)
	root := t.TempDir()
	whole, err := WholeWorkspaceWriteClaim(root)
	if err != nil {
		t.Fatal(err)
	}
	parentRelease, err := s.ReserveParentWrite(whole)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRelease()

	trail := captureSlog(t)
	// Background context: a queueing acquire would hang this test until the
	// go test timeout — the fail-fast contract is what keeps it instant.
	_, err = s.Acquire(context.Background(), AcquireRequest{Writer: true, WritePaths: whole, Label: "explore"})
	if err == nil {
		t.Fatal("writer subagent must fail fast while the parent holds the path (deadlock guard)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "parent write held") {
		t.Fatalf("error must name the parent-held cause, got: %s", msg)
	}
	if strings.Contains(msg, "concurrency limit reached") {
		t.Fatalf("parent-held refusal must not masquerade as a concurrency limit: %s", msg)
	}
	if !strings.Contains(msg, "read-only") || !strings.Contains(msg, "write to finish") {
		t.Fatalf("error must offer both workarounds (wait for the write / dispatch read-only), got: %s", msg)
	}
	out := trail.String()
	if !strings.Contains(out, "subagent dispatch fail-fast") {
		t.Fatalf("fail-fast must leave a slog trail:\n%s", out)
	}
	for _, field := range []string{"parent_claims=1", "label=explore", "optimistic=false"} {
		if !strings.Contains(out, field) {
			t.Fatalf("fail-fast trail missing %s:\n%s", field, out)
		}
	}
}

// TestOptimisticWriteSkipsParentClaimGate (V4, two-state matrix): with
// optimistic_write on, a parent write must gate NO subagent — read-only and
// writer alike — including claims registered before the toggle; switching
// back restores the conservative fail-fast, and the optimistic state registers
// no new claim at all (task 315 × 280 linkage).
func TestOptimisticWriteSkipsParentClaimGate(t *testing.T) {
	s := NewSubagentScheduler(4, 2)
	root := t.TempDir()
	whole, err := WholeWorkspaceWriteClaim(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// State 1 — conservative (default): the claim registers and gates writers.
	held, err := s.ReserveParentWrite(whole)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(ctx, AcquireRequest{Writer: true, WritePaths: whole, Label: "writer"}); err == nil {
		t.Fatal("conservative state must gate a writer subagent behind the parent claim")
	}

	// State 2 — optimistic, claim still registered from before the toggle:
	// the gate lifts for every subagent, read-only or writer.
	s.SetOptimistic(true)
	release, err := s.Acquire(ctx, AcquireRequest{Writer: true, WritePaths: whole, Label: "writer"})
	if err != nil {
		t.Fatalf("optimistic state must gate no subagent behind a parent claim: %v", err)
	}
	release()
	ro, err := s.Acquire(ctx, AcquireRequest{Writer: false, Label: "read-only"})
	if err != nil {
		t.Fatalf("optimistic read-only must stay free too: %v", err)
	}
	ro()

	// Optimistic state registers no new parent claim.
	held()
	fresh, err := s.ReserveParentWrite(whole)
	if err != nil {
		t.Fatalf("optimistic parent write must not be refused: %v", err)
	}
	fresh()

	// State 3 — switched back: the conservative gate is restored and a
	// newly registered claim gates writers again.
	s.SetOptimistic(false)
	again, err := s.ReserveParentWrite(whole)
	if err != nil {
		t.Fatal(err)
	}
	defer again()
	if _, err := s.Acquire(ctx, AcquireRequest{Writer: true, WritePaths: whole, Label: "writer"}); err == nil {
		t.Fatal("switching back to conservative must restore the parent-claim gate")
	}
}
