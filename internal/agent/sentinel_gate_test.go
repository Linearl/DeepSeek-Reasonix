package agent

// 任务 410（Sentinel 降维版）接线测试：固定前置检查位于审批链最前——
// 在 Auto Guard、MCP 信任快路径与普通审批门之前——所以 yolo 门（Mode=Allow、
// 无审批人）也拦得住硬禁区操作，而普通调用不受影响。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/permission"
	"reasonix/internal/sentinel"
)

func forcePushPlan() *toolCallPlan {
	args := json.RawMessage(`{"command":"git push --force origin main"}`)
	return &toolCallPlan{
		permName:     "bash",
		permArgs:     args,
		readOnly:     true, // 拦截与读写分类无关：硬禁区不看 readOnly
		evidenceName: "bash",
		evidenceArgs: args,
	}
}

func TestSentinelPreCheckBlocksForcePushUnderYoloGate(t *testing.T) {
	sentinel.SetHardForbidden(true)
	t.Cleanup(func() { sentinel.SetHardForbidden(false) })

	// yolo：写者回退全放行、无审批人（headless 语义下的全自动门）。
	a := &Agent{svc: agentServices{
		gate: permission.NewGate(permission.New("allow", nil, nil, nil), nil),
	}}
	outcome, stop := a.applyRecoveryAndPermission(context.Background(), forcePushPlan())
	if !stop {
		t.Fatal("sentinel must stop the chain for a hard-forbidden call")
	}
	if !outcome.blocked {
		t.Fatalf("force push under a yolo gate must be blocked, got %+v", outcome)
	}
	if !strings.Contains(outcome.output, "force_push_protected") {
		t.Errorf("block output must name the rule, got: %s", outcome.output)
	}
	// 拒绝 + 说明原因 + 替代路径：提到普通推送与存 patch 的替代。
	if !strings.Contains(outcome.output, "git push") || !strings.Contains(outcome.output, "patch") {
		t.Errorf("block output must carry the alternative path, got: %s", outcome.output)
	}
}

func TestSentinelPreCheckPassesOrdinaryCalls(t *testing.T) {
	sentinel.SetHardForbidden(true)
	t.Cleanup(func() { sentinel.SetHardForbidden(false) })

	a := &Agent{svc: agentServices{
		gate: permission.NewGate(permission.New("allow", nil, nil, nil), nil),
	}}
	args := json.RawMessage(`{"command":"git push origin feature-x"}`)
	plan := &toolCallPlan{
		permName:     "bash",
		permArgs:     args,
		readOnly:     true,
		evidenceName: "bash",
		evidenceArgs: args,
	}
	outcome, stop := a.applyRecoveryAndPermission(context.Background(), plan)
	if stop || outcome.blocked {
		t.Fatalf("ordinary call must pass the sentinel pre-check, got %+v (stop=%v)", outcome, stop)
	}
}

func TestSentinelPreCheckHonorsToggle(t *testing.T) {
	sentinel.SetHardForbidden(false)
	a := &Agent{svc: agentServices{
		gate: permission.NewGate(permission.New("allow", nil, nil, nil), nil),
	}}
	outcome, stop := a.applyRecoveryAndPermission(context.Background(), forcePushPlan())
	if stop || outcome.blocked {
		t.Fatalf("hard-forbidden off must let the chain proceed, got %+v (stop=%v)", outcome, stop)
	}
}
