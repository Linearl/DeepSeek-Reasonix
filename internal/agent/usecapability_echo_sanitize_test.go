package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

// tl1553 钉组：双 \r 漏清洗缺口的防御补全——错误回显清洗（切断坏示范
// 自增强）+ 新实例（2026-11-11 ai-passport 线）双 \r capability_id 复现钉。
//
// 归因链（协调线 19:1x 四点之三）：清洗主链（task 975/tl1310）对单 \r、
// 双 \r 全形态无漏（stripControlCharacters 剥全部 C0+DEL，幂等）；缺口在
// 错误回显面——capability_error_coaching 族与 not registered 文案把模型
// 传入的污染 id 以 strconv.Quote/%q 形态回显，模型重试时看到自己的坏形态
// 被原样确认 → 模仿自增强 → 8 连败（纯 tokenizer 概率难解释连败）。
// 本文件锁四层验收：
//  1. capabilityNameCorrection：wrongID 入口清洗——剥 \r 后建议/回显只
//     出现干净形态，污染形态字面量（backslash-r 转义文本）零出现；
//  2. capabilityCorrectionFor/capabilityValidationCorrection：修正例里
//     回显的 id 一律清洗后 Quote（capabilityQuotedID）；
//  3. 新实例复现钉：capability_id 双 \r（heartbeat\r\r_task\r\r_upsert）
//     全链路 parse→resolve→dispatch 成功，清洗后 id 抵达 registry；
//  4. inspect 的 unknown capability_id 回显面同清洗。

// echoProbeTool occupies the registry with an unrelated name so the polluted
// id is genuinely not registered (the 877 wrong-name error path runs).
type echoProbeTool struct{}

func (echoProbeTool) Name() string        { return "unrelated_probe" }
func (echoProbeTool) Description() string { return "probe" }
func (echoProbeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (echoProbeTool) ReadOnly() bool                                               { return true }
func (echoProbeTool) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil }

// TestCapabilityNameCorrectionStripsPollution is acceptance 1: the correction
// text for a polluted wrong id must never carry the literal backslash-r
// escape form (the model would copy it back), with or without a suggestion hit.
func TestCapabilityNameCorrectionStripsPollution(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&keySanitizeUpsertTool{}) // 建议/dist=0 命中所需的 catalog 真名源
	reg.Add(echoProbeTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, &capability.Audit{}, nil)

	polluted := "tool:heartbeat\r\r_task\r\r_upsert" // 2026-11-11 field form
	guide := proxy.capabilityNameCorrection(polluted)
	if guide == "" {
		t.Fatal("correction guide must not be empty for a shaped wrong id")
	}
	if strings.Contains(guide, `\r`) {
		t.Fatalf("guide echoes the polluted escape form: %q", guide)
	}
	if strings.ContainsRune(strings.ReplaceAll(guide, `\r`, ""), '\r') {
		t.Fatalf("guide must not carry raw CR bytes: %q", guide)
	}

	// Prefix mistake with a clean local name: the dist=0 suggestion path must
	// fire and the suggested id must be the clean catalog form.
	prefixWrong := "skill:heartbeat_task_upsert"
	guide = proxy.capabilityNameCorrection(prefixWrong)
	if !strings.Contains(guide, `"tool:heartbeat_task_upsert"`) {
		t.Fatalf("local-name-equal suggestion must name the clean registry id, got: %q", guide)
	}
}

// TestCapabilityQuotedIDForms pins the helper contract (acceptance 2): clean
// ids pass through quoted unchanged, polluted ids are sanitized before
// quoting, control-only ids fall back to the escaped raw form.
func TestCapabilityQuotedIDForms(t *testing.T) {
	if got := capabilityQuotedID(""); got != `"<exact catalog id>"` {
		t.Fatalf("empty id = %q, want the placeholder", got)
	}
	if got := capabilityQuotedID("tool:talk_to_session"); got != `"tool:talk_to_session"` {
		t.Fatalf("clean id = %q, want plain quoted form", got)
	}
	if got := capabilityQuotedID("tool:heartbeat\r\r_task\r\r_upsert"); got != `"tool:heartbeat_task_upsert"` {
		t.Fatalf("polluted id = %q, want sanitized quoted form", got)
	}
	if got := capabilityQuotedID("\r\r"); got != `"\r\r"` {
		t.Fatalf("control-only id = %q, want the escaped raw fallback", got)
	}
}

// TestCapabilityCorrectionForEchoesCleanID keeps the envelope-shape families
// on the same contract: a polluted shape.id must surface sanitized in every
// correction example.
func TestCapabilityCorrectionForEchoesCleanID(t *testing.T) {
	shape := capabilityEnvelopeShape{id: "tool:heartbeat\r\r_task\r\r_upsert", action: "call"}
	for _, kind := range []capabilityErrorKind{
		capabilityErrMissingID,
		capabilityErrStringifiedArgs,
		capabilityErrDoubleEnvelope,
		capabilityErrSpreadParams,
	} {
		out := capabilityCorrectionFor(kind, shape)
		if out == "" {
			t.Fatalf("%s: correction must not be empty", kind)
		}
		if strings.Contains(out, `\r`) {
			t.Fatalf("%s echoes the polluted escape form: %q", kind, out)
		}
	}
	// 嵌套 id 同契约。
	nested := shape
	nested.id = ""
	nested.nestedID = "tool:heartbeat\r_task\r_upsert"
	if out := capabilityCorrectionFor(capabilityErrNestedID, nested); strings.Contains(out, `\r`) {
		t.Fatalf("nested echo polluted: %q", out)
	}
}

// TestResolveCallDoubleCRCapabilityIDDispatches is acceptance 3, the
// 2026-11-11 field reproduction: capability_id polluted with DOUBLE \r at the
// snake_case split points must resolve and dispatch — the cleanup layer strips
// all control-character forms, not just single \r (the "double \r slips
// through" hypothesis is disproven at this layer; the field failures came
// through the echo-reinforcement loop, not a cleanup gap).
func TestResolveCallDoubleCRCapabilityIDDispatches(t *testing.T) {
	target := &keySanitizeUpsertTool{calls: new(int)}
	reg := tool.NewRegistry()
	reg.Add(target)
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	raw := json.RawMessage(`{"action":"call","capability_id":"tool:heartbeat\r\r_task\r\r_upsert","arguments":{"name":"hb","expected_revision":3}}`)
	resolved, err := proxy.ResolveCall(t.Context(), raw)
	if err != nil {
		t.Fatalf("ResolveCall with double-CR capability_id: %v", err)
	}
	if resolved.CapabilityID != "tool:heartbeat_task_upsert" {
		t.Fatalf("CapabilityID = %q, want the sanitized form", resolved.CapabilityID)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1 (the id cleanup)", got)
	}
	// Execute against a fresh audit — each attempt counts its own heal.
	execAudit := &capability.Audit{}
	execProxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, execAudit, nil)
	out, err := execProxy.Execute(t.Context(), raw)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "upserted") {
		t.Fatalf("output = %q, want the target result", out)
	}
	if got := execAudit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("Execute-phase SelfHealed = %d, want 1 (one heal per attempt)", got)
	}
	// 单 \r 侥幸通过样例（2026-11-11 现场对照）零回退：同链路照常执行。
	single := json.RawMessage(`{"action":"call","capability_id":"tool:heartbeat\r_task\r_upsert","arguments":{"name":"hb","expected_revision":3}}`)
	if _, err := proxy.Execute(t.Context(), single); err != nil {
		t.Fatalf("single-CR form must still dispatch: %v", err)
	}
}

// TestInspectUnknownCapabilityIDEchoesClean is acceptance 4: the inspect miss
// path renders the unknown id sanitized (defense in depth — the parse layer
// already cleans; the echo surface must not reintroduce the polluted form).
func TestInspectUnknownCapabilityIDEchoesClean(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(echoProbeTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, &capability.Audit{}, nil)

	_, err := proxy.inspect(context.Background(), "tool:heartbeat\r\r_task\r\r_upsert")
	if err == nil {
		t.Fatal("unknown id must error")
	}
	msg := err.Error()
	if strings.Contains(msg, `\r`) {
		t.Fatalf("inspect error echoes the polluted escape form: %q", msg)
	}
	if !strings.Contains(msg, "tool:heartbeat_task_upsert") {
		t.Fatalf("inspect error must carry the sanitized id, got: %q", msg)
	}
}
