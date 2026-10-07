package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/mcpdiag"
	"reasonix/internal/mcpregistry"
	"reasonix/internal/plugin"
)

// ServerView is one MCP server for the drawer. Status is "connected" (with
// tool/prompt/resource counts), "deferred" (enabled but idle), "failed" (with
// the connection error), "initializing" (background startup in progress), or
// "disabled".
//
// Product fields for the simplified MCP panel are Enabled/Installed/
// Availability/RuntimeState/ToolCount/ToolList/Action. Legacy AutoStart, Tier,
// and StartIntent remain for one major as derived compatibility fields only.
type ServerView struct {
	Name                   string         `json:"name"`
	Transport              string         `json:"transport"`
	Status                 string         `json:"status"`
	HostProfile            string         `json:"hostProfile,omitempty"`
	ElicitationNegotiated  bool           `json:"elicitationNegotiated,omitempty"`
	AppsNegotiated         bool           `json:"appsNegotiated,omitempty"`
	StartIntent            string         `json:"startIntent,omitempty"` // deprecated: derived from Enabled
	RuntimeState           string         `json:"runtimeState,omitempty"`
	ProtocolVersion        string         `json:"protocolVersion,omitempty"`
	SessionState           string         `json:"sessionState,omitempty"`
	ReconnectAttempts      int            `json:"reconnectAttempts,omitempty"`
	ErrorKind              string         `json:"errorKind,omitempty"`
	Availability           string         `json:"availability,omitempty"`
	Enabled                bool           `json:"enabled"`
	Installed              bool           `json:"installed"`
	Action                 string         `json:"action,omitempty"`
	Source                 string         `json:"source,omitempty"`
	ConfigSource           string         `json:"configSource,omitempty"`
	BuiltIn                bool           `json:"builtIn,omitempty"`
	Configured             bool           `json:"configured,omitempty"`
	AutoStart              bool           `json:"autoStart"` // deprecated: same as Enabled
	Tier                   string         `json:"tier,omitempty"`
	Command                string         `json:"command,omitempty"`
	Args                   []string       `json:"args,omitempty"`
	URL                    string         `json:"url,omitempty"`
	EnvKeys                []string       `json:"envKeys,omitempty"`
	HeaderKeys             []string       `json:"headerKeys,omitempty"`
	Tools                  int            `json:"tools"`
	ToolCount              int            `json:"toolCount"`
	Prompts                int            `json:"prompts"`
	Resources              int            `json:"resources"`
	HasTools               bool           `json:"hasTools,omitempty"`
	Error                  string         `json:"error,omitempty"`
	ToolList               []ToolView     `json:"toolList"`
	CallTimeoutSeconds     int            `json:"callTimeoutSeconds,omitempty"`
	ToolTimeoutSeconds     map[string]int `json:"toolTimeoutSeconds,omitempty"`
	RequiresLaunchApproval bool           `json:"requiresLaunchApproval,omitempty"`
	AuthStatus             string         `json:"authStatus,omitempty"`
	AuthURL                string         `json:"authUrl,omitempty"`
	AuthConfigured         bool           `json:"authConfigured,omitempty"`
	ManagedByPlugin        string         `json:"managedByPlugin,omitempty"`
}

// MCPServers returns only MCP server status for settings pages that do not need
// skill discovery.
func (a *App) MCPServers() []ServerView {
	return a.mcpServersView()
}

type MCPMarketplaceEntryView struct {
	Name              string   `json:"name"`
	SuggestedName     string   `json:"suggestedName"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description,omitempty"`
	Version           string   `json:"version,omitempty"`
	RepositoryURL     string   `json:"repositoryUrl,omitempty"`
	Installable       bool     `json:"installable"`
	UnavailableReason string   `json:"unavailableReason,omitempty"`
	Transport         string   `json:"transport,omitempty"`
	Command           string   `json:"command,omitempty"`
	Args              []string `json:"args"`
	URL               string   `json:"url,omitempty"`
}

type MCPMarketplaceView struct {
	Servers []MCPMarketplaceEntryView `json:"servers"`
	Cached  bool                      `json:"cached"`
	Warning string                    `json:"warning,omitempty"`
}

// MCPMarketplace explicitly queries the official MCP Registry. It is only
// called from the settings marketplace; startup and tool discovery never touch
// the network. A query-specific cache keeps the page useful during a registry
// outage without treating cached entries as installed servers.
func (a *App) MCPMarketplace(query string) (MCPMarketplaceView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := mcpregistry.New(mcpRegistryCachePath()).Search(ctx, query, 50)
	if err != nil {
		return MCPMarketplaceView{Servers: []MCPMarketplaceEntryView{}}, err
	}
	view := MCPMarketplaceView{
		Servers: make([]MCPMarketplaceEntryView, 0, len(result.Entries)),
		Cached:  result.Cached,
		Warning: result.Warning,
	}
	for _, entry := range result.Entries {
		view.Servers = append(view.Servers, mcpMarketplaceEntryView(entry))
	}
	return view, nil
}

// MCPMarketplaceResolve re-fetches one Registry entry immediately before the
// settings UI installs it. Offline cache remains useful for browsing, but it is
// never accepted as installation metadata.
func (a *App) MCPMarketplaceResolve(registryName string) (MCPMarketplaceEntryView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entry, _, err := mcpregistry.New(mcpRegistryCachePath()).Resolve(ctx, registryName)
	if err != nil {
		return MCPMarketplaceEntryView{}, err
	}
	if _, err := entry.PluginEntry(""); err != nil {
		return MCPMarketplaceEntryView{}, err
	}
	return mcpMarketplaceEntryView(entry), nil
}

func mcpRegistryCachePath() string {
	if cacheDir := config.CacheDir(); cacheDir != "" {
		return filepath.Join(cacheDir, "mcp-registry-v0.1.json")
	}
	return ""
}

func mcpMarketplaceEntryView(entry mcpregistry.Entry) MCPMarketplaceEntryView {
	return MCPMarketplaceEntryView{
		Name:              entry.Name,
		SuggestedName:     entry.SuggestedName,
		Title:             entry.Title,
		Description:       entry.Description,
		Version:           entry.Version,
		RepositoryURL:     entry.RepositoryURL,
		Installable:       entry.Installable,
		UnavailableReason: entry.UnavailableReason,
		Transport:         entry.Transport,
		Command:           entry.Command,
		Args:              append([]string{}, entry.Args...),
		URL:               entry.URL,
	}
}

// lockRuntimeMutation serializes controller rebuild/teardown operations and
// freezes runtime admission so a captured controller or Host cannot be replaced
// or closed in flight. The caller must not hold App.mu; the lock order is
// runtimeRebuildMu -> runtimeAdmissionMu -> App/Host/Registry.
func (a *App) lockRuntimeMutation(operation string) func() {
	if hook := a.runtimeMutationBeforeLockHook; hook != nil {
		hook(operation)
	}
	a.runtimeRebuildMu.Lock()
	a.runtimeAdmissionMu.Lock()
	return func() {
		a.runtimeAdmissionMu.Unlock()
		a.runtimeRebuildMu.Unlock()
	}
}

// AuthorizeAndConnectMCPServer is retained for older generated Wails clients.
// Project configuration is trusted by default now, so the normal path simply
// reconnects the effective entry. Explicitly gated host specs still record
// their exact launch grant before reconnecting.
func (a *App) AuthorizeAndConnectMCPServer(name string) error {
	defer a.lockMCPMutation("authorize-connect")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP authorization", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	entry, found, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	spec, err := a.mcpLaunchSpec(root, name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if spec.RequireLaunchApproval {
		if err := plugin.AuthorizeProjectSpecLaunch(ctx, spec); err != nil {
			return err
		}
	}

	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	for i := range controllers {
		if controllers[i].ctrl == ctrl {
			controllers[i].enabled = true
		}
	}
	// Drop any previous identity, then start the effective configured server
	// once and refresh every enabled registry sharing this Host.
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	if err := reconnectMCPServerControllers(entry, controllers); err != nil {
		recordMCPFailure(ctrl, entry, err)
		return err
	}
	a.mu.Lock()
	delete(tab.disabledMCP, name)
	a.mu.Unlock()
	return nil
}

type mcpControllerTarget struct {
	ctrl    control.SessionAPI
	enabled bool
}

// lockMCPHostTurnGates freezes every runtime sharing ctrl's Host. Callers hold
// lockMCPMutation, so runtimeAdmissionMu's write side already prevents new turn
// admissions, builds, and teardown while this helper snapshots and gates the
// existing runtimes.
func (a *App) lockMCPHostTurnGates(setting string, ctrl control.SessionAPI) (*plugin.Host, func(), error) {
	if ctrl == nil {
		return nil, nil, fmt.Errorf("no active session")
	}
	host := ctrl.Host()
	release, err := a.lockRuntimeTurnGates(setting, func(tab *WorkspaceTab) bool {
		if host == nil {
			return tab.Ctrl == ctrl
		}
		return tab.Ctrl != nil && tab.Ctrl.Host() == host
	})
	return host, release, err
}

func disconnectMCPServerControllers(name string, preferred control.SessionAPI, controllers []mcpControllerTarget) bool {
	for _, target := range controllers {
		target.ctrl.UnregisterMCPServerTools(name)
	}
	disconnected := false
	if preferred != nil {
		disconnected = preferred.DisconnectMCPServer(name)
	}
	// Every controller owns an independent capability runtime even when the Host
	// process is shared. Reconcile each one after the preferred controller drops
	// the client so remove/update/rollback cannot leave sibling tabs with stale
	// specs or live-tool snapshots.
	for _, target := range controllers {
		if target.ctrl == preferred {
			continue
		}
		disconnected = target.ctrl.DisconnectMCPServer(name) || disconnected
	}
	return disconnected
}

func (a *App) clearMCPServerTabState(name string, controllers []mcpControllerTarget) {
	selected := make(map[control.SessionAPI]bool, len(controllers))
	for _, target := range controllers {
		selected[target.ctrl] = true
	}
	a.mu.Lock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || !selected[tab.Ctrl] {
			continue
		}
		delete(tab.disabledMCP, name)
		tab.mcpOrder = removeServerOrder(tab.mcpOrder, name)
	}
	a.mu.Unlock()
}

// reconnectMCPServerControllers establishes one shared client, then refreshes
// every enabled controller's provider-visible Registry. Disabled tabs remain
// suspended and reconnect only when explicitly enabled.
func reconnectMCPServerControllers(entry config.PluginEntry, controllers []mcpControllerTarget) error {
	var startErrors []error
	connectedTarget := -1
	for i, target := range controllers {
		if !target.enabled {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			startErrors = append(startErrors, err)
			continue
		}
		connectedTarget = i
		break
	}
	if connectedTarget < 0 {
		// All tabs may have disabled this server. Keeping it disconnected
		// preserves their explicit state.
		return errors.Join(startErrors...)
	}

	var refreshErrors []error
	for i, target := range controllers {
		if !target.enabled || i == connectedTarget {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			refreshErrors = append(refreshErrors, err)
		}
	}
	return errors.Join(refreshErrors...)
}

// mcpControllersSharingHost snapshots visible and detached runtimes before
// calling controller methods. App.mu is never held across Host/controller
// locks or network work. preferred (normally the active tab) is returned first.
func (a *App) mcpControllersSharingHost(host *plugin.Host, name string, preferred control.SessionAPI) []mcpControllerTarget {
	if host == nil {
		enabled := true
		a.mu.RLock()
		for _, tab := range a.runtimeTabsLocked() {
			if tab != nil && tab.Ctrl == preferred {
				_, disabled := tab.disabledMCP[name]
				enabled = !disabled
				break
			}
		}
		a.mu.RUnlock()
		return []mcpControllerTarget{{ctrl: preferred, enabled: enabled}}
	}
	a.mu.RLock()
	candidates := make([]mcpControllerTarget, 0, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		_, disabled := tab.disabledMCP[name]
		candidates = append(candidates, mcpControllerTarget{ctrl: tab.Ctrl, enabled: !disabled})
	}
	a.mu.RUnlock()

	byController := make(map[control.SessionAPI]int, len(candidates))
	targets := make([]mcpControllerTarget, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ctrl.Host() != host {
			continue
		}
		if idx, ok := byController[candidate.ctrl]; ok {
			targets[idx].enabled = targets[idx].enabled || candidate.enabled
			continue
		}
		byController[candidate.ctrl] = len(targets)
		targets = append(targets, candidate)
	}
	if len(targets) == 0 {
		return []mcpControllerTarget{{ctrl: preferred, enabled: true}}
	}
	if idx, ok := byController[preferred]; ok && idx > 0 {
		targets[0], targets[idx] = targets[idx], targets[0]
	}
	return targets
}

// lockRuntimeTurnGates locks the turn gate of every runtime tab selected by
// affected (nil selects all visible and detached runtime tabs) in stable tab-ID
// order, then verifies under the gates that no gated controller has active
// runtime work. Callers must hold runtimeRebuildMu and the write side of
// runtimeAdmissionMu (normally through lockMCPMutation), which freezes new turn
// admission, controller builds, and runtime teardown before this snapshot.
// On success the returned release func unlocks the per-tab gates in reverse
// order; on error every gate acquired here is already unlocked.
func (a *App) lockRuntimeTurnGates(setting string, affected func(*WorkspaceTab) bool) (func(), error) {
	a.mu.RLock()
	all := a.runtimeTabsLocked()
	tabs := make([]*WorkspaceTab, 0, len(all))
	for _, tab := range all {
		if tab == nil || (affected != nil && !affected(tab)) {
			continue
		}
		tabs = append(tabs, tab)
	}
	a.mu.RUnlock()
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].ID < tabs[j].ID })
	locked := 0
	release := func() {
		for i := locked - 1; i >= 0; i-- {
			tabs[i].turnStartMu.Unlock()
		}
	}
	for _, tab := range tabs {
		tab.turnStartMu.Lock()
		locked++
	}
	// Read tab.Ctrl under a.mu rather than through controllerForTab: detached
	// runtimes live in detachedSessions, not a.tabs, and their work counts too.
	a.mu.RLock()
	for _, tab := range tabs {
		if err := rebuildControllerActiveWorkErrorFor(tab.Ctrl, setting); err != nil {
			a.mu.RUnlock()
			release()
			return nil, err
		}
	}
	a.mu.RUnlock()
	return release, nil
}

// disconnectMCPServerAllRuntimes removes an uninstalled MCP server from every
// live runtime: all visible and detached runtime tabs, across every shared
// Host — a global plugin uninstall must not leave sibling tabs exposing stale
// provider-visible tools or other workspaces running the removed server.
// DisconnectMCPServer stops the shared client once per Host and drops the tool
// prefix from every other controller's registry.
func (a *App) disconnectMCPServerAllRuntimes(serverName string) bool {
	a.mu.RLock()
	ctrls := make([]control.SessionAPI, 0, len(a.tabs)+len(a.detachedSessions))
	seen := make(map[control.SessionAPI]bool, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil || seen[tab.Ctrl] {
			continue
		}
		seen[tab.Ctrl] = true
		ctrls = append(ctrls, tab.Ctrl)
	}
	a.mu.RUnlock()
	disconnected := false
	for _, ctrl := range ctrls {
		if ctrl.DisconnectMCPServer(serverName) {
			disconnected = true
		}
	}
	return disconnected
}

func (a *App) mcpServersView() []ServerView {
	out := []ServerView{}
	a.mu.RLock()
	tab := a.activeTabLocked()
	if tab == nil {
		a.mu.RUnlock()
		return out
	}
	ctrl := tab.Ctrl
	disabled := make(map[string]ServerView, len(tab.disabledMCP))
	maps.Copy(disabled, tab.disabledMCP)
	order := append([]string(nil), tab.mcpOrder...)
	workspaceRoot := tab.WorkspaceRoot
	tabID := tab.ID
	a.mu.RUnlock()
	if ctrl == nil {
		return out
	}
	seen := map[string]bool{}
	connected := map[string]bool{}
	retainedDisabled := map[string]ServerView{}
	configured := map[string]config.PluginEntry{}
	managedByPlugin := map[string]string{}
	var configuredEntries []config.PluginEntry
	if cfg, err := config.LoadForRoot(workspaceRoot); err == nil {
		configuredEntries = append(configuredEntries, cfg.Plugins...)
		for _, p := range configuredEntries {
			configured[p.Name] = p
			if owner, ok := cfg.PluginPackageOwner(p.Name); ok {
				managedByPlugin[p.Name] = owner
			}
		}
	}
	if h := ctrl.Host(); h != nil {
		for _, s := range h.Servers() {
			if disabledView, ok := disabled[s.Name]; ok {
				disabledView.Status = "disabled"
				disabledView.RuntimeState = "idle"
				disabledView.StartIntent = "off"
				disabledView.Error = ""
				if p, ok := configured[s.Name]; ok {
					disabledView = withPluginConfigInWorkspace(disabledView, p, workspaceRoot)
				}
				out = append(out, disabledView)
				retainedDisabled[s.Name] = disabledView
				seen[s.Name] = true
				delete(disabled, s.Name)
				continue
			}
			seen[s.Name] = true
			connected[s.Name] = true
			view := pluginServerToView(s)
			if p, ok := configured[s.Name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
		for _, f := range h.Failures() {
			seen[f.Name] = true
			view := ServerView{
				Name: f.Name, Transport: f.Transport, Status: "failed", RuntimeState: "issue", Error: f.Error,
				RequiresLaunchApproval: f.RequiresLaunchApproval,
			}
			if p, ok := configured[f.Name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
		for _, name := range h.ConnectingServers() {
			if seen[name] {
				continue
			}
			seen[name] = true
			view := ServerView{Name: name, Status: "initializing", RuntimeState: "connecting"}
			if p, ok := configured[name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
	}
	// Configured servers that are neither connected, connecting, nor failed are
	// idle: disabled/off or automatic background startup waiting for its next kick.
	if len(configuredEntries) > 0 {
		for _, p := range configuredEntries {
			if seen[p.Name] {
				continue
			}
			if s, ok := disabled[p.Name]; ok {
				s.Status = "disabled"
				s.RuntimeState = "idle"
				s.StartIntent = "off"
				s = withPluginConfigInWorkspace(s, p, workspaceRoot)
				s.Error = ""
				out = append(out, s)
				retainedDisabled[p.Name] = s
				seen[p.Name] = true
				delete(disabled, p.Name)
				continue
			}
			status := "disabled"
			startIntent := "off"
			if mcpEntryEnabled(p, workspaceRoot) {
				status = "deferred"
				startIntent = "automatic"
			}
			out = append(out, withPluginConfigInWorkspace(ServerView{Name: p.Name, Status: status, StartIntent: startIntent, RuntimeState: "idle"}, p, workspaceRoot))
			seen[p.Name] = true
		}
	}
	out = orderServerViews(out, order)
	for i := range out {
		out[i].ManagedByPlugin = managedByPlugin[out[i].Name]
		out[i] = finalizeServerView(out[i])
	}

	a.mu.Lock()
	if tab, ok := a.tabs[tabID]; ok {
		for name := range connected {
			delete(retainedDisabled, name)
		}
		tab.disabledMCP = retainedDisabled
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, out)
	}
	a.mu.Unlock()
	return out
}

func mcpEntryEnabled(p config.PluginEntry, workspace string) bool {
	enabled, err := config.DefaultMCPActivationStore().IsEnabled(p, workspace)
	if err != nil {
		return p.ShouldAutoStart()
	}
	return enabled
}

func mcpRuntimeState(status string) string {
	switch status {
	case "connected":
		return "ready"
	case "initializing":
		return "connecting"
	case "failed":
		return "issue"
	default:
		return "idle"
	}
}

func mcpAvailability(v ServerView) string {
	if !v.Enabled {
		return "disabled"
	}
	switch v.RuntimeState {
	case "ready":
		return "connected"
	case "connecting":
		return "starting"
	case "issue":
		if v.RequiresLaunchApproval {
			return "project_auth_changed"
		}
		if v.AuthStatus == "required" || v.AuthStatus == "possible" {
			return "auth_required"
		}
		return "start_failed"
	default:
		// Idle enabled servers are available on demand, not "disconnected".
		return "available_on_demand"
	}
}

func mcpActionForView(v ServerView) string {
	if v.RequiresLaunchApproval {
		return "authorize"
	}
	if v.AuthStatus == "required" {
		return "authenticate"
	}
	if v.RuntimeState == "issue" {
		return "retry"
	}
	return "none"
}

func finalizeServerView(v ServerView) ServerView {
	if v.ToolList == nil {
		v.ToolList = []ToolView{}
	}
	if v.Args == nil {
		v.Args = []string{}
	}
	if v.EnvKeys == nil {
		v.EnvKeys = []string{}
	}
	if v.HeaderKeys == nil {
		v.HeaderKeys = []string{}
	}
	v.ToolCount = v.Tools
	if v.ToolCount == 0 && len(v.ToolList) > 0 {
		v.ToolCount = len(v.ToolList)
		v.Tools = v.ToolCount
	}
	v.Installed = v.Configured || v.BuiltIn || v.Status != ""
	if v.Source == "" {
		switch {
		case v.BuiltIn:
			v.Source = "builtin"
		case v.ManagedByPlugin != "":
			v.Source = "plugin"
		case v.Configured:
			v.Source = "user"
		}
	}
	if v.RuntimeState == "" {
		v.RuntimeState = mcpRuntimeState(v.Status)
	}
	v.Availability = mcpAvailability(v)
	if v.Action == "" {
		v.Action = mcpActionForView(v)
	}
	// Keep deprecated fields derived from the new product state.
	v.AutoStart = v.Enabled
	if !v.Enabled {
		v.StartIntent = "off"
	} else if v.StartIntent == "" {
		v.StartIntent = "automatic"
	}
	return v
}

func withPluginConfig(v ServerView, p config.PluginEntry) ServerView {
	return withPluginConfigInWorkspace(v, p, "")
}

func withPluginConfigInWorkspace(v ServerView, p config.PluginEntry, workspace string) ServerView {
	tt := p.Type
	if tt == "" {
		tt = "stdio"
	}
	v.Transport = tt
	v.Configured = true
	v.Installed = true
	v.Source, v.ConfigSource = mcpServerSource(p.Source)
	v.Enabled = mcpEntryEnabled(p, workspace)
	v.AutoStart = v.Enabled
	v.Tier = p.ResolvedTier()
	if v.StartIntent == "" {
		if v.Enabled {
			v.StartIntent = "automatic"
		} else {
			v.StartIntent = "off"
		}
	}
	if !v.Enabled || v.Status == "disabled" {
		v.Status = "disabled"
		v.StartIntent = "off"
		v.RuntimeState = "idle"
	}
	if v.RuntimeState == "" {
		v.RuntimeState = mcpRuntimeState(v.Status)
	}
	v.Command = p.Command
	v.Args = append([]string(nil), p.Args...)
	v.URL = p.URL
	v.CallTimeoutSeconds = p.CallTimeoutSeconds
	v.ToolTimeoutSeconds = cloneStringIntMap(p.ToolTimeoutSeconds)
	// Configured MCP entries are explicit installs, including project sources.
	v.RequiresLaunchApproval = false
	v.AuthConfigured = mcpdiag.HasAuthConfig(p.Headers, p.Env, p.URL)
	v.EnvKeys = nil
	v.HeaderKeys = nil
	if len(p.Env) > 0 {
		v.EnvKeys = make([]string, 0, len(p.Env))
		for k := range p.Env {
			v.EnvKeys = append(v.EnvKeys, k)
		}
		sort.Strings(v.EnvKeys)
	}
	if len(p.Headers) > 0 {
		v.HeaderKeys = make([]string, 0, len(p.Headers))
		for k := range p.Headers {
			v.HeaderKeys = append(v.HeaderKeys, k)
		}
		sort.Strings(v.HeaderKeys)
	}
	auth := mcpdiag.DiagnoseAuth(v.Transport, v.Status, v.Error, v.URL, v.AuthConfigured)
	v.AuthStatus = auth.Status
	v.AuthURL = auth.URL
	return v
}

func mcpServerSource(source config.MCPConfigSource) (kind, configSource string) {
	switch source {
	case config.MCPSourceProjectConfig:
		return "project", "reasonix.toml"
	case config.MCPSourceProjectMCPJSON:
		return "project", ".mcp.json"
	case config.MCPSourcePluginPackage:
		return "plugin", "plugin"
	case config.MCPSourceLegacyUser:
		return "user", "legacy config"
	case config.MCPSourceUserConfig:
		return "user", "config.toml"
	default:
		return "", ""
	}
}

// MCPServerInput is the drawer's "add server" form. Transport is "stdio" (Command
// + Args + Env) or "http"/"sse" (URL). Mirrors config.PluginEntry's writable shape.
type MCPServerInput struct {
	Name               string            `json:"name"`
	Transport          string            `json:"transport"`
	Command            string            `json:"command"`
	Args               []string          `json:"args"`
	URL                string            `json:"url"`
	Env                map[string]string `json:"env"`
	Headers            map[string]string `json:"headers"`
	AutoStart          *bool             `json:"autoStart"`
	CallTimeoutSeconds *int              `json:"callTimeoutSeconds"`
	ToolTimeoutSeconds map[string]int    `json:"toolTimeoutSeconds"`
}

func mcpServerInputEntry(in MCPServerInput) config.PluginEntry {
	entry := config.PluginEntry{
		Name:               strings.TrimSpace(in.Name),
		Type:               normalizeMCPTransport(in.Transport),
		Command:            strings.TrimSpace(in.Command),
		Args:               append([]string(nil), in.Args...),
		URL:                strings.TrimSpace(in.URL),
		Env:                in.Env,
		Headers:            in.Headers,
		AutoStart:          in.AutoStart,
		CallTimeoutSeconds: mcpIntValue(in.CallTimeoutSeconds),
		ToolTimeoutSeconds: cloneStringIntMap(in.ToolTimeoutSeconds),
		Source:             config.MCPSourceUserConfig,
	}
	entry, _ = config.NormalizePluginCommandLine(entry)
	return entry
}

// InstallMCPServer is the desktop's high-level install transaction. A normal
// handshake failure leaves no config behind; authentication-required servers
// are retained so the user can complete OAuth and retry. Only a ready result is
// published to every controller sharing the Host.
func (a *App) InstallMCPServer(in MCPServerInput) (plugin.MCPInstallResult, error) {
	defer a.lockMCPMutation("add")()

	_, ctrl, root := a.activeMCPRuntime()
	if ctrl == nil {
		return plugin.MCPInstallResult{}, fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return plugin.MCPInstallResult{}, err
	}
	defer releaseGates()

	entry := mcpServerInputEntry(in)
	if entry.Name == "" {
		return plugin.InstallResultForError(entry.Name, fmt.Errorf("MCP server name is required")), nil
	}
	if _, found, lookupErr := desktopEffectiveMCPServer(root, entry.Name); lookupErr != nil {
		return plugin.MCPInstallResult{}, lookupErr
	} else if found {
		return plugin.InstallResultForError(entry.Name, fmt.Errorf("MCP server %q is already installed", entry.Name)), nil
	}

	controllers := a.mcpControllersSharingHost(host, entry.Name, ctrl)
	toolCount, connectErr := ctrl.ConnectMCPServer(entry)
	if connectErr != nil {
		result := plugin.InstallResultForError(entry.Name, connectErr)
		if result.State != "action_required" {
			if host != nil {
				host.ClearFailure(entry.Name)
			}
			return result, nil
		}
		if err := a.saveDesktopMCPServer(root, entry); err != nil {
			return plugin.MCPInstallResult{}, err
		}
		if err := persistMCPInstallActivation(entry, root); err != nil {
			_, rollbackErr := a.removeDesktopMCPServer(root, entry.Name)
			if host != nil {
				host.ClearFailure(entry.Name)
			}
			return plugin.MCPInstallResult{}, errors.Join(err, rollbackErr)
		}
		a.bumpExtensionGeneration()
		recordMCPFailure(ctrl, entry, connectErr)
		return result, nil
	}
	var publishErrs []error
	for _, target := range controllers {
		if target.ctrl == ctrl || !target.enabled {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			publishErrs = append(publishErrs, err)
		}
	}
	if err := errors.Join(publishErrs...); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, fmt.Errorf("publish MCP tools: %w", err)
	}
	if err := a.saveDesktopMCPServer(root, entry); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, err
	}
	if err := persistMCPInstallActivation(entry, root); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		_, rollbackErr := a.removeDesktopMCPServer(root, entry.Name)
		// The first disconnect happened while the just-saved config still
		// existed, so controller runtimes retained it as disabled. Reconcile once
		// more after rollback removes the config to prevent a phantom proxy entry.
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, errors.Join(err, rollbackErr)
	}
	a.bumpExtensionGeneration()
	return plugin.ReadyInstallResult(entry.Name, toolCount), nil
}

func persistMCPInstallActivation(entry config.PluginEntry, root string) error {
	store := config.DefaultMCPActivationStore()
	if !entry.ShouldAutoStart() {
		return store.ClearServer(entry, root)
	}
	return store.SetServerEnabled(entry, root, true)
}

// AddMCPServer is retained for old generated Wails clients. New clients use
// InstallMCPServer so authentication and retry states remain structured.
func (a *App) AddMCPServer(in MCPServerInput) (int, error) {
	result, err := a.InstallMCPServer(in)
	if err != nil {
		return 0, err
	}
	if result.State != "ready" {
		return 0, fmt.Errorf("%s", result.Message)
	}
	return result.ToolCount, nil
}

// UpdateMCPServer edits a persisted external MCP server. The name is the stable
// identity; callers must remove + add if they want to rename a server.
func (a *App) UpdateMCPServer(name string, in MCPServerInput) error {
	defer a.lockMCPMutation("update")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	if strings.TrimSpace(in.Name) != "" && strings.TrimSpace(in.Name) != name {
		return fmt.Errorf("renaming MCP servers is not supported; remove and add a new server")
	}
	updated, found, err := a.desktopMCPServerForEdit(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	original := updated
	updated.Type = normalizeMCPTransport(in.Transport)
	updated.Command = strings.TrimSpace(in.Command)
	updated.Args = append([]string(nil), in.Args...)
	updated.URL = strings.TrimSpace(in.URL)
	updated.Tier = ""
	if in.Env != nil {
		updated.Env = in.Env
	}
	if in.Headers != nil {
		updated.Headers = in.Headers
	}
	if in.AutoStart != nil {
		value := *in.AutoStart
		updated.AutoStart = &value
	}
	if in.CallTimeoutSeconds != nil {
		updated.CallTimeoutSeconds = *in.CallTimeoutSeconds
	}
	if in.ToolTimeoutSeconds != nil {
		updated.ToolTimeoutSeconds = cloneStringIntMap(in.ToolTimeoutSeconds)
	}
	updated, _ = config.NormalizePluginCommandLine(updated)
	if updated.Type == "stdio" {
		updated.URL = ""
	} else {
		updated.Command = ""
		updated.Args = nil
	}
	enabled := false
	for _, target := range controllers {
		enabled = enabled || target.enabled
	}
	if !enabled {
		return a.saveDesktopMCPServerAndBump(root, updated)
	}
	spec, specErr := a.mcpLaunchSpecForEntry(root, updated)
	if specErr != nil {
		return specErr
	}
	if spec.RequireLaunchApproval {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := plugin.AuthorizeProjectSpecLaunch(ctx, spec); err != nil {
			return err
		}
	}
	disconnectMCPServerControllers(name, ctrl, controllers)
	if err := reconnectMCPServerControllers(updated, controllers); err != nil {
		rollbackErr := reconnectMCPServerControllers(original, controllers)
		recordMCPFailure(ctrl, updated, err)
		return errors.Join(err, rollbackErr)
	}
	if err := a.saveDesktopMCPServer(root, updated); err != nil {
		disconnectMCPServerControllers(name, ctrl, controllers)
		rollbackErr := reconnectMCPServerControllers(original, controllers)
		return errors.Join(err, rollbackErr)
	}
	a.bumpExtensionGeneration()
	return nil
}

// RemoveMCPServer disconnects a live server and drops it from config (the row's ✕).
// Uninstall also clears durable activation overrides for that server.
func (a *App) RemoveMCPServer(name string) error {
	defer a.lockMCPMutation("remove")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	if err := ensureMCPServerDirectlyWritable(root, name); err != nil {
		return err
	}
	entry, hasEntry, _ := desktopEffectiveMCPServer(root, name)
	removed, err := a.removeDesktopMCPServer(root, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no removable MCP server named %q", name)
	}
	if hasEntry {
		_ = config.DefaultMCPActivationStore().ClearServer(entry, root)
	}
	authCleanupErr := reconcileRemovedMCPAuthentication(name, a.mcpWorkspaceRoots(root))
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	restoreMCPServerFallbacks(name, controllers)
	a.clearMCPServerTabState(name, controllers)
	a.bumpExtensionGeneration()
	return authCleanupErr
}

// restoreMCPServerFallbacks makes a lower-priority declaration immediately
// available after its project override is removed. Registration is cache-first:
// it restores cached tools or a connect placeholder without starting a process.
func restoreMCPServerFallbacks(name string, controllers []mcpControllerTarget) {
	for _, target := range controllers {
		root := target.ctrl.WorkspaceRoot()
		cfg, err := config.LoadForRoot(root)
		if err != nil {
			slog.Warn("desktop: reload MCP fallback after remove", "name", name, "workspace", root, "err", err)
			continue
		}
		entry, found := findPluginEntry(cfg.Plugins, name)
		if !found || !mcpEntryEnabled(entry, root) {
			continue
		}
		if _, err := target.ctrl.RegisterMCPServerOnDemand(entry); err != nil {
			slog.Warn("desktop: restore MCP fallback after remove", "name", name, "workspace", root, "err", err)
		}
	}
}

// ReconnectMCPServer disconnects the server if it is already connected (to force
// a fresh handshake and tool re-registration), then reconnects.  Failures are
// recorded on the Host so the UI can render them.
func (a *App) ReconnectMCPServer(name string) error {
	defer a.lockMCPMutation("reconnect")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	entry, found, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	for i := range controllers {
		if controllers[i].ctrl == ctrl {
			controllers[i].enabled = true
		}
	}
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	if err := reconnectMCPServerControllers(entry, controllers); err != nil {
		recordMCPFailure(ctrl, entry, err)
		return err
	}
	a.mu.Lock()
	delete(tab.disabledMCP, name)
	a.mu.Unlock()
	a.bumpExtensionGeneration()
	return nil
}

// SetMCPServerEnabled is the durable enable/disable switch for an installed MCP
// server. It writes $REASONIX_HOME/mcp-activation.json and updates the live
// registry: disable removes tools and may stop the process; enable restores
// cached tools and starts the process only on the next real tool call.
func (a *App) SetMCPServerEnabled(name string, enabled bool) error {
	defer a.lockMCPMutation("set-enabled")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	a.mu.RLock()
	hostKey := tab.SharedHostKey
	a.mu.RUnlock()
	if err := rebuildControllerActiveWorkErrorFor(ctrl, "MCP server"); err != nil {
		return err
	}
	configuredEntry, hasConfiguredEntry, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !hasConfiguredEntry {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	activationStore := config.DefaultMCPActivationStore()
	scope, workspaceFP, source, owner := config.ActivationIdentity(configuredEntry, root)
	previousEnabled, previousFound, err := activationStore.Lookup(scope, workspaceFP, source, owner, configuredEntry.Name)
	if err != nil {
		return err
	}
	if err := activationStore.SetServerEnabled(configuredEntry, root, enabled); err != nil {
		return err
	}
	a.bumpExtensionGeneration()
	if enabled {
		// Restore cached tools (or a cache-miss connect stub) without forcing a
		// process start. Explicit install/retry remains the readiness-probed path.
		_, err := a.registerConfiguredMCPServerForTab(tab, name)
		if err == nil {
			a.mu.Lock()
			delete(tab.disabledMCP, name)
			a.mu.Unlock()
			return nil
		}
		var rollbackErr error
		if previousFound {
			rollbackErr = activationStore.SetServerEnabled(configuredEntry, root, previousEnabled)
		} else {
			rollbackErr = activationStore.ClearServer(configuredEntry, root)
		}
		return errors.Join(err, rollbackErr)
	}
	if s, ok := findMCPServerView(ctrl, name); ok {
		s.Status = "disabled"
		s.Enabled = false
		s.Error = ""
		s = finalizeServerView(s)
		a.mu.Lock()
		if tab.disabledMCP == nil {
			tab.disabledMCP = map[string]ServerView{}
		}
		tab.disabledMCP[name] = s
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, []ServerView{s})
		a.mu.Unlock()
	} else {
		s := finalizeServerView(withPluginConfig(ServerView{Name: name, Status: "disabled", Enabled: false}, configuredEntry))
		a.mu.Lock()
		if tab.disabledMCP == nil {
			tab.disabledMCP = map[string]ServerView{}
		}
		tab.disabledMCP[name] = s
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, []ServerView{s})
		a.mu.Unlock()
	}
	if hostKey != "" {
		ctrl.UnregisterMCPServerTools(name)
	} else {
		ctrl.DisconnectMCPServer(name)
	}
	return nil
}

func (a *App) registerConfiguredMCPServerForTab(tab *WorkspaceTab, name string) (int, error) {
	a.mu.RLock()
	var ctrl control.SessionAPI
	root := ""
	if tab != nil {
		ctrl = tab.Ctrl
		root = tab.WorkspaceRoot
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return 0, fmt.Errorf("no active session")
	}
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return 0, err
	}
	for _, p := range cfg.Plugins {
		if p.Name == name {
			return ctrl.RegisterMCPServerOnDemand(p)
		}
	}
	return 0, fmt.Errorf("no configured MCP server named %q", name)
}

// SetMCPServerTier is kept for old desktop bindings. New config writes drop the
// retired tier field.
func (a *App) SetMCPServerTier(name, tier string) error {
	defer a.lockMCPMutation("set-tier")()

	tier = normalizeMCPTier(tier)
	tab, ctrl, root := a.activeMCPRuntime()
	if tab != nil {
		if err := rebuildControllerActiveWorkErrorFor(ctrl, "MCP server"); err != nil {
			return err
		}
	}
	updated, found, err := a.desktopMCPServerForEdit(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	updated.Tier = tier
	if !updated.ShouldAutoStart() {
		on := true
		updated.AutoStart = &on
	}
	if err := a.saveDesktopMCPServer(root, updated); err != nil {
		return err
	}
	a.bumpExtensionGeneration()
	if tab != nil && ctrl != nil && !mcpConnected(ctrl, name) {
		if _, err := ctrl.ConnectMCPServer(updated); err != nil {
			recordMCPFailure(ctrl, updated, err)
			return nil
		}
		a.mu.Lock()
		delete(tab.disabledMCP, name)
		a.mu.Unlock()
	}
	return nil
}

func (a *App) desktopMCPServerForEdit(root, name string) (config.PluginEntry, bool, error) {
	// Edit the same effective declaration the runtime selected. The entry's
	// provenance is retained so saveDesktopMCPServer writes it back to the
	// owning project/global file instead of promoting it across scopes.
	return desktopEffectiveMCPServer(root, name)
}

// desktopEffectiveMCPServer returns the same merged entry the runtime starts.
// Its provenance identifies the exact project or global declaration that edit
// and remove operations must mutate.
func desktopEffectiveMCPServer(root, name string) (config.PluginEntry, bool, error) {
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return config.PluginEntry{}, false, err
	}
	p, ok := findPluginEntry(cfg.Plugins, name)
	return p, ok, nil
}

func (a *App) saveDesktopMCPServer(root string, entry config.PluginEntry) error {
	if err := ensureMCPServerDirectlyWritable(root, entry.Name); err != nil {
		return err
	}
	_, err := config.UpsertPluginInSourceForRoot(root, entry)
	return err
}

func ensureMCPServerDirectlyWritable(root, name string) error {
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return err
	}
	if owner, ok := cfg.PluginPackageOwner(name); ok {
		return fmt.Errorf("MCP server %q is managed by plugin %q; disable or remove the plugin instead", name, owner)
	}
	return nil
}

func (a *App) removeDesktopMCPServer(root, name string) (bool, error) {
	_, removed, _, err := config.RemovePluginFromEffectiveSourceForRoot(root, name)
	return removed, err
}

func findPluginEntry(entries []config.PluginEntry, name string) (config.PluginEntry, bool) {
	for _, p := range entries {
		if p.Name == name {
			return p, true
		}
	}
	return config.PluginEntry{}, false
}

func normalizeMCPTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "eager":
		return "eager"
	case "background", "lazy":
		return "background"
	case "":
		return "background"
	default:
		return "background"
	}
}

func normalizeMCPTransport(transport string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "http", "streamable-http":
		return "http"
	case "sse":
		return "sse"
	case "", "stdio":
		return "stdio"
	default:
		return strings.ToLower(strings.TrimSpace(transport))
	}
}

func mcpIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func cloneStringIntMap(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	out := make(map[string]int, len(values))
	maps.Copy(out, values)
	return out
}

func mcpConnected(ctrl control.SessionAPI, name string) bool {
	if ctrl == nil || ctrl.Host() == nil {
		return false
	}
	for _, s := range ctrl.Host().Servers() {
		if s.Name == name {
			return true
		}
	}
	return false
}

func mcpFailed(ctrl control.SessionAPI, name string) bool {
	if ctrl == nil || ctrl.Host() == nil {
		return false
	}
	for _, f := range ctrl.Host().Failures() {
		if f.Name == name {
			return true
		}
	}
	return false
}

func recordMCPFailure(ctrl control.SessionAPI, e config.PluginEntry, err error) {
	if ctrl == nil || ctrl.Host() == nil || err == nil {
		return
	}
	exp := e.ExpandedPlugin()
	ctrl.Host().RecordFailure(plugin.Spec{
		Name:    exp.Name,
		Type:    exp.Type,
		Command: exp.Command,
		Args:    exp.Args,
		Env:     exp.Env,
		URL:     exp.URL,
		Headers: exp.Headers,
	}, err)
}

func findMCPServerView(ctrl control.SessionAPI, name string) (ServerView, bool) {
	if ctrl == nil || ctrl.Host() == nil {
		return ServerView{}, false
	}
	for _, s := range ctrl.Host().Servers() {
		if s.Name == name {
			return pluginServerToView(s), true
		}
	}
	for _, f := range ctrl.Host().Failures() {
		if f.Name == name {
			return ServerView{
				Name: f.Name, Transport: f.Transport, Status: "failed", Error: f.Error,
				RequiresLaunchApproval: f.RequiresLaunchApproval,
			}, true
		}
	}
	return ServerView{}, false
}

func pluginToolsToView(tools []plugin.ToolInfo) []ToolView {
	if len(tools) == 0 {
		return []ToolView{}
	}
	out := make([]ToolView, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolView{
			Name: t.Name, Description: t.Description, ReadOnlyHint: t.ReadOnlyHint, DestructiveHint: t.DestructiveHint, SchemaError: t.SchemaError,
		})
	}
	return out
}

func orderServerViews(servers []ServerView, order []string) []ServerView {
	pos := make(map[string]int, len(order))
	for i, name := range order {
		pos[name] = i
	}
	sort.SliceStable(servers, func(i, j int) bool {
		pi, iok := pos[servers[i].Name]
		pj, jok := pos[servers[j].Name]
		switch {
		case iok && jok:
			return pi < pj
		case iok:
			return true
		case jok:
			return false
		default:
			return false
		}
	})
	return servers
}

func mergeServerOrder(order []string, servers []ServerView) []string {
	seen := make(map[string]bool, len(order)+len(servers))
	next := make([]string, 0, len(order)+len(servers))
	for _, name := range order {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		next = append(next, name)
	}
	for _, s := range servers {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		next = append(next, s.Name)
	}
	return next
}

func removeServerOrder(order []string, name string) []string {
	if name == "" || len(order) == 0 {
		return order
	}
	next := order[:0]
	for _, n := range order {
		if n != name {
			next = append(next, n)
		}
	}
	return next
}
