package config

import (
	"strings"
	"testing"
)

// Task 544 — ask 自动续跑子选项的配置链测试：setter 直写（默认关）、渲染表
// 固定键块存活（开态落盘、关态不落盘、与 477 对互相独立）。

func TestSetExperimentalAutopilotAskAutoContinueToggles(t *testing.T) {
	c := &Config{}
	if c.Desktop.ExperimentalAutopilotAskAutoContinue {
		t.Fatal("the sub-option must default to off (铁律 2)")
	}
	if err := c.SetExperimentalAutopilotAskAutoContinue(true); err != nil {
		t.Fatalf("enable = %v, want nil", err)
	}
	if !c.Desktop.ExperimentalAutopilotAskAutoContinue {
		t.Fatal("enable did not land")
	}
	if err := c.SetExperimentalAutopilotAskAutoContinue(false); err != nil {
		t.Fatalf("disable = %v, want nil", err)
	}
	if c.Desktop.ExperimentalAutopilotAskAutoContinue {
		t.Fatal("disable did not land")
	}
}

func TestAutopilotAskAutoContinueRoundTripsThroughRender(t *testing.T) {
	c := &Config{}
	if err := c.SetExperimentalAutopilotAskAutoContinue(true); err != nil {
		t.Fatalf("enable = %v", err)
	}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_autopilot_ask_auto_continue = true") {
		t.Fatalf("rendered user config lost the on switch (save would silently drop it)\n---\n%s", out)
	}
}

func TestAutopilotAskAutoContinueUnsetStaysOutOfTheConfig(t *testing.T) {
	c := &Config{}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if strings.Contains(out, "experimental_autopilot_ask_auto_continue") {
		t.Fatalf("an untouched config must not grow the sub-option key\n---\n%s", out)
	}
}

// The sub-option is independent of the 477 pair: it renders on its own even
// when the ask-timeout switch and dial are entirely absent, and vice versa the
// 477 pair's render does not force the 544 key.
func TestAutopilotAskAutoContinueIndependentOf477Pair(t *testing.T) {
	c := &Config{}
	if err := c.SetExperimentalAutopilotAskAutoContinue(true); err != nil {
		t.Fatalf("enable 544 = %v", err)
	}
	out := RenderTOMLForScope(c, RenderScopeUser)
	if !strings.Contains(out, "experimental_autopilot_ask_auto_continue = true") {
		t.Fatalf("544 key missing while 477 pair is unset\n---\n%s", out)
	}
	if strings.Contains(out, "experimental_autopilot_ask_timeout") || strings.Contains(out, "autopilot_ask_wait_seconds") {
		t.Fatalf("enabling 544 must not materialise the 477 pair\n---\n%s", out)
	}

	c = &Config{}
	if err := c.SetExperimentalAutopilotAskTimeout(true); err != nil {
		t.Fatalf("enable 477 = %v", err)
	}
	out = RenderTOMLForScope(c, RenderScopeUser)
	if strings.Contains(out, "experimental_autopilot_ask_auto_continue") {
		t.Fatalf("enabling 477 must not materialise the 544 key\n---\n%s", out)
	}
}
