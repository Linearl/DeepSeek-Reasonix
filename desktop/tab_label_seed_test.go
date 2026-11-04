package main

// tl1392: the pre-build tab label used to be the raw provider ref — the
// composer showed e.g. "glm-coding-plan-cn-39e502159f6a62c815…" for the whole
// boot window (seconds) until ctrl.Label() landed at publication. The seed
// now resolves through resolvedTabModelLabel (boot's derivation: entry model
// plus the planner suffix), falling back to the raw ref when the name does
// not resolve.

import (
	"testing"

	"reasonix/internal/config"
)

func tabLabelSeedTestConfig(t *testing.T) {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REASONIX_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com",
			Model: "model-a1", Models: []string{"model-a1", "model-a2"},
			APIKeyEnv: "REASONIX_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// TestResolvedTabModelLabel: the helper must produce boot's label derivation
// — the entry model, not the provider ref — and degrade to the raw input.
func TestResolvedTabModelLabel(t *testing.T) {
	tabLabelSeedTestConfig(t)
	app := NewApp()
	tab := &WorkspaceTab{ID: "tab_label_seed", Scope: "global"}
	cfg, err := config.LoadForRoot(tab.WorkspaceRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := app.resolvedTabModelLabel(tab, cfg, "prov-a/model-a1"); got != "model-a1" {
		t.Fatalf("resolved label = %q, want model-a1 (entry model, not the provider ref)", got)
	}
	// Unknown refs keep the raw input — the old behavior — instead of failing.
	if got := app.resolvedTabModelLabel(tab, cfg, "prov-x/nope"); got != "prov-x/nope" {
		t.Fatalf("unresolvable label = %q, want the raw ref", got)
	}
	// Empty input stays empty; nil config degrades to the raw input.
	if got := app.resolvedTabModelLabel(tab, cfg, "  "); got != "" {
		t.Fatalf("blank label = %q, want empty", got)
	}
	if got := app.resolvedTabModelLabel(tab, nil, "prov-a/model-a1"); got != "prov-a/model-a1" {
		t.Fatalf("nil-config label = %q, want the raw ref", got)
	}
}

// TestAlignReusableBlankTabModelSeedsResolvedLabel: the fenced replacement
// build path (no controller yet) must seed tab.Label with the resolved
// display name before the build starts, so the boot window never shows the
// raw provider ref.
func TestAlignReusableBlankTabModelSeedsResolvedLabel(t *testing.T) {
	tabLabelSeedTestConfig(t)
	app := NewApp()
	// ctx nil keeps startTabControllerBuild synchronous — the seeded label is
	// asserted right after the call returns (publication writes the same
	// boot-derived value).
	tab := &WorkspaceTab{
		ID:          "tab_label_align",
		Scope:       "global",
		Ready:       false,
		model:       "prov-a/model-a2",
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	tab.Ctrl = nil
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		tab.releaseSessionLease()
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	if err := app.alignReusableBlankTabModel(tab, "prov-a/model-a1"); err != nil {
		t.Fatalf("align blank tab: %v", err)
	}
	app.mu.RLock()
	label := tab.Label
	model := tab.model
	app.mu.RUnlock()
	if label != "model-a1" {
		t.Fatalf("pre-build tab.Label = %q, want model-a1 (resolved display name, not the provider ref)", label)
	}
	if model != "prov-a/model-a1" {
		t.Fatalf("tab.model = %q, want prov-a/model-a1 (the canonical ref is untouched by the label seed)", model)
	}
}
