package agent

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/provider"
)

// writePlan is a fresh, non-read-only call targeting the fixture sink. It is the
// shape the barrier judges: any new write while an effect is unresolved.
func writePlan(probe *idempotentRecoveryTool, id string) *toolCallPlan {
	return &toolCallPlan{
		call:     provider.ToolCall{ID: id, Name: probe.Name(), Arguments: `{}`},
		permName: probe.Name(),
		permArgs: json.RawMessage(`{}`),
		cctx:     context.Background(),
	}
}

// Task 107 P0-0 filtered which approval modes could pass an unresolved effect;
// Task 482（fence 退役）removes the stop for every mode: a pending effect record
// no longer blocks a new write in ask, unset, unknown, auto or yolo. The record
// itself keeps being written — visible to the panel in every mode.
func TestToolRecoveryWriteNotBlockedInAnyApprovalMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		setMode bool
	}{
		{name: "ask", mode: "ask", setMode: true},
		{name: "unset"},
		{name: "unknown", mode: "manual", setMode: true},
		{name: "auto", mode: "auto", setMode: true},
		{name: "yolo", mode: "yolo", setMode: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, probe, _ := recoveryActionFixture(t)
			ctx := context.Background()
			if tc.setMode {
				ctx = WithToolApprovalMode(ctx, tc.mode)
			}
			if err := a.beginToolRecovery(ctx, writePlan(probe, "new-write")); err != nil {
				t.Fatalf("write blocked with an unresolved effect after fence removal: %v", err)
			}
			// The stop is gone, the evidence is not: the earlier effect stays
			// listed for after-the-fact review in every mode.
			if len(a.PendingToolRecovery()) != 1 {
				t.Fatalf("pending effects = %d, want 1", len(a.PendingToolRecovery()))
			}
		})
	}
}

// A read-only call was never the subject of the barrier; it still records.
func TestToolRecoveryBarrierIgnoresReadOnlyCalls(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	p := writePlan(probe, "read-only-diagnosis")
	p.readOnly = true
	if err := a.beginToolRecovery(context.Background(), p); err != nil {
		t.Fatalf("read-only diagnosis blocked: %v", err)
	}
}

// Task 482（fence 退役）: an inspection no longer gates anything — the write
// goes through, and the inspection still lands on the record for the panel.
func TestToolRecoveryInspectionKeepsWorkingAfterFenceRemoval(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	inspection, err := a.InspectToolRecovery(context.Background(), "original")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.InspectionID == "" {
		t.Fatal("inspection produced no id")
	}
	if err := a.beginToolRecovery(context.Background(), writePlan(probe, "after-inspect")); err != nil {
		t.Fatalf("write blocked after inspection post fence removal: %v", err)
	}
}

// An auto/yolo session is not stranded by an earlier effect either, but a fresh
// read-only call and a retry of the same attempt keep their own semantics.
func TestToolRecoveryAutoApprovedSessionKeepsRetrySemantics(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	ctx := WithToolApprovalMode(context.Background(), "yolo")
	p := writePlan(probe, "new-write")
	if err := a.beginToolRecovery(ctx, p); err != nil {
		t.Fatalf("yolo write blocked: %v", err)
	}
	if p.call.Recovery == nil {
		t.Fatal("exempted write was not recorded as an effect")
	}
	if p.call.Recovery.Identity.CanonicalTool != probe.Name() {
		t.Fatalf("recorded tool = %q", p.call.Recovery.Identity.CanonicalTool)
	}
}

// Task 482（fence 退役）: the old TestToolRecoveryBarrierMessageNamesPanelAndInspectAction
// (the recovery_required block copy naming the panel actions) is withdrawn with
// the barrier — no error copy may point at a removed flow.

// The exemption helper stays: run_loop.go's unattended posture still reads it.
// The fence reads its exemption from the turn context: an auto/yolo session and
// an unattended run both pass, everything else keeps the barrier.
func TestToolRecoveryExemptFromContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{name: "plain", ctx: context.Background()},
		{name: "ask", ctx: WithToolApprovalMode(context.Background(), "ask")},
		{name: "auto", ctx: WithToolApprovalMode(context.Background(), "auto"), want: true},
		{name: "yolo", ctx: WithToolApprovalMode(context.Background(), "yolo"), want: true},
		{name: "unattended", ctx: WithUnattendedRun(context.Background()), want: true},
		{name: "unattended+ask", ctx: WithUnattendedRun(WithToolApprovalMode(context.Background(), "ask")), want: true},
	} {
		if got := toolRecoveryExempt(tc.ctx); got != tc.want {
			t.Fatalf("%s: exempt = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Task 107's hard constraint: an unattended run that has only inspected a
// pending effect must still not lose the record - the exemption lifts the stop,
// not the evidence.
func TestUnattendedRunKeepsThePendingEffectVisible(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	if err := a.beginToolRecovery(WithUnattendedRun(context.Background()), writePlan(probe, "exempt-write")); err != nil {
		t.Fatalf("unattended write was stranded: %v", err)
	}
	if len(a.PendingToolRecovery()) != 1 {
		t.Fatalf("pending effects = %d, want the earlier effect still listed", len(a.PendingToolRecovery()))
	}
}

func TestToolApprovalModeAutoApproved(t *testing.T) {
	for mode, want := range map[string]bool{
		"auto": true, "yolo": true, "AUTO": false, "": false, "ask": false, "plan": false,
	} {
		if got := toolApprovalModeAutoApproved(mode); got != want {
			t.Fatalf("toolApprovalModeAutoApproved(%q) = %v, want %v", mode, got, want)
		}
	}
}

// Task 299's scenario (an autopilot run whose earlier effect stayed pending)
// no longer needs an exemption carve-out: Task 482（fence 退役）removed the
// run-tail join entirely, so no turn ends on ErrToolRecoveryRequired any more.
// The old TestFinishRunRecoveryHonorsExemptionAndKeepsEvidence is withdrawn
// with the join; the pending record staying visible is covered by
// TestUnattendedRunKeepsThePendingEffectVisible above.
