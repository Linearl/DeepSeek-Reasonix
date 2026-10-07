package main

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"reasonix/internal/config"
)

// Task 468: the provider save chain and the direct setter must both carry the
// HighSpeedModels list. Before this task the editor's save draft omitted the
// field, so editing a connection wiped every high-speed mark; the renderer had
// no high_speed_models row, so even hand-written entries were lost on the next
// save.

func TestTask468SaveProviderCarriesHighSpeedModels(t *testing.T) {
	c := &config.Config{Providers: []config.ProviderEntry{{
		Name: "stable", Kind: "openai", BaseURL: "https://example.com/v1",
		Models: []string{"fast-1", "slow-1"}, APIKeyEnv: "TEST_KEY",
		HighSpeedModels: []string{"fast-1"},
	}}}
	c.DefaultModel = "stable/fast-1"

	view := providerViewFromEntry(c.Providers[0], false, true)
	if len(view.HighSpeedModels) != 1 || view.HighSpeedModels[0] != "fast-1" {
		t.Fatalf("view lost highSpeedModels: %v", view.HighSpeedModels)
	}
	// The panel's model-dialog flow saves through this chain: a view that
	// carries the marks must round-trip into the entry.
	if err := saveProviderConfig(c, view); err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].HighSpeedModels; len(got) != 1 || got[0] != "fast-1" {
		t.Fatalf("save chain lost high-speed mark: %v", got)
	}

	// Marks for models no longer on the list are dropped (stale-mark guard),
	// mirroring the vision_models filter.
	view.HighSpeedModels = []string{"fast-1", "ghost"}
	if err := saveProviderConfig(c, view); err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].HighSpeedModels; len(got) != 1 || got[0] != "fast-1" {
		t.Fatalf("stale mark survived save: %v", got)
	}

	// The rendered config must keep the row — this is the render row this
	// task adds; without it the next save would silently drop the mark.
	if err := saveProviderConfig(c, view); err != nil {
		t.Fatal(err)
	}
	rendered := config.RenderTOML(c)
	if !strings.Contains(rendered, "high_speed_models = [\"fast-1\"]") {
		t.Fatalf("render lost high_speed_models:\n%s", rendered)
	}
	var decoded config.Config
	if _, err := toml.Decode(rendered, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Providers) == 0 {
		t.Fatal("decode lost providers")
	}
	if got := decoded.Providers[0].HighSpeedModels; len(got) != 1 || got[0] != "fast-1" {
		t.Fatalf("high_speed_models lost in decode: %v", got)
	}
}

func TestTask468SetProviderModelHighSpeedBridge(t *testing.T) {
	c := &config.Config{Providers: []config.ProviderEntry{{
		Name: "stable", Kind: "openai", BaseURL: "https://example.com/v1",
		Models: []string{"fast-1", "slow-1"}, APIKeyEnv: "TEST_KEY",
	}}}
	c.DefaultModel = "stable/fast-1"

	if err := c.SetProviderModelHighSpeed("stable", "fast-1", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProviderModelHighSpeed("stable", "slow-1", true); err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].HighSpeedModels; len(got) != 2 {
		t.Fatalf("marks: %v", got)
	}
	if err := c.SetProviderModelHighSpeed("stable", "fast-1", false); err != nil {
		t.Fatal(err)
	}
	if got := c.Providers[0].HighSpeedModels; len(got) != 1 || got[0] != "slow-1" {
		t.Fatalf("after unmark: %v", got)
	}
	if err := c.SetProviderModelHighSpeed("stable", "ghost", true); err == nil {
		t.Fatal("unconfigured model must be rejected")
	}
	if err := c.SetProviderModelHighSpeed("missing", "fast-1", true); err == nil {
		t.Fatal("unknown provider must be rejected")
	}
}
