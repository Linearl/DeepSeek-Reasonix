package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/capability"
	"reasonix/internal/plugin"
)

// listServerInfo is one configured MCP server entry returned by action=list.
// It never starts a server or opens a network connection.
type listServerInfo struct {
	Name         string `json:"name"`
	CapabilityID string `json:"capability_id"`
	Status       string `json:"status"`
	Authorized   bool   `json:"authorized"`
	Connected    bool   `json:"connected"`
}

// Task 953 list v2 tuning (experimental_capability_groups on): the default
// page is 30 summary rows, each description truncated to the same 240B the
// search tier uses; a detail=true full dump that would exceed
// maxToolOutputBytes is refused with pagination guidance instead of being
// injected and truncated mid-stream by the agent layer.
const (
	listDefaultLimit     = 30
	listMaxLimit         = 50
	listSummaryDescBytes = 240
)

// capInfoForList is one capability row in an action=list payload (both the
// legacy full tier and the task-953 summary tier).
type capInfoForList struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Status      string `json:"status,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"`
	Description string `json:"description,omitempty"`
	// Group is the task-955 pure-metadata group tag (absent when the
	// group layer is off or the entry is untagged).
	Group string `json:"group,omitempty"`
}

// listServersPayload is the decoded compact server summary block.
type listServersPayload struct {
	Servers []listServerInfo `json:"servers"`
	Note    string           `json:"note"`
}

// listCapabilities returns non-MCP catalog entries plus compact MCP server
// summaries. Concrete MCP directories stay behind action=inspect so one global
// list cannot grow with every cached tool description. The top-level "servers"
// key stays compatible with restricted subagent list filtering.
//
// With capabilityGroups on (task 953), the same call returns the summary tier
// (kind/limit/offset paging over 240B descriptions, plus group rows when
// kind is unset or "group") and gates detail=true full dumps on total volume.
// With it off, behavior is exactly the pre-953 full enumeration. Group tag
// on entries is the task-955 pure-metadata attribution (absent when the
// group layer is off or the entry is untagged).
func (t *UseCapabilityTool) listCapabilities(p useCapabilityArgs) (string, error) {
	var caps []capInfoForList
	if t.currentToolResultTarget() != nil {
		caps = append(caps, capInfoForList{
			ID: sessionToolResultCapabilityID, Kind: "session", Name: "tool_result", Status: "ready", ReadOnly: true,
			Description: "Read one bounded page from a complete tool result retained in this agent's current session.",
		})
	}
	if target := t.currentReadStrategyReceiptTarget(); target != nil {
		caps = append(caps, capInfoForList{
			ID: sessionReadStrategyReceiptCapabilityID, Kind: "session", Name: "read_strategy_receipt", Status: "ready", ReadOnly: true,
			Description: target.Description(),
		})
	}
	if t.catalog != nil {
		for _, e := range t.catalog().Entries {
			// Servers already have a compact representation below. Keep concrete
			// MCP tools in the internal catalog for routing, inspect, and known-ID
			// calls, but do not inject every cached directory into model context.
			if e.Kind == capability.KindMCPServer || e.Kind == capability.KindMCPTool {
				continue
			}
			// Skip provider-visible core tools — they are already top-level.
			if e.Kind == capability.KindTool && t.registry != nil && t.registry.ProviderVisible(e.ToolName) {
				continue
			}
			caps = append(caps, capInfoForList{
				ID:          e.ID,
				Kind:        string(e.Kind),
				Name:        e.Name,
				Status:      string(e.Status),
				ReadOnly:    e.ReadOnly,
				Description: e.Description,
				Group:       e.Group,
			})
		}
	}
	serversJSON, err := t.listServers()
	if err != nil {
		return "", err
	}
	var serversPayload listServersPayload
	_ = json.Unmarshal([]byte(serversJSON), &serversPayload)
	if !t.capabilityGroups {
		payload := map[string]any{
			"capabilities": caps,
			"servers":      serversPayload.Servers,
			"note":         "MCP servers are summarized below. Call action=inspect with capability_id=mcp-server:<name> to list one enabled server's tools without starting it, or action=call with a concrete capability_id to invoke a non-core tool, skill, MCP tool, or other catalog entry without changing the provider tool schema.",
		}
		if serversPayload.Note != "" {
			payload["note"] = payload["note"].(string) + " " + serversPayload.Note
		}
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return t.listCapabilitiesGroupTier(p, caps, serversPayload)
}

// listCapabilitiesGroupTier renders the task-953 summary tier: kind filter,
// limit/offset paging over 240B descriptions, group rows, and the two volume
// gates around detail=true full dumps.
func (t *UseCapabilityTool) listCapabilitiesGroupTier(p useCapabilityArgs, caps []capInfoForList, serversPayload listServersPayload) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(p.Kind))
	switch kind {
	case "":
	case "skill", "tool", "source", "session":
		filtered := caps[:0:0]
		for _, c := range caps {
			if c.Kind == kind {
				filtered = append(filtered, c)
			}
		}
		caps = filtered
	case "mcp":
		// MCP entries never appear in the capabilities array (compact server
		// summaries carry them); kind=mcp narrows to the servers block only.
		caps = nil
	case "group":
		caps = nil
	default:
		return "", capabilityInputErrorf("unknown kind %q for action=list; valid kinds: skill, tool, source, session, mcp, group", p.Kind)
	}

	limit := p.Limit
	if limit == 0 {
		limit = listDefaultLimit
	}
	if limit > listMaxLimit {
		return "", capabilityInputErrorf("limit must be between 1 and %d when provided", listMaxLimit)
	}
	offset := max(p.Offset, 0)

	// Group rows: one disclosure row per group when kind is unset or group.
	var groups []groupRow
	if kind == "" || kind == "group" {
		groups = t.renderGroupRows()
	}

	total := len(caps)
	if p.Detail {
		// Gate 1 — precheck: report total volume BEFORE injecting anything.
		// Gate 2 — refuse a dump that would exceed the injection budget: the
		// agent layer truncates oversized tool output mid-stream, which turns
		// a paged catalog into unreadable fragments; a refusal with paging
		// guidance keeps the model moving.
		full := map[string]any{
			"capabilities": caps,
			"servers":      serversPayload.Servers,
		}
		probe, err := json.MarshalIndent(full, "", "  ")
		if err != nil {
			return "", err
		}
		if len(probe) > maxToolOutputBytes {
			guide := fmt.Sprintf(
				"refused: detail=true full dump would be %d bytes for %d entries, over the %d-byte injection budget. This is a volume guardrail, not a failure. Page instead: {\"action\":\"list\",\"limit\":30,\"offset\":0} then advance offset by 30 (payload reports total and next_offset); or narrow with kind=skill|tool|source|session; or inspect one exact capability_id for its full description.",
				len(probe), total, maxToolOutputBytes)
			payload := map[string]any{
				"refused":  true,
				"total":    total,
				"bytes":    len(probe),
				"budget":   maxToolOutputBytes,
				"guidance": guide,
			}
			b, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
		payload := map[string]any{
			"capabilities": caps,
			"servers":      serversPayload.Servers,
			"total":        total,
			"bytes":        len(probe),
			"note":         "detail=true full dump within the injection budget. " + serversPayload.Note,
		}
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	// Summary tier: truncate every description to the search tier's 240B.
	page := caps
	if offset < len(page) {
		page = page[offset:]
	} else {
		page = nil
	}
	if len(page) > limit {
		page = page[:limit]
	}
	summary := make([]capInfoForList, 0, len(page))
	for _, c := range page {
		c.Description = truncateSearchDescription(c.Description)
		summary = append(summary, c)
	}
	nextOffset := -1
	if offset+len(page) < total {
		nextOffset = offset + len(page)
	}
	payload := map[string]any{
		"capabilities": summary,
		"total":        total,
		"offset":       offset,
		"limit":        limit,
		"servers":      serversPayload.Servers,
		"note":         "Summary tier: descriptions truncated to 240B; call action=inspect with an exact capability_id for the full text. " + serversPayload.Note,
	}
	if nextOffset >= 0 {
		payload["next_offset"] = nextOffset
	} else {
		payload["note"] = payload["note"].(string) + " End of the current kind filter; offset past total returns an empty page."
	}
	if len(groups) > 0 {
		payload["groups"] = groups
		if kind == "" {
			payload["note"] = payload["note"].(string) + " Group rows are listed under groups (also reachable via kind=group); inspect tool:group/<name> for a group's member card."
		}
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// listServers returns sorted configured MCP server names, status, and
// capability IDs without starting servers. Used by Planner discovery when no
// specific capability route was provided.
func (t *UseCapabilityTool) listServers() (string, error) {
	configured := t.configuredServers()
	list := make([]listServerInfo, 0, len(configured))
	for _, server := range configured {
		spec := server.spec
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		// Apply stored project grants without process/network side effects so
		// list status matches resolve/execute authorization.
		resolved := plugin.ResolveStoredAuthorization(context.Background(), spec)
		connected := server.enabled && resolved.ServerAuthorized() && t.host != nil && t.host.HasClientForSpec(resolved)
		status := "configured"
		if !server.enabled {
			status = "disabled"
		} else if connected {
			status = "ready"
		} else if t.host != nil {
			for _, f := range t.host.Failures() {
				if f.Name == name && strings.TrimSpace(f.Error) != "" {
					status = "failed"
					break
				}
			}
		}
		list = append(list, listServerInfo{
			Name:         name,
			CapabilityID: "mcp-server:" + name,
			Status:       status,
			Authorized:   resolved.ServerAuthorized(),
			Connected:    connected,
		})
	}
	b, err := json.MarshalIndent(map[string]any{
		"servers": list,
		"note":    "list does not start MCP servers. Call action=call on mcp-server:<name> to connect after authorization, or mcp-tool:<server>/<tool> for a concrete tool.",
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
