package agent

import (
	"strings"

	"reasonix/internal/tool"
)

// restrictedCapabilityProxy preserves a subagent allowed-tools boundary when
// MCP is available only through use_capability. The pseudo mcp-tool: and
// mcp-server: entries never become provider tools; they select one proxy schema
// whose resolver rejects every capability outside the exact allowlist.
//
// Provider-visible name/description/schema stay identical to the unrestricted
// proxy so allowlist expansion never changes the child cache prefix. Allowlist
// enforcement is host-local (check + filtered list results).
type restrictedCapabilityProxy struct {
	tool.Tool
	resolver tool.CallResolver
	allowed  map[string]bool
	// servers is the set of MCP server names implied by allowed IDs; list
	// results are filtered to this set so profile isolation covers discovery.
	servers map[string]bool
}

// validMCPServerCapabilityID accepts mcp-server:<non-empty-name> only.
func validMCPServerCapabilityID(id string) (server string, ok bool) {
	return tool.ParseMCPServerReference(id)
}

// validMCPToolCapabilityID accepts mcp-tool:<server>/<tool> with both parts non-empty.
func validMCPToolCapabilityID(id string) (server, raw string, ok bool) {
	return tool.ParseMCPToolReference(id)
}

func serversFromCapabilityAllowlist(allowed map[string]bool) map[string]bool {
	servers := map[string]bool{}
	for id := range allowed {
		id = strings.TrimSpace(id)
		if server, ok := validMCPServerCapabilityID(id); ok {
			servers[server] = true
			continue
		}
		if server, _, ok := validMCPToolCapabilityID(id); ok {
			servers[server] = true
		}
	}
	return servers
}

// attachSubagentCapabilityProxy installs a per-agent use_capability frontend.
// Any parent-copied proxy is replaced so children never share Executor ledger
// state. No allowlist → full proxy. Explicit allowlist with MCP names →
// restricted proxy. Explicit "use_capability" → full proxy. Explicit allowlist
// without MCP entries → no proxy.
func attachSubagentCapabilityProxy(parent, sub *tool.Registry, names []string, runtime *MCPCapabilityRuntime) {
	if sub == nil {
		return
	}
	// Drop any provider-copied use_capability so we always install an isolated
	// frontend (shared Host/runtime, independent ledger/audit).
	if _, ok := sub.Get("use_capability"); ok {
		sub.RemovePrefix("use_capability")
	}
	frontend := newSubagentCapabilityFrontend(parent, runtime)
	if frontend == nil {
		return
	}
	if len(names) == 0 || allowlistRequestsUnrestrictedProxy(names) {
		sub.Add(frontend)
		return
	}
	allowed := mcpCapabilityAllowlist(parent, names)
	if len(allowed) == 0 {
		// Custom allowlist with no valid MCP entries: do not expose the proxy.
		return
	}
	servers := serversFromCapabilityAllowlist(allowed)
	if len(servers) == 0 {
		// Incomplete capability IDs produced an empty server set: fail closed
		// rather than installing a restricted proxy that would list everything.
		return
	}
	resolver, ok := frontend.(tool.CallResolver)
	if !ok {
		return
	}
	sub.Add(&restrictedCapabilityProxy{
		Tool:     frontend,
		resolver: resolver,
		allowed:  allowed,
		servers:  servers,
	})
}

func newSubagentCapabilityFrontend(parent *tool.Registry, runtime *MCPCapabilityRuntime) tool.Tool {
	if runtime != nil {
		return runtime.NewFrontend(nil, nil)
	}
	if parent == nil {
		return nil
	}
	inner, ok := parent.Get("use_capability")
	if !ok {
		return nil
	}
	return cloneCapabilityFrontend(inner)
}

// mcpCapabilityAllowlist converts profile/call tool names into capability IDs
// for the restricted use_capability proxy. Accepts complete mcp-tool:<s>/<t>,
// mcp-server:<s>, model-visible mcp__* names, and wildcards expanded against
// the parent. Incomplete prefixes such as "mcp-server:" or "mcp-tool:foo" are
// rejected so they cannot install a restricted proxy with an empty server set.
func mcpCapabilityAllowlist(parent *tool.Registry, names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	expanded := names
	if parent != nil {
		expanded = expandToolPatterns(parent, names)
	}
	allowed := map[string]bool{}
	for _, name := range expanded {
		name = strings.TrimSpace(name)
		switch {
		case name == "use_capability":
			// Explicit proxy grant is handled as a full frontend by the caller
			// when this is the only MCP-related entry; leave empty here so a
			// bare use_capability allowlist entry still installs unrestricted.
			continue
		case strings.HasPrefix(name, "mcp-server:"):
			if server, ok := validMCPServerCapabilityID(name); ok {
				allowed["mcp-server:"+server] = true
			}
		case strings.HasPrefix(name, "mcp-tool:"):
			if server, raw, ok := validMCPToolCapabilityID(name); ok {
				allowed["mcp-tool:"+server+"/"+raw] = true
			}
		default:
			if parent != nil {
				if tl, ok := parent.Get(name); ok {
					if m, ok := tl.(tool.MCPMetadata); ok {
						server := strings.TrimSpace(m.MCPServerName())
						raw := strings.TrimSpace(m.MCPRawToolName())
						if server != "" && raw != "" {
							allowed["mcp-tool:"+server+"/"+raw] = true
							continue
						}
					}
				}
			}
			if server, raw, ok := tool.SplitMCPName(name); ok {
				allowed["mcp-tool:"+server+"/"+raw] = true
			}
		}
	}
	return allowed
}

func allowlistRequestsUnrestrictedProxy(names []string) bool {
	for _, name := range names {
		if strings.TrimSpace(name) == "use_capability" {
			return true
		}
	}
	return false
}
