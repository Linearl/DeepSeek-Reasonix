package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 任务 1005 —— 实验室「稳定」档批1 转档（autopilot / sessionCollab /
// fullAccess / runtimeReuse）的验收矩阵：
//
//	C1 默认开：Default() 四项为 true（新装用户零配置即默认开启）。
//	C4 kill switch：config 手写显式 false 不被默认值覆盖（显式值>默认值，
//	双向保留——fullAccess 安全项尤其不可升级强开）；显式 true 同样保留。
//	渲染面往返：Default() 渲染出的 TOML 把四项如实写成 true（round-trip
//	后仍是默认开，不出现 omit-on-off 弹回）。

// TestLabStableTierDefaultsOn 任务 1005 C1：新装默认开。
func TestLabStableTierDefaultsOn(t *testing.T) {
	d := Default()
	if !d.Desktop.Autopilot {
		t.Errorf("Default().Desktop.Autopilot = false, want true（任务1005 稳定档默认开）")
	}
	if !d.Agent.ExperimentalSessionCollab {
		t.Errorf("Default().Agent.ExperimentalSessionCollab = false, want true（任务1005 稳定档默认开）")
	}
	if !d.Agent.ExperimentalFullAccess {
		t.Errorf("Default().Agent.ExperimentalFullAccess = false, want true（任务1005 稳定档默认开）")
	}
	if !d.Agent.ExperimentalRuntimeReuse {
		t.Errorf("Default().Agent.ExperimentalRuntimeReuse = false, want true（任务1005 稳定档默认开）")
	}
	// autopilot 默认开必须伴生有效时限：desktopAutopilotDefaults 对
	// autopilot=开 但无有效 max_runtime 的组合拒绝降级为关——不伴生则
	// 新装用户的「默认开」是静默空转。
	maxRuntime, err := time.ParseDuration(strings.TrimSpace(d.Desktop.AutopilotMaxRuntime))
	if err != nil || maxRuntime <= 0 {
		t.Errorf("Default().Desktop.AutopilotMaxRuntime = %q 无效（伴生默认缺失，默认开空转）", d.Desktop.AutopilotMaxRuntime)
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

// TestLabStableTierExplicitFalseSurvives 任务 1005 C4 kill switch：手写
// 显式 false 不被稳定档默认值覆盖（存量配置零迁移、零覆盖）。
func TestLabStableTierExplicitFalseSurvives(t *testing.T) {
	body := `
[desktop]
autopilot = false
[agent]
experimental_session_collab = false
experimental_full_access = false
experimental_runtime_reuse = false
`
	c := loadStableTierTOML(t, body)
	if c.Desktop.Autopilot {
		t.Errorf("手写 autopilot = false 被默认值覆盖成 true（C4 kill switch 失效）")
	}
	if c.Agent.ExperimentalSessionCollab {
		t.Errorf("手写 experimental_session_collab = false 被覆盖成 true（C4 kill switch 失效）")
	}
	if c.Agent.ExperimentalFullAccess {
		t.Errorf("手写 experimental_full_access = false 被覆盖成 true（fullAccess 安全项被升级强开，C4 失效）")
	}
	if c.Agent.ExperimentalRuntimeReuse {
		t.Errorf("手写 experimental_runtime_reuse = false 被覆盖成 true（C4 kill switch 失效）")
	}
}

// TestLabStableTierExplicitTrueSurvives 任务 1005 C4 双向：显式 true 同样
// 保留（显式值>默认值，双向皆不丢）。
func TestLabStableTierExplicitTrueSurvives(t *testing.T) {
	body := `
[desktop]
autopilot = true
[agent]
experimental_session_collab = true
experimental_full_access = true
experimental_runtime_reuse = true
`
	c := loadStableTierTOML(t, body)
	if !c.Desktop.Autopilot || !c.Agent.ExperimentalSessionCollab || !c.Agent.ExperimentalFullAccess || !c.Agent.ExperimentalRuntimeReuse {
		t.Errorf("手写显式 true 被丢弃: autopilot=%v collab=%v fullAccess=%v runtimeReuse=%v",
			c.Desktop.Autopilot, c.Agent.ExperimentalSessionCollab, c.Agent.ExperimentalFullAccess, c.Agent.ExperimentalRuntimeReuse)
	}
}

// TestLabStableTierAbsentKeyReadsDefault 任务 1005 C1 补充面：键缺省读
// 默认开（未写过这些键的存量配置升级后默认开启，即「转档」语义本身）。
func TestLabStableTierAbsentKeyReadsDefault(t *testing.T) {
	c := loadStableTierTOML(t, "# nothing lab-related\n")
	if !c.Desktop.Autopilot || !c.Agent.ExperimentalSessionCollab || !c.Agent.ExperimentalFullAccess || !c.Agent.ExperimentalRuntimeReuse {
		t.Errorf("键缺省未读默认开: autopilot=%v collab=%v fullAccess=%v runtimeReuse=%v",
			c.Desktop.Autopilot, c.Agent.ExperimentalSessionCollab, c.Agent.ExperimentalFullAccess, c.Agent.ExperimentalRuntimeReuse)
	}
}

// TestLabStableTierDefaultRenderRoundTrip 任务 1005：默认配置渲染出的 TOML
// 把四项如实写成 true（81/123 丢档教训的反向面——渲染面不隐藏默认开），
// 且渲染产物再 Load 仍为开。
func TestLabStableTierDefaultRenderRoundTrip(t *testing.T) {
	rendered := RenderTOML(Default())
	for key := range map[string]bool{
		"autopilot":                   true,
		"experimental_session_collab": true,
		"experimental_full_access":    true,
		"experimental_runtime_reuse":  true,
	} {
		if !strings.Contains(rendered, key+" = true") {
			t.Errorf("默认配置渲染缺 %s = true（渲染面隐藏了稳定档默认开）", key)
		}
	}
	round := loadStableTierTOML(t, rendered)
	if !round.Desktop.Autopilot || !round.Agent.ExperimentalSessionCollab || !round.Agent.ExperimentalFullAccess || !round.Agent.ExperimentalRuntimeReuse {
		t.Errorf("默认渲染往返后稳定档弹回关: autopilot=%v collab=%v fullAccess=%v runtimeReuse=%v",
			round.Desktop.Autopilot, round.Agent.ExperimentalSessionCollab, round.Agent.ExperimentalFullAccess, round.Agent.ExperimentalRuntimeReuse)
	}
}
