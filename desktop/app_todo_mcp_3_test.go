package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/bot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/plugin"
	"reasonix/internal/pluginpkg"
	"reasonix/internal/tool"
)

func TestAuthorizeAndConnectMCPServerSerializesConcurrentDisable(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)

	disableEntered := make(chan struct{})
	var disableOnce sync.Once
	fixture.app.runtimeMutationBeforeLockHook = func(operation string) {
		if operation == "set-enabled" {
			disableOnce.Do(func() { close(disableEntered) })
		}
	}
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	disableDone := make(chan error, 1)
	go func() { disableDone <- fixture.app.SetMCPServerEnabled("h", false) }()
	select {
	case <-disableEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent disable did not reach the MCP lifecycle lock")
	}
	select {
	case err := <-disableDone:
		t.Fatalf("concurrent disable bypassed authorization serialization: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	if err := <-disableDone; err != nil {
		t.Fatalf("SetMCPServerEnabled(h,false): %v", err)
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); found {
		t.Fatal("authorization reconnect overrode the later per-tab disable")
	}
	if _, disabled := fixture.app.tabs["active"].disabledMCP["h"]; !disabled {
		t.Fatal("active tab did not retain the later disable decision")
	}
	if _, found := fixture.siblingRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active-tab disable removed the shared MCP from its enabled sibling")
	}
}

// RemovePlugin disconnects the uninstalled plugin's MCP servers, so it must
// serialize on the MCP lifecycle lock: an unlocked disconnect interleaving
// with authorization lets the reconnect relaunch the just-removed server from its stale snapshot. The plugin does not need to exist — the lock is taken before the uninstall runs, which is the contract under test.
func TestRemovePluginSerializesWithMCPAuthorization(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)

	removeEntered := make(chan struct{})
	var removeOnce sync.Once
	fixture.app.runtimeMutationBeforeLockHook = func(operation string) {
		if operation == "remove-plugin" {
			removeOnce.Do(func() { close(removeEntered) })
		}
	}
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	removeDone := make(chan error, 1)
	go func() { removeDone <- fixture.app.RemovePlugin("not-an-installed-plugin") }()
	select {
	case <-removeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("RemovePlugin did not reach the MCP lifecycle lock")
	}
	select {
	case err := <-removeDone:
		t.Fatalf("RemovePlugin bypassed authorization serialization: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	// The uninstall itself is expected to fail (the plugin is not installed);
	// only the ordering matters. It must complete once the lock is free.
	select {
	case <-removeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("RemovePlugin did not complete after the trust connection released the lock")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("authorization reconnect result was lost after the serialized RemovePlugin")
	}
}

// installGatedTestPluginPackage registers an installed plugin package whose
// manifest declares the gated fixture's MCP server, so RemovePlugin exercises
// the real uninstall and MCP disconnect flow. Returns the plugin root.
func installGatedTestPluginPackage(t *testing.T, mcpServerName string) string {
	t.Helper()
	reasonixHome := config.ReasonixHomeDir()
	root := filepath.Join(reasonixHome, "plugins", "review-helper")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pluginpkg.NativeManifest), fmt.Appendf(nil, `{"apiVersion": "reasonix.io/plugin/v2",
  "name": "review-helper",
  "version": "1.0.0",
  "mcpServers": {
    %q: { "type": "stdio", "command": "helper" }
  }
}`, mcpServerName), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pluginpkg.Upsert(reasonixHome, pluginpkg.InstalledPlugin{
		Name:         "review-helper",
		Root:         "plugins/review-helper",
		Version:      "1.0.0",
		ManifestKind: "reasonix",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func installedPluginNamed(t *testing.T, name string) bool {
	t.Helper()
	st, err := pluginpkg.LoadState(config.ReasonixHomeDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range st.Plugins {
		if p.Name == name {
			return true
		}
	}
	return false
}

// A global plugin uninstall must clean every runtime, not only the active tab:
// sibling registries on the shared Host would otherwise keep provider-visible
// tools backed by the closed client, and other workspaces would keep running the uninstalled server.
func TestRemovePluginDisconnectsEveryRuntime(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	pluginRoot := installGatedTestPluginPackage(t, "h")

	if err := fixture.app.RemovePlugin("review-helper"); err != nil {
		t.Fatalf("RemovePlugin(review-helper): %v", err)
	}
	if fixture.sharedHost.HasClient("h") {
		t.Fatal("uninstall left the shared MCP client connected")
	}
	for name, reg := range map[string]*tool.Registry{
		"active":   fixture.activeRegistry,
		"sibling":  fixture.siblingRegistry,
		"disabled": fixture.disabledRegistry,
	} {
		if _, found := reg.Get("mcp__h__greet"); found {
			t.Fatalf("%s registry still exposes the uninstalled MCP tool", name)
		}
	}
	if _, err := os.Stat(pluginRoot); !os.IsNotExist(err) {
		t.Fatalf("plugin root still present after uninstall (err=%v)", err)
	}
	if installedPluginNamed(t, "review-helper") {
		t.Fatal("plugin state still lists the uninstalled plugin")
	}
}

// The pre-lock active-work check can go stale during the lifecycle-lock wait.
// Work that starts mid-wait must fail the removal before anything is deleted;
// the old order deleted the plugin first and only then reported the failure.
func TestRemovePluginRechecksActiveWorkUnderLock(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	installGatedTestPluginPackage(t, "h")

	removeEntered := make(chan struct{})
	var removeOnce sync.Once
	fixture.app.runtimeMutationBeforeLockHook = func(operation string) {
		if operation == "remove-plugin" {
			removeOnce.Do(func() { close(removeEntered) })
		}
	}
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	removeDone := make(chan error, 1)
	go func() { removeDone <- fixture.app.RemovePlugin("review-helper") }()
	select {
	case <-removeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("RemovePlugin did not reach the MCP lifecycle lock")
	}
	// While RemovePlugin waits for the lock, background work starts on the
	// active tab — exactly the window the pre-lock check cannot see.
	busy := newBackgroundJobController(t, "remove-plugin-active-work")
	fixture.app.mu.Lock()
	fixture.app.tabs["active"].Ctrl = busy
	fixture.app.mu.Unlock()
	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	err := <-removeDone
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("RemovePlugin during background work error = %v, want active-work guard", err)
	}
	if !installedPluginNamed(t, "review-helper") {
		t.Fatal("active-work guard fired only after the plugin was already uninstalled")
	}
}

// A global uninstall disconnects every runtime, so the busy guard must cover
// every runtime too: a background job on a sibling tab must fail the removal
// before anything is deleted, not silently lose its plugin MCP mid-run.
func TestRemovePluginRejectsBusySiblingRuntime(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	installGatedTestPluginPackage(t, "h")
	busy := newBackgroundJobController(t, "sibling-busy")
	fixture.app.mu.Lock()
	fixture.app.tabs["sibling"].Ctrl = busy
	fixture.app.mu.Unlock()

	err := fixture.app.RemovePlugin("review-helper")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("RemovePlugin with busy sibling error = %v, want active-work guard", err)
	}
	if !installedPluginNamed(t, "review-helper") {
		t.Fatal("plugin was uninstalled despite a busy sibling runtime")
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("busy-sibling guard still disconnected the shared MCP client")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("busy-sibling guard still removed active registry tools")
	}
}

// Detached runtimes keep running after their tab is closed; the uninstall busy
// guard must see them through the same gate sweep as visible tabs.
func TestRemovePluginRejectsBusyDetachedRuntime(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	installGatedTestPluginPackage(t, "h")
	busy := newBackgroundJobController(t, "detached-busy")
	fixture.app.mu.Lock()
	fixture.app.detachedSessions = map[string]*WorkspaceTab{
		"detached": {
			ID: "detached", Scope: "global", Ready: true,
			Ctrl: busy, disabledMCP: map[string]ServerView{},
		},
	}
	fixture.app.mu.Unlock()

	err := fixture.app.RemovePlugin("review-helper")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("RemovePlugin with busy detached runtime error = %v, want active-work guard", err)
	}
	if !installedPluginNamed(t, "review-helper") {
		t.Fatal("plugin was uninstalled despite a busy detached runtime")
	}
}

// A turn start holds its tab's turn gate before the controller reports active
// work, so an idle check done without the gate can go stale immediately. The
// authorization must wait on the sibling's gate — never disconnect first — and must fail once the gated re-check sees the started work.
func TestAuthorizeAndConnectMCPServerWaitsForSiblingTurnGate(t *testing.T) {
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1) // drain the fixture's initial connect
	sibling := fixture.app.tabs["sibling"]

	// Simulate the racing turn: it takes the gate first, and only transitions
	// its controller to busy while holding it.
	sibling.turnStartMu.Lock()
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	select {
	case got := <-attempts:
		t.Fatalf("trust connection launched (attempt %d) while a sibling turn gate was held", got)
	case err := <-authorizeDone:
		t.Fatalf("AuthorizeAndConnectMCPServer returned %v without waiting for the sibling turn gate", err)
	case <-time.After(700 * time.Millisecond):
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("authorization disconnected the shared client while a sibling turn gate was held")
	}
	busy := newBackgroundJobController(t, "sibling-turn")
	fixture.app.mu.Lock()
	sibling.Ctrl = busy
	fixture.app.mu.Unlock()
	sibling.turnStartMu.Unlock()

	err := <-authorizeDone
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("AuthorizeAndConnectMCPServer after sibling turn start error = %v, want active-work guard", err)
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("failed authorization left the shared client disconnected")
	}
	if _, found := fixture.siblingRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("authorization stripped the busy sibling of its MCP tools")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("authorization stripped the active tab of its MCP tools")
	}
}

// waitForRuntimeAdmissionBarrier polls until the work-admission write lock is
// held, marking the point where a lifecycle mutation froze new admissions.
func waitForRuntimeAdmissionBarrier(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if app.runtimeAdmissionMu.TryRLock() {
			app.runtimeAdmissionMu.RUnlock()
			time.Sleep(2 * time.Millisecond)
			continue
		}
		return
	}
	t.Fatal("lifecycle mutation never acquired the work-admission barrier")
}

func TestBridgeDriveReleasesRuntimeAdmissionWhenTakeoverWasReclaimed(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	fixture.app.tabs["active"].sink = &tabEventSink{tabID: "active", app: fixture.app}
	fixture.app.botBridge = &botBridgeHub{
		takeovers:    make(map[string]bot.DesktopWatchRoute),
		takeoverTabs: make(map[string]string),
	}

	err := fixture.app.bridgeDrive("active", "hello", bot.DesktopWatchRoute{})
	if err == nil || !strings.Contains(err.Error(), "接管已解除") {
		t.Fatalf("bridgeDrive error = %v, want lost-takeover error", err)
	}
	if !fixture.app.runtimeAdmissionMu.TryLock() {
		t.Fatal("bridgeDrive leaked the runtime-admission read lock")
	}
	fixture.app.runtimeAdmissionMu.Unlock()
}

func TestBeginTabTurnWorkspaceRepairStaysOutsideLifecycleAdmission(t *testing.T) {
	fixture := newStaleWorkspaceBindingFixture(t, "admission_writer")
	fixture.tab.reconcileMu.Lock()

	turnDone := make(chan error, 1)
	go func() {
		admission, _, err := fixture.app.beginTabTurn(fixture.tab.ID, false)
		if admission != nil {
			admission.abort()
		}
		turnDone <- err
	}()
	writerRebuildLocked := make(chan struct{})
	writerAdmissionLocked := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		fixture.app.runtimeRebuildMu.Lock()
		close(writerRebuildLocked)
		fixture.app.runtimeAdmissionMu.Lock()
		close(writerAdmissionLocked)
		fixture.app.runtimeAdmissionMu.Unlock()
		fixture.app.runtimeRebuildMu.Unlock()
		close(writerDone)
	}()
	<-writerRebuildLocked
	select {
	case <-writerAdmissionLocked:
		// The repair is still blocked on reconcileMu; acquiring the lifecycle
		// writer here proves no slow repair/build I/O owns the read side.
	case <-time.After(15 * time.Second):
		fixture.tab.reconcileMu.Unlock()
		t.Fatal("workspace repair held runtimeAdmissionMu while waiting")
	}
	fixture.tab.reconcileMu.Unlock()

	// Completing the turn means a full controller rebuild, and on Windows that
	// build's prompt stage alone has been measured at ~5.8s on an idle machine
	// (instruction/skill scan), so the bound must clear a loaded-run rebuild while still failing a turn that never completes.
	select {
	case err := <-turnDone:
		if err != nil {
			t.Fatalf("beginTabTurn after workspace repair: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("workspace repair did not complete after lifecycle writer released")
	}
	select {
	case <-writerDone:
	case <-time.After(15 * time.Second):
		t.Fatal("lifecycle writer did not complete after repaired turn admission")
	}
}

func TestAuthorizeAndConnectMCPServerSerializesCloseOfCapturedRuntime(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1)

	dir := fixture.app.tabs["active"].WorkspaceRoot
	otherRoot := robustTempDir(t)
	otherHost := plugin.NewHost()
	t.Cleanup(otherHost.Close)
	otherCtrl := control.New(control.Options{Host: otherHost, WorkspaceRoot: otherRoot})
	t.Cleanup(otherCtrl.Close)
	fixture.app.tabs = map[string]*WorkspaceTab{
		"active": fixture.app.tabs["active"],
		"other": {
			ID: "other", Scope: "project", WorkspaceRoot: otherRoot, Ready: true,
			Ctrl: otherCtrl, disabledMCP: map[string]ServerView{},
		},
	}
	fixture.app.tabOrder = []string{"active", "other"}
	fixture.app.activeTabID = "active"
	fixture.app.sharedHosts = map[string]*sharedPluginHost{
		dir: {host: fixture.sharedHost, refs: 1},
	}

	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.app.CloseTab("active") }()
	select {
	case err := <-closeDone:
		t.Fatalf("CloseTab bypassed the MCP lifecycle barrier: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("CloseTab(active) after trust: %v", err)
	}
}

func TestCloseTabWaitsForPendingTurnAdmission(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	admission, _, err := fixture.app.beginTabTurn("active", false)
	if err != nil {
		t.Fatalf("beginTabTurn(active): %v", err)
	}
	released := false
	defer func() {
		if !released {
			admission.abort()
		}
	}()

	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.app.CloseTab("active") }()
	select {
	case err := <-closeDone:
		admission.abort()
		released = true
		t.Fatalf("CloseTab bypassed a pending turn admission and closed its controller: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	admission.abort()
	released = true
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseTab(active) after turn admission release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CloseTab did not resume after the pending turn admission was released")
	}
}

func TestCloseTabRemainsVisibleToPendingMCPHostGateSnapshot(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	active := fixture.app.tabs["active"]
	active.turnStartMu.Lock()

	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForRuntimeAdmissionBarrier(t, fixture.app)

	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.app.CloseTab("active") }()
	time.Sleep(300 * time.Millisecond)
	fixture.app.mu.RLock()
	stillVisible := fixture.app.tabs["active"] == active
	fixture.app.mu.RUnlock()
	if !stillVisible {
		active.turnStartMu.Unlock()
		t.Fatal("CloseTab unlinked the runtime before the pending MCP Host gate snapshot")
	}
	select {
	case err := <-closeDone:
		active.turnStartMu.Unlock()
		t.Fatalf("CloseTab bypassed the lifecycle barrier: %v", err)
	default:
	}

	active.turnStartMu.Unlock()
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("CloseTab(active): %v", err)
	}
}

func TestAuthorizeAndConnectMCPServerKeepsInvokingWorkspaceWhenActiveTabChanges(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	otherRoot := robustTempDir(t)
	otherCtrl := control.New(control.Options{Host: plugin.NewHost(), WorkspaceRoot: otherRoot})
	t.Cleanup(otherCtrl.Close)
	fixture.app.tabs["other"] = &WorkspaceTab{
		ID: "other", Scope: "project", WorkspaceRoot: otherRoot, Ready: true,
		Ctrl: otherCtrl, disabledMCP: map[string]ServerView{},
	}
	fixture.app.tabOrder = []string{"active", "sibling", "disabled", "other"}

	sibling := fixture.app.tabs["sibling"]
	sibling.turnStartMu.Lock()
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForRuntimeAdmissionBarrier(t, fixture.app)
	if err := fixture.app.SetActiveTab("other"); err != nil {
		sibling.turnStartMu.Unlock()
		t.Fatalf("SetActiveTab(other): %v", err)
	}
	sibling.turnStartMu.Unlock()

	if err := <-authorizeDone; err != nil {
		t.Fatalf("authorization operation drifted from its invoking workspace: %v", err)
	}
}

// Work admission — not tab-set stability — is the gate invariant: a runtime
// added after the gate snapshot must not be able to start a turn while the
// uninstall holds the barrier. The late turn goes through the real beginTabTurn admission path and must only be admitted after the uninstall.
func TestRemovePluginBlocksLateTurnAdmission(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	installGatedTestPluginPackage(t, "h")
	dir := fixture.app.tabs["active"].WorkspaceRoot

	// Hold an existing tab's gate so the uninstall blocks mid-acquisition
	// with the admission barrier already held.
	sibling := fixture.app.tabs["sibling"]
	sibling.turnStartMu.Lock()
	removeDone := make(chan error, 1)
	go func() { removeDone <- fixture.app.RemovePlugin("review-helper") }()
	waitForRuntimeAdmissionBarrier(t, fixture.app)

	lateCtrl := control.New(control.Options{Host: plugin.NewHost(), WorkspaceRoot: dir})
	t.Cleanup(lateCtrl.Close)
	fixture.app.mu.Lock()
	fixture.app.tabs["late"] = &WorkspaceTab{
		ID: "late", Scope: "global", WorkspaceRoot: dir, Ready: true,
		Ctrl: lateCtrl, disabledMCP: map[string]ServerView{},
	}
	fixture.app.mu.Unlock()
	type admission struct {
		turn *tabTurnAdmission
		ctrl control.SessionAPI
		err  error
	}
	admitted := make(chan admission, 1)
	go func() {
		turn, ctrl, err := fixture.app.beginTabTurn("late", false)
		admitted <- admission{turn: turn, ctrl: ctrl, err: err}
	}()
	select {
	case got := <-admitted:
		t.Fatalf("late turn was admitted (err=%v) while the uninstall held the admission barrier", got.err)
	case <-time.After(300 * time.Millisecond):
	}

	sibling.turnStartMu.Unlock()
	if err := <-removeDone; err != nil {
		t.Fatalf("RemovePlugin(review-helper): %v", err)
	}
	got := <-admitted
	if got.err != nil {
		t.Fatalf("late turn admission after uninstall: %v", got.err)
	}
	got.turn.abort()
	if installedPluginNamed(t, "review-helper") {
		t.Fatal("uninstall did not complete before the late turn was admitted")
	}
}

// A tab created during the 30s trust connection must not complete its async
// controller build — attaching to the shared Host mid-connection can relaunch a
// single-instance server or leave a registry the authorization never saw. The build goes through the real startTabControllerBuild path and must only run after the authorization releases the barrier.
func TestAuthorizeAndConnectMCPServerBlocksLateControllerBuild(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1) // drain the fixture's initial connect
	dir := fixture.app.tabs["active"].WorkspaceRoot

	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2) // connection launched: gates held

	late := &WorkspaceTab{
		ID: "late", Scope: "global", WorkspaceRoot: dir,
		disabledMCP: map[string]ServerView{},
	}
	late.sink = &tabEventSink{tabID: "late", app: fixture.app}
	fixture.app.mu.Lock()
	fixture.app.tabs["late"] = late
	fixture.app.mu.Unlock()
	buildDone := make(chan struct{})
	go func() {
		// a.ctx is nil in this fixture, so the build runs synchronously on
		// this goroutine — through the real buildTabControllerWithContext.
		fixture.app.startTabControllerBuild(late)
		close(buildDone)
	}()
	select {
	case <-buildDone:
		t.Fatal("late controller build completed while the trust connection held the admission barrier")
	case <-time.After(400 * time.Millisecond):
	}

	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	// Completing the build means a full controller build, whose prompt stage
	// alone has been measured at ~5.8s on an idle Windows machine and longer
	// beside other boot-heavy tests in the same process — the bound must clear that while still failing a build that never runs.
	select {
	case <-buildDone:
	case <-time.After(30 * time.Second):
		t.Fatal("late controller build never ran after the authorization released the barrier")
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("authorization did not leave the shared client reconnected")
	}
}

func TestSetMCPServerEnabledRejectsBackgroundJobs(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.setTestCtrl(newBackgroundJobController(t, "mcp-enabled-job"), "")

	err := app.SetMCPServerEnabled("time", false)
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("SetMCPServerEnabled with background job error = %v, want active-work guard", err)
	}
	if tab := app.activeTab(); tab == nil || len(tab.disabledMCP) != 0 {
		t.Fatalf("disabled MCP state changed after rejected toggle: %+v", tab)
	}
}
