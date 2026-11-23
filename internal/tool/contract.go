package tool

import (
	"encoding/json"
	"sort"
	"strings"

	"reasonix/internal/provider"
)

// ContractEntry is the provider-visible contract for a tool schema snapshot.
type ContractEntry struct {
	Name        string
	Description string
	ReadOnly    bool
	Schema      json.RawMessage
}

// BuiltinContractEntries returns a stable snapshot of compile-time built-ins.
func BuiltinContractEntries() []ContractEntry {
	return contractEntriesFromTools(Builtins(), nil)
}

func contractEntriesFromTools(tools []Tool, canonical map[string]json.RawMessage) []ContractEntry {
	entries := make([]ContractEntry, 0, len(tools))
	for _, t := range tools {
		schema := provider.CanonicalizeSchema(t.Schema())
		if canonical != nil {
			if c := canonical[t.Name()]; len(c) > 0 {
				schema = append(json.RawMessage(nil), c...)
			}
		}
		entries = append(entries, ContractEntry{
			Name:        t.Name(),
			Description: strings.TrimSpace(t.Description()),
			ReadOnly:    t.ReadOnly(),
			Schema:      schema,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// ContractEntries returns the registry's provider-visible contract snapshot.
// The tool list is captured under the lock, but the per-tool method calls
// (Schema/Description/ReadOnly) run AFTER it is released: a lazy MCP
// placeholder's ReadOnly takes the spawn mutex, and the spawn's trySwap takes
// this registry's write lock — holding the read lock across ReadOnly is an
// AB-BA deadlock (boot's snapshot assembly hit it with a live swap in flight).
func (r *Registry) ContractEntries() []ContractEntry {
	return r.contractEntries(true)
}

// AllContractEntries returns every registered tool's contract, including tools
// hidden from the provider schema. Capability catalogs use this so
// use_capability can list and call tool:<name> targets.
func (r *Registry) AllContractEntries() []ContractEntry {
	return r.contractEntries(false)
}

func (r *Registry) contractEntries(providerVisibleOnly bool) []ContractEntry {
	r.mu.RLock()
	tools := make([]Tool, 0, len(r.order))
	canonical := make(map[string]json.RawMessage, len(r.order))
	for _, name := range r.order {
		if providerVisibleOnly && !r.isProviderVisibleLocked(name) {
			continue
		}
		t := r.tools[name]
		if t == nil {
			continue
		}
		tools = append(tools, t)
		canonical[name] = r.canon[name]
	}
	r.mu.RUnlock()
	return contractEntriesFromTools(tools, canonical)
}
