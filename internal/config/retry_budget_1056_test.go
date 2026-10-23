package config

import (
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

// Task 1056 方案 B: the [network.retry_budget] family must (a) default to
// off — every knob zero, byte-identical to the pre-1056 config — and (b)
// round-trip through the rendered TOML so a settings save cannot silently
// drop a knob (the 81/123 lost-save lesson).

func TestNetworkRetryBudgetDefaultsToOff(t *testing.T) {
	c := Default()
	if c.Network.RetryBudget.Enabled {
		t.Fatal("retry_budget must default to off")
	}
	b := c.NetworkRetryBudget()
	if b.Enabled || b.MaxRetries != 0 || b.DialTimeout != 0 || b.FastBackoff != 0 || b.WallClock != 0 {
		t.Fatalf("off-state resolved budget = %+v, want the zero value", b)
	}
}

func TestNetworkRetryBudgetRoundTripThroughRender(t *testing.T) {
	c := Default()
	if err := c.SetNetworkRetryBudget(RetryBudgetConfig{
		Enabled:            true,
		MaxRetries:         4,
		DialTimeoutSeconds: 8,
		FastBackoffSeconds: 1,
		WallClockSeconds:   180,
	}); err != nil {
		t.Fatalf("set retry budget: %v", err)
	}
	rendered := RenderTOMLForScope(c, RenderScopeUser)
	for _, key := range []string{"enabled = true", "max_retries = 4", "dial_timeout_seconds = 8", "fast_backoff_seconds = 1", "wall_clock_seconds = 180"} {
		if !strings.Contains(rendered, key) {
			t.Fatalf("render lost %q — a settings save would silently drop it\n---\n%s", key, rendered)
		}
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}
	if !got.Network.RetryBudget.Enabled || got.Network.RetryBudget.MaxRetries != 4 ||
		got.Network.RetryBudget.DialTimeoutSeconds != 8 || got.Network.RetryBudget.FastBackoffSeconds != 1 ||
		got.Network.RetryBudget.WallClockSeconds != 180 {
		t.Fatalf("round-tripped config = %+v, want the written values", got.Network.RetryBudget)
	}
}

// The knob-without-switch case must resolve to the off-state budget: writing a
// non-zero dial_timeout without flipping enabled=true changes nothing at
// runtime (NetworkRetryBudget collapses to zero) — the switch is the only
// thing the runtime reads.
func TestNetworkRetryBudgetKnobsInertWhileDisabled(t *testing.T) {
	c := Default()
	c.Network.RetryBudget.DialTimeoutSeconds = 8
	if b := c.NetworkRetryBudget(); b.Enabled || b.DialTimeout != 0 {
		t.Fatalf("knobs must stay inert while disabled, got %+v", b)
	}
}

func TestSetNetworkRetryBudgetRejectsNegativeValues(t *testing.T) {
	c := Default()
	for _, bad := range []RetryBudgetConfig{
		{Enabled: true, MaxRetries: -1},
		{Enabled: true, DialTimeoutSeconds: -1},
		{Enabled: true, FastBackoffSeconds: -1},
		{Enabled: true, WallClockSeconds: -1},
	} {
		if err := c.SetNetworkRetryBudget(bad); err == nil {
			t.Fatalf("negative knob %+v must be rejected", bad)
		}
	}
}

// Sanity-check the seconds→time.Duration conversion the boot snapshot relies
// on, so a stray millisecond-scale mistake can't silently turn a 8-second
// dial deadline into an 8-nanosecond one.
func TestNetworkRetryBudgetResolvesSecondsToDuration(t *testing.T) {
	c := Default()
	c.Network.RetryBudget = RetryBudgetConfig{Enabled: true, DialTimeoutSeconds: 8, WallClockSeconds: 180}
	b := c.NetworkRetryBudget()
	if b.DialTimeout != 8*time.Second || b.WallClock != 180*time.Second {
		t.Fatalf("resolved durations = dial %v wall %v, want 8s and 180s", b.DialTimeout, b.WallClock)
	}
}
