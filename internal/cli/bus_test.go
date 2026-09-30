package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteZcodeBusConfigMerge: enrolling must merge into an existing
// workspace config (other settings survive) and produce a connectable MCP
// entry; a corrupt file must fail loudly instead of being clobbered.
func TestWriteZcodeBusConfigMerge(t *testing.T) {
	ws := t.TempDir()
	existing := `{"mcp":{"servers":{"other":{"type":"stdio","command":"foo"}}},"locale":"zh-CN"}`
	if err := os.MkdirAll(filepath.Join(ws, ".zcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ws, ".zcode", "config.json")
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := writeZcodeBusConfig(ws, "dev", "tok-123", "http://127.0.0.1:8787/mcp")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if got != path {
		t.Fatalf("returned path %q, want %q", got, path)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("result not JSON: %v", err)
	}
	if root["locale"] != "zh-CN" {
		t.Fatalf("locale lost: %v", root["locale"])
	}
	servers := root["mcp"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatal("pre-existing server entry lost")
	}
	reasonix := servers["reasonix"].(map[string]any)
	if reasonix["type"] != "streamableHttp" || reasonix["url"] != "http://127.0.0.1:8787/mcp" {
		t.Fatalf("reasonix entry: %+v", reasonix)
	}
	headers := reasonix["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer tok-123" || headers["X-Zcode-Role"] != "dev" {
		t.Fatalf("headers: %+v", headers)
	}

	// A hand-broken file must not be silently overwritten with our merge.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeZcodeBusConfig(ws, "dev", "tok-123", "http://127.0.0.1:8787/mcp"); err == nil {
		t.Fatal("corrupt config: want error, got nil")
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "{not json") {
		t.Fatal("corrupt config was overwritten")
	}
}
