package config

import (
	"testing"
)

// Task 477 acceptance ③: the ask-timeout wait is configurable 1..3600s. The
// setter refuses out-of-range values (an honest retry beats a silently
// rewritten number) and the reader clamps/defaults so a hand-edited config can
// neither spin the timeout to zero nor reopen an hour-plus hang.
func TestSetAutopilotAskWaitSecondsBounds(t *testing.T) {
	c := &Config{}
	sentinel := 15
	c.Desktop.AutopilotAskWaitSeconds = sentinel
	for _, bad := range []int{-1, 0, 3601, 86400} {
		if err := c.SetAutopilotAskWaitSeconds(bad); err == nil {
			t.Fatalf("SetAutopilotAskWaitSeconds(%d) = nil error, want a refusal", bad)
		}
		if c.Desktop.AutopilotAskWaitSeconds != sentinel {
			t.Fatalf("refused value %d was stored anyway (field = %d)", bad, c.Desktop.AutopilotAskWaitSeconds)
		}
	}
	for _, good := range []int{1, 15, 3600} {
		if err := c.SetAutopilotAskWaitSeconds(good); err != nil {
			t.Fatalf("SetAutopilotAskWaitSeconds(%d) = %v, want accepted", good, err)
		}
		if c.Desktop.AutopilotAskWaitSeconds != good {
			t.Fatalf("stored %d, want %d", c.Desktop.AutopilotAskWaitSeconds, good)
		}
	}
}

func TestAutopilotAskWaitSecondsEffectiveDefaultsAndClamps(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.AutopilotAskWaitSecondsEffective(); got != AutopilotAskWaitDefaultSeconds {
		t.Fatalf("nil config effective wait = %d, want the %ds default", got, AutopilotAskWaitDefaultSeconds)
	}
	empty := &Config{}
	if got := empty.AutopilotAskWaitSecondsEffective(); got != AutopilotAskWaitDefaultSeconds {
		t.Fatalf("unset effective wait = %d, want the %ds default", got, AutopilotAskWaitDefaultSeconds)
	}
	for raw, want := range map[int]int{
		-5:   AutopilotAskWaitDefaultSeconds, // garbage reads as the default
		1:    1,
		15:   15,
		3600: 3600,
		7200: 3600, // hand-edited past the ceiling clamps down
	} {
		c := &Config{}
		c.Desktop.AutopilotAskWaitSeconds = raw
		if got := c.AutopilotAskWaitSecondsEffective(); got != want {
			t.Fatalf("raw %d effective wait = %d, want %d", raw, got, want)
		}
	}
}

func TestSetExperimentalAutopilotAskTimeoutToggles(t *testing.T) {
	c := &Config{}
	if c.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatal("the sub-option must default to off (铁律 2)")
	}
	if err := c.SetExperimentalAutopilotAskTimeout(true); err != nil {
		t.Fatalf("enable = %v, want nil", err)
	}
	if !c.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatal("enable did not land")
	}
	if err := c.SetExperimentalAutopilotAskTimeout(false); err != nil {
		t.Fatalf("disable = %v, want nil", err)
	}
	if c.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatal("disable did not land")
	}
}
