package boot

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// Task 420: the normal-mode (non-delivery) system prompt must carry an explicit
// todo update cadence. Without a timing instruction the visible list goes stale
// across whole stretches of work; delivery mode keeps its own stricter sign-off
// rhythm through agent.DeliveryRuntimeMarker, which this policy does not touch.
func TestBuildAppendsTodoUpdatePolicyToSystemPrompt(t *testing.T) {
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`)

	ctrl, err := Build(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	sys := systemMessage(ctrl.History())
	for _, want := range []string{"Todo list freshness:", "sign off or complete a step, or the plan itself changes", "todo_write"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("todo update policy missing %q from system prompt:\n%s", want, sys)
		}
	}
	if i, j := strings.Index(sys, config.WorkPracticePolicy), strings.Index(sys, config.TodoUpdatePolicy); i < 0 || j < 0 || i > j {
		t.Fatal("todo update policy must follow the work-practice policy in the prompt")
	}
}
