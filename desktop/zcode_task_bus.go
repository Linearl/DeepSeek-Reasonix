package main

// Task 439: the built-in zcode task bus.
//
// Before 439 the bus only ran inside an externally launched `reasonix serve`
// kept alive by a startup-folder vbs — a manual three-piece setup (CLI
// enroll + manual serve + external resident process) with a hardcoded
// version path that broke on every package swap. This host folds the serve
// half into the desktop process: when the [desktop]
// experimental_zcode_task_bus lab switch (铁律 2, ships off) is on, the
// desktop arms a loopback listener at boot and mounts the exact same bus
// routes an external serve would (internal/serve/registerBusRoutes), so
// `reasonix bus enroll` output — the user config role table plus the zcode
// MCP config pointing at http://127.0.0.1:8787/mcp — keeps working
// unchanged.
//
// Relationship with the serve pool (report §4 open question ②): the pool is
// the remote-browser gateway (0.0.0.0, per-project lazily spawned serve
// sub-processes, idle reclaim). The task bus is none of those things — it is
// one long-lived loopback MCP endpoint for enrolled agent runtimes over a
// shared mailbox, and idle reclaiming it would strand the very runtimes it
// exists to serve. So this host stays deliberately independent: no
// sub-processes, no pool, no idle lifetime — one goroutine-owned
// http.Server that dies with the desktop process.
//
// Mail surface: the role table, mailbox directory, hop limit, event target
// and spawn gate all read from [serve.bus_mcp], the same section `reasonix
// bus enroll` writes. The realtime zcodebridge nudge (serve's mail
// injector) is not wired here — durable inbox delivery, mail send, task
// cards, hook events and the audit log are identical; a delivered mail for a
// connected zcode session is simply not pushed live until that bridge is
// hosted desktop-side too.
//
// Failure posture mirrors internal/serve: the bus authenticates every
// request itself (per-role bearer tokens), an unusable role table mounts
// nothing (fail closed), and a bind failure (e.g. an external serve still
// holding 8787) is a visible degraded state — a warning plus a status the
// lab card can show — never a desktop crash.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"reasonix/internal/busmcp"
	"reasonix/internal/config"
)

// zcodeTaskBusAddr returns the listen address for the built-in bus. The
// default is the same 8787 the external serve and `bus enroll` default URL
// use; REASONIX_TASK_BUS_ADDR overrides it (tests, port conflicts).
func zcodeTaskBusAddr() string {
	if v := os.Getenv("REASONIX_TASK_BUS_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:8787"
}

// zcodeTaskBusHost owns the embedded bus listener. Nil on the App means the
// bus is not running — which is the flag-off state and the only state a
// default install ever sees.
type zcodeTaskBusHost struct {
	mu     sync.Mutex
	bus    *busmcp.Server
	srv    *http.Server
	addr   string // actual bound address (host:port)
	roles  []string
	err    string // bind/construct failure, surfaced by the lab card
	closed bool
}

// startZcodeTaskBus arms the embedded bus for this boot. Flag off is the
// zero-behaviour path: the function returns before touching the network,
// so a default config behaves byte-for-byte like a build without the
// feature.
func (a *App) startZcodeTaskBus(cfg *config.Config) {
	if a == nil || cfg == nil || !cfg.Desktop.ExperimentalZcodeTaskBus {
		return
	}
	busCfg := busmcp.Config{
		// The desktop lab switch is the productized gate; the enrolled
		// [serve.bus_mcp] table supplies identity. Enabled passes true so
		// busmcp.New validates the table on its own merits.
		Enabled:         true,
		Roles:           cfg.Serve.BusMCP.Roles,
		MailDir:         cfg.Serve.BusMCP.MailDir,
		HopLimit:        cfg.Serve.BusMCP.HopLimit,
		EventTarget:     cfg.Serve.BusMCP.EventTarget,
		SpawnRoles:      cfg.Serve.BusMCP.SpawnRoles,
		SpawnDailyQuota: cfg.Serve.BusMCP.SpawnDailyQuota,
	}
	bus, err := busmcp.New(busCfg)
	if err != nil {
		// Fail closed, visibly: nothing mounts, the lab card reports why.
		slog.Warn("zcode task bus: disabled by config", "feature", "zcode-task-bus", "err", err)
		a.zcodeTaskBus = &zcodeTaskBusHost{err: err.Error(), closed: true}
		return
	}
	mux := http.NewServeMux()
	handler := bus.Handler()
	// Same method-scoped trio as internal/serve (task 434): a method-less
	// "/mcp" is ambiguous against the catch-all and panics at registration.
	mux.Handle("POST /mcp", handler)
	mux.Handle("GET /mcp", handler)
	mux.Handle("DELETE /mcp", handler)
	mux.HandleFunc("POST /bus/events", bus.HandleEvent)

	ln, err := net.Listen("tcp", zcodeTaskBusAddr())
	if err != nil {
		// Visible degraded, never fatal: an external serve (or anything
		// else) holding the port means the endpoint already exists somewhere
		// else — say so and let the user decide.
		slog.Warn("zcode task bus: listen failed", "feature", "zcode-task-bus", "addr", zcodeTaskBusAddr(), "err", err)
		a.zcodeTaskBus = &zcodeTaskBusHost{err: fmt.Sprintf("listen %s: %v", zcodeTaskBusAddr(), err), closed: true}
		return
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	roles := make([]string, 0, len(cfg.Serve.BusMCP.Roles))
	for role := range cfg.Serve.BusMCP.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	host := &zcodeTaskBusHost{bus: bus, srv: srv, addr: ln.Addr().String(), roles: roles}
	a.zcodeTaskBus = host
	a.goSafe("zcode-task-bus", func() { _ = srv.Serve(ln) })
	slog.Info("zcode task bus: ready", "feature", "zcode-task-bus", "addr", host.addr, "roles", roles)
}

// closeZcodeTaskBus stops the embedded bus. Safe to call when never started.
func (a *App) closeZcodeTaskBus() {
	if a == nil || a.zcodeTaskBus == nil {
		return
	}
	host := a.zcodeTaskBus
	a.zcodeTaskBus = nil
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		return
	}
	host.closed = true
	host.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = host.srv.Shutdown(ctx)
}

// ZcodeTaskBusStatus reports the built-in bus state for the lab card:
// enabled is the config intent (a flip needs a restart), running is whether
// this process actually hosts the listener, roles is the enrolled role
// list, and err carries a bind/construct failure.
func (a *App) ZcodeTaskBusStatus() map[string]any {
	enabled := false
	if cfg, err := config.Load(); err == nil {
		enabled = cfg.Desktop.ExperimentalZcodeTaskBus
	}
	status := map[string]any{
		"enabled":  enabled,
		"running":  false,
		"addr":     "",
		"endpoint": "",
		"roles":    []string{},
		"err":      "",
	}
	if a == nil || a.zcodeTaskBus == nil {
		return status
	}
	host := a.zcodeTaskBus
	host.mu.Lock()
	defer host.mu.Unlock()
	roles := host.roles
	if roles == nil {
		roles = []string{}
	}
	status["running"] = host.bus != nil && !host.closed
	status["addr"] = host.addr
	status["endpoint"] = fmt.Sprintf("http://%s/mcp", host.addr)
	status["roles"] = roles
	status["err"] = host.err
	return status
}
