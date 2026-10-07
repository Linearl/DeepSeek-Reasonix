package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// recordingBash stands in for the parent registry's bash tool: it records the
// last executed args so tests can assert whether a call reached the inner
// execution layer or was stopped by the read-only classifier.
type recordingBash struct {
	executed int
	lastArgs string
}

func (b *recordingBash) Name() string { return "bash" }
func (b *recordingBash) Description() string {
	return "Execute a command in the shell and return combined stdout/stderr."
}
func (b *recordingBash) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (b *recordingBash) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	b.executed++
	b.lastArgs = string(args)
	return "ok", nil
}
func (b *recordingBash) ReadOnly() bool { return false }

// readOnlyStubTool keeps the path-bound registry non-empty so the injection
// tests exercise the bash fallback, not the no-tools guard.
type readOnlyStubTool struct{ name string }

func (t readOnlyStubTool) Name() string        { return t.name }
func (t readOnlyStubTool) Description() string { return "read-only stub" }
func (t readOnlyStubTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t readOnlyStubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return "stub", nil
}
func (t readOnlyStubTool) ReadOnly() bool { return true }

func newParallelWriterRegistryTestTool(t *testing.T, parentReg *tool.Registry, sandboxEnforced bool, fallbackOn bool) *TaskTool {
	t.Helper()
	tt := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       nil,
		ParentRegistry: parentReg,
		MaxSteps:       20,
		SysPrompt:      "sys",
	}).WithTranscripts(NewSubagentStore(t.TempDir()), t.TempDir(), "base-model", "base-effort").
		WithBashSandboxEnforced(func() bool { return sandboxEnforced })
	if fallbackOn {
		tt = tt.WithParallelWriterReadOnlyBash(true)
	}
	return tt
}

func parallelWriterSpec(t *testing.T, root string) ProfileExecSpec {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	claim, err := NormalizeWritePaths(root, []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	return ProfileExecSpec{Grant: CapabilityGrant{WritePaths: claim}}
}

// Task 573: with the experimental fallback on, a parallel writer whose bash was
// dropped (OS sandbox cannot enforce the claim roots) gets bash back behind the
// read-only classifier — read-only commands reach the shell, write attempts and
// background requests are refused before execution.
func TestParallelWriterReadOnlyBashFallbackGatesCommands(t *testing.T) {
	root := t.TempDir()
	inner := &recordingBash{}
	parentReg := tool.NewRegistry()
	parentReg.Add(inner)
	parentReg.Add(readOnlyStubTool{name: "read_file"})
	tt := newParallelWriterRegistryTestTool(t, parentReg, false, true)

	reg, _, err := tt.buildSubagentRegistry(parallelWriterSpec(t, root), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	bash, ok := reg.Get("bash")
	if !ok {
		t.Fatal("fallback switch on but bash is still absent from the path-bound writer registry")
	}
	if _, isRO := bash.(readOnlyBash); !isRO {
		t.Fatalf("re-admitted bash is %T, want readOnlyBash wrapper", bash)
	}

	out, err := bash.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`))
	if err != nil || out != "ok" {
		t.Fatalf("read-only command outcome = %q, %v; want passthrough", out, err)
	}
	if inner.executed != 1 {
		t.Fatalf("inner executed %d times, want 1", inner.executed)
	}

	_, err = bash.Execute(context.Background(), json.RawMessage(`{"command":"touch docs/x.txt"}`))
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("write command error = %v, want read-only refusal", err)
	}
	_, err = bash.Execute(context.Background(), json.RawMessage(`{"command":"echo hi","run_in_background":true}`))
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("background request error = %v, want read-only refusal", err)
	}
	if inner.executed != 1 {
		t.Fatalf("inner executed %d times after blocked calls, want 1", inner.executed)
	}
}

// Task 573 acceptance: with the switch off (default), the path-bound writer
// registry keeps today's shape — bash absent, nothing else changes.
func TestParallelWriterReadOnlyBashOffKeepsCurrentShape(t *testing.T) {
	root := t.TempDir()
	parentReg := tool.NewRegistry()
	parentReg.Add(&recordingBash{})
	parentReg.Add(readOnlyStubTool{name: "read_file"})
	tt := newParallelWriterRegistryTestTool(t, parentReg, false, false)

	reg, _, err := tt.buildSubagentRegistry(parallelWriterSpec(t, root), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("bash"); ok {
		t.Fatal("fallback switch off but bash reappeared in the path-bound writer registry")
	}
	if _, ok := reg.Get("read_file"); !ok {
		t.Fatal("read-only tool vanished from the path-bound writer registry")
	}
}

// Task 573 boundary: the fallback only covers the dropped-bash quadrant. When
// the OS sandbox can confine bash to the claim roots, the rebind path keeps the
// full foreground bash — the switch must not downgrade it to read-only.
func TestParallelWriterReadOnlyBashNotAppliedWhenSandboxRebinds(t *testing.T) {
	root := t.TempDir()
	parentReg := tool.NewRegistry()
	parentReg.Add(builtin.ConfineBash(sandbox.Spec{
		Mode:       "enforce",
		WriteRoots: []string{root},
	}, builtin.SessionDataGuard{}))
	parentReg.Add(readOnlyStubTool{name: "read_file"})
	tt := newParallelWriterRegistryTestTool(t, parentReg, true, true)

	reg, _, err := tt.buildSubagentRegistry(parallelWriterSpec(t, root), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	bash, ok := reg.Get("bash")
	if !ok {
		t.Fatal("sandbox-rebindable bash was dropped despite enforced write roots")
	}
	if _, isRO := bash.(readOnlyBash); isRO {
		t.Fatal("sandbox-confined bash must keep the full foreground wrapper, not the read-only fallback")
	}
	if _, isFO := bash.(foregroundOnlyBash); !isFO {
		t.Fatalf("kept bash is %T, want foregroundOnlyBash", bash)
	}
}

// Defense in depth: the injected readOnlyBash wraps the raw builtin bash, so
// BindChildWriteRoots still rebinds the inner OS-sandbox write roots onto the
// child claim set — the classifier is the primary wall, the sandbox the second.
func TestBindChildWriteRootsRebindsInjectedReadOnlyBashInner(t *testing.T) {
	root := t.TempDir()
	raw := builtin.ConfineBash(sandbox.Spec{
		Mode:       "enforce",
		WriteRoots: []string{root},
	}, builtin.SessionDataGuard{})
	reg := tool.NewRegistry()
	reg.Add(readOnlyBash{inner: raw})

	bound, childSet := BindChildWriteRoots(reg, sandbox.NewWritableRootSet([]string{root}), WritePathSet{})
	if childSet == nil {
		t.Fatal("BindChildWriteRoots returned no child set")
	}
	wrapped, ok := bound.Get("bash")
	if !ok {
		t.Fatal("bash missing after BindChildWriteRoots")
	}
	ro, isRO := wrapped.(readOnlyBash)
	if !isRO {
		t.Fatalf("wrapped bash is %T, want readOnlyBash preserved", wrapped)
	}
	innerSet := reflect.ValueOf(ro.inner).FieldByName("rootSet")
	if !innerSet.IsValid() || innerSet.IsNil() {
		t.Fatal("readOnlyBash inner lost its write-root set binding")
	}
	if innerSet.Pointer() != reflect.ValueOf(childSet).Pointer() {
		t.Fatal("readOnlyBash inner was not rebound to the child write-root set")
	}
}
