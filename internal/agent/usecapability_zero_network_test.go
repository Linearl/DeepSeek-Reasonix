package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/capability"
	"reasonix/internal/config"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// guardDefaultTransportDials swaps http.DefaultTransport for a clone whose
// dialer counts and refuses every connection attempt. The MCP http transport
// derives its base transport from http.DefaultTransport (via netclient), so any
// network attempt made during the window is caught — the zero-attempt assertion
// demanded by task 168's acceptance ("全程零网络请求，测试断言零连接尝试").
func guardDefaultTransportDials(t *testing.T) func() int {
	t.Helper()
	var attempts atomic.Int32
	previous := http.DefaultTransport
	base, ok := previous.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is %T, want *http.Transport", previous)
	}
	guarded := base.Clone()
	guarded.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		attempts.Add(1)
		return nil, fmt.Errorf("task 168 guard: unexpected network dial %s %s", network, addr)
	}
	http.DefaultTransport = guarded
	t.Cleanup(func() { http.DefaultTransport = previous })
	return func() int { return int(attempts.Load()) }
}

// Task 168 regression: use_capability search/list/inspect are pure local
// catalog queries. A configured-but-never-connected overseas http MCP server
// (the exa incident: 108s hang on a cold-start search) must be listed as
// "configured" (unconnected) without any probe, connection, or tools/list.
func TestUseCapabilityDiscoveryColdStartMakesZeroNetworkAttempts(t *testing.T) {
	t.Setenv("REASONIX_CACHE_HOME", t.TempDir())
	dialAttempts := guardDefaultTransportDials(t)
	plugin.ResetProtocolMetricsForTest()
	host := plugin.NewHost()
	defer host.Close()

	specs := []plugin.Spec{{
		Name: "exa", Type: "http", URL: "https://mcp.exa.ai/mcp", Authorized: true,
	}}
	cached := map[string][]plugin.CachedTool{"exa": {{
		Name:        "web_search_advanced_exa",
		Description: "Advanced web search over the internet",
		ReadOnly:    true,
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"web search query"}}}`),
	}}}
	catalog := func() capability.Catalog {
		return capability.BuildCatalog(capability.CatalogOptions{
			Plugins:     []config.PluginEntry{{Name: "exa"}},
			Connected:   map[string]bool{},
			Failed:      map[string]string{},
			CachedTools: cached,
			CacheKeyOK:  map[string]bool{"exa": true},
		})
	}
	runtime := NewMCPCapabilityRuntime(context.Background(), host, specs, tool.NewRegistry(), catalog)
	proxy := runtime.NewFrontend(nil, nil)

	// search: cold start, <2s, zero dials, unconnected entry labelled configured.
	started := time.Now()
	out, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"search","query":"web search exa"}`))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("cold-start search took %s, want <2s", elapsed)
	}
	if got := dialAttempts(); got != 0 {
		t.Fatalf("search made %d network dial attempts, want 0", got)
	}
	if got := plugin.ToolsListCount(); got != 0 {
		t.Fatalf("search issued %d remote tools/list calls, want 0", got)
	}
	var payload struct {
		Results []struct {
			CapabilityID string `json:"capability_id"`
			Status       string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode search result: %v\n%s", err, out)
	}
	found := false
	for _, result := range payload.Results {
		if result.CapabilityID == "mcp-tool:exa/web_search_advanced_exa" {
			found = true
			if result.Status != string(capability.StatusConfigured) {
				t.Fatalf("unconnected exa tool status = %q, want %q (unconnected entries must be labelled, not probed)", result.Status, capability.StatusConfigured)
			}
		}
	}
	if !found {
		t.Fatalf("search results missing mcp-tool:exa/web_search_advanced_exa:\n%s", out)
	}

	// list: same guarantees; the unconnected server stays "configured".
	started = time.Now()
	listOut, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("cold-start list took %s, want <2s", elapsed)
	}
	if got := dialAttempts(); got != 0 {
		t.Fatalf("list made %d network dial attempts, want 0", got)
	}
	var listPayload struct {
		Servers []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			Connected bool   `json:"connected"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(listOut), &listPayload); err != nil {
		t.Fatalf("decode list result: %v\n%s", err, listOut)
	}
	sawExa := false
	for _, server := range listPayload.Servers {
		if server.Name == "exa" {
			sawExa = true
			if server.Connected {
				t.Fatal("exa reported connected without any connection attempt")
			}
			if server.Status != "configured" && server.Status != "ready" {
				t.Fatalf("exa list status = %q, want configured", server.Status)
			}
		}
	}
	if !sawExa {
		t.Fatalf("list output missing exa server:\n%s", listOut)
	}

	// inspect: still zero dials; the unconnected server says how to connect
	// instead of connecting.
	inspectOut, err := proxy.Execute(context.Background(), json.RawMessage(`{"action":"inspect","capability_id":"mcp-server:exa"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := dialAttempts(); got != 0 {
		t.Fatalf("inspect made %d network dial attempts, want 0", got)
	}
	if !strings.Contains(inspectOut, "not connected") {
		t.Fatalf("inspect of unconnected server lacks the not-connected note:\n%s", inspectOut)
	}
}
