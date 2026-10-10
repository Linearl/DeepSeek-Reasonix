package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// 任务 722 验收：安全/成本控制细粒度子开关——
//
//	① nil（缺省，旧配置）继承总开关 experimental_safety_cost_control（默认值=现状）；
//	② 显式覆盖优先：关总开子=开、开总关子=关（子项不再始终跟随总开关）；
//	③ 条件渲染：nil 子键不落盘（否则任一次保存都会把跟随者转成显式关，
//	   总开关就名存实亡——81/123 丢存教训的反向面），非 nil 键逐值往返；
//	④ setter 只写对应子键，不动总开关与兄弟子键。
func TestSafetyCostSubSwitchInheritAndOverride(t *testing.T) {
	t.Run("nil inherits master", func(t *testing.T) {
		c := Default()
		c.Agent.ExperimentalSafetyCostControl = true
		if !c.SafetyIdleTerminateEnabled() || !c.SafetyLoopStreakNoteEnabled() || !c.SafetyEventWaitRecheckEnabled() {
			t.Fatal("nil sub-switches must inherit an on master")
		}
		c.Agent.ExperimentalSafetyCostControl = false
		if c.SafetyIdleTerminateEnabled() || c.SafetyLoopStreakNoteEnabled() || c.SafetyEventWaitRecheckEnabled() {
			t.Fatal("nil sub-switches must inherit an off master")
		}
	})

	t.Run("explicit override wins both ways", func(t *testing.T) {
		// 关总开子。
		c := Default()
		c.Agent.ExperimentalSafetyCostControl = false
		on := true
		c.Agent.SafetyIdleTerminate = &on
		if !c.SafetyIdleTerminateEnabled() {
			t.Fatal("explicit on must win over an off master (关总开子)")
		}
		if c.SafetyLoopStreakNoteEnabled() || c.SafetyEventWaitRecheckEnabled() {
			t.Fatal("siblings without an override must keep following the off master")
		}
		// 开总关子。
		c.Agent.ExperimentalSafetyCostControl = true
		off := false
		c.Agent.SafetyLoopStreakNote = &off
		if c.SafetyLoopStreakNoteEnabled() {
			t.Fatal("explicit off must win over an on master (开总关子)")
		}
		if !c.SafetyIdleTerminateEnabled() || !c.SafetyEventWaitRecheckEnabled() {
			t.Fatal("followers must ride the on master")
		}
	})

	t.Run("nil sub keys never render, non-nil round-trip", func(t *testing.T) {
		c := Default()
		c.Agent.ExperimentalSafetyCostControl = true
		rendered := RenderTOML(c)
		for _, key := range []string{"safety_idle_terminate", "safety_loop_streak_note", "safety_event_wait_recheck"} {
			if strings.Contains(rendered, key+" =") {
				t.Fatalf("nil sub-switch %q must stay absent from the render face (nil = follow)", key)
			}
		}

		off := false
		c.Agent.SafetyIdleTerminate = &off
		on := true
		c.Agent.SafetyEventWaitRecheck = &on
		rendered = RenderTOML(c)
		if !strings.Contains(rendered, "safety_idle_terminate = false") || !strings.Contains(rendered, "safety_event_wait_recheck = true") {
			t.Fatal("non-nil sub-switches must render their explicit values")
		}
		if strings.Contains(rendered, "safety_loop_streak_note =") {
			t.Fatal("nil sibling must stay absent while its siblings render")
		}

		got := Default()
		if _, err := toml.Decode(rendered, got); err != nil {
			t.Fatal(err)
		}
		if got.SafetyIdleTerminateEnabled() {
			t.Fatal("explicit false sub-switch must read false after round-trip")
		}
		if !got.SafetyEventWaitRecheckEnabled() {
			t.Fatal("explicit true sub-switch must read true after round-trip")
		}
		if !got.SafetyLoopStreakNoteEnabled() {
			t.Fatal("nil sibling must still inherit the on master after round-trip")
		}
	})

	t.Run("setters write only their own key", func(t *testing.T) {
		c := Default()
		if err := c.SetSafetyLoopStreakNote(true); err != nil {
			t.Fatal(err)
		}
		if c.Agent.SafetyLoopStreakNote == nil || !*c.Agent.SafetyLoopStreakNote {
			t.Fatal("SetSafetyLoopStreakNote(true) must write an explicit on override")
		}
		if c.Agent.SafetyIdleTerminate != nil || c.Agent.SafetyEventWaitRecheck != nil {
			t.Fatal("a sub-switch setter must not touch its siblings")
		}
		if c.Agent.ExperimentalSafetyCostControl {
			t.Fatal("a sub-switch setter must not flip the master")
		}
	})
}
