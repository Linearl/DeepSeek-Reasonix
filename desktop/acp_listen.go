package main

// Task 790: the resident ACP listener host.
//
// The stdio command (`reasonix acp`) stays the client-spawned ACP form: an
// editor launches the process and owns its lifetime. This host adds the
// opposite shape — when the [desktop] experimental_acp_listen lab switch
// (铁律 2, ships off) is on, the desktop arms a loopback-only TCP listener at
// boot (127.0.0.1 is hardcoded here; the port is the only configurable part)
// and every accepted connection becomes an independent ACP client surface
// running the exact same protocol stack as the stdio command (internal/acp
// Serve, boot.Build session assembly, shared transcript persistence — so a
// client that reconnects resumes via session/load).
//
// The flag-off path is the zero-behaviour path — the function returns before
// touching the network, so a default config behaves byte-for-byte like a build
// without the feature. A bind failure (port already held) is a visible
// degraded state reported by the lab card, never a desktop crash.
//
// Session assembly deliberately mirrors internal/cli's acpFactory rather than
// importing it: internal/cli drags the CLI TUI dependency tree into the
// desktop binary, and the ACP session needs none of it. The per-session boot
// options below are the same assembly the stdio command uses (RequireKey,
// session cwd as workspace root, host-supplied MCP servers, the ACP
// cleanup-pending reconciler) minus the CLI flag overrides, which an
// editor-driven resident session does not have. Session config/runtime state
// selectors stay unimplemented for now: the protocol degrades gracefully
// (set_config_option / runtime state report unavailable), and no acceptance
// path needs them.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reasonix/internal/acp"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// acpListenDefaultPort is the loopback port the resident ACP listener binds
// when acp_listen_port is unset (tl-1048: the former 8787 task-bus neighbour
// reference is retired along with the bus itself).
const acpListenDefaultPort = 8790

// acpListenAddr returns the listen address for the resident ACP listener. The
// host is pinned to 127.0.0.1; REASONIX_ACP_LISTEN_ADDR overrides the whole
// address for tests and port conflicts (still expected to be loopback).
func acpListenAddr() string {
	if v := os.Getenv("REASONIX_ACP_LISTEN_ADDR"); v != "" {
		return v
	}
	port := acpListenDefaultPort
	if cfg, err := config.Load(); err == nil && cfg != nil && cfg.Desktop.ACPListenPort > 0 {
		port = cfg.Desktop.ACPListenPort
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// acpListenHost owns the resident ACP listener. Nil on the App means the
// listener is not running — which is the flag-off state and the only state a
// default install ever sees.
type acpListenHost struct {
	mu     sync.Mutex
	ln     *acp.Listener
	addr   string // actual bound address
	err    string // bind failure, surfaced by the lab card
	closed bool
}

// startACPListener arms the resident ACP listener for this boot. Flag off is
// the zero-behaviour path: the function returns before touching the network.
func (a *App) startACPListener(cfg *config.Config) {
	if a == nil || cfg == nil || !cfg.Desktop.ExperimentalACPListen {
		return
	}
	addr := acpListenAddr()
	ln, err := acp.Listen(addr, acpListenFactory{}, acp.AgentInfo{Name: "reasonix", Version: version})
	if err != nil {
		// Visible degraded, never fatal: whatever holds the port, the lab
		// card says so and the rest of the desktop is unaffected.
		slog.Warn("acp listen: failed", "feature", "acp-listen", "addr", addr, "err", err)
		a.acpListener = &acpListenHost{err: fmt.Sprintf("listen %s: %v", addr, err), closed: true}
		return
	}
	host := &acpListenHost{ln: ln, addr: ln.Addr().String()}
	a.acpListener = host
	a.goSafe("acp-listen", func() { _ = ln.Serve(context.Background()) })
	slog.Info("acp listen: ready", "feature", "acp-listen", "addr", host.addr)
}

// closeACPListener stops the resident ACP listener and waits for its
// connections to unwind. Safe to call when never started.
func (a *App) closeACPListener() {
	if a == nil || a.acpListener == nil {
		return
	}
	host := a.acpListener
	a.acpListener = nil
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		return
	}
	host.closed = true
	ln := host.ln
	host.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
}

// ACPListenStatus reports the resident listener state for the lab card:
// enabled is the config intent (a flip needs a restart), running is whether
// this process actually hosts the listener, addr is the bound loopback
// address, and err carries a bind failure.
func (a *App) ACPListenStatus() map[string]any {
	enabled := false
	port := 0
	if cfg, err := config.Load(); err == nil && cfg != nil {
		enabled = cfg.Desktop.ExperimentalACPListen
		port = cfg.Desktop.ACPListenPort
	}
	status := map[string]any{
		"enabled": enabled,
		"port":    port,
		"running": false,
		"addr":    "",
		"err":     "",
	}
	if a == nil || a.acpListener == nil {
		return status
	}
	host := a.acpListener
	host.mu.Lock()
	defer host.mu.Unlock()
	status["running"] = host.ln != nil && !host.closed
	status["addr"] = host.addr
	status["err"] = host.err
	return status
}

// acpListenFactory assembles one control.Controller per ACP session the same
// way the stdio `reasonix acp` command does: boot.Build rooted at the session
// cwd. See the file comment for why this mirrors internal/cli's acpFactory
// instead of importing it.
type acpListenFactory struct{}

func (acpListenFactory) SessionDir() string { return config.SessionDir() }

func (acpListenFactory) NewSession(ctx context.Context, p acp.SessionParams) (*control.Controller, error) {
	opts, err := acpListenSessionOptions(p)
	if err != nil {
		return nil, err
	}
	return boot.Build(ctx, opts)
}

// RebuildSession rebuilds the session controller with the same boot.Options
// via boot.Rebuild; the session state migrates off old inside the boot layer.
func (acpListenFactory) RebuildSession(ctx context.Context, p acp.SessionParams, old *control.Controller) (*control.Controller, error) {
	opts, err := acpListenSessionOptions(p)
	if err != nil {
		return nil, err
	}
	res, err := boot.Rebuild(ctx, old, opts)
	if err != nil {
		return nil, err
	}
	return res.Controller, nil
}

// acpListenSessionOptions builds the boot.Options for a resident-listener ACP
// session — the stdio command's assembly minus the CLI flag overrides.
func acpListenSessionOptions(p acp.SessionParams) (boot.Options, error) {
	root := strings.TrimSpace(p.Cwd)
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	if root != "" && !filepath.IsAbs(root) {
		return boot.Options{}, fmt.Errorf("session cwd must be an absolute path: %s", root)
	}
	return boot.Options{
		RequireKey:               true,
		StatsSource:              "desktop",
		Sink:                     p.Sink,
		Stderr:                   os.Stderr,
		WorkspaceRoot:            root,
		ExtraPlugins:             p.MCPServers,
		CleanupPendingReconciler: acp.ReconcileCleanupPending,
		OnSessionRecovered:       p.OnSessionRecovered,
		OnSessionTransition:      p.OnSessionTransition,
		FileOverlay:              p.FileOverlay,
		TerminalRunner:           p.Terminal,
	}, nil
}
