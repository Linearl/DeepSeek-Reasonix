package plugin

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

const (
	codeGraphDaemonIdleTimeoutEnv = "CODEGRAPH_DAEMON_IDLE_TIMEOUT_MS"
	// Keep CodeGraph's shared daemon enabled, but do not leave it holding
	// watchers for the upstream default 300s after the last MCP client exits.
	codeGraphDaemonIdleTimeoutDefaultMS = "5000"
)

// ApplyKnownOverrides fills compatibility hints for known MCP servers. These
// are runtime-only adjustments; they do not make a server built-in or change
// startup behavior.
func ApplyKnownOverrides(s Spec, workspaceRoot string) Spec {
	// Task 256: computer-use 0.9.0-preview rejects the SDK's SEP-2575
	// server/discover preamble with "invalid request" before the classic
	// initialize handshake ever runs. Pin it to 2025-06-18 so Connect starts
	// with initialize + notifications/initialized directly. An explicit
	// protocol_version in the config always wins.
	if isComputerUseSpec(s) && strings.TrimSpace(s.ProtocolVersion) == "" {
		s.ProtocolVersion = "2025-06-18"
	}
	if isCodeGraphSpecName(s.Name) {
		if isStdioSpecType(s.Type) {
			if s.Dir == "" {
				s.Dir = strings.TrimSpace(workspaceRoot)
			}
			s.Env = mergeDefaultEnv(s.Env, codeGraphDaemonIdleTimeoutEnv, codeGraphDaemonIdleTimeoutDefaultMS)
		}
		// CodeGraph does full-tree indexing + file-watching; run it below normal
		// scheduling priority so a background indexer can never starve the user's
		// machine (#3797, #2992). The proc-level mechanism already exists but was
		// never wired to the spec, so it stayed disabled.
		s.LowPriority = true
	}
	if isCodebaseMemorySpec(s) {
		if isStdioSpecType(s.Type) && s.Dir == "" {
			s.Dir = strings.TrimSpace(workspaceRoot)
		}
		// codebase-memory-mcp detects the session root from its subprocess cwd
		// during initialize, then optionally starts its own auto-index thread.
		// Its initial full-tree indexing can be CPU-heavy; keep it out of the
		// foreground scheduling lane just like CodeGraph.
		s.LowPriority = true
	}
	return s
}

func isCodeGraphSpecName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "codegraph")
}

// isComputerUseSpec matches the computer-use screen-interaction MCP server
// (Plocr/Reasonix-computer-use), by server name or command fragment.
func isComputerUseSpec(s Spec) bool {
	if strings.EqualFold(strings.TrimSpace(s.Name), "computer-use") ||
		strings.EqualFold(strings.TrimSpace(s.Name), "computer_use") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(s.Command)), "computer-use")
}

func isCodebaseMemorySpec(s Spec) bool {
	if isCodebaseMemoryID(s.Name) || isCodebaseMemoryCommand(s.Command) {
		return true
	}
	return slices.ContainsFunc(s.Args, isCodebaseMemoryID)
}

func isCodebaseMemoryCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	command = strings.ReplaceAll(command, `\`, `/`)
	return isCodebaseMemoryID(filepath.Base(command))
}

func isCodebaseMemoryID(raw string) bool {
	id := strings.ToLower(strings.TrimSpace(raw))
	id = strings.TrimSuffix(id, ".exe")
	id = strings.TrimPrefix(id, "io.github.deusdata/")
	if strings.HasPrefix(id, "codebase-memory-mcp@") {
		return true
	}
	switch id {
	case "codebase-memory-mcp", "codebase-memory":
		return true
	default:
		return false
	}
}

func isStdioSpecType(typ string) bool {
	typ = strings.ToLower(strings.TrimSpace(typ))
	return typ == "" || typ == "stdio"
}

func mergeDefaultEnv(existing map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(existing)+1)
	maps.Copy(out, existing)
	if _, ok := out[key]; !ok {
		out[key] = value
	}
	return out
}
