package main

import (
	"testing"

	"reasonix/internal/config"
)

// TestDesktopModelCatalogSkipsHiddenProviders: connections the user hid must not be
// offered by the model picker (or any other surface that reads the catalog), while
// the connection itself stays fully usable through its `<provider>/<model>` ref.
func TestDesktopModelCatalogSkipsHiddenProviders(t *testing.T) {
	isolateDesktopUserDirs(t)
	// A loopback endpoint needs no credential, so Configured() is true without
	// touching the credential store.

	cfg := config.Default()
	cfg.Providers = []config.ProviderEntry{
		{
			Name: "visible-one", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1",
			Models: []string{"visible-model"},
		},
		{
			Name: "hidden-one", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1",
			Models: []string{"hidden-model"}, Hidden: true,
		},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	loaded, err := config.LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	refs := map[string]bool{}
	a := &App{}
	for _, m := range a.desktopModelCatalog("", t.TempDir(), nil) {
		refs[m.Ref] = true
	}

	if !refs["visible-one/visible-model"] {
		t.Fatalf("visible provider missing from the catalog: %v", refs)
	}
	if refs["hidden-one/hidden-model"] {
		t.Fatalf("hidden provider leaked into the catalog: %v", refs)
	}

	// Hiding is a display concern: the ref must resolve straight from the config.
	if _, ok := loaded.ResolveModel("hidden-one/hidden-model"); !ok {
		t.Fatal("hidden provider ref must still resolve after a reload")
	}
}

// TestDesktopModelCatalogFiltersDisabledThenDeletedProviders (tl1654r2): the
// model picker and the provider settings page must agree on the disable /
// delete lifecycle. Disable (Hidden) removes the connection from every picker;
// delete removes the entry outright — and a stale provider_access reference
// (project-level reasonix.toml or a hand-edited user config) must not resurrect
// the deleted connection in the picker while the settings page shows nothing.
func TestDesktopModelCatalogFiltersDisabledThenDeletedProviders(t *testing.T) {
	isolateDesktopUserDirs(t)

	writeUserConfig := func(entries []config.ProviderEntry, access []string) {
		t.Helper()
		cfg := config.Default()
		cfg.Providers = entries
		cfg.Desktop.ProviderAccess = access
		if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
			t.Fatalf("save user config: %v", err)
		}
	}

	const providerName = "mimo-api"
	entry := func(hidden bool) config.ProviderEntry {
		return config.ProviderEntry{
			Name: providerName, Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1",
			Models: []string{"mimo-v2.5-pro"}, Hidden: hidden,
		}
	}

	a := &App{}
	catalogRefs := func(root string) map[string]bool {
		t.Helper()
		refs := map[string]bool{}
		for _, m := range a.desktopModelCatalog("", root, nil) {
			refs[m.Ref] = true
		}
		return refs
	}

	// Enabled + granted: the control group the filter must not over-prune.
	writeUserConfig([]config.ProviderEntry{entry(false)}, []string{providerName, "deepseek"})
	root := t.TempDir()
	if refs := catalogRefs(root); !refs[providerName+"/mimo-v2.5-pro"] {
		t.Fatalf("enabled provider must stay in the picker: %v", refs)
	}

	// Disabled first (the settings-page toggle): the entry survives but the
	// picker must drop it even though provider_access still lists the name.
	writeUserConfig([]config.ProviderEntry{entry(true)}, []string{providerName, "deepseek"})
	if refs := catalogRefs(t.TempDir()); refs[providerName+"/mimo-v2.5-pro"] {
		t.Fatalf("disabled provider leaked into the picker while access still lists it: %v", refs)
	}

	// Then deleted (access_remove removes the entry): the user config no longer
	// carries the provider, but a stale access reference — the project-level
	// reasonix.toml form the settings page can never rewrite — must not
	// resurrect it in the picker the settings page shows as gone.
	writeUserConfig(nil, []string{providerName, "deepseek"})
	if refs := catalogRefs(t.TempDir()); refs[providerName+"/mimo-v2.5-pro"] {
		t.Fatalf("deleted provider resurrected in the picker from a stale access reference: %v", refs)
	}
}
