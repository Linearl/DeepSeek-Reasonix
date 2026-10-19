package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 996: capability_id 同族拼写别名（975 交付回板实测：ability_id 与
// capabilityId 均触发 "capability_id is required"）。与 728 层级救援同一
// 设计哲学（宽容入、严格出），但宽容面收敛为闭合清单——只有实测发现的两个
// 变体被接受，其余拼写错误照旧报 missing-id 并给纠错引导。本文件锁定四层
// 验收：
//  1. 两个实测变体在 parse 归一、Execute 端到端成功、SelfHealNote 提示拼写；
//  2. 规范键优先：canonical capability_id 存在且可用时别名不覆盖；
//  3. 清单收敛：非清单键（cap_id/abilityId 等）不被宽容，missing-id 站得住；
//  4. 975 的账本键与子代理 allowlist 两处独立解析与 resolver 一致（不产生
//     幽灵账本条目或上层拒收）。

// TestParseUseCapabilityArgsAliasNormalization pins the parse contract: the
// two observed variants normalize, the canonical key always wins, and the
// converged list does not leak to lookalike keys.
func TestParseUseCapabilityArgsAliasNormalization(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantID    string
		wantAlias string
	}{
		{
			name:      "ability_id variant (975 field form)",
			raw:       `{"action":"call","ability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
			wantID:    "tool:notify",
			wantAlias: "ability_id",
		},
		{
			name:      "capabilityId camelCase variant (975 field form)",
			raw:       `{"action":"call","capabilityId":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
			wantID:    "tool:notify",
			wantAlias: "capabilityId",
		},
		{
			name:   "canonical key unchanged, no alias heal",
			raw:    `{"action":"call","capability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
			wantID: "tool:notify",
		},
		{
			name:   "canonical wins when both present",
			raw:    `{"action":"call","capability_id":"tool:canonical","ability_id":"tool:alias","arguments":{}}`,
			wantID: "tool:canonical",
		},
		{
			name:   "canonical control-only falls back to alias (alias rescued, not missing)",
			raw:    `{"action":"call","capability_id":"\r\n","ability_id":"tool:notify","arguments":{}}`,
			wantID: "tool:notify", wantAlias: "ability_id",
		},
		{
			// 收敛边界：清单外的近似拼写一律不宽容——宽松解析若无限扩展会
			// 掩盖真实调用错误（任务书风险面）。
			name:   "lookalike cap_id not on the list",
			raw:    `{"action":"call","cap_id":"tool:notify","arguments":{}}`,
			wantID: "",
		},
		{
			name:   "lookalike abilityId casing not on the list",
			raw:    `{"action":"call","abilityId":"tool:notify","arguments":{}}`,
			wantID: "",
		},
		{
			name:   "lookalike capability_id_ variant not on the list",
			raw:    `{"action":"call","capability_id_":"tool:notify","arguments":{}}`,
			wantID: "",
		},
		{
			name:   "alias with empty value never fires",
			raw:    `{"action":"call","ability_id":"","arguments":{}}`,
			wantID: "",
		},
		{
			name:   "alias with non-string value never fires",
			raw:    `{"action":"call","ability_id":123,"arguments":{}}`,
			wantID: "",
		},
		{
			name:      "alias value is control-char cleaned like the canonical path",
			raw:       `{"action":"call","ability_id":"tool:not\rify","arguments":{}}`,
			wantID:    "tool:notify",
			wantAlias: "ability_id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, id, err := parseUseCapabilityArgs(json.RawMessage(test.raw))
			if err != nil {
				t.Fatalf("parse(%s): %v", test.raw, err)
			}
			if id != test.wantID {
				t.Fatalf("id = %q, want %q", id, test.wantID)
			}
			args, _, _, _ := parseUseCapabilityArgs(json.RawMessage(test.raw))
			if args.healedAliasKey != test.wantAlias {
				t.Fatalf("healedAliasKey = %q, want %q", args.healedAliasKey, test.wantAlias)
			}
		})
	}
}

// TestUseCapabilityAliasResolvesEndToEnd is acceptance 1: both observed
// variants dispatch the real target, stay observable through the audit
// counter, and append the model-visible spelling note so the next call uses
// capability_id.
func TestUseCapabilityAliasResolvesEndToEnd(t *testing.T) {
	tests := []struct {
		name, raw, alias string
	}{
		{name: "ability_id", raw: `{"action":"call","ability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}`, alias: "ability_id"},
		{name: "capabilityId", raw: `{"action":"call","capabilityId":"tool:notify","arguments":{"message":"hi","to":"you"}}`, alias: "capabilityId"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reg := tool.NewRegistry()
			calls := 0
			reg.Add(readOnlyBoundaryTarget{name: "notify", readOnly: true, calls: &calls})
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
				t.Fatalf("SelfHealed = %d, want 1 (the alias rescue stays observable)", got)
			}
			// 拼写提示必须到达模型（与 975 控制字符纯宿主侧相反：键名是模型
			// 可纠正的生成错误）。
			if want := test.alias + " was accepted as an alias of capability_id"; !strings.Contains(out, want) {
				t.Fatalf("output = %q, want the alias spelling note %q", out, want)
			}
		})
	}
}

// TestUseCapabilityCanonicalCallHasNoAliasNote guards the boundary the other
// way: a canonical capability_id call takes the byte-identical pre-996 path —
// no note, no audit bump.
func TestUseCapabilityCanonicalCallHasNoAliasNote(t *testing.T) {
	reg := tool.NewRegistry()
	calls := 0
	reg.Add(readOnlyBoundaryTarget{name: "notify", readOnly: true, calls: &calls})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	out, err := proxy.Execute(t.Context(), json.RawMessage(`{"action":"call","capability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "target executed") || strings.Contains(out, "alias of capability_id") {
		t.Fatalf("output = %q, want the clean result with no alias note", out)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 for a canonical call", got)
	}
}

// TestUseCapabilityAliasUnknownKeyStillRequiresID is acceptance 3: a
// lookalike key outside the converged list keeps the missing-id error and its
// correction guide — the tolerance never becomes silent acceptance of
// arbitrary spellings.
func TestUseCapabilityAliasUnknownKeyStillRequiresID(t *testing.T) {
	for _, raw := range []string{
		`{"action":"call","cap_id":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
		`{"action":"call","abilityId":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
	} {
		reg := tool.NewRegistry()
		reg.Add(spreadNotifyTool{})
		proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)

		_, err := proxy.ResolveCall(t.Context(), json.RawMessage(raw))
		if err == nil {
			t.Fatalf("ResolveCall(%s) succeeded, want the missing-id error (converged list)", raw)
		}
		if !strings.Contains(err.Error(), "capability_id is required") {
			t.Fatalf("error = %v, want the missing-id diagnosis", err)
		}
	}
}

// TestCapabilityIDFromToolCallAlias guards the ledger key (task 975 agreement
// discipline): an alias-rescued call must mark the id it actually dispatched,
// or the outcome lands in a phantom entry the gate never reconciles.
func TestCapabilityIDFromToolCallAlias(t *testing.T) {
	for _, raw := range []string{
		`{"action":"call","ability_id":"tool:notify"}`,
		`{"action":"call","capabilityId":"tool:notify"}`,
	} {
		if got := capabilityIDFromToolCall("use_capability", json.RawMessage(raw)); got != "tool:notify" {
			t.Fatalf("ledger key for %s = %q, want tool:notify", raw, got)
		}
	}
	// A lookalike key never produces a ledger key the resolver would not make.
	if got := capabilityIDFromToolCall("use_capability", json.RawMessage(`{"action":"call","cap_id":"tool:notify"}`)); got != "" {
		t.Fatalf("ledger key for cap_id = %q, want empty", got)
	}
}

// TestRestrictedCapabilityProxyCheckAlias guards the subagent allowlist: an
// alias call must match the allowed entry instead of being rejected one layer
// above a resolver that accepts it.
func TestRestrictedCapabilityProxyCheckAlias(t *testing.T) {
	proxy := &restrictedCapabilityProxy{allowed: map[string]bool{"tool:notify": true}}
	for _, raw := range []string{
		`{"action":"call","ability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
		`{"action":"call","capabilityId":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
	} {
		if err := proxy.check(json.RawMessage(raw)); err != nil {
			t.Fatalf("check(%s): %v, want pass after alias normalization", raw, err)
		}
	}
	// A lookalike outside the list is still rejected at the boundary.
	if err := proxy.check(json.RawMessage(`{"action":"call","cap_id":"tool:notify","arguments":{}}`)); err == nil {
		t.Fatal("check with cap_id passed, want capability_id required error")
	}
}

// TestUseCapabilityAliasKeysAreEnvelopeMembers pins the 457 spread-merge
// boundary: an alias key at the envelope top level is an envelope member, not
// a target parameter — it must not ride the spread merge into the target's
// argument object.
func TestUseCapabilityAliasKeysAreEnvelopeMembers(t *testing.T) {
	if !capabilityEnvelopeKeys["ability_id"] || !capabilityEnvelopeKeys["capabilityId"] {
		t.Fatal("alias keys must be envelope members so the spread merge never forwards them to the target")
	}
	if capabilityEnvelopeKeys["cap_id"] {
		t.Fatal("lookalike keys must not join the envelope set")
	}
}

// TestCanonicalCapabilityIDEnvelope pins the validation-path agreement: an
// envelope whose alias fired validates as the canonical envelope (so a SECOND
// input error never blames the accepted alias with "remove: ability_id"),
// while a lookalike key keeps its additionalProperties violation — the
// 明确报错 for non-converged spellings.
func TestCanonicalCapabilityIDEnvelope(t *testing.T) {
	proxy := &UseCapabilityTool{}

	// Alias fired → validation sees the renamed envelope: no violations.
	aliased := json.RawMessage(`{"action":"decline","ability_id":"tool:notify","reason":"r"}`)
	result := tool.ValidateArguments(proxy, canonicalCapabilityIDEnvelope(aliased))
	if result.CompileErr != nil || len(result.Violations) != 0 {
		t.Fatalf("alias envelope must validate as canonical, got %+v (compile %v)", result.Violations, result.CompileErr)
	}

	// Canonical already present → byte-identical passthrough.
	canonical := json.RawMessage(`{"action":"decline","capability_id":"tool:notify","reason":"r"}`)
	if got := string(canonicalCapabilityIDEnvelope(canonical)); got != string(canonical) {
		t.Fatalf("canonical envelope rewritten: %s", got)
	}

	// Lookalike key → additionalProperties violation stands (明确报错).
	lookalike := json.RawMessage(`{"action":"decline","cap_id":"tool:notify","reason":"r"}`)
	result = tool.ValidateArguments(proxy, canonicalCapabilityIDEnvelope(lookalike))
	if len(result.Violations) == 0 {
		t.Fatal("lookalike key must keep its additionalProperties violation")
	}
}

// TestAliasCallSecondErrorDoesNotBlameAlias is the end-to-end form of the
// validation-path agreement: an alias call that fails for a DIFFERENT reason
// (decline without reason) reports that reason — not "remove: ability_id",
// which would teach the model the opposite of the success-path alias note.
func TestAliasCallSecondErrorDoesNotBlameAlias(t *testing.T) {
	reg := tool.NewRegistry()
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "1", Name: "use_capability",
		Arguments: `{"action":"decline","ability_id":"tool:notify"}`,
	})
	if !strings.Contains(out.output, "reason is required for action=decline") {
		t.Fatalf("output = %q, want the decline-reason diagnosis", out.output)
	}
	if strings.Contains(out.output, "remove: ability_id") {
		t.Fatalf("output = %q, must not blame the accepted alias key", out.output)
	}
}
