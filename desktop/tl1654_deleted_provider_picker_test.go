package main

import (
	"testing"

	"reasonix/internal/config"
)

// TestDesktopModelCatalogThreeProviderStates (tl1654 round 3) pins the three
// states the model picker has to tell apart:
//
//  1. 已删 — the user deleted the connection, but the access list still names
//     it (a project-level reasonix.toml entry the settings page can never
//     rewrite). The picker must NOT offer it, even when the credential the
//     deleted connection used is still in Reasonix's global .env: that is
//     exactly the shape of the machine where the deleted "MiMo API" group
//     kept reappearing in the composer dropdown.
//  2. hidden — the connection is still configured and the user parked it on
//     purpose. It stays out of the picker, its section stays, and its refs
//     keep resolving (nothing switches it to another provider).
//  3. live — a normal connection stays in the picker.
func TestDesktopModelCatalogThreeProviderStates(t *testing.T) {
	isolateDesktopUserDirs(t)
	// The credential of the DELETED connection is still on the machine: the
	// compat entry the loader used to rebuild inherited it, which is what made
	// the deleted connection look alive.
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	writeUserConfig := func(entries []config.ProviderEntry, access []string) {
		t.Helper()
		cfg := config.Default()
		cfg.Providers = entries
		cfg.Desktop.ProviderAccess = access
		if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
			t.Fatalf("save user config: %v", err)
		}
	}

	live := config.ProviderEntry{
		Name: "kept-connection", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1",
		Models: []string{"kept-model"},
	}
	hidden := config.ProviderEntry{
		Name: "parked-connection", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1",
		Models: []string{"parked-model"}, Hidden: true,
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

	// State 3 (control): a live connection is offered.
	writeUserConfig([]config.ProviderEntry{live, hidden}, []string{"kept-connection", "parked-connection", "mimo-api"})
	refs := catalogRefs(t.TempDir())
	if !refs["kept-connection/kept-model"] {
		t.Fatalf("live connection missing from the picker: %v", refs)
	}

	// State 2: hidden — out of the picker, but the section and its refs stay.
	if refs["parked-connection/parked-model"] {
		t.Fatalf("hidden connection leaked into the picker: %v", refs)
	}
	loaded, err := config.LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if _, ok := loaded.Provider("parked-connection"); !ok {
		t.Fatal("hidden connection section must survive: it is parked, not deleted")
	}
	if _, ok := loaded.ResolveModel("parked-connection/parked-model"); !ok {
		t.Fatal("hidden connection ref must still resolve")
	}

	// State 1: deleted — the section is gone, the access list still names it,
	// and its credential is still on the machine. The picker must stay clean.
	writeUserConfig([]config.ProviderEntry{live}, []string{"kept-connection", "mimo-api"})
	refs = catalogRefs(t.TempDir())
	for _, ref := range []string{"mimo-api/mimo-v2.5-pro", "mimo-api/mimo-v2.5", "mimo-api/mimo-v2-omni"} {
		if refs[ref] {
			t.Fatalf("deleted connection %q reappeared in the picker: %v", ref, refs)
		}
	}
	if !refs["kept-connection/kept-model"] {
		t.Fatalf("live connection missing from the picker after the delete: %v", refs)
	}

	// The deleted connection's ref must land on the default model instead of
	// keeping the deleted provider alive.
	loaded, err = config.LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if _, ok := loaded.ResolveModel("mimo-api/mimo-v2.5-pro"); ok {
		t.Fatal("a deleted connection's ref must not resolve")
	}
	loaded.DefaultModel = "kept-connection/kept-model"
	resolved, fallback, ok := loaded.ResolveModelWithFallback("mimo-api/mimo-v2.5-pro")
	if !ok || !fallback || resolved != "kept-connection/kept-model" {
		t.Fatalf("historical ref fallback = %q (fallback=%v ok=%v), want kept-connection/kept-model", resolved, fallback, ok)
	}
	// A new session starts on the default model, not on the deleted connection.
	if got := resolveNewSessionModel(loaded); got != "kept-connection/kept-model" {
		t.Fatalf("new session model = %q, want kept-connection/kept-model", got)
	}
}
