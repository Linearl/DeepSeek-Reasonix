package agent

import (
	"strings"
	"testing"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// Task 572 (484-a): the task tool's subagent boundary summary must match the
// registry a sub-agent actually receives. On hosts where the OS bash sandbox
// cannot enforce write roots (currently Windows), BindWritePaths fail-closes
// and removes bash entirely from sub-agents dispatched with explicit
// write_paths — while the summary claimed "bash is exposed as foreground-only
// inside subagents" unconditionally. The description must disclose the absence
// in that quadrant and keep the historical text byte-identical everywhere else.

// TestTask572BoundaryDescriptionMatchesWritePathsToolset binds the description
// to the actual toolset of the failing quadrant: explicit write_paths × no OS
// bash sandbox drops bash from the sub-agent registry, so the parent-facing
// description and schema must say so instead of promising foreground-only bash.
func TestTask572BoundaryDescriptionMatchesWritePathsToolset(t *testing.T) {
	root := t.TempDir()
	claim, err := NormalizeWritePaths(root, []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	reg.Add(builtin.ConfineBash(sandbox.Spec{
		Mode:       "enforce",
		WriteRoots: []string{root},
	}, builtin.SessionDataGuard{}))
	reg.Add(foregroundOnlyBash{inner: mustGet(t, reg, "bash")})

	// The quadrant: sandbox cannot enforce write roots → bash is removed.
	bound, removed := BindWritePaths(reg, claim, root, false)
	if len(removed) != 1 || removed[0] != "bash" {
		t.Fatalf("removed = %v, want [bash]", removed)
	}
	if _, ok := bound.Get("bash"); ok {
		t.Fatal("write_paths sub-agent should have no bash when the OS sandbox cannot enforce write roots")
	}

	// …so the description the parent dispatches against must disclose it.
	task := &TaskTool{bashSandboxEnforced: func() bool { return false }}
	for label, text := range map[string]string{
		"description": task.Description(),
		"schema":      string(task.Schema()),
	} {
		if !strings.Contains(text, "foreground-only") {
			t.Fatalf("%s should keep the enforced-sandbox boundary claim: %s", label, text)
		}
		if !strings.Contains(text, "write_paths") || !strings.Contains(text, "without a bash tool") {
			t.Fatalf("%s must disclose bash absence for explicit write_paths on sandbox-less hosts: %s", label, text)
		}
	}
}

// TestTask572BoundaryUnwiredFailsClosed keeps the summary honest for callers
// that never wire WithBashSandboxEnforced: bashCanEnforceWriteRoots returns
// false, BindWritePaths drops bash for path-bound writers, and the summary
// must disclose that instead of the unconditional foreground-only claim.
func TestTask572BoundaryUnwiredFailsClosed(t *testing.T) {
	task := &TaskTool{}
	if !strings.Contains(task.Description(), "without a bash tool") {
		t.Fatalf("unwired task description should fail closed with the bash-absence disclosure: %s", task.Description())
	}
}

// TestTask572BoundaryZeroRegressionWhenSandboxEnforced locks acceptance for
// the existing quadrants: with the sandbox able to enforce write roots, the
// description and schema carry exactly the historical boundary summary and no
// disclosure clause (byte-identical modulo the untouched surrounding text).
func TestTask572BoundaryZeroRegressionWhenSandboxEnforced(t *testing.T) {
	task := &TaskTool{bashSandboxEnforced: func() bool { return true }}
	desc := task.Description()
	if !strings.Contains(desc, subagentToolBoundarySummary) {
		t.Fatalf("enforced-sandbox description should keep the historical boundary summary: %s", desc)
	}
	if strings.Contains(desc, "without a bash tool") {
		t.Fatalf("enforced-sandbox description must not append the no-sandbox disclosure: %s", desc)
	}
	schema := string(task.Schema())
	if !strings.Contains(schema, subagentToolBoundarySummary) {
		t.Fatalf("enforced-sandbox schema should keep the historical boundary summary: %s", schema)
	}
	if strings.Contains(schema, "without a bash tool") {
		t.Fatalf("enforced-sandbox schema must not append the no-sandbox disclosure: %s", schema)
	}
}
