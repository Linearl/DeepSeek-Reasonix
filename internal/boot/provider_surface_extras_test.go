package boot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// surfaceProbeTool is a minimal tool.Tool for registry visibility tests.
type surfaceProbeTool struct{ name string }

// boolPtr arms the task-915 *bool family field in test configs.
func boolPtr(v bool) *bool { return &v }

func (p surfaceProbeTool) Name() string        { return p.name }
func (p surfaceProbeTool) Description() string { return p.name + " desc" }
func (p surfaceProbeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (p surfaceProbeTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (p surfaceProbeTool) ReadOnly() bool { return true }

// TestProviderSurfaceExtrasFollowTheFlag pins the gate: restart_update joins
// the provider-visible surface only while experimental_autonomous_update is
// on (task 254 second fault — the model tools list must carry it, or the
// first auto-update run cannot bootstrap). Task 915 extends the same gate to
// multi_edit under the 工具优化 family key.
func TestProviderSurfaceExtrasFollowTheFlag(t *testing.T) {
	on := &config.Config{}
	on.Desktop.ExperimentalAutonomousUpdate = true
	if got := providerSurfaceExtras(on); len(got) != 1 || got[0] != "restart_update" {
		t.Fatalf("extras with the switch on = %v", got)
	}
	off := &config.Config{}
	if got := providerSurfaceExtras(off); len(got) != 0 {
		t.Fatalf("extras with the switch off = %v, want none", got)
	}
	if got := providerSurfaceExtras(nil); len(got) != 0 {
		t.Fatalf("extras with nil config = %v, want none", got)
	}
}

// TestProviderSurfaceExtrasCarryMultiEditWithToolOptimizations (task 915)
// pins the multi_edit gate on the same extras channel: the family switch
// (experimental_tool_optimizations) is the single source, both switches can
// stack, and the default-off config stays empty (golden/forbidden untouched).
func TestProviderSurfaceExtrasCarryMultiEditWithToolOptimizations(t *testing.T) {
	familyOn := &config.Config{}
	familyOn.Agent.ExperimentalToolOptimizations = boolPtr(true)
	if got := providerSurfaceExtras(familyOn); len(got) != 1 || got[0] != "multi_edit" {
		t.Fatalf("extras with the family on = %v, want [multi_edit]", got)
	}
	both := &config.Config{}
	both.Desktop.ExperimentalAutonomousUpdate = true
	both.Agent.ExperimentalToolOptimizations = boolPtr(true)
	if got := providerSurfaceExtras(both); len(got) != 2 || got[0] != "restart_update" || got[1] != "multi_edit" {
		t.Fatalf("extras with both switches on = %v, want [restart_update multi_edit]", got)
	}
}

// TestProviderVisibleSurfaceCarriesRestartUpdate pins the third call surface:
// with the tool registered and the flag-gated extra passed, the model-visible
// schema surface includes restart_update; with extras empty it stays hidden
// while remaining executable via Get (capability dispatch).
func TestProviderVisibleSurfaceCarriesRestartUpdate(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(surfaceProbeTool{name: "restart_update"})
	reg.Add(surfaceProbeTool{name: "use_capability"})

	// Flag off: registered and dispatchable, but not in the model tools list.
	applyUnifiedProviderToolSurface(reg, providerSurfaceExtras(&config.Config{})...)
	if reg.ProviderVisible("restart_update") {
		t.Fatal("restart_update must stay hidden while the switch is off")
	}
	if !reg.ProviderVisible("use_capability") {
		t.Fatal("sanity: use_capability should be visible in the unified surface")
	}

	// Flag on: the model tools list carries restart_update.
	on := &config.Config{}
	on.Desktop.ExperimentalAutonomousUpdate = true
	applyUnifiedProviderToolSurface(reg, providerSurfaceExtras(on)...)
	if !reg.ProviderVisible("restart_update") {
		t.Fatal("restart_update must be provider-visible with the switch on")
	}
	if !reg.ProviderVisible("use_capability") {
		t.Fatal("the unified surface must keep its core tools")
	}
	// Still executable via Get (the capability-dispatch surface).
	if _, ok := reg.Get("restart_update"); ok != true {
		t.Fatal("restart_update must remain executable via Get")
	}
}

// TestProviderVisibleSurfaceCarriesMultiEditWithFamilyLit (task 915) pins the
// registry half of the multi_edit exposure: with the family extra passed, the
// model-visible surface includes multi_edit; with extras empty it stays hidden
// while remaining registered (the capability-dispatch surface).
func TestProviderVisibleSurfaceCarriesMultiEditWithFamilyLit(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(surfaceProbeTool{name: "multi_edit"})
	reg.Add(surfaceProbeTool{name: "use_capability"})

	// Family off: registered and dispatchable, but not in the model tools list.
	applyUnifiedProviderToolSurface(reg, providerSurfaceExtras(&config.Config{})...)
	if reg.ProviderVisible("multi_edit") {
		t.Fatal("multi_edit must stay hidden while the family switch is off")
	}
	if _, ok := reg.Get("multi_edit"); !ok {
		t.Fatal("multi_edit must remain executable via Get while hidden")
	}

	// Family on: the model tools list carries multi_edit.
	on := &config.Config{}
	on.Agent.ExperimentalToolOptimizations = boolPtr(true)
	applyUnifiedProviderToolSurface(reg, providerSurfaceExtras(on)...)
	if !reg.ProviderVisible("multi_edit") {
		t.Fatal("multi_edit must be provider-visible with the family switch on")
	}
	if !reg.ProviderVisible("use_capability") {
		t.Fatal("the unified surface must keep its core tools")
	}
}

// TestBootRequestCarriesMultiEditWhenFamilyLit (task 915) is the on-state
// acceptance capture: a real boot with experimental_tool_optimizations = true
// puts multi_edit into the request tools array, and its schema carries the
// task-875 readBack parameter (the family's first member rides along with the
// injection). Go DefaultConfig stays off, so this config is explicit.
func TestBootRequestCarriesMultiEditWhenFamilyLit(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"
experimental_tool_optimizations = true

[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
model = "x"
`)
	req := firstTokenProfileRequest(t, "economy")
	if !requestHasTool(req, "multi_edit") {
		t.Fatalf("family-on first request must carry multi_edit; tools=%v", toolSchemaNames(req.Tools))
	}
	if !requestHasTool(req, "edit_file") {
		t.Fatalf("sanity: edit_file must stay on the unified surface; tools=%v", toolSchemaNames(req.Tools))
	}
	if requestHasTool(req, "restart_update") {
		t.Fatalf("restart_update belongs to a different flag and must stay hidden; tools=%v", toolSchemaNames(req.Tools))
	}
	if !requestToolSchemaContains(req, "multi_edit", "readBack") {
		t.Fatal("family-on multi_edit schema must carry the task-875 readBack parameter")
	}
}

// TestUseCapabilityDispatchesMultiEditWhileFamilyExplicitlyOff (task 915) is
// the execution-surface-always-available pin: with the family explicitly off
// the tool stays off the provider-visible surface but a use_capability call
// still executes it end to end (read first so the evidence gate is fed).
func TestUseCapabilityDispatchesMultiEditWhileFamilyExplicitlyOff(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "note.txt", "alpha\nbeta\n")
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"
experimental_tool_optimizations = false

[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
model = "x"
`)
	registerBootTokenProfileTestProvider()
	raw, _ := json.Marshal(map[string]any{
		"action":        "call",
		"capability_id": "tool:multi_edit",
		"arguments": map[string]any{
			"path":  "note.txt",
			"edits": []map[string]any{{"old_string": "alpha", "new_string": "ALPHA"}},
		},
	})
	prov := testutil.NewMock("ucap-multiedit",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"note.txt"}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "use_capability", Arguments: string(raw)}}},
		testutil.Turn{Text: "done"},
	)
	setBootTokenProfileTestProvider(t, prov)
	ctrl, err := Build(context.Background(), Options{Sink: event.Discard, TokenMode: "economy"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()
	if err := ctrl.Run(context.Background(), "patch the note"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, req := range mainConversationRequests(prov.Requests()) {
		if requestHasTool(req, "multi_edit") {
			t.Fatalf("multi_edit must stay off the provider surface while the family is off; tools=%v", toolSchemaNames(req.Tools))
		}
	}
	var toolOut strings.Builder
	for _, msg := range ctrl.History() {
		if msg.Role == provider.RoleTool {
			toolOut.WriteString(msg.Content)
		}
	}
	if !strings.Contains(toolOut.String(), "1 edits applied (1 total replacements)") ||
		!strings.Contains(toolOut.String(), "+ALPHA") {
		t.Fatalf("use_capability(tool:multi_edit) did not execute:\n%s", toolOut.String())
	}
	body, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(body) != "ALPHA\nbeta\n" {
		t.Fatalf("multi_edit did not patch the file: %q", body)
	}
}
