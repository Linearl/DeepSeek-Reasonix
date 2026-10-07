package agent

import (
	"context"
	"sort"
	"strings"
	"sync"

	"reasonix/internal/capability"
	"reasonix/internal/config"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// MCPCapabilityRuntime is the session-shared MCP substrate: Host, boot specs,
// provider-visible registry for already-registered tools, schema cache catalog,
// and live connection snapshots. Each agent (executor, planner, task/fleet
// child) gets its own UseCapabilityTool frontend so ledger/audit never cross
// agent boundaries, while process connections remain on the shared Host.
type MCPCapabilityRuntime struct {
	lifeCtx  context.Context
	host     *plugin.Host
	registry *tool.Registry
	catalog  func() capability.Catalog

	// dispatchMu linearizes server enable/spec mutations against MCP process
	// startup and tools/call. Calls may run concurrently under RLock; a disable,
	// uninstall, or hot update waits for in-flight dispatch and invalidates every
	// target that has not begun its final runtime-bound execution check.
	dispatchMu sync.RWMutex
	mu         sync.RWMutex
	servers    map[string]mcpRuntimeServer
	gates      mcpServerGates
	// shared connection observation across all frontends on this session.
	state       *mcpProxySharedState
	frontendsMu sync.RWMutex
	frontends   map[*UseCapabilityTool]int
}

type mcpRuntimeServer struct {
	entry      config.PluginEntry
	spec       plugin.Spec
	enabled    bool
	cached     []plugin.CachedTool
	cacheKeyOK bool
}

type mcpProxySharedState struct {
	mu        sync.Mutex
	connected map[string]bool
	liveTools map[string][]plugin.CachedTool
}

// hostProfile returns the session host's capability profile. A nil host (tests)
// resolves to core-v1.
func (r *MCPCapabilityRuntime) hostProfile() plugin.HostProfile {
	if r == nil || r.host == nil {
		return plugin.HostProfileCore
	}
	return r.host.Profile()
}

// NewMCPCapabilityRuntime builds the session-shared MCP substrate. lifeCtx owns
// on-demand MCP child process lifetimes; specs must be the boot-converted specs.
func NewMCPCapabilityRuntime(lifeCtx context.Context, host *plugin.Host, specs []plugin.Spec, reg *tool.Registry, catalog func() capability.Catalog) *MCPCapabilityRuntime {
	r := &MCPCapabilityRuntime{
		lifeCtx:   lifeCtx,
		host:      host,
		registry:  reg,
		catalog:   catalog,
		servers:   map[string]mcpRuntimeServer{},
		state:     &mcpProxySharedState{connected: map[string]bool{}},
		frontends: map[*UseCapabilityTool]int{},
	}
	r.ConfigureServers(nil, specs, nil)
	if host != nil {
		host.SubscribeToolListChangesWithReplay(lifeCtx, r.applyToolListChange)
	}
	return r
}

// ConfigureServers replaces the runtime's configured MCP inventory. enabled is
// keyed by server name; nil keeps the standalone/test default that every spec is
// enabled. Boot passes the activation-resolved set so disabled servers are
// visible to discovery but cannot reuse a sibling tab's shared Host client.
func (r *MCPCapabilityRuntime) ConfigureServers(entries []config.PluginEntry, specs []plugin.Spec, enabled map[string]bool) {
	if r == nil {
		return
	}
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	byName := make(map[string]config.PluginEntry, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name != "" {
			byName[name] = runtimePluginEntry(entry)
		}
	}
	next := make(map[string]mcpRuntimeServer, len(specs))
	for _, raw := range specs {
		spec := cloneMCPSpec(raw)
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		entry, ok := byName[name]
		if !ok {
			entry = config.PluginEntry{Name: name}
		}
		isEnabled := true
		if enabled != nil {
			isEnabled = enabled[name]
		}
		cached, keyOK := cachedToolsForSpec(spec, r.hostProfile())
		next[name] = mcpRuntimeServer{
			entry:      entry,
			spec:       spec,
			enabled:    isEnabled,
			cached:     cached,
			cacheKeyOK: keyOK,
		}
	}
	r.mu.Lock()
	previous := r.servers
	r.servers = next
	r.mu.Unlock()
	r.syncRegistryInventory(previous, next)
}

// UpsertServer makes a hot-added or updated MCP spec authoritative for every
// frontend on this controller. Dynamic state stays host-local and never changes
// the provider-visible use_capability schema.
func (r *MCPCapabilityRuntime) UpsertServer(entry config.PluginEntry, raw plugin.Spec, enabled bool) {
	if r == nil {
		return
	}
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	spec := cloneMCPSpec(raw)
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return
	}
	entry = runtimePluginEntry(entry)
	if strings.TrimSpace(entry.Name) == "" {
		entry.Name = name
	}
	cached, keyOK := cachedToolsForSpec(spec, r.hostProfile())
	r.mu.Lock()
	r.servers[name] = mcpRuntimeServer{
		entry:      entry,
		spec:       spec,
		enabled:    enabled,
		cached:     cached,
		cacheKeyOK: keyOK,
	}
	r.mu.Unlock()
	r.setRegistryServerEnabled(name, enabled)
	// Endpoint/tool metadata may have changed. Never route a stale live snapshot
	// across an update; a connected client or the next call will repopulate it.
	r.state.clearServer(name)
}

// SetServerEnabled revokes or restores this controller's right to use a server.
// It is intentionally independent from Host connectivity because desktop tabs
// may share one Host while keeping different enable states.
func (r *MCPCapabilityRuntime) SetServerEnabled(name string, enabled bool) bool {
	if r == nil {
		return false
	}
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	name = strings.TrimSpace(name)
	r.mu.Lock()
	server, ok := r.servers[name]
	if ok {
		server.enabled = enabled
		r.servers[name] = server
	}
	r.mu.Unlock()
	if ok {
		r.setRegistryServerEnabled(name, enabled)
		if !enabled {
			r.state.clearServer(name)
		}
	}
	return ok
}

// RemoveServer removes an uninstalled/runtime-only MCP from discovery and
// clears any live tool snapshot that could otherwise keep it routable.
func (r *MCPCapabilityRuntime) RemoveServer(name string) bool {
	if r == nil {
		return false
	}
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	name = strings.TrimSpace(name)
	r.mu.Lock()
	_, ok := r.servers[name]
	delete(r.servers, name)
	r.mu.Unlock()
	r.setRegistryServerEnabled(name, false)
	r.state.clearServer(name)
	return ok
}

// CatalogState returns deterministic, privacy-minimal routing inputs for this
// controller. Configuration secrets are never copied into the transient route.
func (r *MCPCapabilityRuntime) CatalogState() (entries []config.PluginEntry, cached map[string][]plugin.CachedTool, keyOK map[string]bool, disabled map[string]bool) {
	if r == nil {
		return nil, nil, nil, nil
	}
	r.dispatchMu.RLock()
	defer r.dispatchMu.RUnlock()
	return r.catalogStateLocked()
}

// CapabilityCatalogState returns configuration and live proxy tools from one
// lifecycle generation. Callers must use this combined snapshot when building
// a route: taking the two halves separately can otherwise pair a just-updated
// spec with a stale pre-update live-tool directory.
func (r *MCPCapabilityRuntime) CapabilityCatalogState() (entries []config.PluginEntry, cached map[string][]plugin.CachedTool, keyOK map[string]bool, disabled map[string]bool, proxyTools map[string][]plugin.CachedTool) {
	if r == nil {
		return nil, nil, nil, nil, nil
	}
	r.dispatchMu.RLock()
	defer r.dispatchMu.RUnlock()
	entries, cached, keyOK, disabled = r.catalogStateLocked()
	proxyTools = r.connectedProxyToolsLocked()
	return entries, cached, keyOK, disabled, proxyTools
}

func (r *MCPCapabilityRuntime) catalogStateLocked() (entries []config.PluginEntry, cached map[string][]plugin.CachedTool, keyOK map[string]bool, disabled map[string]bool) {
	r.mu.RLock()
	names := make([]string, 0, len(r.servers))
	for name := range r.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	entries = make([]config.PluginEntry, 0, len(names))
	cached = make(map[string][]plugin.CachedTool, len(names))
	keyOK = make(map[string]bool, len(names))
	disabled = make(map[string]bool)
	for _, name := range names {
		server := r.servers[name]
		entries = append(entries, runtimePluginEntry(server.entry))
		if len(server.cached) > 0 {
			cached[name] = cloneCachedTools(server.cached)
			keyOK[name] = server.cacheKeyOK
		}
		if !server.enabled {
			disabled[name] = true
		}
	}
	r.mu.RUnlock()
	if len(cached) == 0 {
		cached = nil
		keyOK = nil
	}
	if len(disabled) == 0 {
		disabled = nil
	}
	return entries, cached, keyOK, disabled
}

func (r *MCPCapabilityRuntime) configuredServers() []mcpRuntimeServer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	names := make([]string, 0, len(r.servers))
	for name := range r.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]mcpRuntimeServer, 0, len(names))
	for _, name := range names {
		server := r.servers[name]
		server.spec = cloneMCPSpec(server.spec)
		server.entry = runtimePluginEntry(server.entry)
		server.cached = cloneCachedTools(server.cached)
		out = append(out, server)
	}
	r.mu.RUnlock()
	return out
}

func (r *MCPCapabilityRuntime) enabledSpec(server string) (plugin.Spec, bool) {
	if r == nil {
		return plugin.Spec{}, false
	}
	r.mu.RLock()
	configured, ok := r.servers[strings.TrimSpace(server)]
	r.mu.RUnlock()
	if !ok || !configured.enabled {
		return plugin.Spec{}, false
	}
	return cloneMCPSpec(configured.spec), true
}

func (r *MCPCapabilityRuntime) serverEnabled(server string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	configured, ok := r.servers[strings.TrimSpace(server)]
	r.mu.RUnlock()
	return ok && configured.enabled
}

// NewFrontend returns a per-agent use_capability instance. ledger/audit may be
// nil for ordinary sub-agents that do not run Delivery capability gates.
func (r *MCPCapabilityRuntime) NewFrontend(ledger *capability.Ledger, audit *capability.Audit) *UseCapabilityTool {
	if r == nil {
		return NewUseCapabilityTool(context.Background(), nil, nil, nil, ledger, audit, nil)
	}
	frontend := &UseCapabilityTool{
		host:     r.host,
		lifeCtx:  r.lifeCtx,
		runtime:  r,
		registry: r.registry,
		ledger:   ledger,
		audit:    audit,
		catalog:  r.catalog,
		state:    r.state,
	}
	return frontend
}

func (r *MCPCapabilityRuntime) activateFrontend(frontend *UseCapabilityTool) func() {
	if r == nil || frontend == nil {
		return func() {}
	}
	r.frontendsMu.Lock()
	if r.frontends == nil {
		r.frontends = map[*UseCapabilityTool]int{}
	}
	r.frontends[frontend]++
	r.frontendsMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.frontendsMu.Lock()
			if r.frontends[frontend] <= 1 {
				delete(r.frontends, frontend)
			} else {
				r.frontends[frontend]--
			}
			r.frontendsMu.Unlock()
		})
	}
}

func (r *MCPCapabilityRuntime) notifyToolListChanged(server string, tools []tool.Tool) {
	if r == nil {
		return
	}
	schemaBytes := 0
	for _, target := range tools {
		if target != nil {
			schemaBytes += len(target.Schema())
		}
	}
	r.frontendsMu.RLock()
	frontends := make([]*UseCapabilityTool, 0, len(r.frontends))
	for frontend := range r.frontends {
		frontends = append(frontends, frontend)
	}
	r.frontendsMu.RUnlock()
	for _, frontend := range frontends {
		frontend.capabilityAudit().RecordMCPList("remote", "list_changed", 0, len(tools), schemaBytes)
		frontend.observeMCPList(mcpListObservation{
			Server: server, Source: "remote", Trigger: "list_changed",
			ToolCount: len(tools), SchemaBytes: schemaBytes, NetworkCall: true,
		})
	}
}

// ConnectedProxyTools returns live tool metadata for servers connected through
// any frontend on this runtime, keyed by server name.
func (r *MCPCapabilityRuntime) ConnectedProxyTools() map[string][]plugin.CachedTool {
	if r == nil || r.state == nil {
		return nil
	}
	r.dispatchMu.RLock()
	defer r.dispatchMu.RUnlock()
	return r.connectedProxyToolsLocked()
}

func (r *MCPCapabilityRuntime) connectedProxyToolsLocked() map[string][]plugin.CachedTool {
	live := r.state.snapshotLiveTools()
	if len(live) == 0 {
		return nil
	}
	r.mu.RLock()
	for name := range live {
		server, ok := r.servers[name]
		if !ok || !server.enabled {
			delete(live, name)
		}
	}
	r.mu.RUnlock()
	if len(live) == 0 {
		return nil
	}
	return live
}
