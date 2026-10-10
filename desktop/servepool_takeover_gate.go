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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/servepool"
)

// gatewayTakeoverPromptTimeout matches the marker watcher's prompt window and
// the 539 T2 yield window: the user can read the notice at leisure because
// accepting no longer interrupts the running turn. An unanswered prompt
// resolves as a refusal.
const gatewayTakeoverPromptTimeout = agent.SessionTakeoverYieldWindow

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
//   - forced request (GC's double-gated force button): no desktop prompt —
//     the running turn is cancelled (T3) and the yield machine starts; the
//     desktop user is told via a runtime event banner;
//   - accept: the holding tabs ENTER THE YIELDING STATE (539: the lease is
//     kept until the turn finishes; the forwarded serve request 202s and
//     polls), so allow returns immediately without waiting;
//   - reject / timeout: deny with an explicit 409 message.
func (a *App) servePoolTakeoverGate(req servepool.TakeoverGateRequest) (bool, int, string) {
	path := a.resolveGatewayTakeoverSessionPath(req.ProjectRoot, req.SessionName)
	if path == "" {
		return true, 0, ""
	}
	if req.Force {
		slog.Info("desktop: gateway FORCED takeover request", "path", path, "from", req.From)
		a.yieldTabsToGatewayTakeover(path, true)
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
		a.yieldTabsToGatewayTakeover(path, false)
		return true, 0, ""
	case <-time.After(timeout):
		unregisterTakeoverPending(marker)
		slog.Info("desktop: gateway takeover prompt timed out, refusing", "path", path)
		return false, http.StatusConflict, fmt.Sprintf("takeover request timed out (no desktop response within %s); the session stays under local control", gatewayTakeoverPromptTimeout)
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
// takeover: every tab holding the session enters the yielding state (539 —
// the lease is KEPT until the tab's turn finishes, then released with a
// handoff reservation; the persisted ReadOnly stays untouched). With
// force=true the running turn is cancelled first (remote forced takeover,
// T3) and the desktop user is notified — the double confirmation gate lives
// on the GC side. The marker file is discovered by the yield machine once
// the forwarded serve request writes it. Non-blocking: the gateway forwards
// immediately and the serve answers 202 + polls for the yield.
func (a *App) yieldTabsToGatewayTakeover(path string, force bool) {
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
		if force && tab.hasActiveRuntimeWork() && tab.Ctrl != nil {
			slog.Info("desktop: forced takeover, cancelling active turn", "path", path, "tab", tab.ID)
			tab.Ctrl.Cancel()
			notifyTakeoverYield("forced", path, "远程设备已强制接管，本地回合被中断")
		}
		beginSessionYieldToTakeover(tab, path, "", force)
	}
}
