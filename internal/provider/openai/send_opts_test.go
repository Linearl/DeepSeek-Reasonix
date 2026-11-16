package openai

import (
	"testing"

	"reasonix/internal/provider"
)

// TestSendOptsCarriesEveryField pins the tl1632 consolidation on the OpenAI
// client: sendOpts() must map every field through the shared
// provider.BuildSendOptions — a dropped argument surfaces as a zero field here
// instead of silently losing it on the OpenAI wire.
func TestSendOptsCarriesEveryField(t *testing.T) {
	newClient := func(apiKey string) *client {
		c := &client{
			name:      "p1",
			identity:  provider.RequestIdentity{Provider: "p1", DisplayName: "Provider One", Protocol: "openai"},
			apiKey:    apiKey,
			keyEnv:    "KEY_ENV",
			keySource: "settings file",
		}
		c.authed.Store(true)
		c.reauth = func() (string, bool) { return "rotated-key", true }
		return c
	}

	withKey := newClient("k1")
	got := withKey.sendOpts()
	if got.Provider != "p1" {
		t.Errorf("Provider = %q, want %q", got.Provider, "p1")
	}
	if got.ProviderDisplayName != "Provider One" {
		t.Errorf("ProviderDisplayName = %q, want %q", got.ProviderDisplayName, "Provider One")
	}
	if got.Protocol != "openai" {
		t.Errorf("Protocol = %q, want %q", got.Protocol, "openai")
	}
	if got.KeyEnv != "KEY_ENV" {
		t.Errorf("KeyEnv = %q, want %q", got.KeyEnv, "KEY_ENV")
	}
	if got.KeySource != "settings file" {
		t.Errorf("KeySource = %q, want %q", got.KeySource, "settings file")
	}
	if !got.KeyPresent {
		t.Error("KeyPresent = false, want true when an API key is set")
	}
	if !got.RetryAuth {
		t.Error("RetryAuth = false, want true after authed.Store(true)")
	}
	if got.RefreshCredential == nil {
		t.Fatal("RefreshCredential = nil, want the client's refreshCredential method value")
	}
	// The callback must be bound to this client: calling it swaps the key.
	if !got.RefreshCredential() {
		t.Error("RefreshCredential() = false, want true (reauth wired)")
	}
	if withKey.currentAPIKey() != "rotated-key" {
		t.Errorf("currentAPIKey after refresh = %q, want %q", withKey.currentAPIKey(), "rotated-key")
	}

	// Empty key must map KeyPresent to false — "missing" stays distinguishable
	// from "rejected" on the OpenAI path too.
	if newClient("").sendOpts().KeyPresent {
		t.Error("KeyPresent = true, want false when the API key is empty")
	}
}
