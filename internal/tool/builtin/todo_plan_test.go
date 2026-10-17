package builtin

// Task 972 acceptance: todo_write cross-task plan switching (plan_id scope)
// plus the three council-decided fix points.
//
// Reproduction criteria (调研员2 判据一/二/三, verified 2026-10-17):
//  1. The rejected write's step_id and content are byte-identical before and
//     after the rejection and that step_id was already completed in an earlier
//     round ⇒ this is not history rewriting, it is cross-task scope bleed.
//  2. Keeping the old completed items verbatim and appending new items writes
//     successfully ⇒ validation scope is the current plan table only.
//  3. Combined with todo_read clearing its list between turns, the two
//     requirements (keep old completed / do not invent new) become unsolvable.
//
// Fix C: an explicit plan_id scopes validation to one plan; a DIFFERENT
// plan_id is a legal wholesale replacement with a notice; same-plan writes
// keep every anti-forgery check (reverse case kept).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

// ledgerWithPlanTodos records a prior successful todo_write the way the agent
// does: through ReceiptFromToolCall, so plan_id stamping and host-assigned
// step ids behave exactly as in production.
func ledgerWithPlanTodos(t *testing.T, args string) context.Context {
	t.Helper()
	ledger := evidence.NewLedger()
	ledger.Record(evidence.ReceiptFromToolCall("todo_write", json.RawMessage(args), true, true))
	return evidence.WithLedger(context.Background(), ledger)
}

const oldPlanArgs = `{"plan_id":"plan-old","todos":[
	{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
	{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
	{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"}
]}`

// 判据①: a byte-fresh plan (new step_ids, some already completed) written under
// a NEW plan_id is a legal switch — previously rejected as "not invented" even
// though nothing was rewritten. The ack must carry the archive notice.
func TestTodoWritePlanSwitchAcceptsAFreshPlanWithNotice(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	out, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-new","todos":[
		{"content":"Investigate the new bug","status":"completed","step_id":"new_step_01"},
		{"content":"Fix the new bug","status":"in_progress","step_id":"new_step_02"}
	]}`))
	if err != nil {
		t.Fatalf("cross-plan replacement should be legal: %v", err)
	}
	if !strings.Contains(out, "计划已切换，旧计划 3 项已归档") {
		t.Fatalf("plan switch ack must carry the archive notice, got %q", out)
	}
}

// 判据①反面（修复前的复现）: the SAME fresh plan without a plan marker is still
// rejected exactly as before — the scope fix must not open the same-plan gate.
func TestTodoWriteFreshPlanWithoutMarkerStillRejected(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"todos":[
		{"content":"Investigate the new bug","status":"completed","step_id":"new_step_01"},
		{"content":"Fix the new bug","status":"in_progress","step_id":"new_step_02"}
	]}`))
	if err == nil || !(strings.Contains(err.Error(), "completed step disappeared") || strings.Contains(err.Error(), "not a step from the current plan")) {
		t.Fatalf("unmarked fresh plan should still hit the same-plan checks, got %v", err)
	}
}

// 判据②: inside one plan, keeping old completed items verbatim and appending
// new items writes successfully — with or without an explicit same plan_id.
func TestTodoWriteSamePlanKeepsOldCompletedAndAppends(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
	}{
		{"no marker", `{"todos":[
			{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
			{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
			{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"},
			{"content":"New extra step","status":"pending","step_id":"new_step_09"}
		]}`},
		{"same marker", `{"plan_id":"plan-old","todos":[
			{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
			{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
			{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"},
			{"content":"New extra step","status":"pending","step_id":"new_step_09"}
		]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ledgerWithPlanTodos(t, oldPlanArgs)
			out, err := (todoWrite{}).Execute(ctx, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("same-plan append should succeed: %v", err)
			}
			if strings.Contains(out, "计划已切换") {
				t.Fatalf("same-plan write must not report a plan switch: %q", out)
			}
		})
	}
}

// 判据③: the deadlock pair — the model cannot see the old list (todo_read was
// cleared) yet the write must both keep old completed items and not invent new
// ones — is solvable by declaring the new plan.
func TestTodoWritePlanSwitchSolvesClearedReadDeadlock(t *testing.T) {
	// Cross-turn baseline: ledger is a fresh turn (todo_read would come back
	// empty at the ledger layer), the old plan only survives as canonical state.
	ctx := evidence.WithLedger(context.Background(), evidence.NewLedger())
	ctx = evidence.WithTodoState(ctx, []evidence.TodoItem{
		{Content: "Ship the old task", Status: "completed", StepID: "old_step_01", PlanID: "plan-old"},
		{Content: "Write the old report", Status: "in_progress", StepID: "old_step_03", PlanID: "plan-old"},
	})

	// Without the marker the two requirements collide: keeping old completed
	// items is impossible to know, inventing new ones is rejected.
	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"todos":[
		{"content":"Investigate the new bug","status":"in_progress","step_id":"new_step_02"}
	]}`))
	if err == nil {
		t.Fatal("unmarked write over a canonical plan should still be checked")
	}

	out, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-new","todos":[
		{"content":"Investigate the new bug","status":"in_progress","step_id":"new_step_02"}
	]}`))
	if err != nil {
		t.Fatalf("declared plan switch must resolve the deadlock: %v", err)
	}
	if !strings.Contains(out, "计划已切换，旧计划 2 项已归档") {
		t.Fatalf("deadlock-resolving switch must carry the archive notice, got %q", out)
	}
}

// 同计划防伪造反向用例: an explicit plan_id EQUAL to the current plan buys no
// forgiveness — a fabricated completed step is still rejected, and the notice
// must not appear.
func TestTodoWriteSamePlanIDStillRejectsInventedCompleted(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-old","todos":[
		{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
		{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
		{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"},
		{"content":"Fabricated glory","status":"completed","step_id":"old_step_77"}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "not a step from the current plan") {
		t.Fatalf("same-plan fabrication must stay rejected, got %v", err)
	}
}

// The first plan write establishes the marker; the second write without a
// marker is still same-plan scoped (marker inheritance, not a free pass).
func TestTodoWriteFirstMarkerThenUnmarkedContinuation(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, `{"plan_id":"plan-a","todos":[
		{"content":"Step one","status":"completed","step_id":"a_step_01"},
		{"content":"Step two","status":"in_progress","step_id":"a_step_02"}
	]}`)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"todos":[
		{"content":"Fabricated","status":"completed","step_id":"a_step_99"}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "not a step from the current plan") {
		t.Fatalf("unmarked continuation of a marked plan must stay scoped, got %v", err)
	}
}

// A plan switch requires the full todos path — ops edit the current plan only.
func TestTodoWritePlanSwitchRejectsOpsPath(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-new","ops":[
		{"op":"replace","step_id":"old_step_03","item":{"content":"done","status":"completed"}}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "full todos list") {
		t.Fatalf("ops-based plan switch should be rejected with guidance, got %v", err)
	}
}

// todo_read echoes the current plan marker so the model can re-send it.
func TestTodoReadEchoesPlanMarker(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	out, err := (todoRead{}).Execute(ctx, nil)
	if err != nil {
		t.Fatalf("todo_read: %v", err)
	}
	if !strings.Contains(out, `"plan_id":"plan-old"`) {
		t.Fatalf("todo_read must echo the plan marker, got %s", out)
	}
}

// 修点②: items written without step_id get host-assigned stable ids, and a
// later completion of the same wording resolves by that identity.
func TestTodoWriteAutoAssignedStepIDsAreStableAcrossCalls(t *testing.T) {
	// First call via ReceiptFromToolCall so the recorded baseline carries the
	// same auto id the validator assigns (identical deterministic function).
	ledger := evidence.NewLedger()
	ledger.Record(evidence.ReceiptFromToolCall("todo_write", json.RawMessage(`{"todos":[
		{"content":"Reproduce the flake","status":"in_progress"}
	]}`), true, true))
	ctx := evidence.WithLedger(context.Background(), ledger)

	items, _ := ledger.LatestTodos()
	if len(items) != 1 || !strings.HasPrefix(items[0].StepID, evidence.AutoStepIDPrefix) {
		t.Fatalf("host must assign a stable auto step id, got %+v", items)
	}

	// Same content, no id sent again → same hash → completion matches.
	out, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"todos":[
		{"content":"Reproduce the flake","status":"completed"}
	]}`))
	if err != nil {
		t.Fatalf("completing a host-identified item without an id should work: %v", err)
	}
	if !strings.Contains(out, "1 completed") {
		t.Fatalf("unexpected ack: %q", out)
	}

	// Determinism: identical content derives identical ids.
	if evidence.StableTodoStepID("Reproduce the flake", 0) != evidence.StableTodoStepID("  reproduce the FLAKE ", 0) {
		t.Fatal("auto step id must be case/whitespace stable")
	}
	if evidence.StableTodoStepID("x", 0) == evidence.StableTodoStepID("x", 1) {
		t.Fatal("collision counter must differentiate repeated wording")
	}
}

// 修点② double: duplicate wording inside one list gets distinct collision
// suffixes, so verifyUniqueStepIDs stays satisfied.
func TestTodoWriteAutoAssignedIDsDisambiguateDuplicateWording(t *testing.T) {
	ledger := evidence.NewLedger()
	ledger.Record(evidence.ReceiptFromToolCall("todo_write", json.RawMessage(`{"todos":[
		{"content":"Same wording","status":"in_progress"},
		{"content":"Same wording","status":"pending"}
	]}`), true, true))
	got, _ := ledger.LatestTodos()
	if len(got) != 2 || got[0].StepID == got[1].StepID || !strings.HasSuffix(got[1].StepID, "_2") {
		t.Fatalf("duplicate wording must get distinct host ids, got %+v", got)
	}
}

// 修点①: a completed entry WITHOUT a step_id that cannot be matched gets the
// dedicated missing-id message, not the "invented" verdict.
func TestTodoWriteMissingStepIDGetsDedicatedMessage(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"todos":[
		{"content":"Ship the old task","status":"completed","step_id":"old_step_01"},
		{"content":"Clean up the old task","status":"abandoned","step_id":"old_step_02"},
		{"content":"Write the old report","status":"in_progress","step_id":"old_step_03"},
		{"content":"An unverifiable rewording of something else entirely","status":"completed"}
	]}`))
	if err == nil {
		t.Fatal("unverifiable completed entry should still be rejected")
	}
	if !strings.Contains(err.Error(), "has no step_id") {
		t.Fatalf("missing step_id must get the dedicated message, got %v", err)
	}
	if strings.Contains(err.Error(), "is not a step from the current plan") {
		t.Fatalf("missing step_id must not be reported as an invention: %v", err)
	}
}

// A plan switch still enforces the structural serial-shape checks.
func TestTodoWritePlanSwitchStillValidatesSerialShape(t *testing.T) {
	ctx := ledgerWithPlanTodos(t, oldPlanArgs)

	_, err := (todoWrite{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-new","todos":[
		{"content":"Pending only","status":"pending"}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "no in_progress") {
		t.Fatalf("plan switch must keep serial-shape validation, got %v", err)
	}
}

var _ = tool.HasPlanReplacementAuthorization
