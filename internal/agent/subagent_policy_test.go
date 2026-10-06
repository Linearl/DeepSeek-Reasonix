package agent

import (
	"strings"
	"testing"
)

func TestNormalizeSubagentPolicy(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want SubagentPolicy
		ok   bool
	}{
		{"", SubagentPolicyLight, true},
		{"light", SubagentPolicyLight, true},
		{"  LIGHT  ", SubagentPolicyLight, true},
		{"balanced", SubagentPolicyBalanced, true},
		{"aggressive", SubagentPolicyAggressive, true},
		{"full", SubagentPolicyAggressive, true},
		{"turbo", "", false},
		{"", SubagentPolicyLight, true},
	} {
		got, err := NormalizeSubagentPolicy(tc.in)
		if (err == nil) != tc.ok {
			t.Fatalf("NormalizeSubagentPolicy(%q) err=%v, want ok=%v", tc.in, err, tc.ok)
		}
		if err == nil && got != tc.want {
			t.Fatalf("NormalizeSubagentPolicy(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestSubagentPolicyGuidance(t *testing.T) {
	if got := SubagentPolicyGuidance(SubagentPolicyLight); got != "" {
		t.Fatalf("light must inject nothing, got %q", got)
	}
	for _, p := range []SubagentPolicy{SubagentPolicyBalanced, SubagentPolicyAggressive} {
		got := SubagentPolicyGuidance(p)
		if !strings.HasPrefix(got, "<subagent-policy>"+string(p)+"\n") {
			t.Fatalf("%s guidance must open with its own block tag", p)
		}
		// 并行度提示必须解耦（不在 prompt 层）
		if strings.Contains(got, "parallel") && p == SubagentPolicyBalanced {
			t.Fatalf("balanced must not guide parallelism")
		}
		if p == SubagentPolicyAggressive && !strings.Contains(got, "30 seconds") {
			t.Fatalf("aggressive must carry the cost thresholds")
		}
	}
}

// TestSubagentPolicyGuidancePositiveGuidance pins task 531: the guidance must
// carry the positive half of the delegation decision — when to dispatch, what
// dispatching buys, bounded costs — alongside the negative thresholds, so the
// model does not see only a "dispatching is not worth it" list. The
// aggressive tier additionally carries the write_paths hint (declaring
// non-overlapping write_paths unlocks parallel writers).
func TestSubagentPolicyGuidancePositiveGuidance(t *testing.T) {
	for _, p := range []SubagentPolicy{SubagentPolicyBalanced, SubagentPolicyAggressive} {
		got := SubagentPolicyGuidance(p)
		for _, want := range []string{
			"When to dispatch (a positive list", // 何时该派：正面场景清单
			"What dispatching buys",             // 派了得到什么：主上下文干净/上下文隔离
			"Dispatching is affordable",         // 成本可控：步数上限已是 max(parent*2/3,12)
			"two-thirds of the parent's cap (min 12)",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s guidance missing positive guidance %q:\n%s", p, want, got)
			}
		}
	}
	aggressive := SubagentPolicyGuidance(SubagentPolicyAggressive)
	// write_paths 正面引导：声明非重叠路径才解锁写并行；不声明则串行化全部写者
	for _, want := range []string{
		"unlock parallel writers",
		"omitting write_paths claims the whole workspace and serializes every writer behind it",
	} {
		if !strings.Contains(aggressive, want) {
			t.Fatalf("aggressive guidance missing write_paths unlock guidance %q:\n%s", want, aggressive)
		}
	}
	// balanced 维持 #9004 契约：不承诺并发/并行（调度器护栏，与派遣策略解耦）
	if balanced := SubagentPolicyGuidance(SubagentPolicyBalanced); strings.Contains(balanced, "parallel") {
		t.Fatalf("balanced positive guidance must stay free of parallelism claims:\n%s", balanced)
	}
}

// TestSubagentPolicyGuidanceStepCapWordingMatchesSchema is the drift guard for
// the task-531 positive line: the guidance quotes the default step cap in the
// same words as the task tool schemas, so the prompt and the mechanism cannot
// quietly disagree after either side changes.
func TestSubagentPolicyGuidanceStepCapWordingMatchesSchema(t *testing.T) {
	const capWording = "two-thirds of the parent's cap (min 12)"
	if got := SubagentPolicyGuidance(SubagentPolicyAggressive); !strings.Contains(got, capWording) {
		t.Fatalf("aggressive guidance must quote the step-cap default verbatim")
	}
	for _, tc := range []struct {
		name   string
		schema string
	}{
		{"task", string((&TaskTool{}).Schema())},
		{"read_only_task", string((&ReadOnlyTaskTool{}).Schema())},
	} {
		if !strings.Contains(tc.schema, capWording) {
			t.Fatalf("%s schema step-cap wording drifted from the guidance quote %q", tc.name, capWording)
		}
	}
}

func TestStripTransientUserBlocksSubagentPolicy(t *testing.T) {
	content := "<subagent-policy>balanced\nDelegation guidance for this turn:\n- Delegate ...\n</subagent-policy>\n\nuser question"
	stripped := StripTransientUserBlocks(content)
	if strings.Contains(stripped, "subagent-policy") || strings.Contains(stripped, "Delegation guidance") {
		t.Fatalf("transient block must be stripped, got %q", stripped)
	}
	if !strings.Contains(stripped, "user question") {
		t.Fatalf("user text must survive, got %q", stripped)
	}
}
