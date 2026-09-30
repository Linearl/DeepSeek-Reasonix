package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"reasonix/internal/busmcp"
	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

// runBus dispatches `reasonix bus ...` — the CLI face of the task bus. It
// exists so enrolling an external runtime (zcode) is one command instead of a
// hand-edit of two config files that must agree on the same token.
func runBus(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, busUsage)
		return 2
	}
	switch args[0] {
	case "enroll":
		return runBusEnroll(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown bus command %q\n%s", args[0], busUsage)
		return 2
	}
}

const busUsage = `usage: reasonix bus enroll --role <name> --ws <zcode workspace> [--url <mcp endpoint>]

Enrolls one external runtime as a bus role:
  1. generates a 256-bit bearer token and stores it under [serve.bus_mcp]
     in the user config (creating/enabling the section),
  2. writes the MCP server entry into <ws>/.zcode/config.json so the
     runtime's sessions connect with that token,
  3. prints what to do next.

The role name must be [a-z0-9-]+; the bus contact for a role is
"zcode-<role>" (e.g. role "dev" → contact "zcode-dev").
`

var validBusRole = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// defaultBusURL mirrors the serve command's default listen address.
const defaultBusURL = "http://127.0.0.1:8787/mcp"

func runBusEnroll(args []string) int {
	fs := flag.NewFlagSet("bus enroll", flag.ContinueOnError)
	role := fs.String("role", "", "role name ([a-z0-9-]+), e.g. heartbeat or dev")
	ws := fs.String("ws", "", "zcode workspace root (its .zcode/config.json is written)")
	url := fs.String("url", defaultBusURL, "MCP endpoint of the running serve")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !validBusRole.MatchString(*role) {
		fmt.Fprintf(os.Stderr, "bus enroll: --role must match [a-z0-9-]+ (got %q)\n", *role)
		return 2
	}
	if strings.TrimSpace(*ws) == "" {
		fmt.Fprintln(os.Stderr, "bus enroll: --ws is required (zcode workspace root)")
		return 2
	}
	info, err := os.Stat(*ws)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "bus enroll: --ws is not a directory: %s\n", *ws)
		return 2
	}

	token, err := busmcp.GenerateToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus enroll: generate token: %v\n", err)
		return 1
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus enroll: load config: %v\n", err)
		return 1
	}
	if cfg.Serve.BusMCP.Roles == nil {
		cfg.Serve.BusMCP.Roles = map[string]string{}
	}
	cfg.Serve.BusMCP.Enabled = true
	cfg.Serve.BusMCP.Roles[*role] = token
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "bus enroll: save config: %v\n", err)
		return 1
	}

	zcodeCfgPath, err := writeZcodeBusConfig(*ws, *role, token, *url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus enroll: write zcode config: %v\n", err)
		return 1
	}

	fmt.Printf("enrolled role %q as contact zcode-%s\n", *role, *role)
	fmt.Printf("  reasonix side: [serve.bus_mcp] enabled in user config (token stored there)\n")
	fmt.Printf("  zcode side:    %s (mcp server \"reasonix\")\n", zcodeCfgPath)
	fmt.Printf("  endpoint:      %s\n", *url)
	fmt.Println("next: restart `reasonix serve` so the bus routes mount, then check /mcp shows connected in zcode.")
	return 0
}

// writeZcodeBusConfig merges the reasonix MCP server entry into the
// workspace's .zcode/config.json. Merge, not overwrite: the file may already
// carry the workspace's own settings. The token lands here in plaintext by
// necessity (zcode's MCP headers have no env expansion), so the caller is
// expected to keep .zcode/ out of any repo.
func writeZcodeBusConfig(wsRoot, role, token, url string) (string, error) {
	path := filepath.Join(wsRoot, ".zcode", "config.json")
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &root); err != nil {
			return "", fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	}
	mcpSection, _ := root["mcp"].(map[string]any)
	if mcpSection == nil {
		mcpSection = map[string]any{}
		root["mcp"] = mcpSection
	}
	servers, _ := mcpSection["servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		mcpSection["servers"] = servers
	}
	servers["reasonix"] = map[string]any{
		"type": "streamableHttp",
		"url":  url,
		"headers": map[string]any{
			"Authorization": "Bearer " + token,
			"X-Zcode-Role":  role,
		},
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// 0600: the file now carries a bearer token, same posture as the
	// reasonix side.
	if err := fileutil.AtomicWriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
