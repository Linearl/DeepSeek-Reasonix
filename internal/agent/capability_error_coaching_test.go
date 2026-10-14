package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// memoryShapedTool 是 8 型族测试共用的目标：名字贴近记忆工具真名族
// （tool:remember/tool:forget/tool:memory），schema 简单可预测。
type memoryShapedTool struct{ name string }

func (t *memoryShapedTool) Name() string        { return t.name }
func (t *memoryShapedTool) Description() string { return "stores a fact" }
func (t *memoryShapedTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"description":{"type":"string"},"body":{"type":"string"}},"required":["description","body"],"additionalProperties":false}`)
}
func (*memoryShapedTool) ReadOnly() bool { return false }
func (*memoryShapedTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return `{"saved":true}`, nil
}

func coachingProxyWithMemoryTools(t *testing.T) (*UseCapabilityTool, *Agent) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(&memoryShapedTool{name: "remember"})
	reg.Add(&memoryShapedTool{name: "forget"})
	reg.Add(&memoryShapedTool{name: "memory"})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	return proxy, a
}

// TestClassifyCapabilityInputErrorCoversEightFamilies pins the 8 型族识别表：
// 每族至少一个真实触发形状被归入正确的 kind，自解释错误不追加引导。
func TestClassifyCapabilityInputErrorCoversEightFamilies(t *testing.T) {
	tests := []struct {
		name string
		err  string
		raw  string
		want capabilityErrorKind
	}{
		{
			name: "1_missing_id",
			err:  "capability_id is required for action=call; capability_id must be a top-level field, not inside arguments",
			raw:  `{"action":"call","arguments":{"message":"hi","to":"you"}}`,
			want: capabilityErrMissingID,
		},
		{
			name: "2_nested_id",
			err:  "capability_id is required for action=call",
			raw:  `{"action":"call","arguments":{"capability_id":"tool:remember","arguments":{"wrong":"x"}}}`,
			want: capabilityErrNestedID,
		},
		{
			name: "3_wrong_name",
			err:  `tool "remerber" is not registered in this session`,
			raw:  `{"action":"call","capability_id":"tool:remerber","arguments":{"description":"d","body":"b"}}`,
			want: capabilityErrWrongName,
		},
		{
			name: "4_unshaped_id",
			err:  "action=call needs a shaped capability id: local/builtin tools use tool:<name>",
			raw:  `{"action":"call","capability_id":"remember","arguments":{"description":"d","body":"b"}}`,
			want: capabilityErrUnshapedID,
		},
		{
			name: "5_stringified_args",
			err:  "argument validation failed",
			raw:  `{"action":"call","capability_id":"tool:remember","arguments":"{\"description\":\"d\",\"body\":\"b\"}"}`,
			want: capabilityErrStringifiedArgs,
		},
		{
			name: "6_multiline_json",
			err:  "argument validation failed",
			raw:  "{\n\t\"action\":\"call\",\n\t\"capability_id\":\"tool:remember\",\n\t\"arguments\":\"{\\n  \\\"description\\\": \\\"d\\\"\\n}\"\n}",
			want: capabilityErrMultilineJSON,
		},
		{
			name: "7_double_envelope",
			err:  "argument validation failed",
			raw:  `{"action":"call","capability_id":"tool:remember","arguments":{"action":"call","capability_id":"tool:remember","arguments":{"description":7,"body":"b"}}}`,
			want: capabilityErrDoubleEnvelope,
		},
		{
			name: "8_spread_params",
			err:  "argument validation failed",
			raw:  `{"action":"call","capability_id":"tool:remember","description":"d"}`,
			want: capabilityErrSpreadParams,
		},
		{
			name: "self_explanatory_decline_reason",
			err:  "reason is required for action=decline",
			raw:  `{"action":"decline","capability_id":"tool:remember"}`,
			want: capabilityErrSelfExplanatory,
		},
		{
			name: "self_explanatory_limit",
			err:  "limit must be between 1 and 8 when provided",
			raw:  `{"action":"search","query":"x","limit":99}`,
			want: capabilityErrSelfExplanatory,
		},
		{
			name: "undiagnosed_with_id",
			err:  "an opaque resolver failure",
			raw:  `{"action":"call","capability_id":"tool:remember","arguments":{"description":"d","body":"b"}}`,
			want: capabilityErrUndiagnosed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyCapabilityInputError(test.err, json.RawMessage(test.raw)); got != test.want {
				t.Fatalf("kind = %q, want %q", got, test.want)
			}
		})
	}
}

// TestCoachingCorrectionGivesCopyableExamples 断言每个已定诊错族返回可复制
// 修正例（直接给改好的参数形状，不指向文档），且不回显任何参数值。
func TestCoachingCorrectionGivesCopyableExamples(t *testing.T) {
	tests := []struct {
		name   string
		err    string
		raw    string
		want   []string
		forbid []string
	}{
		{
			name: "missing_id_names_top_level_and_shape",
			err:  "capability_id is required for action=call; capability_id must be a top-level field, not inside arguments",
			raw:  `{"action":"call","arguments":{"message":"hi"}}`,
			want: []string{
				`{"action":"call","capability_id":`,
				"TOP-LEVEL field",
				`{"action":"search","query":"<topic>","limit":5}`,
			},
			forbid: []string{"hi"},
		},
		{
			name: "nested_id_hoists_actual_id",
			err:  "capability_id is required for action=call; capability_id must be a top-level field, not inside arguments",
			raw:  `{"action":"call","arguments":{"capability_id":"tool:remember","arguments":{"wrong":"x"}}}`,
			want: []string{
				"INSIDE arguments",
				`"capability_id":"tool:remember"`,
			},
			forbid: []string{"wrong"},
		},
		{
			name: "stringified_args_demands_object",
			raw:  `{"action":"call","capability_id":"tool:remember","arguments":"{\"description\":\"d\",\"body\":\"b\"}"}`,
			want: []string{
				"JSON string, not an object",
				"single-line compact JSON object",
			},
			forbid: nil,
		},
		{
			name: "multiline_json_demands_single_line",
			raw:  "{\"action\":\"call\",\"capability_id\":\"tool:remember\",\"arguments\":\"{\\n  \\\"description\\\": \\\"d\\\"\\n}\"}",
			want: []string{
				"JSON string, not an object",
				"Remove the newlines/indentation",
			},
			forbid: nil,
		},
		{
			name: "double_envelope_points_to_inner",
			raw:  `{"action":"call","capability_id":"tool:remember","arguments":{"action":"call","capability_id":"tool:remember","arguments":{"description":7,"body":"b"}}}`,
			want: []string{
				"double envelope",
				`"arguments":{ ...the inner object's members... }`,
			},
			forbid: nil,
		},
		{
			name: "spread_params_packs_keys",
			raw:  `{"action":"call","capability_id":"tool:remember","description":"d","body":"b"}`,
			want: []string{
				"[body, description]",
				`"arguments":{"body":"…","description":"…"}`,
			},
			forbid: []string{`"d"`, `"b"`},
		},
		{
			name: "undiagnosed_gives_search指引",
			raw:  `{"action":"call","capability_id":"tool:remember","arguments":{"description":"d","body":"b"}}`,
			want: []string{
				`{"action":"search","query":"<keywords>","limit":5}`,
				"action=inspect",
			},
			forbid: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guide := capabilityInputCorrection(test.err, json.RawMessage(test.raw))
			if guide == "" {
				t.Fatal("empty correction guidance")
			}
			for _, want := range test.want {
				if !strings.Contains(guide, want) {
					t.Fatalf("guidance missing %q:\n%s", want, guide)
				}
			}
			for _, forbid := range test.forbid {
				if strings.Contains(guide, forbid) {
					t.Fatalf("guidance leaked value %q:\n%s", forbid, guide)
				}
			}
		})
	}
}

// TestCoachingSelfExplanatoryAndProxyExitsStaySilent：自解释错误与代理出口族
// （真名错/裸名——引导已挂在代理出口）在报错分类器面不追加，避免双重引导。
func TestCoachingSelfExplanatoryAndProxyExitsStaySilent(t *testing.T) {
	for _, tc := range []struct{ name, err, raw string }{
		{"decline_reason", "reason is required for action=decline", `{"action":"decline","capability_id":"tool:remember"}`},
		{"limit", "limit must be between 1 and 8 when provided", `{"action":"search","query":"x","limit":99}`},
		{"unknown_action", `unknown action "clal"; use list, search, inspect, call, or decline`, `{"action":"clal"}`},
		{"wrong_name", `tool "remerber" is not registered in this session`, `{"action":"call","capability_id":"tool:remerber"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := capabilityInputCorrection(tc.err, json.RawMessage(tc.raw)); got != "" {
				t.Fatalf("guidance should be empty, got:\n%s", got)
			}
		})
	}
}

// TestWrongCapabilityNameSuggestsClosestCatalogTrueName 是合并线 924 家族的
// 初始触发形状：capability_id 拼写错（tool:remerber）。文案必须含最接近的
// catalog 真名（tool:remember）、可复制重试例与 search 占引用。
func TestWrongCapabilityNameSuggestsClosestCatalogTrueName(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "typo-1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"tool:remerber","arguments":{"description":"d","body":"b"}}`,
	})
	for _, want := range []string{
		`capability unavailable: tool "remerber" is not registered in this session`,
		`Did you mean "tool:remember"?`,
		`Retry with capability_id="tool:remember"`,
		`{"action":"call","capability_id":"tool:remember"`,
		`{"action":"search","query":"remerber","limit":5}`,
	} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("output missing %q:\n%s", want, out.output)
		}
	}
	// errMsg 只留首行（原错误行），引导不污染一行摘要。
	if !strings.Contains(out.errMsg, "not registered in this session") {
		t.Fatalf("errMsg should keep the original first line, got %q", out.errMsg)
	}
	if strings.Contains(out.errMsg, "Did you mean") {
		t.Fatalf("errMsg must not carry the guidance line, got %q", out.errMsg)
	}

	// 下一轮自愈：模型照提示改用真名重发，即成功，无需人工干预。
	healed := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "typo-2", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"tool:remember","arguments":{"description":"d","body":"b"}}`,
	})
	if healed.errMsg != "" || !strings.Contains(healed.output, `{"saved":true}`) {
		t.Fatalf("next-round self-heal failed: %+v output=%s", healed, healed.output)
	}
}

// TestUnshapedBareNameSuggestsNamespacedTrueName：裸名（漏 tool: 前缀）是最
// 高频的 unshaped_id 形态，本地名完全相等即应命中真名建议。
func TestUnshapedBareNameSuggestsNamespacedTrueName(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "bare-1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"remember","arguments":{"description":"d","body":"b"}}`,
	})
	for _, want := range []string{
		"needs a shaped capability id",
		`Did you mean "tool:remember"?`,
	} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("output missing %q:\n%s", want, out.output)
		}
	}
}

// TestInspectUnknownIDSuggestsClosestCatalogName：inspect 的 unknown id 同样
// 收到真名建议（与 call 出口共用同一引导函数）。
func TestInspectUnknownIDSuggestsClosestCatalogName(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "insp-1", Name: "use_capability",
		Arguments: `{"action":"inspect","capability_id":"tool:remerber"}`,
	})
	for _, want := range []string{
		`unknown capability_id "tool:remerber"`,
		`Did you mean "tool:remember"?`,
	} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("output missing %q:\n%s", want, out.output)
		}
	}
}

// TestClosestCapabilityNamesMatching：近名匹配的边界——本地名全等（命名空间
// 错）距离 0 优先；编辑距离受容差约束；无关名不推荐。
func TestClosestCapabilityNamesMatching(t *testing.T) {
	proxy, _ := coachingProxyWithMemoryTools(t)
	tests := []struct {
		name      string
		wrong     string
		wantFirst string
		wantAny   []string
	}{
		{"typo_edit_distance", "tool:remerber", "tool:remember", nil},
		{"namespace_wrong_local_equal", "memory:remember", "tool:remember", nil},
		{"bare_name", "rember", "tool:remember", nil},
		{"unrelated_stays_quiet", "tool:completelyunrelatedthing", "", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := proxy.closestCapabilityNames(test.wrong, 3)
			if test.wantFirst == "" {
				if len(got) != 0 {
					t.Fatalf("candidates = %v, want none", got)
				}
				return
			}
			if len(got) == 0 || got[0] != test.wantFirst {
				t.Fatalf("candidates = %v, want first %q", got, test.wantFirst)
			}
			for _, want := range test.wantAny {
				if !containsString(got, want) {
					t.Fatalf("candidates %v missing %q", got, want)
				}
			}
		})
	}
}

// TestSpreadParamsFailureGuidesPacking：spread 自愈失败（键名错）后，gate 报
// required 违规时必须点名被丢在顶层的参数并给打包修正例。
func TestSpreadParamsFailureGuidesPacking(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "spread-1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"tool:remember","descrption":"d","body":"b"}`,
	})
	if out.errMsg == "" {
		t.Fatalf("expected a validation failure, got output=%s", out.output)
	}
	for _, want := range []string{
		"The target parameters [body, descrption] were spread across the envelope top level",
		`"arguments":{"body":"…","descrption":"…"}`,
	} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("output missing %q:\n%s", want, out.output)
		}
	}
	if strings.Contains(out.output, `"d"`) || strings.Contains(out.output, `"b"`) {
		t.Fatalf("guidance leaked argument values:\n%s", out.output)
	}
}

// TestDoubleEnvelopeFailureAddsCopyableFix：双包自愈失败（inner 也违规）后，
// 既有提示之外补可复制修正例。
func TestDoubleEnvelopeFailureAddsCopyableFix(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "dbl-1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"tool:remember","arguments":{"action":"call","capability_id":"tool:remember","arguments":{"description":7,"body":"b"}}}`,
	})
	if out.errMsg == "" {
		t.Fatalf("expected a validation failure, got output=%s", out.output)
	}
	for _, want := range []string{
		"Double-enveloped call detected",
		`Copyable fix: {"action":"call","capability_id":"tool:remember"`,
	} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("output missing %q:\n%s", want, out.output)
		}
	}
}

// TestStringifiedArgumentsGuidesSingleLine：tool: 目标不走 MCP 归一化，字符串
// 化 arguments 落到校验面——引导必须要求单行紧凑 JSON 对象并给修正例。
func TestStringifiedArgumentsGuidesSingleLine(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "str-1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"tool:remember","arguments":"{\"description\":\"d\",\"body\":\"b\"}"}`,
	})
	if out.errMsg == "" {
		t.Fatalf("expected a validation failure, got output=%s", out.output)
	}
	for _, want := range []string{
		`Actual arguments received: a JSON string wrapping an object with top-level keys [body, description]`,
		"single-line compact JSON OBJECT",
		`"capability_id":"tool:remember"`,
	} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("output missing %q:\n%s", want, out.output)
		}
	}
}

// TestMissingIDInputErrorAddsCorrectionGuide：missing_id 报错面（diagnose 入口）
// 追加顶层放置修正例与 search 指引，keys 摘要保持在前。
func TestMissingIDInputErrorAddsCorrectionGuide(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "miss-1", Name: "use_capability",
		Arguments: `{"action":"call","arguments":{"description":"d","body":"b"}}`,
	})
	for _, want := range []string{
		"capability_id is required for action=call",
		"an object with top-level keys [action, arguments]",
		`Corrected call (single line): {"action":"call","capability_id":`,
		"TOP-LEVEL field",
		`{"action":"search","query":"<topic>","limit":5}`,
	} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("output missing %q:\n%s", want, out.output)
		}
	}
	if strings.Contains(out.output, `"d"`) {
		t.Fatalf("guidance leaked argument values:\n%s", out.output)
	}
}

// TestSuccessPathCarriesNoCoachingGuidance 验收②：成功路径行为零变化——正确
// 封装的调用结果不含任何纠错引导文本。
func TestSuccessPathCarriesNoCoachingGuidance(t *testing.T) {
	_, a := coachingProxyWithMemoryTools(t)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "ok-1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"tool:remember","arguments":{"description":"d","body":"b"}}`,
	})
	if out.errMsg != "" {
		t.Fatalf("valid call failed: %+v", out)
	}
	if !strings.HasPrefix(out.output, `{"saved":true}`) {
		t.Fatalf("output = %q, want the plain target result", out.output)
	}
	for _, noise := range []string{"Did you mean", "Corrected call", "single-line", "Copyable fix", "action=search"} {
		if strings.Contains(out.output, noise) {
			t.Fatalf("success path leaked coaching %q:\n%s", noise, out.output)
		}
	}
}
