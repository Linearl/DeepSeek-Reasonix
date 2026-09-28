package main

import (
	"context"
	"testing"
)

// Task 279 (provider enable/disable): disabling a provider must remove its
// models from every model picker that reads config — the local desktop
// catalog (composer model switcher, ModelSwitcher.tsx) and the remote-proxied
// catalog both filter on the stored Hidden flag — while an unrelated
// provider's models stay put, and re-enabling restores the exact baseline
// (default-all-enabled zero regression, iron-rule 2).
func TestProviderToggleRemovesModelsFromCatalog(t *testing.T) {
	isolateDesktopUserDirs(t)
	configureSwitchableDefaultModels(t)
	app := NewApp()
	app.ctx = context.Background()

	hasModel := func(list []ModelInfo, ref string) bool {
		for _, m := range list {
			if m.Ref == ref {
				return true
			}
		}
		return false
	}
	assertBoth := func(list []ModelInfo, stage string) {
		if !hasModel(list, "old/old-model") {
			t.Fatalf("%s: missing old/old-model in catalog: %+v", stage, list)
		}
		if !hasModel(list, "new/new-model") {
			t.Fatalf("%s: missing new/new-model in catalog: %+v", stage, list)
		}
	}

	baseline := app.Models()
	assertBoth(baseline, "baseline")
	baselineRemote := app.remoteProxyModelCatalog("")
	assertBoth(baselineRemote, "baseline remote")

	// Disable provider "old" through the settings pipeline (fingerprint CAS).
	enabledOff := false
	result := app.ApplyModelSettings(ModelSettingsChange{
		Kind:                "provider_toggle",
		RequestID:           "disable-old",
		ExpectedFingerprint: app.Settings().ModelSettingsFingerprint,
		Name:                "old",
		Enabled:             &enabledOff,
	})
	if !result.Persisted {
		t.Fatalf("disable not persisted: %+v", result)
	}

	// Enumeration assertion: the disabled provider's model leaves every picker,
	// the other provider's model stays (no collateral filtering).
	if hasModel(app.Models(), "old/old-model") {
		t.Fatal("disabled provider model still present in local catalog")
	}
	if !hasModel(app.Models(), "new/new-model") {
		t.Fatalf("unrelated provider model vanished from local catalog: %+v", app.Models())
	}
	if hasModel(app.remoteProxyModelCatalog(""), "old/old-model") {
		t.Fatal("disabled provider model still present in remote-proxy catalog")
	}
	if !hasModel(app.remoteProxyModelCatalog(""), "new/new-model") {
		t.Fatal("unrelated provider model vanished from remote-proxy catalog")
	}

	// Re-enable → exact baseline restored (enabled-by-default zero regression).
	enabledOn := true
	result = app.ApplyModelSettings(ModelSettingsChange{
		Kind:                "provider_toggle",
		RequestID:           "enable-old",
		ExpectedFingerprint: app.Settings().ModelSettingsFingerprint,
		Name:                "old",
		Enabled:             &enabledOn,
	})
	if !result.Persisted {
		t.Fatalf("enable not persisted: %+v", result)
	}
	restored := app.Models()
	assertBoth(restored, "restored")
	if len(restored) != len(baseline) {
		t.Fatalf("restored catalog differs from baseline: %d vs %d (%+v / %+v)", len(restored), len(baseline), restored, baseline)
	}
	restoredRemote := app.remoteProxyModelCatalog("")
	assertBoth(restoredRemote, "restored remote")
	if len(restoredRemote) != len(baselineRemote) {
		t.Fatalf("restored remote catalog differs from baseline: %d vs %d", len(restoredRemote), len(baselineRemote))
	}
}

// Unknown provider names must be rejected without writing configuration.
func TestProviderToggleUnknownProviderRejected(t *testing.T) {
	isolateDesktopUserDirs(t)
	configureSwitchableDefaultModels(t)
	app := NewApp()
	app.ctx = context.Background()

	enabledOff := false
	result := app.ApplyModelSettings(ModelSettingsChange{
		Kind:                "provider_toggle",
		RequestID:           "disable-ghost",
		ExpectedFingerprint: app.Settings().ModelSettingsFingerprint,
		Name:                "ghost-provider",
		Enabled:             &enabledOff,
	})
	if result.Persisted {
		t.Fatalf("unknown provider accepted: %+v", result)
	}
	if len(result.Issues) == 0 {
		t.Fatal("unknown provider rejection carries no issue")
	}
}
