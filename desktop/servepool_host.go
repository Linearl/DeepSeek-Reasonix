package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/serve"
	"reasonix/internal/servepool"
)

// ServePoolAddress returns the gateway listen address ("" when disabled).
func (a *App) ServePoolAddress() string {
	if a == nil {
		return ""
	}
	return a.gatewayAddr
}

// startServePool starts the single-entry remote gateway in front of a pool of
// lazily spawned per-project serve processes (#8983). It binds 0.0.0.0 so
// remote clients can reach it over the LAN or a Tailscale network (the
// gateway-token Bearer auth protects it; see the desktop Settings → 集成与连接
// → 本地服务器服务 panel). Failures disable the gateway silently (e.g. port
// taken) and log a warning.
func (a *App) startServePool(ctx context.Context) {
	roots := projectRootsFromRegistry()
	mgr, err := servepool.NewManager(servepool.Config{
		ProjectRoots:  roots,
		ProjectColors: projectColorsFromRegistry(),
		ProjectGroups: projectGroupsFromRegistry(),
		// The desktop "global" session scope (config.SessionDir()) is not a
		// project root, so GrandCouncil could never see it (#task16). Expose
		// it as a manifest-only virtual project whose /sessions the gateway
		// serves inline from the app below.
		Virtual: []servepool.ProjectState{{
			ID:   "global",
			Name: "Global",
			Root: "Global",
		}},
	})
	if err != nil {
		slog.Warn("servepool: disabled", "err", err)
		return
	}
	token := loadOrCreateGatewayToken()
	gw := servepool.NewGateway(mgr, token)
	gw.SetVirtualSource("global", servepool.VirtualSource{
		Sessions:   a.globalServePoolSessions,
		History:    serve.HistoryJSONForFile,
		AllowedDir: config.SessionDir(),
	})
	port := gatewayPort()
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		mgr.Close()
		slog.Warn("servepool: gateway listen failed", "port", port, "err", err)
		return
	}
	a.gatewayBind = ln.Addr().String()
	a.servePool = mgr
	a.gatewayAddr = ln.Addr().String()
	srv := &http.Server{Handler: gw}
	a.gatewaySrv = srv
	a.goSafe("servepool-gateway", func() { _ = srv.Serve(ln) })
	slog.Info("servepool: gateway ready", "addr", a.gatewayAddr)
}

// closeServePool stops the gateway and all pooled serves.
func (a *App) closeServePool() {
	if a.gatewaySrv != nil {
		_ = a.gatewaySrv.Close()
		a.gatewaySrv = nil
	}
	if a.servePool != nil {
		a.servePool.Close()
		a.servePool = nil
	}
	a.gatewayAddr = ""
	a.gatewayBind = ""
}

func projectRootsFromRegistry() []string {
	f := loadProjectsFile()
	roots := make([]string, 0, len(f.Projects))
	for _, p := range f.Projects {
		if strings.TrimSpace(p.Root) != "" {
			roots = append(roots, p.Root)
		}
	}
	return roots
}

// globalServePoolSessions lists the desktop global-scope sessions
// (config.SessionDir() — the "Global" area of the app, not tied to any
// project root) in the wire shape the gateway serves for virtual projects.
// It reuses the history panel's catalog-backed listing so titles, turn
// counts, and ordering match what the desktop UI shows.
func (a *App) globalServePoolSessions() []servepool.SessionEntry {
	dir := config.SessionDir()
	if strings.TrimSpace(dir) == "" {
		return []servepool.SessionEntry{}
	}
	metas := a.listSessionsFromDir(dir, "")
	out := make([]servepool.SessionEntry, 0, len(metas))
	for _, m := range metas {
		name := strings.TrimSuffix(filepath.Base(m.Path), ".jsonl")
		title := m.Title
		if strings.TrimSpace(title) == "" {
			title = m.Preview
		}
		if r := []rune(title); len(r) > 50 {
			title = string(r[:47]) + "..."
		}
		out = append(out, servepool.SessionEntry{
			Name:       name,
			Path:       m.Path,
			Title:      title,
			Turns:      m.Turns,
			Current:    m.Current,
			Running:    m.Open,
			MtimeMilli: m.LastActivityAt,
		})
	}
	return out
}

// projectColorsFromRegistry maps each project root (cleaned) to its color
// token from desktop-projects.json, so the serve pool /manifest can carry the
// color for remote clients (GrandCouncil project coloring).
func projectColorsFromRegistry() map[string]string {
	f := loadProjectsFile()
	colors := make(map[string]string, len(f.Projects))
	for _, p := range f.Projects {
		root := filepath.Clean(p.Root)
		if root != "" && strings.TrimSpace(p.Color) != "" {
			colors[root] = strings.TrimSpace(p.Color)
		}
	}
	return colors
}

// projectGroupsFromRegistry maps each project root (cleaned) to its configured
// project group name (desktop-projects.json projectGroup), so the serve pool
// /manifest carries the grouping for remote clients (GrandCouncil folders).
func projectGroupsFromRegistry() map[string]string {
	f := loadProjectsFile()
	groups := make(map[string]string, len(f.Projects))
	for _, p := range f.Projects {
		root := filepath.Clean(p.Root)
		if root != "" && strings.TrimSpace(p.ProjectGroup) != "" {
			groups[root] = strings.TrimSpace(p.ProjectGroup)
		}
	}
	return groups
}

func loadOrCreateGatewayToken() string {
	path := filepath.Join(config.MemoryUserDir(), "gateway-token")
	if b, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok
		}
	}
	tok := servepool.RandomToken()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(tok), 0o600)
	return tok
}

// gatewayPort picks the gateway port: REASONIX_GATEWAY_PORT env override or
// the default 18789 (the desktop's internal serve keeps 8787).
func gatewayPort() int {
	if v := os.Getenv("REASONIX_GATEWAY_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return 18789
}

// servepoolSettingsPath returns the path of the servepool enablement file that
// backs the desktop Settings → 集成与连接 → 本地服务器服务 toggle.
func servepoolSettingsPath() string {
	return filepath.Join(config.MemoryUserDir(), "servepool.json")
}

// servepoolEnabled reports whether the remote gateway should run (default off).
func servepoolEnabled() bool {
	b, err := os.ReadFile(servepoolSettingsPath())
	if err != nil {
		return false
	}
	var s struct {
		Enabled bool `json:"enabled"`
	}
	if json.Unmarshal(b, &s) != nil {
		return false
	}
	return s.Enabled
}

func saveServePoolEnabled(enabled bool) error {
	path := servepoolSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]bool{"enabled": enabled})
	return os.WriteFile(path, b, 0o600)
}

// SetServePoolEnabled enables or disables the remote gateway from the settings
// panel and persists the toggle so the desktop restores it on next startup.
func (a *App) SetServePoolEnabled(enabled bool) error {
	if err := saveServePoolEnabled(enabled); err != nil {
		return err
	}
	a.closeServePool()
	if enabled {
		a.startServePool(context.Background())
	}
	return nil
}

// ServePoolStatus reports the gateway state for the settings panel.
func (a *App) ServePoolStatus() map[string]any {
	return map[string]any{
		"enabled": servepoolEnabled(),
		"running": a.gatewaySrv != nil,
		"bind":    a.gatewayBind,
		"addr":    a.gatewayAddr,
		"port":    gatewayPort(),
		"token":   loadOrCreateGatewayToken(),
		"listen":  fmt.Sprintf("0.0.0.0:%d", gatewayPort()),
	}
}

// GatewayToken returns the persistent gateway token so the settings panel can
// offer "copy to clipboard".
func (a *App) GatewayToken() string {
	return loadOrCreateGatewayToken()
}

// RequestOwnershipFromRemote asks the project's spawned serve (if any) to
// release its lease on topicID so the desktop can take the session back.
// Used by the sidebar context menu ("请求获取所有权") while a session is
// held by a remote client. A not-spawned project holds nothing: no-op.
func (a *App) RequestOwnershipFromRemote(workspaceRoot, topicID string) error {
	if a == nil || a.servePool == nil {
		return nil // gateway disabled: nothing is held remotely
	}
	slug := servepool.WorkspaceSlug(workspaceRoot)
	port := a.servePool.Port(slug)
	if port == 0 {
		return nil // serve not spawned: no remote lease to release
	}
	token := a.servePool.Token(slug)
	body := fmt.Sprintf(`{"name":%q}`, topicID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/release-session?token=%s", port, token), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("serve unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		slog.Info("servepool: remote ownership released", "project", slug, "topic", topicID)
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("会话未被远程持有（可能已释放）")
	}
	return fmt.Errorf("serve returned %d", resp.StatusCode)
}
