package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestProviderHiddenKeepsRefsResolvable: hiding a connection is a display concern
// only. Refs persisted before the flag was set — tab state, session meta, heartbeat
// task overrides — must keep resolving, otherwise the flag would break installs that
// already reference the connection.
func TestProviderHiddenKeepsRefsResolvable(t *testing.T) {
	c := Default()
	if err := c.UpsertProvider(ProviderEntry{
		Name: "deepseek-flash", Kind: "openai", BaseURL: "https://api.deepseek.com",
		Models: []string{"deepseek-v4-flash"}, Default: "deepseek-v4-flash",
		APIKeyEnv: "DEEPSEEK_API_KEY", Hidden: true,
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	entry, ok := c.ResolveModel("deepseek-flash/deepseek-v4-flash")
	if !ok {
		t.Fatal("a hidden provider's <provider>/<model> ref must still resolve")
	}
	if entry.Name != "deepseek-flash" || !entry.Hidden {
		t.Fatalf("resolved entry = %+v", entry)
	}
	if _, ok := c.Provider("deepseek-flash"); !ok {
		t.Fatal("a hidden provider must stay addressable by name")
	}

	// The ref a task persisted before hiding the connection keeps working.
	if ref := entry.Name + "/" + entry.DefaultModel(); ref != "deepseek-flash/deepseek-v4-flash" {
		t.Fatalf("persisted ref changed: %q", ref)
	}
}

// TestRenderProviderHiddenRoundTrips: the flag is written only when set, so a config
// that never uses it renders the exact lines it rendered before, and a config that
// does use it survives a render/decode cycle.
func TestRenderProviderHiddenRoundTrips(t *testing.T) {
	c := Default()
	if err := c.UpsertProvider(ProviderEntry{
		Name: "quiet-one", Kind: "openai", BaseURL: "https://example.com/v1",
		Models: []string{"m"}, APIKeyEnv: "QUIET_KEY", Hidden: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertProvider(ProviderEntry{
		Name: "loud-one", Kind: "openai", BaseURL: "https://example.com/v1",
		Models: []string{"m"}, APIKeyEnv: "LOUD_KEY",
	}); err != nil {
		t.Fatal(err)
	}

	rendered := RenderTOML(c)
	if got := strings.Count(rendered, "hidden      = true"); got != 1 {
		t.Fatalf("expected exactly one hidden line, got %d:\n%s", got, rendered)
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	hidden, ok := got.Provider("quiet-one")
	if !ok || !hidden.Hidden {
		t.Fatalf("hidden flag did not survive the round trip: %+v", hidden)
	}
	visible, ok := got.Provider("loud-one")
	if !ok || visible.Hidden {
		t.Fatalf("a provider without the flag must stay visible: %+v", visible)
	}
}

// TestProviderHiddenSurvivesRuntimeSnapshot: the optimistic-edit log compares
// persisted config snapshots. The flag has to be part of that comparison, otherwise
// toggling it would be silently dropped as "no change".
func TestProviderHiddenSurvivesRuntimeSnapshot(t *testing.T) {
	visible := ProviderEntry{Name: "p", Kind: "openai", BaseURL: "https://example.com/v1"}
	hidden := visible
	hidden.Hidden = true

	if ProviderEntriesConfigEqual(visible, hidden) {
		t.Fatal("toggling hidden must count as a config change")
	}
}
