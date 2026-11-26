package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// 任务 722 验收：安全/成本控制细粒度子开关——
//	① 显式值优先：关总开子=开、开总关子=关（tl1816 后总开关已退役，
//	  「关总开子/开总关子」由「显式子键 vs 兄弟子键缺省」表达）；
//	② 条件渲染：nil 子键不落盘（否则任一次保存都会把跟随者转成显式关——
//	  81/123 丢存教训的反向面；tl1816 后 nil 只在 hand-built/hand-edited
//	  配置出现，读数=退役总开关自身默认 false），非 nil 键逐值往返；
//	③ setter 只写对应子键，不动兄弟子键与已退役总开关。
// tl1816（方案A 一次性迁移）语义收口：nil 继承总开关的旧读数面退役——
// load 时 migrateSafetyCostControlMasterRetire 把 nil 全部写实（行为逐位
// 不变），此后 Effective 读数只看显式子键（缺省=false）。
func TestSafetyCostSubSwitchInheritAndOverride(t *testing.T) {
	t.Run("explicit override wins both ways", func(t *testing.T) {
		// 显式开=开（旧「关总开子」形态：其余子键缺省读 off）。
		c := Default()
		on := true
		c.Agent.SafetyIdleTerminate = &on
		if !c.SafetyIdleTerminateEnabled() {
			t.Fatal("explicit on must read on")
		}
		if c.SafetyLoopStreakNoteEnabled() || c.SafetyEventWaitRecheckEnabled() {
			t.Fatal("siblings without an explicit value read off after the tl1816 retirement")
		}
		// 显式关=关（旧「开总关子」形态：其余子键显式开）。
		off := false
		c.Agent.SafetyLoopStreakNote = &off
		if c.SafetyLoopStreakNoteEnabled() {
			t.Fatal("explicit off must read off")
		}
		c.Agent.SafetyEventWaitRecheck = &on
		if !c.SafetyEventWaitRecheckEnabled() || !c.SafetyIdleTerminateEnabled() {
			t.Fatal("explicit on siblings keep reading on")
		}
	})

	t.Run("nil reads off and nil keys never render, non-nil round-trip", func(t *testing.T) {
		c := Default()
		if c.SafetyIdleTerminateEnabled() || c.SafetyLoopStreakNoteEnabled() || c.SafetyEventWaitRecheckEnabled() {
			t.Fatal("nil sub-switches read off after the tl1816 master retirement (retired master default)")
		}
		rendered := RenderTOML(c)
		for _, key := range []string{"safety_idle_terminate", "safety_loop_streak_note", "safety_event_wait_recheck"} {
			if strings.Contains(rendered, key+" =") {
				t.Fatalf("nil sub-switch %q must stay absent from the render face", key)
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
		if got.SafetyLoopStreakNoteEnabled() {
			t.Fatal("nil sibling reads off after round-trip (tl1816: no master to inherit)")
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
			t.Fatal("a sub-switch setter must not flip the retired master tombstone")
		}
	})
}
