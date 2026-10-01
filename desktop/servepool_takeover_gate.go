package main

// Task 249 (GC 接管 → desktop 弹窗): the servepool gateway is the only remote
// entry (GrandCouncil → POST /p/<project>/takeover-session), and the pooled
// serves it fronts are separate processes. A takeover that found no
// desktop-held lease used to acquire straight away (204) with zero desktop
// feedback: the Phase-1 marker watcher only fires when the serve writes a
// marker, and the serve only writes one on a LEASE CONFLICT — and the pooled
// serve's notifyRemoteWriteAuthority call lands in a subprocess that has no
// desktop hook registered. The gate below intercepts the takeover at the
// gateway — inside the desktop process — and routes it through the same
// prompt bridge the marker watcher uses, so every explicit remote takeover
// prompts the desktop user, yields the tab lease on accept, and answers the
// phone with an explicit verdict either way.

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/servepool"
)

// gatewayTakeoverPromptTimeout matches the marker watcher's 9s window (and
// serve's own takeover poll): an unanswered prompt resolves as a refusal.
const gatewayTakeoverPromptTimeout = 9 * time.Second

// gatewayTakeoverPromptTimeoutForTest lets tests shrink the blocking window.
var gatewayTakeoverPromptTimeoutForTest = gatewayTakeoverPromptTimeout

// installServePoolTakeoverGate wires the app-owned gate into the gateway.
func (a *App) installServePoolTakeoverGate(gw *servepool.Gateway) {
	if gw == nil {
		return
	}
	gw.SetTakeoverGate(a.servePoolTakeoverGate)
}

// servePoolTakeoverGate prompts the desktop user for every explicit remote
// takeover and reports the verdict to the gateway.
//   - unknown session path: pass through so the serve answers honestly (404);
//   - no prompt sink (headless/test): pass through, matching the marker
//     watcher's historical immediate yield;
//   - accept: yield every tab holding the session (interrupt a running turn
//     first, release the lease) so the forwarded acquire succeeds;
//   - reject / timeout: deny with an explicit 409 message.
func (a *App) servePoolTakeoverGate(req servepool.TakeoverGateRequest) (bool, int, string) {
	path := a.resolveGatewayTakeoverSessionPath(req.ProjectRoot, req.SessionName)
	if path == "" {
		return true, 0, ""
	}
	takeoverBridgeMu.Lock()
	sink := takeoverPromptSink
	takeoverBridgeMu.Unlock()
	if sink == nil {
		slog.Info("desktop: gateway takeover passed through (no prompt sink)", "path", path, "from", req.From)
		return true, 0, ""
	}
	// The marker id is a bridge correlation key only — no marker file is
	// written or removed on this path.
	marker := "gateway-takeover-" + servepool.RandomToken()
	slog.Info("desktop: gateway takeover request, prompting user", "path", path, "from", req.From)
	reply := registerTakeoverPending(marker)
	sink(takeoverDecisionReq{Marker: marker, Path: path, From: req.From, Reply: reply})
	timeout := gatewayTakeoverPromptTimeoutForTest
	select {
	case accept := <-reply:
		unregisterTakeoverPending(marker)
		if !accept {
			slog.Info("desktop: gateway takeover rejected by user", "path", path)
			return false, http.StatusConflict, "takeover rejected by the desktop user; this device keeps local control"
		}
		slog.Info("desktop: gateway takeover accepted by user", "path", path)
		a.yieldTabsToGatewayTakeover(path)
		return true, 0, ""
	case <-time.After(timeout):
		unregisterTakeoverPending(marker)
		slog.Info("desktop: gateway takeover prompt timed out, refusing", "path", path)
		return false, http.StatusConflict, "takeover request timed out (no desktop response within 9s); the session stays under local control"
	}
}

// resolveGatewayTakeoverSessionPath maps the takeover body's session name to
// the transcript path the desktop knows, mirroring serve's
// sessionFileForName containment rules (the name must be a bare file stem).
// The project's own session directory is consulted first, then the known
// session dirs as a fallback (the gateway may front a project whose root it
// cannot map).
func (a *App) resolveGatewayTakeoverSessionPath(projectRoot, sessionName string) string {
	name := strings.TrimSpace(sessionName)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return ""
	}
	dirs := make([]string, 0, 4)
	if strings.TrimSpace(projectRoot) != "" {
		dirs = append(dirs, desktopSessionDir(projectRoot))
	}
	dirs = append(dirs, a.knownSessionDirs()...)
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(dir, name+".jsonl"))
		if err != nil {
			continue
		}
		if st, serr := os.Stat(abs); serr == nil && !st.IsDir() {
			return abs
		}
	}
	return ""
}

// yieldTabsToGatewayTakeover performs the accept side for a gateway-gated
// takeover: every tab holding the session yields exactly like the marker
// watcher's accept path (interrupt a running turn first, release the lease,
// keep the persisted ReadOnly untouched). No marker file is involved here.
func (a *App) yieldTabsToGatewayTakeover(path string) {
	key := sessionRuntimeKey(path)
	a.mu.Lock()
	tabs := make([]*WorkspaceTab, 0, 2)
	for _, tab := range a.tabs {
		if tab != nil && sessionRuntimeKey(tab.currentSessionPath()) == key {
			tabs = append(tabs, tab)
		}
	}
	a.mu.Unlock()
	for _, tab := range tabs {
		// The synthetic bridge marker is not a file path; the yield's
		// os.Remove on it is a harmless no-op.
		tab.yieldSessionLeaseToTakeover(path, "", true)
	}
}
