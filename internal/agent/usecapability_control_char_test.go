package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

// Task 975: capability_id 混入控制字符（2026-10-18 用户截图，ai-passport 会话，
// 生成模型 mimo-v2.6-flash）—— capability_id "tool:talk\r_to\r_session" 带裸 \r
// 报 "not registered in this session"，同批 multi_edit 被连坐跳过。937 曾判
// 「疑纯存量」，本截图实锤为生成侧仍在发生的增量活形态；R1（728）只做层级提升
// 不做字符清洗，救不了此类。本文件锁定三层验收：
//  1. 带 \r/\n/\x00 的 id 清洗后命中注册表、端到端执行成功（Execute 复现）；
//  2. 清洗后仍非法（只有控制字符）→ 明确报错，非 panic、不误报 missing-id；
//  3. 728 层级提升容错零回归（既有测试文件不改，本文件补嵌套+清洗组合态）。

// TestSanitizeCapabilityID pins the helper contract: clean ids pass through
// byte-identical after the historical TrimSpace; control characters anywhere
// (embedded, leading, trailing) are removed; a control-only id cleans to "".
func TestSanitizeCapabilityID(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantID    string
		wantClean bool
	}{
		{name: "clean id unchanged", raw: "tool:talk_to_session", wantID: "tool:talk_to_session"},
		{name: "surrounding spaces trimmed like before", raw: "  tool:notify  ", wantID: "tool:notify"},
		{name: "embedded carriage returns (10-18 field form)", raw: "tool:talk\r_to\r_session", wantID: "tool:talk_to_session", wantClean: true},
		{name: "embedded newline and CR pair", raw: "tool:talk\r\n_to\r_session", wantID: "tool:talk_to_session", wantClean: true},
		{name: "embedded NUL bytes", raw: "tool:talk\x00_to\x00_session", wantID: "tool:talk_to_session", wantClean: true},
		{name: "embedded tab", raw: "tool:talk\t_to_session", wantID: "tool:talk_to_session", wantClean: true},
		{name: "leading and trailing controls", raw: "\rtool:notify\n", wantID: "tool:notify", wantClean: true},
		{name: "control-only id cleans to empty", raw: "\r\n\x00", wantID: "", wantClean: true},
		{name: "DEL also stripped", raw: "tool:not\x7fify", wantID: "tool:notify", wantClean: true},
		{name: "multi-byte utf-8 untouched", raw: "skill:审\r查", wantID: "skill:审查", wantClean: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, cleaned := sanitizeCapabilityID(test.raw)
			if got != test.wantID {
				t.Fatalf("sanitizeCapabilityID(%q) = %q, want %q", test.raw, got, test.wantID)
			}
			if cleaned != test.wantClean {
				t.Fatalf("cleaned = %v, want %v", cleaned, test.wantClean)
			}
		})
	}
}

// TestUseCapabilityExecuteStripsControlCharactersFromCapabilityID is the
// acceptance-1 reproduction: the 10-18 screenshot call (capability_id with
// embedded \r, \n and \x00 variants) resolves against the registry and the
// target actually executes — no "not registered" failure, no batch drag-down.
func TestUseCapabilityExecuteStripsControlCharactersFromCapabilityID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "embedded carriage returns (10-18 field form)", raw: `{"action":"call","capability_id":"tool:talk\r_to\r_session","arguments":{"message":"hi"}}`},
		{name: "embedded newline", raw: `{"action":"call","capability_id":"tool:talk\r\n_to\r_session","arguments":{"message":"hi"}}`},
		{name: "embedded NUL", raw: `{"action":"call","capability_id":"tool:talk\u0000_to\u0000_session","arguments":{"message":"hi"}}`},
		{name: "leading and trailing controls", raw: `{"action":"call","capability_id":"\rtool:talk_to_session\n","arguments":{"message":"hi"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reg := tool.NewRegistry()
			calls := 0
			reg.Add(readOnlyBoundaryTarget{name: "talk_to_session", readOnly: true, calls: &calls})
			audit := &capability.Audit{}
			proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

			out, err := proxy.Execute(t.Context(), json.RawMessage(test.raw))
			if err != nil {
				t.Fatalf("Execute(%s): %v", test.raw, err)
			}
			if !strings.Contains(out, "target executed") {
				t.Fatalf("output = %q, want the target's own result", out)
			}
			if calls != 1 {
				t.Fatalf("target calls = %d, want exactly 1", calls)
			}
			if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
				t.Fatalf("SelfHealed = %d, want 1 (the control-char cleanup stays observable)", got)
			}
		})
	}
}

// TestUseCapabilityCleanIDDoesNotCountSelfHeal guards the observable boundary
// the other way: an id without control characters must take the byte-identical
// pre-975 path — no audit bump, no rewrite.
func TestUseCapabilityCleanIDDoesNotCountSelfHeal(t *testing.T) {
	reg := tool.NewRegistry()
	calls := 0
	reg.Add(readOnlyBoundaryTarget{name: "talk_to_session", readOnly: true, calls: &calls})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	out, err := proxy.Execute(t.Context(), json.RawMessage(`{"action":"call","capability_id":"tool:talk_to_session","arguments":{"message":"hi"}}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "target executed") {
		t.Fatalf("output = %q, want the target's own result with no appended note", out)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 for a clean id", got)
	}
}

// TestUseCapabilityControlOnlyTopLevelIDStillPromotesNested pins the ordering
// between task 975 and task 728: a control-only top-level id must not block
// the nested-capability_id rescue — promotion runs first, the control-char
// error only names the failure when nothing rescued it.
func TestUseCapabilityControlOnlyTopLevelIDStillPromotesNested(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"\r\n",
		"arguments":{"capability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v (728 promotion must rescue a control-only top-level id)", err)
	}
	if resolved.CapabilityID != "tool:notify" {
		t.Fatalf("CapabilityID = %q, want tool:notify from the 728 promotion", resolved.CapabilityID)
	}
}

// TestUseCapabilityControlOnlyIDFailsClearly is acceptance 2: an id that is
// nothing but control characters cleans to empty and must fail with the
// control-character message — not a panic, and not the generic missing-id
// error that would blame the (correctly placed) top-level field.
func TestUseCapabilityControlOnlyIDFailsClearly(t *testing.T) {
	for _, action := range []string{"call", "inspect"} {
		t.Run(action, func(t *testing.T) {
			reg := tool.NewRegistry()
			reg.Add(spreadNotifyTool{})
			proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)

			raw := json.RawMessage(`{"action":"` + action + `","capability_id":"\r\n\u0000"}`)
			_, err := proxy.ResolveCall(t.Context(), raw)
			if err == nil {
				t.Fatalf("ResolveCall(%s) succeeded, want a clear control-character error", raw)
			}
			if !strings.Contains(err.Error(), "contained only control characters") {
				t.Fatalf("error = %v, want the control-character diagnosis", err)
			}
			// The coach must classify it self-explanatory: no misleading
			// search/hoist guidance appended.
			if guide := capabilityInputCorrection(err.Error(), raw); guide != "" {
				t.Fatalf("correction guide = %q, want none (self-explanatory)", guide)
			}
		})
	}
}

// TestUseCapabilityDirtyButUsableIDStillClearsControlOnlyBoundary documents
// the line between the two failure modes: a dirty id that cleans to a REAL id
// succeeds (acceptance 1), one that cleans to "" fails clearly (acceptance 2),
// and a dirty id that cleans to a NONSENSE id fails with the ordinary
// not-registered wording — the sanitize layer never invents validity.
func TestUseCapabilityDirtyIDFailureModes(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)

	// Dirty id that cleans to a wrong-but-well-formed name: the ordinary
	// unknown-tool unavailable path, no special treatment (unavailable caps
	// surface as a result string, not an error).
	out, err := proxy.Execute(t.Context(), json.RawMessage(`{"action":"call","capability_id":"tool:notifyX\r","arguments":{"message":"hi","to":"you"}}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "not registered") {
		t.Fatalf("output = %q, want the ordinary unknown-name wording", out)
	}
}

// TestUseCapabilityControlCharHealObservability pins the host-side discipline
// (task 457/798): the WARN log fires through noteArgumentSelfHeal and the
// audit counter increments — self-healing is never silent — while the model
// sees the plain successful result (no note text appended), because a
// tokenizer-emitted \r is not an envelope-shape mistake it can act on.
func TestUseCapabilityControlCharHealObservability(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{"action":"call","capability_id":"tool:notify\r","arguments":{"message":"hi","to":"you"}}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if resolved.CapabilityID != "tool:notify" {
		t.Fatalf("CapabilityID = %q, want the sanitized tool:notify", resolved.CapabilityID)
	}
	if resolved.SelfHealNote != "" {
		t.Fatalf("SelfHealNote = %q, want none (control-char cleanup is host-side only)", resolved.SelfHealNote)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestPromoteNestedCapabilityIDStripsControlCharacters keeps the 728
// promotion path consistent with the same sanitize helper: a nested id with
// embedded control characters still promotes instead of failing registry
// lookup one layer down.
func TestPromoteNestedCapabilityIDStripsControlCharacters(t *testing.T) {
	id, inner, ok := promoteNestedCapabilityID(json.RawMessage(`{"capability_id":"tool:notify\r","arguments":{"message":"hi","to":"you"}}`))
	if !ok {
		t.Fatal("promote did not fire for a dirty-but-usable nested id")
	}
	if id != "tool:notify" {
		t.Fatalf("promoted id = %q, want sanitized tool:notify", id)
	}
	if string(inner) != `{"message":"hi","to":"you"}` {
		t.Fatalf("inner = %s, want the nested target parameters", inner)
	}

	// A nested id that is all control characters never promotes — the same
	// missing-id fallback the 728 boundary tests pin.
	if _, _, ok := promoteNestedCapabilityID(json.RawMessage(`{"capability_id":"\r\n","arguments":{"message":"hi"}}`)); ok {
		t.Fatal("control-only nested id must not promote")
	}
}

// TestUseCapabilityDirtyNestedIDSelfHealsEndToEnd wires the nested cleanup
// into the full 728 flow: dirty nested id → promote → dispatch inner args,
// with exactly one self-heal counted.
func TestUseCapabilityDirtyNestedIDSelfHealsEndToEnd(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{"capability_id":"tool:not\rify","arguments":{"message":"hi","to":"you"}}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if resolved.CapabilityID != "tool:notify" {
		t.Fatalf("CapabilityID = %q, want the sanitized promoted id tool:notify", resolved.CapabilityID)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestClassifyCallStripsControlCharacters guards the batch classifier: the
// shared parser feeds ClassifyCall, so a dirty id must still classify as a
// known read-only parallel-safe call instead of degrading the whole parallel
// batch to serial (the 10-18 连坐 mechanism).
func TestClassifyCallStripsControlCharacters(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)

	class := proxy.ClassifyCall(json.RawMessage(`{"action":"call","capability_id":"tool:notify\r","arguments":{"message":"hi","to":"you"}}`))
	if !class.Known || !class.ReadOnly || !class.ParallelSafe {
		t.Fatalf("class = %+v, want known read-only parallel-safe (clean id path)", class)
	}
}

// TestDeclineCapabilityIDsStripsControlCharacters covers the batch decline
// surface: dirty ids clean to their ledger keys instead of marking phantom
// entries nothing can reconcile.
func TestDeclineCapabilityIDsStripsControlCharacters(t *testing.T) {
	got := declineCapabilityIDs("tool:a\r", []string{"tool:b\n", "tool:c", "tool:b\n"})
	want := []string{"tool:a", "tool:b", "tool:c"}
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// TestCapabilityIDFromToolCallStripsControlCharacters guards the ledger key:
// the post-execution bookkeeping must mark the SAME sanitized id the proxy
// resolved with, or the outcome lands in a phantom entry the gate never
// reconciles.
func TestCapabilityIDFromToolCallStripsControlCharacters(t *testing.T) {
	got := capabilityIDFromToolCall("use_capability", json.RawMessage(`{"action":"call","capability_id":"tool:talk\r_to\r_session"}`))
	if got != "tool:talk_to_session" {
		t.Fatalf("ledger key = %q, want sanitized tool:talk_to_session", got)
	}
}

// TestRestrictedCapabilityProxyCheckStripsControlCharacters guards the
// subagent allowlist boundary: a dirty id must match the allowed entry after
// cleaning instead of being rejected one layer above a resolver that would
// accept it.
func TestRestrictedCapabilityProxyCheckStripsControlCharacters(t *testing.T) {
	proxy := &restrictedCapabilityProxy{
		allowed: map[string]bool{"tool:notify": true},
	}
	if err := proxy.check(json.RawMessage(`{"action":"call","capability_id":"tool:not\rify","arguments":{"message":"hi","to":"you"}}`)); err != nil {
		t.Fatalf("check with dirty id: %v, want pass after sanitize", err)
	}
	if err := proxy.check(json.RawMessage(`{"action":"call","capability_id":"\r\n","arguments":{}}`)); err == nil {
		t.Fatal("check with control-only id passed, want capability_id required error")
	}
}
