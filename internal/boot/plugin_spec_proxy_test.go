package boot

import (
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
)

// Task 168: the session's resolved proxy must ride on every converted MCP spec
// so http/sse servers route through the user's [network] proxy settings.
func TestPluginSpecsCarryNetworkProxy(t *testing.T) {
	proxy := netclient.ProxySpec{
		Mode: netclient.ModeCustom,
		URL:  "http://127.0.0.1:7890",
	}
	specs := PluginSpecsForRootWithOptions([]config.PluginEntry{
		{Name: "exa", Type: "http", URL: "https://mcp.exa.ai/mcp"},
	}, "", PluginSpecOptions{NetworkProxy: proxy})
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	if specs[0].Proxy.Mode != netclient.ModeCustom || specs[0].Proxy.URL != proxy.URL {
		t.Fatalf("spec proxy = %+v, want the configured NetworkProxy %+v", specs[0].Proxy, proxy)
	}

	// Zero-value options keep the fail-open auto mode (env, then OS proxy).
	fallback := PluginSpecsForRootWithOptions([]config.PluginEntry{
		{Name: "exa", Type: "http", URL: "https://mcp.exa.ai/mcp"},
	}, "", PluginSpecOptions{})
	if got := netclient.NormalizeMode(fallback[0].Proxy.Mode); got != netclient.ModeAuto {
		t.Fatalf("zero-value spec proxy mode = %q, want auto", got)
	}
}
