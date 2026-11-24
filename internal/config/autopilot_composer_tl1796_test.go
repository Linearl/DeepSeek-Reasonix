package config

import (
	"strings"
	"testing"
)

// tl1796 r2 — composer 模式条 autopilot 档显隐开关回退：恒显落地。
//
// 打回根因（装机实测，双重）：一期 opt-in 键默认关 = 选项默认缺失（切 YOLO
// 后下拉仍无 autopilot，用户口径「任何时候都应该出现」）；config 渲染行把
// 零值 false 显性化，观感「该键被异常关闭」（调查结论：唯一写点是 lab 卡
// 行级开关 on/off 同一 setter，无批量翻转、无显式 false 写者——false 即
// 默认值首次渲染，非被翻关）。
//
// r2 语义（tl1741 stable 档无开关口径同源，collab_hub 先例）：键自 struct
// 退役、渲染行随撤、deprecated 清单承接存量手写键（true/false 皆然，WARN
// 可见不阻断，下次保存清行）；前端 autopilot 档恒显无门（前端钉见
// desktop/frontend/src/__tests__/tl1796-autopilot-composer-offer.test.ts）。

// TestAutopilotComposerSwitchRetiredFromRender r2 验收②：一期 opt-in 键随撤
// 开关退役——fixed-key-set 渲染表收缩，渲染面不再输出该键（存量手写行下次
// 保存即清）。
func TestAutopilotComposerSwitchRetiredFromRender(t *testing.T) {
	rendered := RenderTOMLForScope(Default(), RenderScopeUser)
	if strings.Contains(rendered, "experimental_autopilot_composer") {
		t.Fatal("退役开关键 experimental_autopilot_composer 仍出现在渲染面——fixed-key-set 未收缩，一期 opt-in 门回潮")
	}
	// 键退役但选项常开：渲染面不再有任何 autopilot 显隐门，且退役主键
	// [desktop] autopilot 不复活（tl1741 拍板语义零回潮，r2 不加回）。
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "autopilot =") {
			t.Fatalf("退役主键 [desktop] autopilot 重新出现在渲染面（%q）——tl1741 拍板语义回潮", trimmed)
		}
	}
}

// TestAutopilotComposerSwitchRetiredDeprecatedLedger r2 验收②承接面：存量
// 手写键（true/false 皆然）由 deprecated 清单承接——账本在册、加载不阻断、
// 渲染面清行。
func TestAutopilotComposerSwitchRetiredDeprecatedLedger(t *testing.T) {
	hint, ok := deprecatedConfigKeys["desktop.experimental_autopilot_composer"]
	if !ok {
		t.Fatal("desktop.experimental_autopilot_composer 未入 deprecated 清单——存量手写键将按未知键告警，迁移提示丢失")
	}
	if !strings.Contains(hint, "always offers the autopilot tier") {
		t.Fatalf("deprecated 提示未说明行为去向（%q）——承接提示必须指向「恒显」", hint)
	}
	for _, flag := range []string{"true", "false"} {
		c := loadStableTierTOML(t, `
[desktop]
experimental_autopilot_composer = `+flag+`
`)
		if c == nil {
			t.Fatalf("手写 %s 残留的 config 必须正常加载（deprecated 承接，WARN 可见不阻断）", flag)
		}
		rendered := RenderTOMLForScope(c, RenderScopeUser)
		if strings.Contains(rendered, "experimental_autopilot_composer") {
			t.Fatalf("手写 %s 残留未被渲染面清行（fixed-key-set 未收缩，WARN 将永不消失）", flag)
		}
	}
}

// TestAutopilotComposerOptionAlwaysOffered r2 验收①的 Go 侧语义钉：恒显=
// 「不存在任何可关它的键」——编译期事实（struct 无字段）+ 渲染面无行共同
// 构成双证据；本件补渲染面证据的负载形态（含既有键的完整用户态渲染不因
// 移除该行缺行——fixed-key-set 其余键原样）。
func TestAutopilotComposerOptionAlwaysOffered(t *testing.T) {
	c := loadStableTierTOML(t, "")
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	// 相邻键仍在位（撤行只撤一行，不牵连渲染表其余键——81/123 教验的反向
	// 断言：本次是收缩而非漂移）。
	for _, anchor := range []string{
		"experimental_tasklist_panel = ",
		"experimental_trajectory_view = ",
	} {
		if !strings.Contains(rendered, anchor) {
			t.Fatalf("锚键 %s 从渲染面消失——撤行操作牵连了渲染表其余键", anchor)
		}
	}
}
