package main

import (
	"fmt"
	"sort"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/tool"
)

// SetSubagentPolicyForTab sets the per-session sub-agent delegation tier for a
// tab (light|balanced|aggressive). The value is persisted by the controller to
// the session's BranchMeta, so it survives restart. Returns an error on an
// invalid tier or a missing tab.
func (a *App) SetSubagentPolicyForTab(tabID, policy string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return fmt.Errorf("tab not found")
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return fmt.Errorf("tab changed while setting subagent policy")
	}
	tab.subagentPolicy = policy
	ctrl := tab.Ctrl
	a.mu.Unlock()
	if ctrl != nil {
		if err := ctrl.SetSubagentPolicy(policy); err != nil {
			return err
		}
	}
	a.mu.Lock()
	if a.tabs[tab.ID] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return nil
}

type ToolView struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	ReadOnlyHint    bool   `json:"readOnlyHint,omitempty"`
	DestructiveHint bool   `json:"destructiveHint,omitempty"`
	SchemaError     string `json:"schemaError,omitempty"`
}

// subagentOverrideFor resolves a per-name subagent override with the same
// underscore/hyphen alias fallback the runtime dispatch uses
// (boot.SubagentModelKeys) — an exact-key read would show a legacy `security_review` config entry as "inherit default" while it still won at dispatch time.
func subagentOverrideFor(overrides map[string]string, name string) string {
	for _, key := range boot.SubagentModelKeys(name) {
		if v := strings.TrimSpace(overrides[key]); v != "" {
			return v
		}
	}
	return ""
}

// AvailableSubagentTools lists the tool names a subagent profile's
// "available tools" picker may offer. Scoped to compile-time builtins for
// v1 — MCP/plugin tools are per-session/per-connection and would need a new live-registry accessor on control.Capabilities to enumerate safely; a profile's allowed-tools already degrades gracefully (FilterRegistry drops unknown names silently) if extended to MCP names by hand later. Tools that are always excluded from every subagent regardless of an explicit allowlist (agent.AlwaysHiddenSubagentTools) are left out entirely — they'd be a selectable no-op otherwise.
func (a *App) AvailableSubagentTools() []ToolView {
	hidden := map[string]bool{}
	for _, name := range agent.AlwaysHiddenSubagentTools() {
		hidden[name] = true
	}
	entries := tool.BuiltinContractEntries()
	out := make([]ToolView, 0, len(entries))
	for _, e := range entries {
		if hidden[e.Name] {
			continue
		}
		out = append(out, ToolView{Name: e.Name, Description: e.Description, ReadOnlyHint: e.ReadOnly})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sameStringList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
