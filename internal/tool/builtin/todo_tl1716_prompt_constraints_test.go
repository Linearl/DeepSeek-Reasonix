package builtin

// tl1716: the tool description must front-load the three constraints the new-15
// field case (2026-11-19) had to learn by trial and error, burning three
// rejections before a whole-list rewrite succeeded:
//  1. ops element shape — op/step_id/item siblings at the element top level;
//  2. single in_progress — exactly one, always the first unfinished item;
//  3. identity immovable — changing a step's id requires insert+delete.
// Plus a minimal ops example that must stay EXECUTABLE against the ids it
// names (fork iron rule: every next step an error or example offers must
// actually exist), and the wholesale-rewrite fallback posture.

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

func todoWriteDescription() string { return (todoWrite{}).Description() }

// The description must carry all three constraints, and they must appear
// BEFORE the minimal example (front-loaded, not buried in the tail prose).
func TestTodoWriteDescriptionFrontloadsThreeConstraints(t *testing.T) {
	desc := todoWriteDescription()
	constraints := []string{
		"OPS SHAPE",
		"never goes inside `item`",
		"ONE in_progress",
		"FIRST unfinished item",
		"[completed, in_progress, pending]",
		"IDENTITY IS IMMOVABLE",
		"INSERT a new item with a fresh id and DELETE the old one",
		"wholesale rewrite",
	}
	exampleIndex := strings.Index(desc, "Minimal ops example:")
	if exampleIndex < 0 {
		t.Fatal("description must carry a Minimal ops example")
	}
	for _, want := range constraints {
		index := strings.Index(desc, want)
		if index < 0 {
			t.Fatalf("description missing constraint %q", want)
		}
		if index > exampleIndex {
			t.Fatalf("constraint %q must appear before the minimal example (front-loaded), got it after", want)
		}
	}
}

// The example JSON embedded in the description must parse, and executing it
// verbatim on a matching baseline must succeed — the ids it names
// (plan_step_01, plan_step_06, plan_step_07) are real steps there.
func TestTodoWriteMinimalExampleIsExecutable(t *testing.T) {
	desc := todoWriteDescription()
	prefix := "Minimal ops example: "
	start := strings.Index(desc, prefix)
	if start < 0 {
		t.Fatal("no Minimal ops example found")
	}
	start += len(prefix)
	// Brace-count extraction: the example is compact JSON followed by prose.
	depth := 0
	end := -1
	for i := start; i < len(desc); i++ {
		switch desc[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end > 0 {
			break
		}
	}
	if end < 0 {
		t.Fatal("example JSON is unbalanced")
	}
	raw := json.RawMessage(desc[start:end])
	var parsed struct {
		Ops []todoOp `json:"ops"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("example JSON does not parse: %v", err)
	}
	if len(parsed.Ops) == 0 {
		t.Fatal("example carries no ops")
	}

	// Baseline matching the ids the example names: step_01 in progress,
	// steps 02..06 pending (step_06 is the insert anchor).
	baseline := []evidence.TodoItem{
		{Content: "step 1", Status: "in_progress", StepID: "plan_step_01"},
		{Content: "step 2", Status: "pending", StepID: "plan_step_02"},
		{Content: "step 3", Status: "pending", StepID: "plan_step_03"},
		{Content: "step 4", Status: "pending", StepID: "plan_step_04"},
		{Content: "step 5", Status: "pending", StepID: "plan_step_05"},
		{Content: "step 6", Status: "pending", StepID: "plan_step_06"},
	}
	ctx := ledgerWithBaseline(t, baseline)
	out, err := (todoWrite{}).Execute(ctx, raw)
	if err != nil {
		t.Fatalf("the description's own example must execute cleanly: %v", err)
	}
	if !strings.Contains(out, "plan_step") && !strings.Contains(out, "Todos updated") {
		t.Fatalf("unexpected ack: %q", out)
	}
}

// Schema regression (tl1716 field form): a REAL ops array passes schema
// validation untouched; a stringified ops array fails with exactly the
// /ops type violation (the host-side validation itself never misreports a
// real array — the field case was a stringified value, so the fix is the
// coaching line on the agent side plus schema prose here).
func TestTodoWriteSchemaOpsArrayShapeValidation(t *testing.T) {
	target := todoWrite{}
	real := tool.ValidateArguments(target, json.RawMessage(
		`{"ops":[{"op":"replace","step_id":"plan_step_01","item":{"content":"x","status":"completed"}}]}`))
	if real.CompileErr != nil || len(real.Violations) != 0 {
		t.Fatalf("real ops array must pass schema validation: %+v", real)
	}
	stringified := tool.ValidateArguments(target, json.RawMessage(
		`{"ops":"[{\"op\":\"replace\",\"step_id\":\"plan_step_01\",\"item\":{\"content\":\"x\",\"status\":\"completed\"}}]"}`))
	if len(stringified.Violations) != 1 {
		t.Fatalf("stringified ops array must yield exactly one violation: %+v", stringified.Violations)
	}
	if stringified.Violations[0].Path != "/ops" || stringified.Violations[0].Keyword != "type" ||
		stringified.Violations[0].Expected != "array" {
		t.Fatalf("unexpected violation: %+v", stringified.Violations[0])
	}
}

// Schema prose pins: the ops array value shape, the identity-immovable rule
// on item.step_id, and the wholesale-rewrite fallback on todos.
func TestTodoWriteSchemaDocumentsTL1716Constraints(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(todoWrite{}.Schema(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)

	todosDesc, _ := props["todos"].(map[string]any)["description"].(string)
	if !strings.Contains(strings.ToLower(todosDesc), "wholesale rewrite") ||
		!strings.Contains(strings.ToLower(todosDesc), "fallback") ||
		!strings.Contains(strings.ToLower(todosDesc), "original step_id") {
		t.Fatalf("todos description must document the wholesale-rewrite fallback with original ids, got: %q", todosDesc)
	}

	opsDesc, _ := props["ops"].(map[string]any)["description"].(string)
	if !strings.Contains(strings.ToLower(opsDesc), "never a quoted string") {
		t.Fatalf("ops description must state the value must be a real array, got: %q", opsDesc)
	}

	items, _ := props["ops"].(map[string]any)["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	payload, _ := itemProps["item"].(map[string]any)
	payloadProps, _ := payload["properties"].(map[string]any)
	innerDesc, _ := payloadProps["step_id"].(map[string]any)["description"].(string)
	low := strings.ToLower(innerDesc)
	if !strings.Contains(low, "identity is immovable") ||
		!strings.Contains(low, "insert") || !strings.Contains(low, "delete") {
		t.Fatalf("item.step_id description must state identity immovability with insert+delete, got: %q", innerDesc)
	}
}
