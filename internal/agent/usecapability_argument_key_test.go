package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

// tl1310: use_capability arguments 系统性混入 \r（2026-10-31 ai-passport 会话
// 实测转达）——capability_id 已由 task 975 清洗，但目标参数 KEY 名没有：
// "expected_revision" 以 "expected\r_revision" 抵达，schema 门禁按
// required-missing/unknown-field 拒绝，模型无从诊断，同类调用重试 5+ 轮。
// 本文件锁定三层验收：
//  1. sanitizeArgumentKeys 帮助函数契约：干净载荷逐字节透传（含值内合法
//     \n/\r 转义的长 prompt）；脏键任意深度清洗；只改键名不改值；
//  2. 端到端：脏键 arguments 通过 ResolveCall 后抵达 schema 门禁即合法，
//     目标工具收到清洗后的键，audit 计一次 self-heal，模型侧无 note；
//  3. 容错路径组合态不回归：spread 顶层脏键、stringified / 双层信封的
//     arguments 脏键同样清洗；干净载荷零 heal 零改写。

// keySanitizeUpsertTool mirrors the observed heartbeat_task_upsert target:
// two flat parameters with a strict schema, recording what it received so the
// test can pin the exact dispatched arguments.
type keySanitizeUpsertTool struct {
	received string
	calls    *int
}

func (t *keySanitizeUpsertTool) Name() string        { return "heartbeat_task_upsert" }
func (t *keySanitizeUpsertTool) Description() string { return "upserts a heartbeat task" }
func (t *keySanitizeUpsertTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"expected_revision":{"type":"integer"}},"required":["name","expected_revision"],"additionalProperties":false}`)
}
func (t *keySanitizeUpsertTool) ReadOnly() bool { return true }
func (t *keySanitizeUpsertTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	t.received = string(args)
	if t.calls != nil {
		(*t.calls)++
	}
	return "upserted", nil
}

// keySanitizeNestedTool gates one parameter behind a nested required key, so
// second-level key pollution has a schema-visible failure mode.
type keySanitizeNestedTool struct{}

func (keySanitizeNestedTool) Name() string        { return "nested_opts" }
func (keySanitizeNestedTool) Description() string { return "takes nested options" }
func (keySanitizeNestedTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"opts":{"type":"object","properties":{"expected_revision":{"type":"integer"}},"required":["expected_revision"],"additionalProperties":false}},"required":["opts"],"additionalProperties":false}`)
}
func (keySanitizeNestedTool) ReadOnly() bool { return true }
func (keySanitizeNestedTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "nested executed", nil
}

func newKeySanitizeRegistry(target tool.Tool) *tool.Registry {
	reg := tool.NewRegistry()
	reg.Add(target)
	return reg
}

// TestSanitizeArgumentKeys pins the helper contract (acceptance 1): clean
// payloads pass through byte-identical with count 0 — including payloads whose
// VALUES carry legitimate \n/\r escapes in long prompt strings — while dirty
// keys are cleaned at every depth without touching any value bytes.
func TestSanitizeArgumentKeys(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		want      string // "" means byte-identical passthrough expected
		wantCount int
	}{
		{name: "clean object unchanged", raw: `{"name":"hb","expected_revision":3}`},
		{
			name:      "top-level dirty key (10-31 field form)",
			raw:       `{"name":"hb","expected\r_revision":3}`,
			want:      `{"expected_revision":3,"name":"hb"}`,
			wantCount: 1,
		},
		{
			name:      "nested dirty key",
			raw:       `{"opts":{"expected\r_revision":3}}`,
			want:      `{"opts":{"expected_revision":3}}`,
			wantCount: 1,
		},
		{
			name:      "dirty key inside array element",
			raw:       `{"items":[{"expected\r_revision":1},{"name":"x"}]}`,
			want:      `{"items":[{"expected_revision":1},{"name":"x"}]}`,
			wantCount: 1,
		},
		{
			name:      "two dirty keys counted separately",
			raw:       `{"expected\r_revision":1,"capabilit\ry_id":"x"}`,
			want:      `{"capability_id":"x","expected_revision":1}`,
			wantCount: 2,
		},
		{
			name:      "value escapes preserved while key cleaned",
			raw:       `{"prompt":"line1\r\nline2","expected\r_revision":3}`,
			want:      `{"expected_revision":3,"prompt":"line1\r\nline2"}`,
			wantCount: 1,
		},
		{
			name:      "value with real newline escapes only, clean keys: passthrough",
			raw:       `{"prompt":"a\nb\r\nc"}`,
			wantCount: 0,
		},
		{
			name:      "DEL escape in key stripped",
			raw:       `{"expected\u007f_revision":1}`,
			want:      `{"expected_revision":1}`,
			wantCount: 1,
		},
		{name: "multi-byte utf-8 key untouched", raw: `{"任务\r名":"x"}`, want: `{"任务名":"x"}`, wantCount: 1},
		{name: "json string arguments untouched", raw: `"{\"expected\r_revision\":1}"`},
		{name: "array of scalars untouched", raw: `[1,2,3]`},
		{name: "empty object untouched", raw: `{}`},
		{name: "null untouched", raw: `null`},
		{name: "empty untouched", raw: ``},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, count := sanitizeArgumentKeys(json.RawMessage(test.raw))
			if count != test.wantCount {
				t.Fatalf("count = %d, want %d", count, test.wantCount)
			}
			if test.want == "" {
				if string(got) != test.raw {
					t.Fatalf("clean payload must pass through byte-identical, got %s want %s", got, test.raw)
				}
				return
			}
			if string(got) != test.want {
				t.Fatalf("sanitizeArgumentKeys(%s) = %s, want %s", test.raw, got, test.want)
			}
		})
	}
}

// TestSanitizeArgumentKeysNeverRewritesValues guards the one-way boundary: a
// payload with dirty keys keeps every value byte-for-byte, including escaped
// control sequences that belong to the VALUE (prompt text), so the target
// receives the model's actual content.
func TestSanitizeArgumentKeysNeverRewritesValues(t *testing.T) {
	raw := json.RawMessage(`{"prompt":"第一行\r\n第二行 end","expected\r_revision":3}`)
	got, count := sanitizeArgumentKeys(raw)
	if count != 1 {
		t.Fatalf("count = %d, want 1 (only the key)", count)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decoded: %v", err)
	}
	var prompt string
	if err := json.Unmarshal(decoded["prompt"], &prompt); err != nil {
		t.Fatalf("prompt value: %v", err)
	}
	if prompt != "第一行\r\n第二行 end" {
		t.Fatalf("prompt value rewritten: %q", prompt)
	}
	if _, ok := decoded["expected_revision"]; !ok {
		t.Fatalf("key not cleaned: %s", got)
	}
}

// TestUseCapabilityDirtyArgumentKeyDispatches is acceptance 2, the 10-31
// field reproduction: capability_id clean (975 already covers dirty ids) but
// the target parameter key polluted. The resolved arguments must pass the
// REAL dispatch gate (ValidateArguments) and the target must receive the
// cleaned key — previously this shape failed schema validation and fed the
// 5+ retry loop.
func TestUseCapabilityDirtyArgumentKeyDispatches(t *testing.T) {
	raw := json.RawMessage(`{"action":"call","capability_id":"tool:heartbeat_task_upsert","arguments":{"name":"hb","expected\r_revision":3}}`)
	// Phase 1: resolver-level — the cleaned arguments pass the schema gate the
	// agent loop runs before execution; one self-heal counted, no model note.
	audit := &capability.Audit{}
	gateProxy := NewUseCapabilityTool(context.Background(), nil, nil, newKeySanitizeRegistry(&keySanitizeUpsertTool{}), nil, audit, nil)
	resolved, err := gateProxy.ResolveCall(t.Context(), raw)
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	result := tool.ValidateArguments(resolved.Target, resolved.Args)
	if result.Skipped || result.CompileErr != nil || len(result.Violations) > 0 {
		t.Fatalf("cleaned arguments must pass the schema gate: skipped=%v compile=%v violations=%+v args=%s",
			result.Skipped, result.CompileErr, result.Violations, resolved.Args)
	}
	if resolved.SelfHealNote != "" {
		t.Fatalf("SelfHealNote = %q, want none (key cleanup is host-side only)", resolved.SelfHealNote)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
	// Phase 2: end-to-end Execute against a fresh audit — the target receives
	// the cleaned key.
	target := &keySanitizeUpsertTool{calls: new(int)}
	execAudit := &capability.Audit{}
	execProxy := NewUseCapabilityTool(context.Background(), nil, nil, newKeySanitizeRegistry(target), nil, execAudit, nil)
	out, err := execProxy.Execute(t.Context(), raw)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "upserted") {
		t.Fatalf("output = %q, want the target's own result", out)
	}
	if target.received != `{"expected_revision":3,"name":"hb"}` {
		t.Fatalf("target received %s, want cleaned keys", target.received)
	}
	if got := execAudit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("Execute-phase SelfHealed = %d, want 1 (one heal per attempt)", got)
	}
}

// TestUseCapabilityCleanArgumentKeysDoNotCountSelfHeal guards the boundary the
// other way: keys without control characters take the byte-identical
// pre-tl1310 path — no audit bump, dispatched args untouched.
func TestUseCapabilityCleanArgumentKeysDoNotCountSelfHeal(t *testing.T) {
	target := &keySanitizeUpsertTool{}
	reg := tool.NewRegistry()
	reg.Add(target)
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	raw := json.RawMessage(`{"action":"call","capability_id":"tool:heartbeat_task_upsert","arguments":{"name":"hb","expected_revision":3,"prompt":"a\nb"}}`)
	resolved, err := proxy.ResolveCall(t.Context(), raw)
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"name":"hb","expected_revision":3,"prompt":"a\nb"}` {
		t.Fatalf("Args rewritten for a clean payload: %s", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 for clean keys", got)
	}
}

// TestUseCapabilityDirtyNestedArgumentKeyDispatches covers second-level key
// pollution against a nested schema gate — the same mechanism one level down.
func TestUseCapabilityDirtyNestedArgumentKeyDispatches(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(keySanitizeNestedTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{"action":"call","capability_id":"tool:nested_opts","arguments":{"opts":{"expected\r_revision":7}}}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"opts":{"expected_revision":7}}` {
		t.Fatalf("Args = %s, want the cleaned nested key", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestUseCapabilitySpreadDirtyKeysSelfHeal keeps the 457 spread merge on the
// same contract: envelope-foreign top-level keys carry the same pollution and
// must dispatch clean through mergedSpreadArguments.
func TestUseCapabilitySpreadDirtyKeysSelfHeal(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{"action":"call","capability_id":"tool:notify","mess\rage":"hi","t\ro":"you"}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want merged spread parameters with cleaned keys", resolved.Args)
	}
	// Two observable heals by design: the key cleanup at capture AND the 457
	// spread merge — each is a distinct host correction with its own WARN.
	if got := audit.Snapshot().Arguments.SelfHealed; got != 2 {
		t.Fatalf("SelfHealed = %d, want 2 (key cleanup + spread merge)", got)
	}
}

// TestParseStringifiedDirtyKeyArgumentsSelfHeal covers the tolerance-path
// composition at the parse layer, where the 457 stringified unwrap lives
// (mcp-tool ids only): a JSON-string arguments value whose decoded object
// carries keys polluted with a backslash-r escape sequence unwraps AND
// cleans in one parse. The outer string encodes the inner escape doubled
// so the inner JSON text stays valid after one decode (a raw control byte
// inside the inner text fails the 457 re-parse by design - that boundary
// predates tl1310).
func TestParseStringifiedDirtyKeyArgumentsSelfHeal(t *testing.T) {
	args, _, _, err := parseUseCapabilityArgs(json.RawMessage(`{"action":"call","capability_id":"mcp-tool:srv/tool","arguments":"{\"mess\\rage\":\"hi\",\"t\\ro\":\"you\"}"}`))
	if err != nil {
		t.Fatalf("parseUseCapabilityArgs: %v", err)
	}
	if string(args.Arguments) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Arguments = %s, want unwrapped arguments with cleaned keys", args.Arguments)
	}
	if !args.healedStringified {
		t.Fatal("healedStringified not recorded")
	}
	if args.healedArgumentKeys != 2 {
		t.Fatalf("healedArgumentKeys = %d, want 2", args.healedArgumentKeys)
	}
}

// TestParseDoubleEnvelopeDirtyKeyArgumentsSelfHeal covers the 212 path at the
// parse layer: a stringified full call envelope one level deep, its inner
// arguments carrying a backslash-r-polluted key (same escape layering).
func TestParseDoubleEnvelopeDirtyKeyArgumentsSelfHeal(t *testing.T) {
	args, _, _, err := parseUseCapabilityArgs(json.RawMessage(`{"action":"call","capability_id":"mcp-tool:srv/tool","arguments":"{\"action\":\"call\",\"capability_id\":\"mcp-tool:srv/tool\",\"arguments\":{\"mess\\rage\":\"hi\",\"t\\ro\":\"you\"}}"}`))
	if err != nil {
		t.Fatalf("parseUseCapabilityArgs: %v", err)
	}
	if string(args.Arguments) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Arguments = %s, want inner arguments with cleaned keys", args.Arguments)
	}
	if !args.healedDoubleEnvelope {
		t.Fatal("healedDoubleEnvelope not recorded")
	}
	if args.healedArgumentKeys != 2 {
		t.Fatalf("healedArgumentKeys = %d, want 2 (both inner keys are dirty)", args.healedArgumentKeys)
	}
}
