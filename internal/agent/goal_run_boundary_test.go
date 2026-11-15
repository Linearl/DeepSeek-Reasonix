package agent

import (
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// tl1604 (#8228 同款「提示指向不存在的配置」): the round-limit pause copy used
// to tell every user to "set max_steps higher or to 0" — but the only config
// key by that name ([agent].max_steps / planner_max_steps) is retired: the
// adaptive progress policy replaced it and config migration strips the keys
// from user TOML (internal/config load.go + config_key_warn.go). The copy must
// either name a registered user-settable control (CLI --max-steps flag or the
// [bot].max_steps config key) or carry no set-instruction at all. The
// always-valid next step — sending another message to continue — stays in
// every variant.
func TestMaxStepsPauseCopyNamesExistingControls(t *testing.T) {
	// ① Generic runtime key (desktop / task-spawned subagent default): no
	// user-facing config item named max_steps exists, so the copy must not
	// ask the user to set one.
	generic := (&maxStepsPause{steps: 40, key: genericMaxStepsKey}).Error()
	if strings.Contains(generic, "set ") {
		t.Fatalf("generic-key copy must not tell the user to set a nonexistent config item: %q", generic)
	}
	if strings.Contains(generic, genericMaxStepsKey) {
		t.Fatalf("generic-key copy should not surface the raw runtime key as a control name: %q", generic)
	}
	if !strings.Contains(generic, "send another message to continue") {
		t.Fatalf("generic-key copy lost the continuation next step: %q", generic)
	}

	// ② The whitelist itself is pinned: both known real controls must stay
	// registered, or the set-instruction silently disappears for CLI/bot runs.
	if len(userSettableStepLimitKeys) != 2 {
		t.Fatalf("userSettableStepLimitKeys = %v, want exactly the two registered controls", userSettableStepLimitKeys)
	}
	for _, key := range []string{"--max-steps", "bot.max_steps"} {
		if _, ok := userSettableStepLimitKeys[key]; !ok {
			t.Fatalf("userSettableStepLimitKeys lost %q — re-register it or update the copy contract", key)
		}
	}

	// ③ Registered controls keep their set-instruction with the exact key.
	for key := range userSettableStepLimitKeys {
		msg := (&maxStepsPause{steps: 12, key: key}).Error()
		want := "set " + key + " higher or to 0 for no limit"
		if !strings.Contains(msg, want) {
			t.Fatalf("copy for settable key %q lost the set-instruction %q: %q", key, want, msg)
		}
		if !strings.Contains(msg, "send another message to continue") {
			t.Fatalf("copy for settable key %q lost the continuation next step: %q", key, msg)
		}
	}

	// ④ The registered config key must decode through the real config schema:
	// Config.Bot carries toml:"bot" and BotConfig.MaxSteps carries
	// toml:"max_steps", so the copy's [bot].max_steps path exists in the
	// decoder. (The CLI --max-steps flag is compile-checked at its
	// registration site in internal/cli/cli.go.)
	cfgType := reflect.TypeOf(config.Config{})
	botField, ok := cfgType.FieldByName("Bot")
	if !ok || botField.Tag.Get("toml") != "bot" {
		t.Fatalf(`config.Config.Bot missing or retagged: field ok=%v, want toml:"bot"`, ok)
	}
	stepsField, ok := reflect.TypeOf(config.BotConfig{}).FieldByName("MaxSteps")
	if !ok || stepsField.Tag.Get("toml") != "max_steps" {
		t.Fatalf(`config.BotConfig.MaxSteps missing or retagged: field ok=%v, want toml:"max_steps"`, ok)
	}

	// ⑤ No copy variant may ever name the retired keys.
	all := []string{generic}
	for key := range userSettableStepLimitKeys {
		all = append(all, (&maxStepsPause{steps: 5, key: key}).Error())
	}
	for _, msg := range all {
		if strings.Contains(msg, "agent.max_steps") || strings.Contains(msg, "planner_max_steps") {
			t.Fatalf("copy names a retired config key: %q", msg)
		}
	}
}
