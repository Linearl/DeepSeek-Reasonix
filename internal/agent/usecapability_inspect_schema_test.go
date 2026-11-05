package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
	_ "reasonix/internal/tool/builtin"
)

// tl1438 harness: one registry-backed local tool whose schema is the engine's
// own registered definition (heartbeat_task_upsert), reached through inspect.
func builtinToolByName(t *testing.T, name string) tool.Tool {
	t.Helper()
	for _, tl := range tool.Builtins() {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("builtin tool %q not registered", name)
	return nil
}

func inspectCapability(t *testing.T, tl *UseCapabilityTool, id string) map[string]any {
	t.Helper()
	out, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("inspect %s: err=%v out=%q", id, err, out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("inspect %s output is not JSON: %v\n%s", id, err, out)
	}
	return payload
}

// Test1438InspectRegistryToolExposesEngineSchema is the acceptance gate:
// the machine layer must be generated from the engine's own registered
// Schema() (single source — the same bytes argument validation enforces),
// while the prose layer stays untouched.
func Test1438InspectRegistryToolExposesEngineSchema(t *testing.T) {
	target := builtinToolByName(t, "heartbeat_task_upsert")
	reg := tool.NewRegistry()
	reg.Add(target)
	entry := capability.Entry{
		ID: "tool:heartbeat_task_upsert", Kind: capability.KindTool,
		Name: "heartbeat_task_upsert", Description: "Create or update one scheduled heartbeat task.",
		Status: capability.StatusReady, ToolName: "heartbeat_task_upsert",
	}
	tl := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, func() capability.Catalog {
		return capability.Catalog{Entries: []capability.Entry{entry}}
	})

	payload := inspectCapability(t, tl, "tool:heartbeat_task_upsert")

	// Prose layer preserved (byte-identical description, no replacement).
	if payload["description"] != entry.Description {
		t.Fatalf("prose layer changed: %q", payload["description"])
	}

	// Machine layer is the engine schema itself, not a hand-written copy.
	var rawSchema map[string]any
	if err := json.Unmarshal(target.Schema(), &rawSchema); err != nil {
		t.Fatal(err)
	}
	gotSchema, ok := payload["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema missing from inspect payload: %s", payload)
	}
	if !reflect.DeepEqual(gotSchema, rawSchema) {
		t.Fatalf("input_schema drifted from the engine definition")
	}
	if payload["schema_fingerprint"] != tool.SchemaFingerprint(target.Schema()) {
		t.Fatalf("schema_fingerprint does not match the engine schema")
	}

	// The per-parameter table is derived from the same schema: every property
	// appears exactly once, with required/enum facts matching the definition.
	rows, ok := payload["parameters"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("parameters table missing: %s", payload)
	}
	properties := rawSchema["properties"].(map[string]any)
	if len(rows) != len(properties) {
		t.Fatalf("parameters rows = %d, engine properties = %d", len(rows), len(properties))
	}
	required := map[string]bool{}
	for _, r := range rawSchema["required"].([]any) {
		required[r.(string)] = true
	}
	seen := map[string]bool{}
	for _, rowAny := range rows {
		row := rowAny.(map[string]any)
		name := row["name"].(string)
		if !seen[name] {
			seen[name] = true
		} else {
			t.Fatalf("duplicate parameter row %q", name)
		}
		property := properties[name]
		if property == nil {
			t.Fatalf("parameter row %q has no engine property", name)
		}
		propertyMap := property.(map[string]any)
		if row["type"] != propertyMap["type"] {
			t.Fatalf("parameter %q type = %v, engine %v", name, row["type"], propertyMap["type"])
		}
		if row["required"] != required[name] {
			t.Fatalf("parameter %q required = %v, engine %v", name, row["required"], required[name])
		}
		if engineEnum, hasEnum := propertyMap["enum"]; hasEnum {
			if !reflect.DeepEqual(row["enum"], engineEnum) {
				t.Fatalf("parameter %q enum = %v, engine %v", name, row["enum"], engineEnum)
			}
		}
	}
	if !seen["expected_revision"] || !required["expected_revision"] {
		t.Fatalf("expected_revision row missing or not required: rows=%v", seen)
	}
	if scopeRow := find1438Row(t, rows, "scope"); !reflect.DeepEqual(scopeRow["enum"], []any{"", "global", "project"}) {
		t.Fatalf("scope enum = %v", scopeRow["enum"])
	}
}

func find1438Row(t *testing.T, rows []any, name string) map[string]any {
	t.Helper()
	for _, rowAny := range rows {
		row := rowAny.(map[string]any)
		if row["name"] == name {
			return row
		}
	}
	t.Fatalf("parameter row %q missing", name)
	return nil
}

// Test1438InspectToolWithoutParameterSchemaIsLabelled: a tool whose engine
// schema declares no properties gets an explicit note — never fabricated
// parameter rows or an empty-schema passthrough.
func Test1438InspectToolWithoutParameterSchemaIsLabelled(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(noParamSchemaTool{})
	entry := capability.Entry{
		ID: "tool:no_param", Kind: capability.KindTool, Name: "no_param",
		Description: "a tool with a bare object schema", Status: capability.StatusReady,
		ToolName: "no_param",
	}
	tl := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, func() capability.Catalog {
		return capability.Catalog{Entries: []capability.Entry{entry}}
	})
	payload := inspectCapability(t, tl, "tool:no_param")
	if _, has := payload["input_schema"]; has {
		t.Fatalf("input_schema must stay absent for a property-less schema: %s", payload)
	}
	if _, has := payload["parameters"]; has {
		t.Fatalf("parameters must stay absent for a property-less schema: %s", payload)
	}
	note, _ := payload["schema_note"].(string)
	if !strings.Contains(note, "declares no per-parameter schema") {
		t.Fatalf("schema_note = %q, want the no-schema label", note)
	}
	if payload["description"] != entry.Description {
		t.Fatalf("prose layer changed: %q", payload["description"])
	}
}

type noParamSchemaTool struct{}

func (noParamSchemaTool) Name() string            { return "no_param" }
func (noParamSchemaTool) Description() string     { return "bare object schema" }
func (noParamSchemaTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (noParamSchemaTool) ReadOnly() bool          { return true }
func (noParamSchemaTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}

// Test1438InspectSkillWithoutContractIsLabelled: a skill entry with no
// argument contract provider must say so instead of staying silent.
func Test1438InspectSkillWithoutContractIsLabelled(t *testing.T) {
	entry := capability.Entry{
		ID: "skill:orphan", Kind: capability.KindSkill, Name: "orphan",
		Description: "skill without a contract", Status: capability.StatusReady,
	}
	tl := NewUseCapabilityTool(context.Background(), nil, nil, tool.NewRegistry(), nil, nil, func() capability.Catalog {
		return capability.Catalog{Entries: []capability.Entry{entry}}
	})
	payload := inspectCapability(t, tl, "skill:orphan")
	note, _ := payload["schema_note"].(string)
	if !strings.Contains(note, "no argument contract is available for this skill") {
		t.Fatalf("schema_note = %q, want the skill no-contract label", note)
	}
}

// Test1438InspectMCPToolWithoutPropertiesIsLabelled: a cached MCP schema that
// is real but property-less is labelled, not passed through as if complete.
func Test1438InspectMCPToolWithoutPropertiesIsLabelled(t *testing.T) {
	t.Setenv("REASONIX_CACHE_HOME", t.TempDir())
	spec := plugin.Spec{Name: "bare", Authorized: true}
	if err := plugin.SaveCachedSchema(spec.Name, plugin.CachedSchema{
		CacheKey: plugin.SchemaCacheKey(spec),
		Tools: []plugin.CachedTool{
			{Name: "noop", Description: "no-parameter MCP tool", Schema: json.RawMessage(`{"type":"object"}`), ReadOnly: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	entry := capability.Entry{
		ID: "mcp-tool:bare/noop", Kind: capability.KindMCPTool, Name: "bare/noop",
		Description: "no-parameter MCP tool", Source: "bare", Status: capability.StatusConfigured,
	}
	tl := NewUseCapabilityTool(context.Background(), nil, []plugin.Spec{spec}, tool.NewRegistry(), nil, nil, func() capability.Catalog {
		return capability.Catalog{Entries: []capability.Entry{entry}}
	})
	payload := inspectCapability(t, tl, "mcp-tool:bare/noop")
	note, _ := payload["schema_note"].(string)
	if !strings.Contains(note, "declares no per-parameter properties") {
		t.Fatalf("schema_note = %q, want the MCP no-properties label", note)
	}
	if _, has := payload["parameters"]; has {
		t.Fatalf("parameters must stay absent for a property-less MCP schema: %s", payload)
	}
}
