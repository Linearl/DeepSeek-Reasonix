package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Task 845 (user-ratified three-lane retry semantics): the four retry knobs
// must (a) default to zero — which the agent package maps onto the pre-845
// behavior, so an untouched config stays byte-identical — and (b) round-trip
// through the rendered TOML so a settings save cannot silently drop them
// (the 81/123 lost-save lesson).

func TestRetryKnobsDefaultToZero(t *testing.T) {
	zero := Default()
	if zero.Agent.RetryWaitBudgetMinutes != 0 || zero.Agent.RetryQueueMax != 0 ||
		zero.Agent.QuotaWaitBudgetMinutes != 0 || zero.Agent.QuotaWaitIntervalMinutes != 0 {
		t.Fatalf("default config carries non-zero retry knobs: %+v", zero.Agent)
	}
}

func TestRetryKnobsRoundTripThroughRender(t *testing.T) {
	in := &Config{}
	in.Agent.RetryWaitBudgetMinutes = 20
	in.Agent.RetryQueueMax = 12
	in.Agent.QuotaWaitBudgetMinutes = 60
	in.Agent.QuotaWaitIntervalMinutes = 2
	rendered := RenderTOMLForScope(in, RenderScopeUser)
	for _, key := range []string{"retry_wait_budget_minutes", "retry_queue_max", "quota_wait_budget_minutes", "quota_wait_interval_minutes"} {
		if !strings.Contains(rendered, key+" = ") {
			t.Fatalf("render lost %s — a settings save would silently drop it\n---\n%s", key, rendered)
		}
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}
	if got.Agent.RetryWaitBudgetMinutes != 20 || got.Agent.RetryQueueMax != 12 ||
		got.Agent.QuotaWaitBudgetMinutes != 60 || got.Agent.QuotaWaitIntervalMinutes != 2 {
		t.Fatalf("round-trip drifted: %+v", got.Agent)
	}
}

// The default render must NOT emit the keys (zero = the documented default),
// matching the render contract every other opt-in knob follows.
func TestRetryKnobsAbsentFromDefaultRender(t *testing.T) {
	rendered := RenderTOMLForScope(&Config{}, RenderScopeUser)
	for _, key := range []string{"retry_wait_budget_minutes", "retry_queue_max", "quota_wait_budget_minutes", "quota_wait_interval_minutes"} {
		if strings.Contains(rendered, key+" = ") {
			t.Fatalf("default render emits %s; zero must stay the implicit default\n---\n%s", key, rendered)
		}
	}
}
