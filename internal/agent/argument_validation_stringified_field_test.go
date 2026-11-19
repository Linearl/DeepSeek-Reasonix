package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// todoWriteTool returns the real registered todo_write builtin.
func todoWriteTool() tool.Tool {
	for _, bt := range tool.Builtins() {
		if bt.Name() == "todo_write" {
			return bt
		}
	}
	panic("todo_write builtin not registered")
}

// tl1716: a type violation whose value actually arrived as a JSON string
// wrapping the expected array must not surface as a bare `/ops: type;
// expected array` — that reads as a false alarm to the model, which believes
// it sent an array, and the field case (new-15, 2026-11-19) burned three
// retries before a whole-list rewrite worked. The coaching line names the
// packaging fact and the correct JSON shape, value-free.
func TestTL1716StringifiedOpsArrayCoaching(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(todoWriteTool())
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "tl1716-1", Name: "todo_write",
		Arguments: `{"ops":"[{\"op\":\"replace\",\"step_id\":\"plan_step_01\",\"item\":{\"content\":\"x\",\"status\":\"completed\"}}]"}`,
	})
	if !strings.Contains(out.output, "argument validation failed") {
		t.Fatalf("expected validation failure, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, "/ops: type; expected array") {
		t.Fatalf("expected the type violation line, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, "is a JSON string wrapping an array") {
		t.Fatalf("missing stringified-array packaging fact:\n%s", out.output)
	}
	if !strings.Contains(out.output, `"ops": [ ... ]`) {
		t.Fatalf("missing correct-shape example:\n%s", out.output)
	}
	// Value-free discipline: the stringified payload is never echoed back.
	if strings.Contains(out.output, "plan_step_01") || strings.Contains(out.output, "Read config") {
		t.Fatalf("coaching echoed model values:\n%s", out.output)
	}
}

// The false-alarm guard: a REAL ops array must pass schema validation and
// reach the tool's own business checks — never tagged as a stringified value.
func TestTL1716RealOpsArrayNotFlagged(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(todoWriteTool())
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "tl1716-2", Name: "todo_write",
		Arguments: `{"ops":[{"op":"replace","step_id":"plan_step_01","item":{"content":"x","status":"completed"}}]}`,
	})
	if strings.Contains(out.output, "argument validation failed") {
		t.Fatalf("real array must not be schema-rejected:\n%s", out.output)
	}
	if strings.Contains(out.output, "wrapping an array") || strings.Contains(out.output, "wrapping an object") {
		t.Fatalf("real array must not get stringified coaching:\n%s", out.output)
	}
	// Reaching the business layer is the point: with an empty baseline the
	// replace target is unknown, which is a tool-level error, not a schema one.
	if !strings.Contains(out.output, "not in the current list") {
		t.Fatalf("expected business-layer unknown-target error, got:\n%s", out.output)
	}
}

func TestTL1716StringifiedObjectFieldCoaching(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(todoWriteTool())
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	// An ops element whose payload item arrived stringified.
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "tl1716-3", Name: "todo_write",
		Arguments: `{"ops":[{"op":"replace","step_id":"plan_step_01","item":"{\"content\":\"x\",\"status\":\"completed\"}"}]}`,
	})
	if !strings.Contains(out.output, "/ops/0/item: type; expected object") {
		t.Fatalf("expected nested type violation, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, "is a JSON string wrapping an object") {
		t.Fatalf("missing stringified-object packaging fact:\n%s", out.output)
	}
	if !strings.Contains(out.output, `"item": { ... }`) {
		t.Fatalf("missing correct-shape example:\n%s", out.output)
	}
}

// Unit pins for the detection helper: root-level violations stay out (they
// already have Task 216/877 coverage), non-JSON string values stay out, and
// multi-token pointers resolve to the innermost value.
func TestTL1716StringifiedFieldCorrectionsUnit(t *testing.T) {
	violations := func(path, expected string) []tool.ArgumentViolation {
		return []tool.ArgumentViolation{{Path: path, Keyword: "type", Expected: expected}}
	}
	// String wrapping an array where array is expected -> coaching.
	if got := stringifiedFieldCorrections(json.RawMessage(`{"ops":"[1]"}`), violations("/ops", "array")); got == "" {
		t.Fatal("stringified array must produce coaching")
	}
	// String wrapping an object where array is expected -> no coaching.
	if got := stringifiedFieldCorrections(json.RawMessage(`{"ops":"{\"a\":1}"}`), violations("/ops", "array")); got != "" {
		t.Fatalf("object value must not get array coaching: %s", got)
	}
	// String wrapping a string -> no coaching.
	if got := stringifiedFieldCorrections(json.RawMessage(`{"ops":"[1]"}`), violations("/ops", "string")); got != "" {
		t.Fatalf("string value must not get array coaching: %s", got)
	}
	// Root pointer is out of scope here.
	if got := stringifiedFieldCorrections(json.RawMessage(`"[1]"`), violations("", "array")); got != "" {
		t.Fatalf("root-level stringified value is covered elsewhere: %s", got)
	}
	// Nested pointer resolves correctly.
	if got := stringifiedFieldCorrections(json.RawMessage(`{"a":{"b":"[1]"}}`), violations("/a/b", "array")); got == "" {
		t.Fatal("nested stringified array must produce coaching")
	}
	// Real (non-string) value -> no coaching.
	if got := stringifiedFieldCorrections(json.RawMessage(`{"ops":[1]}`), violations("/ops", "array")); got != "" {
		t.Fatalf("real array must not get coaching: %s", got)
	}
	// Non-JSON string -> no coaching.
	if got := stringifiedFieldCorrections(json.RawMessage(`{"ops":"not json ["}`), violations("/ops", "array")); got != "" {
		t.Fatalf("non-JSON string must not get coaching: %s", got)
	}
	// Escaped pointer token resolves the real key.
	if got := stringifiedFieldCorrections(json.RawMessage(`{"a/b":"[1]"}`), violations("/a~1b", "array")); got == "" {
		t.Fatal("escaped pointer token must resolve")
	}
}
