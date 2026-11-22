package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 任务 1005 批1 验收矩阵 → tl1741 换代（用户 20261121 三拍：稳定档不再提供
// 开关，清理 false 分支非写死 true）：
//
//	autopilot / sessionCollab / runtimeReuse：键自 struct 退役，功能常开
//	（1005 C1/C4 kill switch 口径对三者废止——false 分支与 config 读取项
//	一并清，不存在「读 false 关功能」的路径）。
//	fullAccess：键保留，默认开 + 手写 false 生效语义不变（外部目录访问
//	控制有真实需求）。
//	存量手写键（true/false 皆然）由 config_key_warn deprecated 清单承接：
//	WARN 可见、不阻断加载；fixed-key-set 渲染在下次保存时清行。
//	AutopilotMaxRuntime 伴生默认保留（无界 autopilot 是 CLI 直接拒绝的
//	组合，护栏不可丢）。

// TestLabStableTierDefaultsOnAfterRetire tl1741：fullAccess 默认开不变；
// autopilot 护栏伴生默认不丢。
func TestLabStableTierDefaultsOnAfterRetire(t *testing.T) {
	d := Default()
	if !d.Agent.ExperimentalFullAccess {
		t.Errorf("Default().Agent.ExperimentalFullAccess = false, want true（fullAccess 默认开语义保留）")
	}
	maxRuntime, err := time.ParseDuration(strings.TrimSpace(d.Desktop.AutopilotMaxRuntime))
	if err != nil || maxRuntime <= 0 {
		t.Errorf("Default().Desktop.AutopilotMaxRuntime = %q 无效（护栏伴生默认缺失）", d.Desktop.AutopilotMaxRuntime)
	}
}

// loadStableTierTOML writes the given TOML body to a temp user config and
// loads it through the same entry the desktop settings path uses.
func loadStableTierTOML(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "reasonix.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadForEdit(path)
}

// TestRetiredLabKeysHandwrittenFalseHarmless tl1741 核心钉（验收「config 手写
// false 启动仍为开」）：历史 false 残留加载不报错、不产生任何「读 false 关
// 功能」路径（键已退役，不存在读取层）；fullAccess 显式 false 仍生效。
func TestRetiredLabKeysHandwrittenFalseHarmless(t *testing.T) {
	body := `
[desktop]
autopilot = false
experimental_collab_hub = false
experimental_workflow_panel = false
experimental_workflow_templates = false
experimental_cdp_debug_port = false
experimental_runtime_reuse = false
experimental_session_collab = false
[agent]
experimental_session_collab = false
experimental_runtime_reuse = false
experimental_full_access = false
`
	c := loadStableTierTOML(t, body)
	if c == nil {
		t.Fatal("带 8 项手写 false 残留的 config 必须正常加载（deprecated 清单承接，WARN 可见不阻断）")
	}
	if c.Agent.ExperimentalFullAccess {
		t.Errorf("手写 experimental_full_access = false 被覆盖成 true（fullAccess 安全项被升级强开）")
	}
	// 「仍为开」的机制实证：读取层已不存在（编译期事实——无字段可读），
	// 这里钉渲染面行为：fixed-key-set 渲染不再输出退役键（下次保存清行，
	// WARN 自然消失），fullAccess 仍如实输出。
	rendered := RenderTOML(c)
	for _, key := range []string{
		"autopilot = ", "experimental_session_collab = ", "experimental_collab_hub = ",
		"experimental_workflow_panel = ", "experimental_workflow_templates = ",
		"experimental_cdp_debug_port = ", "experimental_runtime_reuse = ",
	} {
		if strings.Contains(rendered, key) {
			t.Errorf("退役键 %s 仍出现在渲染面（fixed-key-set 未收缩，残留行不会被清）", key)
		}
	}
	if !strings.Contains(rendered, "experimental_full_access = false") {
		t.Errorf("fullAccess 显式 false 必须如实渲染（安全项往返不可丢）\n---\n%s", rendered)
	}
}

// TestRetiredLabKeysHandwrittenTrueHarmless tl1741：手写 true 残留同样无害
// （本机实测基线——用户 config 六键显式 true，升级后行为零变化）。
func TestRetiredLabKeysHandwrittenTrueHarmless(t *testing.T) {
	body := `
[desktop]
autopilot = true
experimental_collab_hub = true
experimental_cdp_debug_port = true
[agent]
experimental_session_collab = true
experimental_runtime_reuse = true
experimental_full_access = true
`
	c := loadStableTierTOML(t, body)
	if c == nil {
		t.Fatal("带退役键 true 残留的 config 必须正常加载")
	}
	if !c.Agent.ExperimentalFullAccess {
		t.Errorf("手写 experimental_full_access = true 被丢弃")
	}
	rendered := RenderTOML(c)
	if !strings.Contains(rendered, "experimental_full_access = true") {
		t.Errorf("fullAccess 显式 true 必须如实渲染\n---\n%s", rendered)
	}
}

// TestRetiredLabKeysAbsentFromRender tl1741：默认配置渲染不再输出退役键
// （render 表随键收缩；1005「渲染面如实写 true」口径对退役项废止）。
func TestRetiredLabKeysAbsentFromRender(t *testing.T) {
	rendered := RenderTOML(Default())
	for _, key := range []string{
		"autopilot = ", "experimental_session_collab = ", "experimental_collab_hub = ",
		"experimental_workflow_panel = ", "experimental_workflow_templates = ",
		"experimental_cdp_debug_port = ", "experimental_runtime_reuse = ",
	} {
		if strings.Contains(rendered, key) {
			t.Errorf("退役键 %s 不应出现在默认渲染（键已退役）", key)
		}
	}
	if !strings.Contains(rendered, "experimental_full_access = true") {
		t.Errorf("fullAccess 默认开必须如实渲染（渲染面不隐藏稳定档默认开）")
	}
}
