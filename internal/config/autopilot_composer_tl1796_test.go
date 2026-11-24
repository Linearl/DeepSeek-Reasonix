package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tl1796 — composer 模式条 autopilot 档显隐 opt-in 门（写点缺失返修）。
//
// 根因（验收件①）：tl1741 撤 [desktop] autopilot 偏好键时连带撤了
// SettingsView.Autopilot 镜像与 SetDesktopAutopilot 写点，但前端门
// （App.tsx setAutopilotEnabled）仍读 settings.autopilot === true——字段
// 永远缺失 ⇒ autopilotEnabled 恒 false ⇒ 模式下拉永久不含 autopilot 档。
// 本件补新写点 experimental_autopilot_composer（config 字段+setter+渲染行）
// 并登记 labNonFeatureKeys（tl1598 宪法 IV 账本门禁）。
//
// 本文件钉持久化链路（验收件②③的机制底座）：默认关（铁律 2 = 关态渲染面
// 与引入前逐字节一致）、翻转落盘、保存往返不丢（fixed-key-set 渲染行在位，
// 81/123 教训）。

// TestAutopilotComposerDefaultOff tl1796 验收③底座：默认关 = 前端门读 false
// = 模式条三档 ask/auto/yolo 布局不变。
func TestAutopilotComposerDefaultOff(t *testing.T) {
	d := Default()
	if d.Desktop.ExperimentalAutopilotComposer {
		t.Fatal("Default().Desktop.ExperimentalAutopilotComposer = true, want false（opt-in 默认关，铁律 2 零回归）")
	}
	rendered := RenderTOMLForScope(Default(), RenderScopeUser)
	if !strings.Contains(rendered, "experimental_autopilot_composer = false") {
		t.Fatal("用户态渲染面缺 experimental_autopilot_composer = false 行——固定键集规则（81/123 教训）：不在渲染表的键下次保存即被丢弃，开关会自己翻回关")
	}
}

// TestAutopilotComposerFlipSurvivesSave tl1796 验收②底座：写点翻转→渲染→
// 重新加载读回 true；关→读回 false。任何一次 settings 保存往返后开关不得
// 自己回弹（这正是死读时代的永久消失形态）。
func TestAutopilotComposerFlipSurvivesSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reasonix.toml")

	write := func(body string) *Config {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return LoadForEdit(path)
	}
	load := func() *Config {
		t.Helper()
		return LoadForEdit(path)
	}

	on := write("")
	if err := on.SetExperimentalAutopilotComposer(true); err != nil {
		t.Fatalf("SetExperimentalAutopilotComposer(true) = %v", err)
	}
	// SaveToScope(RenderScopeUser)：[desktop] 键属用户态（project scope 有意
	// 排除 desktop 偏好），生产 setter（applyConfigOnly→SaveTo 对用户配置路径）
	// 走的正是 user scope——TempDir 路径的裸 SaveTo 会被 renderScopeForPath
	// 判成 project scope 而丢掉整个 desktop 块，那是测试假阴性，不是缺陷。
	if err := on.SaveToScope(path, RenderScopeUser); err != nil {
		t.Fatalf("SaveToScope(user) = %v", err)
	}
	if reloaded := load(); !reloaded.Desktop.ExperimentalAutopilotComposer {
		t.Fatal("开→保存→重载后读回 false——写点在保存往返中丢失（tl1796 缺陷形态回归）")
	}

	off := write("")
	if err := off.SetExperimentalAutopilotComposer(false); err != nil {
		t.Fatalf("SetExperimentalAutopilotComposer(false) = %v", err)
	}
	if err := off.SaveToScope(path, RenderScopeUser); err != nil {
		t.Fatalf("SaveToScope(user) = %v", err)
	}
	if reloaded := load(); reloaded.Desktop.ExperimentalAutopilotComposer {
		t.Fatal("关→保存→重载后读回 true——关态不落盘")
	}
}

// TestAutopilotComposerRetiredKeyStillRetired tl1741 语义不回潮：撤掉的
// [desktop] autopilot 主键保持退役（deprecated 承接、渲染面不再输出），
// 新键是独立的 composer 显隐门，不是主键复活。
func TestAutopilotComposerRetiredKeyStillRetired(t *testing.T) {
	c := loadStableTierTOML(t, "")
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "autopilot =") || strings.HasPrefix(trimmed, "autopilot = ") {
			t.Fatalf("退役主键 [desktop] autopilot 重新出现在渲染面（%q）——tl1741 拍板语义回潮", trimmed)
		}
	}
	if !strings.Contains(rendered, "experimental_autopilot_composer =") {
		t.Fatal("渲染面缺新键 experimental_autopilot_composer——写点不完整")
	}
}
