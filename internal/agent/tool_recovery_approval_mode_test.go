package agent

import (
	"context"
	"encoding/json"
	"strings"
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

// Task 107 P0-0: only sessions whose approval mode already delegates writes to
// policy (auto/yolo) may pass an unresolved effect; ask and "no mode recorded"
// keep the barrier. The default is deliberately the blocking one.
func TestToolRecoveryBarrierHonoursApprovalMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		setMode bool
		blocked bool
	}{
		{name: "ask", mode: "ask", setMode: true, blocked: true},
		{name: "unset", blocked: true},
		{name: "unknown", mode: "manual", setMode: true, blocked: true},
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
			err := a.beginToolRecovery(ctx, writePlan(probe, "new-write"))
			if tc.blocked {
				if err == nil {
					t.Fatal("write passed the barrier with an unresolved effect")
				}
				if !strings.Contains(err.Error(), "recovery_required") {
					t.Fatalf("barrier error lost its kind: %v", err)
				}
			} else if err != nil {
				t.Fatalf("auto-approved session was stranded: %v", err)
			}
			// Lifting the stop never erases the evidence: the effect stays
			// listed for after-the-fact review in every mode.
			if len(a.PendingToolRecovery()) != 1 {
				t.Fatalf("pending effects = %d, want 1", len(a.PendingToolRecovery()))
			}
		})
	}
}

// A read-only call is never the subject of the barrier, in any mode.
func TestToolRecoveryBarrierIgnoresReadOnlyCalls(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	p := writePlan(probe, "read-only-diagnosis")
	p.readOnly = true
	if err := a.beginToolRecovery(context.Background(), p); err != nil {
		t.Fatalf("read-only diagnosis blocked: %v", err)
	}
}

// P0-0 must not let an inspection stand in for a decision: in ask mode a write
// still stops until the user confirms or rejects the attempt.
func TestToolRecoveryBarrierSurvivesInspectionInAskMode(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	inspection, err := a.InspectToolRecovery(context.Background(), "original")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.InspectionID == "" {
		t.Fatal("inspection produced no id")
	}
	err = a.beginToolRecovery(context.Background(), writePlan(probe, "after-inspect"))
	if err == nil {
		t.Fatal("inspection alone lifted the write barrier")
	}
	if !strings.Contains(err.Error(), "recovery_required") {
		t.Fatalf("barrier error lost its kind: %v", err)
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

// P0-1: the block message must tell the agent where the effect is cleared and
// how to inspect it, so a stuck run can recover instead of guessing.
func TestToolRecoveryBarrierMessageNamesPanelAndInspectAction(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	err := a.beginToolRecovery(context.Background(), writePlan(probe, "new-write"))
	if err == nil {
		t.Fatal("write passed the barrier")
	}
	msg := err.Error()
	for _, want := range []string{
		"中断的工具需要核实",
		"Interrupted tool needs review",
		"Inspect current state",
		"I verified the effect happened",
		"Do not retry",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("block message is missing %q: %s", want, msg)
		}
	}
}

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
