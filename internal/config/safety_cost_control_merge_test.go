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
// tl1816（方案A 一次性迁移）：合并键（总开关）本身退役——fold 之后
// migrateSafetyCostControlMasterRetire 把总开关现值写实到全部 nil 子键，
// 行为逐位不变；渲染面不再写总开关行，Effective 读数只看子键。

func writeSafetyCostConfig(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadForEdit(path)
}

// requireMaterialized pins the tl1816 materialization end-state: after load,
// every sub-switch carries an explicit value equal to the (retired) master.
func requireMaterialized(t *testing.T, c *Config, want bool) {
	t.Helper()
	subs := map[string]*bool{
		"safety_idle_terminate":     c.Agent.SafetyIdleTerminate,
		"safety_loop_streak_note":   c.Agent.SafetyLoopStreakNote,
		"safety_event_wait_recheck": c.Agent.SafetyEventWaitRecheck,
	}
	for name, sub := range subs {
		if sub == nil {
			t.Fatalf("%s must be materialized (non-nil) after the tl1816 one-shot migration", name)
		}
		if *sub != want {
			t.Fatalf("%s = %v, want %v (master value materialized verbatim)", name, *sub, want)
		}
	}
	if c.SafetyIdleTerminateEnabled() != want || c.SafetyLoopStreakNoteEnabled() != want || c.SafetyEventWaitRecheckEnabled() != want {
		t.Fatalf("the three Effective gates must all read %v after materialization", want)
	}
}

// 验收②：每个 legacy [agent] true 都把合并键点亮，随后 legacy 字段清零
// （agent + desktop 两侧）；tl1816 后写实进三子键（真终态）。
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
			// tl1816：fold 后一次性写实——三子键显式 true（行为逐位不变）。
			requireMaterialized(t, cfg, true)
		})
	}
}

// 验收②：旧桌面版只写 [desktop] 的 legacy true 经 473 fold（镜像→[agent]
// legacy 字段）再经 517 merge（→合并键）再经 tl1816 materialize（→子键），
// 同样无损。
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
	requireMaterialized(t, cfg, true)
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
	requireMaterialized(t, cfg, true)
}

// 验收③（铁律 2）：全关配置零变化——合并键与全部 legacy 字段保持 false；
// tl1816 后三子键写实为显式 false（验收⑤「总开关 false+子键 nil 的配置→
// 移除后三子项=false」），Effective 读数仍全 false。
func TestSafetyCostControlAllOffStaysOff(t *testing.T) {
	cfg := writeSafetyCostConfig(t, "[agent]\nmodel = \"m\"\n")
	if cfg.Agent.ExperimentalSafetyCostControl {
		t.Fatal("an all-off config must not light the merged switch")
	}
	if cfg.Agent.ExperimentalAutonomousIdleTerminate || cfg.Agent.ExperimentalLoopStreakNote || cfg.Agent.ExperimentalEventWaitRecheck {
		t.Fatal("an all-off config must not light any legacy guard")
	}
	requireMaterialized(t, cfg, false)
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
	requireMaterialized(t, cfg, true)
}

// tl1816（方案A 验收⑤）：总开关显式 true + 三子键 nil 的存量配置——迁移后
// 三子键 = true（行为逐位不变）；总开关显式 false + 三子键 nil——迁移后
// 三子键 = false。
func TestSafetyCostControlMasterRetireMaterializesNilSubs(t *testing.T) {
	t.Run("on master materializes true", func(t *testing.T) {
		cfg := writeSafetyCostConfig(t, "[agent]\nexperimental_safety_cost_control = true\n")
		requireMaterialized(t, cfg, true)
	})
	t.Run("off master materializes false", func(t *testing.T) {
		cfg := writeSafetyCostConfig(t, "[agent]\nexperimental_safety_cost_control = false\n")
		requireMaterialized(t, cfg, false)
	})
	t.Run("explicit sub-switch is never overwritten", func(t *testing.T) {
		cfg := writeSafetyCostConfig(t, `[agent]
experimental_safety_cost_control = false
safety_loop_streak_note = true
`)
		if cfg.Agent.SafetyLoopStreakNote == nil || !*cfg.Agent.SafetyLoopStreakNote {
			t.Fatal("an explicit sub-switch must survive the materialization untouched")
		}
		if !cfg.SafetyLoopStreakNoteEnabled() {
			t.Fatal("关总开子 must keep reading on (关总开子/开总关子 expressible)")
		}
		if cfg.SafetyIdleTerminateEnabled() || cfg.SafetyEventWaitRecheckEnabled() {
			t.Fatal("nil siblings materialize the off master")
		}
	})
}

// tl1816：迁移结果往返无损——load → render → decode 后三子键携带显式值、
// 总开关行从渲染面消失（下一次保存即弃键）；legacy 键渲染为 false（旧二进制
// 绝不会看到 stale on，task 449 先例）。
func TestSafetyCostControlMergedStateRoundTrips(t *testing.T) {
	cfg := writeSafetyCostConfig(t, `[agent]
experimental_event_wait_recheck = true
`)
	out := RenderTOMLForScope(cfg, RenderScopeUser)
	// 「 =」后缀防 legacy 行注释里的键名误中（legacy 行 comment 仍提总开关键）。
	if strings.Contains(out, "experimental_safety_cost_control =") {
		t.Fatalf("tl1816: the retired master line must be gone from the render face:\n%s", out)
	}
	for _, want := range []string{
		"safety_idle_terminate = true",
		"safety_loop_streak_note = true",
		"safety_event_wait_recheck = true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render face must carry the materialized sub-switch %q:\n%s", want, out)
		}
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
	if !back.SafetyIdleTerminateEnabled() || !back.SafetyLoopStreakNoteEnabled() || !back.SafetyEventWaitRecheckEnabled() {
		t.Fatal("the materialized on-state must round-trip through the render face")
	}
}

// tl1816：幂等——已写实配置二次 load 零改动（idempotent materialization），
// 且渲染面不再携带总开关（save 即弃键的机制级保证）。
func TestSafetyCostControlMasterRetireIdempotent(t *testing.T) {
	cfg := writeSafetyCostConfig(t, "[agent]\nexperimental_safety_cost_control = true\n")
	out := RenderTOMLForScope(cfg, RenderScopeUser)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	again := LoadForEdit(path)
	requireMaterialized(t, again, true)
	reout := RenderTOMLForScope(again, RenderScopeUser)
	if strings.Contains(reout, "experimental_safety_cost_control =") {
		t.Fatalf("tl1816: the retired master must stay gone after a reload:\n%s", reout)
	}
}

// tl1816：无总开关行、无子键行的新式配置（升级后手写删行场景）——三读数
// false（退役总开关自身默认值），加载不报错、不回写总开关。
func TestSafetyCostControlAbsentKeysReadOff(t *testing.T) {
	cfg := writeSafetyCostConfig(t, "[agent]\nmodel = \"m\"\n")
	if cfg.SafetyIdleTerminateEnabled() || cfg.SafetyLoopStreakNoteEnabled() || cfg.SafetyEventWaitRecheckEnabled() {
		t.Fatal("absent sub-switches read off after the master retirement")
	}
}
