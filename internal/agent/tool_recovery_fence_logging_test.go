package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 406: the four fence-lifecycle log lines (fence created, timeout-
// continued, fence-waived, fence released) must exist and be greppable, and
// they must be log-only — every assertion below also pins that the underlying
// behavior (state transitions, exemption, join) is exactly what it was.

// captureFenceLogs routes the default slog logger into a buffer for the
// emission assertions (same pattern as compact_rate_limit_wait_test).
func captureFenceLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func fenceFixtureRecord() *provider.ToolCallRecord {
	return &provider.ToolCallRecord{
		Identity: provider.ActionIdentity{
			AttemptID:      "attempt-1",
			CallID:         "call-1",
			CanonicalTool:  "write_file",
			ArgumentDigest: "digest-abc",
		},
		State:     provider.ToolRunStarted,
		ReadOnly:  false,
		StartedAt: time.Now().UnixMilli() - 5000,
	}
}

// TestFenceCreationLogsInterruptedWrite pins log line 1: an interrupted write
// that leaves an unresolved record emits "recovery record created" with the
// session, mode, tool, args digest and durations — and the record's state
// transition is the original one (finishToolRecovery unchanged).
func TestFenceCreationLogsInterruptedWrite(t *testing.T) {
	buf := captureFenceLogs(t)
	s := recoverySessionWithCall("call-1", fenceFixtureRecord())
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)

	a.finishToolRecovery(context.Background(), provider.ToolCall{ID: "call-1", Name: "write_file"},
		toolOutcome{runState: provider.ToolRunUnknown, executed: true, output: "interrupted"})

	got := buf.String()
	for _, want := range []string{
		"recovery record created",
		"mode=normal",
		"tool=write_file",
		"args_digest=digest-abc",
		"state=unknown",
		"duration_ms=",
		"turn_duration_ms=",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("recovery-record log missing %q\n---\n%s", want, got)
		}
	}
	// Log-only: the record still lands in the unresolved state the fence needs.
	rec := s.toolRecoveryRecord("call-1")
	if rec == nil || rec.State != provider.ToolRunUnknown {
		t.Fatalf("record state changed by logging: %+v", rec)
	}
	if len(a.PendingToolRecovery()) != 1 {
		t.Fatal("the interrupted write must still be pending")
	}
}

// TestResolvedWriteDoesNotLogFenceCreated keeps the creation line signal-
// rich: a clean completion leaves no unresolved record, so no fence line.
func TestResolvedWriteDoesNotLogFenceCreated(t *testing.T) {
	buf := captureFenceLogs(t)
	s := recoverySessionWithCall("call-1", fenceFixtureRecord())
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)

	a.finishToolRecovery(context.Background(), provider.ToolCall{ID: "call-1", Name: "write_file"},
		toolOutcome{runState: provider.ToolRunCompleted, executed: true, output: "ok"})

	if got := buf.String(); strings.Contains(got, "recovery record created") {
		t.Fatalf("a completed write must not log an unresolved record:\n%s", got)
	}
}

// TestRecoveryTimeoutContinuedLogsOnNextTurn pins log line 2: a deadline-
// ended Run marks the session, the next turn logs recovery-timeout-continued
// once (with the pending fence count), and only real deadlines mark it — a
// deliberate budget pause does not.
func TestRecoveryTimeoutContinuedLogsOnNextTurn(t *testing.T) {
	buf := captureFenceLogs(t)
	s := recoverySessionWithCall("call-1", fenceFixtureRecord())
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)

	// Set side: only a DeadlineExceeded marks the run.
	a.noteRunTimeout(nil)
	if a.turnTimedOutPendingContinue {
		t.Fatal("a clean run must not be marked as timed out")
	}
	a.noteRunTimeout(&taskBudgetPause{axis: "time", detail: "task budget reached"})
	if a.turnTimedOutPendingContinue {
		t.Fatal("a deliberate budget pause must not count as a timeout")
	}
	a.noteRunTimeout(context.DeadlineExceeded)
	if !a.turnTimedOutPendingContinue {
		t.Fatal("a deadline must mark the run")
	}

	// Consume side: the next turn starts and logs the continuation once.
	a.beginRunTurn(context.Background(), "continue the task", pinnedRevisionPlan{})
	got := buf.String()
	for _, want := range []string{"recovery-timeout-continued", "pending_fences=1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeout-continued log missing %q\n---\n%s", want, got)
		}
	}
	if a.turnTimedOutPendingContinue {
		t.Fatal("the marker must be consumed by the turn that logged it")
	}

	buf.Reset()
	a.beginRunTurn(context.Background(), "another turn", pinnedRevisionPlan{})
	if got := buf.String(); strings.Contains(got, "recovery-timeout-continued") {
		t.Fatalf("the marker must fire exactly once:\n%s", got)
	}
}

// Task 482（fence 退役）: the "fence-waived" log line and its tests
// (TestFinishRunRecoveryWaivedLogsMode / TestFenceWaivedNotLoggedWithoutPendingFence)
// are withdrawn with finishRunRecovery — the run-tail join they described no
// longer exists, so there is nothing to waive.

// TestRecoveryReleaseLogsManualAndAuto pinned log line 4 on both release paths.
// Task 482 removed the host auto-release half (resolveHostVerifiableEffects)
// with the barrier; the manual panel path below keeps its log and its record
// settlement.
func TestRecoveryReleaseLogsManual(t *testing.T) {
	buf := captureFenceLogs(t)

	a, _, _ := recoveryActionFixture(t)
	insp, err := a.InspectToolRecovery(context.Background(), "original")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if err := a.ResolveToolRecovery("original", insp.InspectionID, "confirm"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"recovery fence released",
		"source=manual",
		"resolution=confirm",
		"tool=recovery_probe",
		"wait_ms=",
		"fence_wait_ms=",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("manual release log missing %q\n---\n%s", want, got)
		}
	}
	if len(a.PendingToolRecovery()) != 0 {
		t.Fatal("manual release must settle the record as before")
	}
}
