package agent

import (
	"bytes"
	"context"
	"errors"
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
// that leaves an unresolved record emits "recovery fence created" with the
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
		"recovery fence created",
		"mode=normal",
		"tool=write_file",
		"args_digest=digest-abc",
		"state=unknown",
		"duration_ms=",
		"turn_duration_ms=",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fence-created log missing %q\n---\n%s", want, got)
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

	if got := buf.String(); strings.Contains(got, "recovery fence created") {
		t.Fatalf("a completed write must not log a fence:\n%s", got)
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

// TestFinishRunRecoveryWaivedLogsMode pins log line 3: when a write fence is
// standing and the turn is exempt, the join is still skipped (original
// behavior) and "fence-waived" names the mode; ask (not exempt) still joins
// and logs nothing.
func TestFinishRunRecoveryWaivedLogsMode(t *testing.T) {
	buf := captureFenceLogs(t)
	s := recoverySessionWithCall("call-1", fenceFixtureRecord())
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)

	// auto/yolo exempt: no joined error, one waiver line.
	var autoErr error
	a.finishRunRecovery(WithToolApprovalMode(context.Background(), toolApprovalModeAuto), &autoErr)
	if autoErr != nil {
		t.Fatalf("exempt turn must not join the barrier error: %v", autoErr)
	}
	if got := buf.String(); !strings.Contains(got, "fence-waived") || !strings.Contains(got, "mode=auto") {
		t.Fatalf("waiver log missing fence-waived/mode=auto\n---\n%s", got)
	}

	buf.Reset()
	var yoloErr error
	a.finishRunRecovery(WithToolApprovalMode(context.Background(), toolApprovalModeYolo), &yoloErr)
	if yoloErr != nil {
		t.Fatalf("yolo turn must not join the barrier error: %v", yoloErr)
	}
	if got := buf.String(); !strings.Contains(got, "mode=yolo") {
		t.Fatalf("waiver log missing mode=yolo\n---\n%s", got)
	}

	// Autopilot posture wins over the unattended flag its controller sets.
	s2 := recoverySessionWithCall("call-1", fenceFixtureRecord())
	aAuto := New(nil, tool.NewRegistry(), s2, Options{Autopilot: true}, event.Discard)
	var auto2Err error
	aAuto.finishRunRecovery(WithUnattendedRun(context.Background()), &auto2Err)
	if auto2Err != nil {
		t.Fatalf("autopilot turn must not join the barrier error: %v", auto2Err)
	}
	if got := buf.String(); !strings.Contains(got, "mode=autopilot") {
		t.Fatalf("waiver log missing mode=autopilot\n---\n%s", got)
	}

	// ask keeps the barrier: the join is untouched and no waiver line appears.
	buf.Reset()
	var askErr error
	a.finishRunRecovery(context.Background(), &askErr)
	if !errors.Is(askErr, ErrToolRecoveryRequired) {
		t.Fatalf("ask must keep joining the barrier error, got %v", askErr)
	}
	if got := buf.String(); strings.Contains(got, "fence-waived") {
		t.Fatalf("an exempt-only marker leaked into a non-exempt turn:\n%s", got)
	}
}

// TestFenceWaivedNotLoggedWithoutPendingFence keeps the waiver line signal-
// rich: exempt with no standing write fence logs nothing.
func TestFenceWaivedNotLoggedWithoutPendingFence(t *testing.T) {
	buf := captureFenceLogs(t)
	s := NewSession("")
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)

	var err error
	a.finishRunRecovery(WithToolApprovalMode(context.Background(), toolApprovalModeYolo), &err)
	if err != nil {
		t.Fatalf("clean exempt turn must stay error-free: %v", err)
	}
	if got := buf.String(); strings.Contains(got, "fence-waived") {
		t.Fatalf("no standing fence means no waiver line:\n%s", got)
	}
}

// TestRecoveryReleaseLogsManualAndAuto pins log line 4: both release paths
// emit "recovery fence released" with the source (manual panel action vs host
// auto-verify) and the wait durations, while lifting the barrier exactly as
// before.
func TestRecoveryReleaseLogsManualAndAuto(t *testing.T) {
	buf := captureFenceLogs(t)

	// Manual: the panel confirm.
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
		t.Fatal("manual release must lift the barrier as before")
	}

	// Auto: the host proves the effect absent by itself.
	a2, probe, _ := recoveryActionFixture(t)
	probe.inspection = tool.EffectInspection{State: "absent", Fenced: true}
	buf.Reset()
	a2.resolveHostVerifiableEffects(context.Background())
	got = buf.String()
	for _, want := range []string{
		"recovery fence released",
		"source=auto",
		"resolution=host_verified_absent",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("auto release log missing %q\n---\n%s", want, got)
		}
	}
	if len(a2.PendingToolRecovery()) != 0 {
		t.Fatal("auto release must lift the barrier as before")
	}
}
