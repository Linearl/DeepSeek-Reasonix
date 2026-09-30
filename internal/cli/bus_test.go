package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"reasonix/internal/config"
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

var busTokenShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestRunBusEnrollPersistsServeBusMCPSection is the task 432 regression: enroll
// must leave [serve.bus_mcp] (enabled + role token) in the user config ON DISK
// — not just in memory — and report success only after the round trip holds.
// Before the fix the renderer dropped the section, enroll printed success, and
// the file's mtime changed with no bus content inside.
func TestRunBusEnrollPersistsServeBusMCPSection(t *testing.T) {
	isolateCLIConfigHome(t)
	ws := t.TempDir()

	if code := runBusEnroll([]string{"--role", "dev", "--ws", ws}); code != 0 {
		t.Fatalf("runBusEnroll exit = %d, want 0", code)
	}

	b, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read user config after enroll: %v", err)
	}
	rendered := string(b)
	if !strings.Contains(rendered, "[serve.bus_mcp]") {
		t.Fatalf("user config on disk has no [serve.bus_mcp] section after enroll:\n%s", rendered)
	}
	if !strings.Contains(rendered, "enabled = true") {
		t.Fatalf("user config on disk does not enable [serve.bus_mcp]:\n%s", rendered)
	}

	loaded, err := config.LoadUserConfigReadOnly()
	if err != nil {
		t.Fatalf("reload user config: %v", err)
	}
	if !loaded.Serve.BusMCP.Enabled {
		t.Fatal("reloaded config: [serve.bus_mcp] enabled = false, want true")
	}
	token := loaded.Serve.BusMCP.Roles["dev"]
	if !busTokenShape.MatchString(token) {
		t.Fatalf("reloaded role dev token = %q, want a 64-hex token", token)
	}

	// The zcode side must carry the same token, or the two configs disagree.
	zb, err := os.ReadFile(filepath.Join(ws, ".zcode", "config.json"))
	if err != nil {
		t.Fatalf("read zcode config after enroll: %v", err)
	}
	var zroot map[string]any
	if err := json.Unmarshal(zb, &zroot); err != nil {
		t.Fatalf("zcode config not JSON: %v", err)
	}
	servers := zroot["mcp"].(map[string]any)["servers"].(map[string]any)
	headers := servers["reasonix"].(map[string]any)["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer "+token {
		t.Fatalf("zcode token mismatch with reasonix side: %q vs %q", headers["Authorization"], token)
	}
}

// TestVerifyBusEnrollPersisted: the self-check must pass when the section
// round-trips and fail loudly when the saved file lost the enabled flag or the
// role token — the failure mode task 432 shipped with.
func TestVerifyBusEnrollPersisted(t *testing.T) {
	isolateCLIConfigHome(t)

	userPath := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}

	persisted := "[serve.bus_mcp]\nenabled = true\nroles = { dev = \"tok-123\" }\n"
	if err := os.WriteFile(userPath, []byte(persisted), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBusEnrollPersisted("dev", "tok-123"); err != nil {
		t.Fatalf("persisted role: verify = %v, want nil", err)
	}

	if err := os.WriteFile(userPath, []byte(persisted), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBusEnrollPersisted("dev", "other-token"); err == nil {
		t.Fatal("token mismatch: verify = nil, want error")
	}

	if err := os.WriteFile(userPath, []byte("# no bus section\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBusEnrollPersisted("dev", "tok-123"); err == nil {
		t.Fatal("dropped section: verify = nil, want error (this is the task 432 failure mode)")
	}
}
