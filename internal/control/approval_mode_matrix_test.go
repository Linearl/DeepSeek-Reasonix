package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"reasonix/internal/permission"
	"reasonix/internal/sentinel"
)

// 任务 426 验收：开关组合矩阵逐态测试。四个审批姿态（ask / auto / yolo /
// dontask）× 决策类别（普通写入[仅兜底] / 显式 ask 规则 / 显式 deny 规则 /
// fresh 人批 / requireHuman / 会话授权）× 判定面（模式兜底映射、headless 门、
// 交互旁路捷径），逐格断言。历史上「autopilot+ask 下 fence 仍拦」一类的组合
// 漏接就是这些格子里的某一格悄悄变了——任何一格变红都必须是有意的行为变更，
// 并在提交信息里点名该格。

// matrixPolicy builds the deny-only base policy (fallback semantics visible);
// withAskRule layers an explicit write_file ask rule on top so rule-vs-fallback
// cells stay distinct.
func matrixPolicy(askRule bool) permission.Policy {
	var ask []string
	if askRule {
		ask = []string{"write_file"}
	}
	return permission.New("ask", nil, ask, []string{"Bash(git push:*)"})
}

func matrixCall(t *testing.T, g permissionGateLike, toolName, args string) bool {
	t.Helper()
	allow, _, err := g.Check(context.Background(), toolName, json.RawMessage(args), false)
	if err != nil {
		t.Fatalf("Check(%s, %s) returned error: %v", toolName, args, err)
	}
	return allow
}

// permissionGateLike is the Check surface shared by *permission.Gate and
// *freshHumanHeadlessGate, so the matrix can drive both without reflection.
type permissionGateLike interface {
	Check(ctx context.Context, toolName string, args json.RawMessage, readOnly bool) (bool, string, error)
}

// TestMatrixModeFallbackMapping pins the single-point mode → writer-fallback
// mapping every gate builder now shares.
func TestMatrixModeFallbackMapping(t *testing.T) {
	cases := []struct {
		mode   string
		want   permission.Decision
		wantOK bool
	}{
		{mode: "ask", want: permission.Ask, wantOK: false},
		{mode: "auto", want: permission.Allow, wantOK: true},
		{mode: "yolo", want: permission.Allow, wantOK: true},
		{mode: "dontask", want: permission.Deny, wantOK: true},
	}
	for _, tc := range cases {
		got, ok := approvalModeFallback(tc.mode)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("approvalModeFallback(%q) = (%v,%v), want (%v,%v)", tc.mode, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestMatrixHeadlessGate walks the headless decision surface: 4 modes ×
// {fallback-only write, ask-rule write, deny-rule command, fresh-human tool}.
func TestMatrixHeadlessGate(t *testing.T) {
	cases := []struct {
		mode    string
		askRule bool
		tool    string
		args    string
		want    bool
	}{
		// 普通写入（无规则命中 → 走写入兜底）。
		{mode: "ask", tool: "write_file", args: `{"path":"/tmp/a"}`, want: false},
		{mode: "auto", tool: "write_file", args: `{"path":"/tmp/a"}`, want: true},
		{mode: "yolo", tool: "write_file", args: `{"path":"/tmp/a"}`, want: true},
		{mode: "dontask", tool: "write_file", args: `{"path":"/tmp/a"}`, want: false},
		// 显式 ask 规则：auto 无人在场 fail closed（deny）而非静默放行；
		// yolo 跳过普通提问。
		{mode: "ask", askRule: true, tool: "write_file", args: `{"path":"/tmp/ruled"}`, want: false},
		{mode: "auto", askRule: true, tool: "write_file", args: `{"path":"/tmp/ruled"}`, want: false},
		{mode: "yolo", askRule: true, tool: "write_file", args: `{"path":"/tmp/ruled"}`, want: true},
		{mode: "dontask", askRule: true, tool: "write_file", args: `{"path":"/tmp/ruled"}`, want: false},
		// 显式 deny 规则：四个模式全部拦截。
		{mode: "ask", tool: "bash", args: `{"command":"git push origin main"}`, want: false},
		{mode: "auto", tool: "bash", args: `{"command":"git push origin main"}`, want: false},
		{mode: "yolo", tool: "bash", args: `{"command":"git push origin main"}`, want: false},
		{mode: "dontask", tool: "bash", args: `{"command":"git push origin main"}`, want: false},
		// fresh 人批工具：headless 永远不放行（无 allowLowRiskFreshAction）。
		{mode: "ask", tool: "remember", args: `{}`, want: false},
		{mode: "auto", tool: "remember", args: `{}`, want: false},
		{mode: "yolo", tool: "remember", args: `{}`, want: false},
		{mode: "dontask", tool: "remember", args: `{}`, want: false},
	}
	for _, tc := range cases {
		gate := BuildHeadlessApprovalGate(matrixPolicy(tc.askRule), tc.mode)
		if got := matrixCall(t, gate, tc.tool, tc.args); got != tc.want {
			t.Errorf("headless gate mode=%q askRule=%v %s(%s) = %v, want %v", tc.mode, tc.askRule, tc.tool, tc.args, got, tc.want)
		}
	}
}

// TestMatrixInteractiveBypassShortcut walks the interactive pre-prompt
// shortcut (approvalManager.preApproved / preApprovedForDecisionOptions):
// mode × plan-execution window × decision class.
func TestMatrixInteractiveBypassShortcut(t *testing.T) {
	type cell struct {
		mode       string
		askRule    bool
		planWindow bool
		check      func(a *approvalManager) bool
		want       bool
	}
	plainWrite := func(a *approvalManager) bool {
		return a.preApproved("write_file", "/tmp/a", json.RawMessage(`{"path":"/tmp/a"}`))
	}
	ruledWrite := func(a *approvalManager) bool {
		return a.preApproved("write_file", "/tmp/ruled", json.RawMessage(`{"path":"/tmp/ruled"}`))
	}
	cells := []cell{
		// 普通写入：只有 yolo 与 plan 执行窗（重放兜底=Allow）能跳过提问。
		{mode: "ask", planWindow: false, check: plainWrite, want: false},
		{mode: "ask", planWindow: true, check: plainWrite, want: true},
		{mode: "auto", planWindow: false, check: plainWrite, want: false},
		{mode: "auto", planWindow: true, check: plainWrite, want: true},
		{mode: "yolo", planWindow: false, check: plainWrite, want: true},
		// plan 窗的既有语义按 Auto 而非姿态本身（bypassAllowsLocked 注释明示
		// "matching Auto rather than YOLO"），dontask 也不例外——此格钉住的是
		// 收敛前的真值，不是理想值。
		{mode: "dontask", planWindow: true, check: plainWrite, want: true},
		// 显式 ask 规则：plan 窗重放仍命中 Ask（规则优先级高于兜底）→ 不跳过；
		// yolo 是唯一跳过普通提问的姿态。
		{mode: "ask", askRule: true, planWindow: true, check: ruledWrite, want: false},
		{mode: "auto", askRule: true, planWindow: true, check: ruledWrite, want: false},
		{mode: "yolo", askRule: true, planWindow: false, check: ruledWrite, want: true},
		// auto 的语义：不跳过提问，但无规则命中的普通写入提示会被 auto 排空
		// （autoApprovalWouldAllowLocked=true）；ask 规则命中则保留。
		{mode: "auto", planWindow: false, check: func(a *approvalManager) bool {
			return a.autoApprovalWouldAllowLocked("write_file", "/tmp/a", json.RawMessage(`{"path":"/tmp/a"}`))
		}, want: true},
		{mode: "auto", askRule: true, planWindow: false, check: func(a *approvalManager) bool {
			return a.autoApprovalWouldAllowLocked("write_file", "/tmp/ruled", json.RawMessage(`{"path":"/tmp/ruled"}`))
		}, want: false},
		// deny 规则在策略层四模式全拦：yolo 的捷径只跳过提问，不放行执行。
		{mode: "yolo", planWindow: false, check: func(a *approvalManager) bool {
			policy := a.policy
			policy.Mode, _ = approvalModeFallback("yolo")
			return policy.Decide("bash", false, json.RawMessage(`{"command":"git push origin main"}`)) == permission.Deny
		}, want: true},
	}
	for _, tc := range cells {
		a := newApprovalManager(matrixPolicy(tc.askRule), tc.mode, time.Second)
		a.planAutoApprove = tc.planWindow
		if got := tc.check(&a); got != tc.want {
			t.Errorf("bypass matrix mode=%q askRule=%v planWindow=%v → %v, want %v", tc.mode, tc.askRule, tc.planWindow, got, tc.want)
		}
	}
}

// TestMatrixFreshAndRequiredHumanDecisions pins the decision classes that
// mode shortcuts must never answer: fresh decisions only follow explicit
// session grants; requireHuman only yolo or a grant for a grantable tool.
func TestMatrixFreshAndRequiredHumanDecisions(t *testing.T) {
	for _, mode := range []string{"ask", "auto", "yolo", "dontask"} {
		a := newApprovalManager(matrixPolicy(false), mode, time.Second)
		if a.preApprovedForDecision("remember", "{}", json.RawMessage(`{}`), true) {
			t.Errorf("mode=%q: fresh decision answered without a grant", mode)
		}
		if a.preApprovedForDecisionOptions("sandbox_escape", "", nil, false, true) && mode != "yolo" {
			t.Errorf("mode=%q: requireHuman decision answered outside yolo", mode)
		}
	}
	// 显式会话授权：普通工具覆盖 fresh 决策类；不可授权的 fresh 工具
	// （remember）即使有授权记录也不放行。
	a := newApprovalManager(matrixPolicy(false), "ask", time.Second)
	a.grantSession("write_file", "")
	if !a.preApprovedForDecision("write_file", "/tmp/g", json.RawMessage(`{"path":"/tmp/g"}`), true) {
		t.Error("explicit session grant must cover a fresh ordinary-tool decision")
	}
	a2 := newApprovalManager(matrixPolicy(false), "yolo", time.Second)
	a2.grantSession("remember", "")
	if a2.preApprovedForDecision("remember", "", json.RawMessage(`{}`), true) {
		t.Error("remember is not grantable: a recorded grant must not answer its fresh decision")
	}
}

// TestMatrixAutopilotPreapproveCross keeps the autopilot tiered pre-approval
// (任务 388) honest inside the matrix: the master switch AND autopilot AND the
// per-category checkbox must ALL hold — autopilot under ask mode never
// widens.
func TestMatrixAutopilotPreapproveCross(t *testing.T) {
	opts := PreapproveManagedOptions{Enabled: true, Skills: true}
	for _, kind := range []ManagedWriteKind{managedKindSkills, managedKindHooks, managedKindStores, managedKindBash, managedKindOther} {
		if allows := opts.allows(false, kind); allows {
			t.Errorf("kind=%v pre-approved without autopilot", kind)
		}
	}
	if !opts.allows(true, managedKindSkills) {
		t.Error("skills kind should pre-approve under autopilot with its checkbox on")
	}
	if opts.allows(true, managedKindHooks) {
		t.Error("hooks kind must follow its own checkbox, not the skills one")
	}
	if (PreapproveManagedOptions{}).allows(true, managedKindSkills) {
		t.Error("zero-value options must pre-approve nothing (铁律 2)")
	}
}

// TestMatrixSentinelFenceSurvivesEveryMode pins the execution-layer hard
// fence (任务 410 底线)：sentinel 不接受任何审批模式作为输入，yolo 也不存在
// 「绕过底线」的入口——它在判定层之外。
func TestMatrixSentinelFenceSurvivesEveryMode(t *testing.T) {
	verdict := sentinel.CheckToolCall("bash", json.RawMessage(`{"command":"git push -f origin"}`), t.TempDir())
	if !verdict.Blocked {
		t.Fatalf("bare force push must be hard-blocked regardless of approval mode, got %+v", verdict)
	}
}
