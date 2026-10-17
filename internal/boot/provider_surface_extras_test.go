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
// pins the multi_edit gate on the same extras channel. Task 943 re-pins it
// under the split key semantics: experimental_multi_edit_injection is the
// explicit source, an absent injection key falls back to the legacy family
// key (migration — legacy family-on configs keep the injection face), an
// explicit injection key overrides the family key in BOTH directions
// (正向/反向独立开关), and both keys absent/off stays empty (golden/forbidden
// untouched).
func TestProviderSurfaceExtrasCarryMultiEditWithToolOptimizations(t *testing.T) {
	// 迁移回落：注入键缺省 + 族键开 → multi_edit 仍进可见面（拆分前行为不变）。
	familyOn := &config.Config{}
	familyOn.Agent.ExperimentalToolOptimizations = boolPtr(true)
	if got := providerSurfaceExtras(familyOn); len(got) != 1 || got[0] != "multi_edit" {
		t.Fatalf("extras with the legacy family on (injection key absent) = %v, want [multi_edit] (task-943 migration fallback)", got)
	}
	both := &config.Config{}
	both.Desktop.ExperimentalAutonomousUpdate = true
	both.Agent.ExperimentalToolOptimizations = boolPtr(true)
	if got := providerSurfaceExtras(both); len(got) != 2 || got[0] != "restart_update" || got[1] != "multi_edit" {
		t.Fatalf("extras with both switches on = %v, want [restart_update multi_edit]", got)
	}
	// 正向独立：readBack 开（族键开）+ 注入显式关 → multi_edit 不进可见面。
	readBackOnly := &config.Config{}
	readBackOnly.Agent.ExperimentalToolOptimizations = boolPtr(true)
	readBackOnly.Agent.ExperimentalMultiEditInjection = boolPtr(false)
	if got := providerSurfaceExtras(readBackOnly); len(got) != 0 {
		t.Fatalf("extras with readBack on + injection explicitly off = %v, want none (task-943 forward split)", got)
	}
	// 反向独立：族键关 + 注入显式开 → multi_edit 进可见面。
	injectionOnly := &config.Config{}
	injectionOnly.Agent.ExperimentalToolOptimizations = boolPtr(false)
	injectionOnly.Agent.ExperimentalMultiEditInjection = boolPtr(true)
	if got := providerSurfaceExtras(injectionOnly); len(got) != 1 || got[0] != "multi_edit" {
		t.Fatalf("extras with family off + injection explicitly on = %v, want [multi_edit] (task-943 reverse split)", got)
	}
	// 两键显式全关 → 空面。
	bothOff := &config.Config{}
	bothOff.Agent.ExperimentalToolOptimizations = boolPtr(false)
	bothOff.Agent.ExperimentalMultiEditInjection = boolPtr(false)
	if got := providerSurfaceExtras(bothOff); len(got) != 0 {
		t.Fatalf("extras with both keys explicitly off = %v, want none", got)
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
// 任务 943：注入键缺省时本用例同时钉住迁移回落（族键开 = 注入开，与拆分
// 前行为一致）。
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

// TestBootRequestReadBackOnInjectionExplicitlyOff (task 943 正向独立) is the
// forward-split acceptance capture: experimental_tool_optimizations = true +
// experimental_multi_edit_injection = false → edit_file keeps the readBack
// parameter (readBack face follows the family key) while multi_edit leaves
// the provider-visible surface. 行为与「只开 readBack」的期望一致。
func TestBootRequestReadBackOnInjectionExplicitlyOff(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"
experimental_tool_optimizations = true
experimental_multi_edit_injection = false

[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
model = "x"
`)
	req := firstTokenProfileRequest(t, "economy")
	if requestHasTool(req, "multi_edit") {
		t.Fatalf("multi_edit must stay off the provider surface while injection is explicitly off; tools=%v", toolSchemaNames(req.Tools))
	}
	if !requestHasTool(req, "edit_file") {
		t.Fatalf("sanity: edit_file must stay on the unified surface; tools=%v", toolSchemaNames(req.Tools))
	}
	if !requestToolSchemaContains(req, "edit_file", "readBack") {
		t.Fatal("readBack face must stay lit (family key on) — edit_file schema must carry the readBack parameter")
	}
}

// TestBootRequestInjectionOnFamilyExplicitlyOff (task 943 反向独立) is the
// reverse-split acceptance capture: experimental_tool_optimizations = false +
// experimental_multi_edit_injection = true → multi_edit joins the
// provider-visible surface while its schema does NOT carry readBack (the
// readBack face follows the family key, which is off).
func TestBootRequestInjectionOnFamilyExplicitlyOff(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"
experimental_tool_optimizations = false
experimental_multi_edit_injection = true

[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
model = "x"
`)
	req := firstTokenProfileRequest(t, "economy")
	if !requestHasTool(req, "multi_edit") {
		t.Fatalf("injection-on first request must carry multi_edit; tools=%v", toolSchemaNames(req.Tools))
	}
	if requestToolSchemaContains(req, "multi_edit", "readBack") {
		t.Fatal("readBack face must stay dark (family key off) — multi_edit schema must NOT carry the readBack parameter")
	}
	if requestToolSchemaContains(req, "edit_file", "readBack") {
		t.Fatal("readBack face must stay dark (family key off) — edit_file schema must NOT carry the readBack parameter")
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
