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

// TestSubagentPolicyAggressiveCoordinationContract pins task 820 (816 P0
// wording): the aggressive block must open with the coordinator role framing
// (borrowed from the Codex orchestrator template), must no longer carry the
// "keep quick, small tasks in the main session" clause that contradicted
// "delegation is the default strategy" inside the same block, and must tilt
// ambiguous calls toward dispatching instead.
func TestSubagentPolicyAggressiveCoordinationContract(t *testing.T) {
	got := SubagentPolicyGuidance(SubagentPolicyAggressive)

	const roleFraming = "Your only role for delegable sub-work is to coordinate: dispatch, then collect and verify"
	if !strings.Contains(got, roleFraming) {
		t.Fatalf("aggressive guidance missing the coordinator role framing:\n%s", got)
	}
	for _, stale := range []string{
		"Keep quick, small tasks", // 旧保守条款：与 default strategy 同块矛盾，已删
		"delegation overhead is not worth it",
	} {
		if strings.Contains(got, stale) {
			t.Fatalf("aggressive guidance still carries the contradictory clause %q:\n%s", stale, got)
		}
	}
	// 阈值保留但方向反转：命令式「不要内联」，含糊时单向倾斜到派发。
	for _, want := range []string{
		"Do NOT inline sub-work that meets ANY of these thresholds",
		"When in doubt, dispatch",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("aggressive guidance missing %q:\n%s", want, got)
		}
	}
	// 角色框定必须是第一条引导：模型最先读到的是身份，不是阈值。
	roleAt := strings.Index(got, roleFraming)
	thresholdAt := strings.Index(got, "Do NOT inline")
	if roleAt < 0 || thresholdAt < 0 || roleAt > thresholdAt {
		t.Fatalf("role framing must precede the thresholds (role %d, thresholds %d)", roleAt, thresholdAt)
	}
	// balanced 档不受 820 影响：不携带角色框定，维持 531 契约原文。
	if balanced := SubagentPolicyGuidance(SubagentPolicyBalanced); strings.Contains(balanced, "Your only role") {
		t.Fatalf("balanced guidance must stay untouched by 820:\n%s", balanced)
	}
}
