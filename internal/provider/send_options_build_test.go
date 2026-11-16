package provider

import (
	"reflect"
	"testing"
)

// TestBuildSendOptionsCarriesEveryField pins the tl1632 consolidation: every
// SendOptions field must flow through BuildSendOptions unchanged. A new
// SendOptions field wired into the struct but not into this builder must turn
// this test red instead of silently dropping from all three protocol clients
// (anthropic/openai/responses).
func TestBuildSendOptionsCarriesEveryField(t *testing.T) {
	refresh := func() bool { return true }
	got := BuildSendOptions("p1",
		RequestIdentity{Provider: "p1", DisplayName: "Provider One", Protocol: "anthropic"},
		"KEY_ENV", "settings file", true, true, refresh)
	if got.Provider != "p1" {
		t.Errorf("Provider = %q, want %q", got.Provider, "p1")
	}
	if got.ProviderDisplayName != "Provider One" {
		t.Errorf("ProviderDisplayName = %q, want %q", got.ProviderDisplayName, "Provider One")
	}
	if got.Protocol != "anthropic" {
		t.Errorf("Protocol = %q, want %q", got.Protocol, "anthropic")
	}
	if got.KeyEnv != "KEY_ENV" {
		t.Errorf("KeyEnv = %q, want %q", got.KeyEnv, "KEY_ENV")
	}
	if got.KeySource != "settings file" {
		t.Errorf("KeySource = %q, want %q", got.KeySource, "settings file")
	}
	if !got.KeyPresent {
		t.Error("KeyPresent = false, want true")
	}
	if !got.RetryAuth {
		t.Error("RetryAuth = false, want true")
	}
	if got.RefreshCredential == nil {
		t.Fatal("RefreshCredential = nil, want wired callback")
	}
	if !got.RefreshCredential() {
		t.Error("RefreshCredential() = false, want true")
	}
}

// TestSendOptionsFieldCountPin is the structural half of the tl1632 guard:
// changing the SendOptions field count must update BuildSendOptions (and the
// per-client sendOpts pins) in the same change, or this pin goes red.
func TestSendOptionsFieldCountPin(t *testing.T) {
	if n := reflect.TypeOf(SendOptions{}).NumField(); n != 8 {
		t.Fatalf("SendOptions field count = %d, want 8 — a field changed without updating BuildSendOptions (tl1632 field-drop guard)", n)
	}
}
