package boot

import (
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

// Task 1056 方案 B: providerBudgetFromConfig is the sole seam where the
// [network.retry_budget] lab switch (default off) reaches the concrete
// dialer. Off-state must be a hard nil — no Extra key, no transport change —
// so a config that never touched the switch builds exactly today's provider.

func TestProviderBudgetFromConfigOffStateIsNil(t *testing.T) {
	if b := providerBudgetFromConfig(nil); b != nil {
		t.Fatalf("nil config = %+v, want nil (no Extra key)", b)
	}
	if b := providerBudgetFromConfig(config.Default()); b != nil {
		t.Fatalf("default config = %+v, want nil (switch off by default)", b)
	}
	// Knobs written without the switch must stay inert — no Extra key either.
	c := config.Default()
	c.Network.RetryBudget.DialTimeoutSeconds = 8
	if b := providerBudgetFromConfig(c); b != nil {
		t.Fatalf("knobs-without-switch = %+v, want nil", b)
	}
}

func TestProviderBudgetFromConfigOnState(t *testing.T) {
	c := config.Default()
	if err := c.SetNetworkRetryBudget(config.RetryBudgetConfig{
		Enabled:            true,
		DialTimeoutSeconds: 8,
	}); err != nil {
		t.Fatalf("set retry budget: %v", err)
	}
	b := providerBudgetFromConfig(c)
	if b == nil {
		t.Fatal("on-state must produce a non-nil transport budget")
	}
	if b.DialTimeout != 8*time.Second || !b.ForceDNSRefresh {
		t.Fatalf("budget = %+v, want an 8s dial timeout with DNS refresh forced", b)
	}
}

// TransportTuning consumes the same Extra key the on-state path stamps, and
// the off-state default (30s) must survive untouched when the key is absent.
func TestTransportTuningOffStateKeepsDefault(t *testing.T) {
	dialTimeout, forceDNS := provider.TransportTuning(nil)
	if dialTimeout != provider.DefaultDialTimeout || forceDNS {
		t.Fatalf("nil Extra = (%v, %v), want (%v, false)", dialTimeout, forceDNS, provider.DefaultDialTimeout)
	}
}

// NewProviderWithProxyAndModelInfo (legacy signature, no config in scope)
// must keep behaving exactly as before — the config-aware variant is opt-in,
// never a silent behaviour change for callers that pass no config.
func TestLegacyProviderBuilderIgnoresRetryBudget(t *testing.T) {
	entry := &config.ProviderEntry{
		Name: "openai", PresetID: "openai", Kind: "openai",
		BaseURL: "https://api.openai.com/v1", Model: "gpt-4o",
	}
	// No assertion on the returned provider's internals — the point is that
	// the legacy path compiles and runs without a config argument, and that
	// providerBudgetFromConfig(nil) above proves no Extra key gets injected.
	if _, err := NewProviderWithProxyAndModelInfo(entry, netclient.ProxySpec{}, nil); err != nil {
		t.Fatalf("legacy builder must not error on a plain entry: %v", err)
	}
}
