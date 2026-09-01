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

	"reasonix/internal/config"
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
		ProjectRoots:   roots,
		ProjectColors:  projectColorsFromRegistry(),
		ProjectGroups:  projectGroupsFromRegistry(),
	})
	if err != nil {
		slog.Warn("servepool: disabled", "err", err)
		return
	}
	token := loadOrCreateGatewayToken()
	gw := servepool.NewGateway(mgr, token)
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
		"enabled":  servepoolEnabled(),
		"running":  a.gatewaySrv != nil,
		"bind":     a.gatewayBind,
		"addr":     a.gatewayAddr,
		"port":     gatewayPort(),
		"token":    loadOrCreateGatewayToken(),
		"listen":   fmt.Sprintf("0.0.0.0:%d", gatewayPort()),
	}
}

// GatewayToken returns the persistent gateway token so the settings panel can
// offer "copy to clipboard".
func (a *App) GatewayToken() string {
	return loadOrCreateGatewayToken()
}
