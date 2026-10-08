package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// 任务 517：把任务 244 B1/B2/B3 三个实验开关合并为单个
// experimental_safety_cost_control（「安全 / 成本控制」）。本文件钉迁移语义
// （task 449 先例）：任一 legacy true ⇒ 合并键开（union），legacy 字段随后
// 清零，显式关永不复活；全关配置零变化（关闭态零行为，铁律 2）。

func writeSafetyCostConfig(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadForEdit(path)
}

// 验收②：每个 legacy [agent] true 都把合并键点亮，随后 legacy 字段清零
// （agent + desktop 两侧），下次 save 只携带合并键。
func TestSafetyCostControlMergeFoldsLegacyAgentTrues(t *testing.T) {
	legacy := map[string]func(c *Config) bool{
		"experimental_autonomous_idle_terminate": func(c *Config) bool { return c.Agent.ExperimentalAutonomousIdleTerminate },
		"experimental_loop_streak_note":          func(c *Config) bool { return c.Agent.ExperimentalLoopStreakNote },
		"experimental_event_wait_recheck":        func(c *Config) bool { return c.Agent.ExperimentalEventWaitRecheck },
	}
	for key := range legacy {
		t.Run(key, func(t *testing.T) {
			cfg := writeSafetyCostConfig(t, "[agent]\n"+key+" = true\n")
			if !cfg.Agent.ExperimentalSafetyCostControl {
				t.Fatalf("legacy %s = true must fold into experimental_safety_cost_control", key)
			}
			if cfg.Agent.ExperimentalAutonomousIdleTerminate || cfg.Agent.ExperimentalLoopStreakNote || cfg.Agent.ExperimentalEventWaitRecheck {
				t.Fatal("all legacy [agent] fields must be cleared after the fold")
			}
			if cfg.Desktop.ExperimentalAutonomousIdleTerminate || cfg.Desktop.ExperimentalLoopStreakNote || cfg.Desktop.ExperimentalEventWaitRecheck {
				t.Fatal("all retired [desktop] mirrors must stay cleared after the fold")
			}
		})
	}
}

// 验收②：旧桌面版只写 [desktop] 的 legacy true 经 473 fold（镜像→[agent]
// legacy 字段）再经 517 merge（→合并键），同样无损。
func TestSafetyCostControlMergeFoldsLegacyDesktopTrues(t *testing.T) {
	cfg := writeSafetyCostConfig(t, `[desktop]
experimental_autonomous_idle_terminate = true
experimental_event_wait_recheck = true
`)
	if !cfg.Agent.ExperimentalSafetyCostControl {
		t.Fatal("[desktop]-only legacy trues must fold into experimental_safety_cost_control")
	}
	if cfg.Desktop.ExperimentalAutonomousIdleTerminate || cfg.Desktop.ExperimentalEventWaitRecheck {
		t.Fatal("the retired [desktop] mirrors must be cleared after the fold")
	}
	if cfg.Agent.ExperimentalAutonomousIdleTerminate || cfg.Agent.ExperimentalEventWaitRecheck {
		t.Fatal("the legacy [agent] fields must be cleared after the fold")
	}
}

// 验收②：部分开启（仅 B1）也迁移为合并键开——三个守卫同属一个安全类，
// union 迁移意味着此前关闭的子项随合并键一并生效（任务书「首次加载自动归并」
// 口径，task 449 先例「either half on = the merged switch on」）。
func TestSafetyCostControlMergePartialOnUnions(t *testing.T) {
	cfg := writeSafetyCostConfig(t, `[agent]
experimental_autonomous_idle_terminate = true
`)
	if !cfg.Agent.ExperimentalSafetyCostControl {
		t.Fatal("a single legacy true must turn the merged switch on")
	}
	if cfg.Agent.ExperimentalAutonomousIdleTerminate {
		t.Fatal("the folded legacy field must be cleared")
	}
}

// 验收③（铁律 2）：全关配置零变化——合并键与全部 legacy 字段保持 false。
func TestSafetyCostControlAllOffStaysOff(t *testing.T) {
	cfg := writeSafetyCostConfig(t, "[agent]\nmodel = \"m\"\n")
	if cfg.Agent.ExperimentalSafetyCostControl {
		t.Fatal("an all-off config must not light the merged switch")
	}
	if cfg.Agent.ExperimentalAutonomousIdleTerminate || cfg.Agent.ExperimentalLoopStreakNote || cfg.Agent.ExperimentalEventWaitRecheck {
		t.Fatal("an all-off config must not light any legacy guard")
	}
}

// 合并键显式关 + 残留 legacy true：legacy true 携带意图（bool 无法区分
// 「缺失」与 false），按 449 口径迁移为开，避免旧意图丢失。
func TestSafetyCostControlStaleLegacyTrueStillMerges(t *testing.T) {
	cfg := writeSafetyCostConfig(t, `[agent]
experimental_safety_cost_control = false
experimental_loop_streak_note = true
`)
	if !cfg.Agent.ExperimentalSafetyCostControl {
		t.Fatal("a stale legacy true must merge on (bool cannot carry an explicit off)")
	}
	if cfg.Agent.ExperimentalLoopStreakNote {
		t.Fatal("the stale legacy field must be cleared after the merge")
	}
}

// 验收②：迁移结果往返无损——load → render → decode 后合并键保持开、
// legacy 键渲染为 false（旧二进制绝不会看到 stale on）。
func TestSafetyCostControlMergedStateRoundTrips(t *testing.T) {
	cfg := writeSafetyCostConfig(t, `[agent]
experimental_event_wait_recheck = true
`)
	out := RenderTOMLForScope(cfg, RenderScopeUser)
	if !strings.Contains(out, "experimental_safety_cost_control = true") {
		t.Fatalf("render face must carry the merged switch as true:\n%s", out)
	}
	for _, legacy := range []string{
		"experimental_autonomous_idle_terminate = false",
		"experimental_loop_streak_note = false",
		"experimental_event_wait_recheck = false",
	} {
		if !strings.Contains(out, legacy) {
			t.Fatalf("render face must keep the legacy row %q (task 449 precedent):\n%s", legacy, out)
		}
	}
	var back Config
	if _, err := toml.Decode(out, &back); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	if !back.Agent.ExperimentalSafetyCostControl {
		t.Fatal("the merged switch must round-trip through the render face")
	}
}

// 写路径：SetExperimentalSafetyCostControl 单写合并键并清 legacy 字段
// （显式关不会被残留 legacy true 复活）。
func TestSetSafetyCostControlSingleWrite(t *testing.T) {
	c := &Config{}
	c.Agent.ExperimentalLoopStreakNote = true // 未归一的 legacy 残留
	if err := c.SetExperimentalSafetyCostControl(true); err != nil {
		t.Fatal(err)
	}
	if !c.Agent.ExperimentalSafetyCostControl {
		t.Fatal("SetExperimentalSafetyCostControl(true) must write the merged key")
	}
	if c.Agent.ExperimentalLoopStreakNote || c.Agent.ExperimentalAutonomousIdleTerminate || c.Agent.ExperimentalEventWaitRecheck {
		t.Fatal("the setter must clear the legacy fields")
	}
	if err := c.SetExperimentalSafetyCostControl(false); err != nil {
		t.Fatal(err)
	}
	if c.Agent.ExperimentalSafetyCostControl {
		t.Fatal("SetExperimentalSafetyCostControl(false) must clear the merged key")
	}
}
