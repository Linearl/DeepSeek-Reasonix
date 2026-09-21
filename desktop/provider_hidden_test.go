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
