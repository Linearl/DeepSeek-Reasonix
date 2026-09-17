package agent

import (
	"testing"

	"reasonix/internal/provider"
)

func TestNormalizedFailureRequiresConsecutiveBatches(t *testing.T) {
	var loop turnLoopState
	call := []provider.ToolCall{{Name: "bash"}}
	failure := []toolOutcome{{errMsg: "exit status 255"}}
	if consecutiveNormalizedFailure(call, failure, &loop) {
		t.Fatal("first failure must not trip")
	}
	if consecutiveNormalizedFailure(call, []toolOutcome{{}}, &loop) {
		t.Fatal("success must reset the category streak")
	}
	if consecutiveNormalizedFailure(call, failure, &loop) {
		t.Fatal("failure after success must start a new streak")
	}
}

func TestNormalizedFailureChecksEveryBatchOutcome(t *testing.T) {
	var loop turnLoopState
	calls := []provider.ToolCall{{Name: "read_file"}, {Name: "bash"}}
	outcomes := []toolOutcome{{}, {errMsg: "exit code: 1"}}
	if consecutiveNormalizedFailure(calls, outcomes, &loop) {
		t.Fatal("first batch failure must not trip")
	}
	if !consecutiveNormalizedFailure(calls, outcomes, &loop) {
		t.Fatal("repeated failure in the second batch item must trip")
	}
}

// Task 23 P1-c: a todo_write rejection names the offending item and repeats its
// content, so the raw message differs on every retry and the storm breaker
// never saw a repeat. The category has to fold the whole validation family into
// one signature while other tools keep their own.
func TestTodoWriteRejectionsShareOneErrorCategory(t *testing.T) {
	first := errorCategory("todo_write", `todo 2 "step two" is a second in_progress item; a serial task list allows exactly one current item - demote this one back to pending, or mark the current item completed first`)
	second := errorCategory("todo_write", `todo 7 "another step" is in_progress after pending work; the current item must be the first unfinished item`)
	if first != second {
		t.Fatalf("todo_write rejections must share one category: %q vs %q", first, second)
	}
	if first != "todo_write:validation" {
		t.Fatalf("category = %q, want todo_write:validation", first)
	}
	if got := errorCategory("write_file", "boom"); got != "write_file:boom" {
		t.Fatalf("other tools keep their own signature, got %q", got)
	}
}

func TestBatchStormSignatureIgnoresTheRejectedTodoContent(t *testing.T) {
	calls := []provider.ToolCall{{Name: "todo_write"}}
	first, ok := batchStormSignature(calls, []toolOutcome{{errMsg: `todo 2 "step two" is a second in_progress item`}})
	if !ok {
		t.Fatal("a failed call must produce a storm signature")
	}
	second, ok := batchStormSignature(calls, []toolOutcome{{errMsg: `todo 9 "a rewritten step" is a level-1 sub-step with no phase above it`}})
	if !ok || first != second {
		t.Fatalf("reworded retries of a rejected todo list must match: %q vs %q", first, second)
	}
}
