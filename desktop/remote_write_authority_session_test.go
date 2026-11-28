package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// tl1854 R5/S5: the desktop remote write-authority hook narrows from
// "release every tab's lease + app-wide read-only flag" to per-session:
// only the tabs on the taken-over session lose their lease; the desktop's
// other sessions keep working untouched.

func newSessionScopeHookFixture(t *testing.T) (*App, []*WorkspaceTab, []string) {
	t.Helper()
	root := t.TempDir()
	dir := config.ProjectSessionDir(root)
	if dir == "" {
		t.Fatal("project session dir unavailable")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var tabs []*WorkspaceTab
	var paths []string
	for _, name := range []string{"sess-a", "sess-b"} {
		path := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		tab := &WorkspaceTab{ID: "tab-" + name, SessionPath: path, WorkspaceRoot: root}
		lease, err := agent.TryAcquireSessionLease(path)
		if err != nil {
			t.Fatal(err)
		}
		tab.sessionLease = lease
		tab.storeSessionLeaseRuntimeKey(sessionRuntimeKey(path))
		tabs = append(tabs, tab)
		paths = append(paths, path)
	}
	app := &App{tabs: map[string]*WorkspaceTab{tabs[0].ID: tabs[0], tabs[1].ID: tabs[1]}}
	t.Cleanup(func() {
		for _, tab := range tabs {
			tab.releaseSessionLeaseQuietly()
		}
	})
	return app, tabs, paths
}

func TestRemoteWriteAuthorityHookIsSessionScoped(t *testing.T) {
	app, tabs, paths := newSessionScopeHookFixture(t)
	pathA, pathB := agent.CanonicalSessionPath(paths[0]), agent.CanonicalSessionPath(paths[1])

	// A remote device takes over session A: only tab A's lease is released;
	// tab B keeps working (its lease stays — the S5 all-tabs regression pin).
	app.handleRemoteWriteAuthority("gc-device-1", true, pathA)
	if !RemoteDeviceHoldsSession(pathA) {
		t.Fatal("session A not marked remote-held")
	}
	if RemoteDeviceHoldsSession(pathB) {
		t.Fatal("session B marked remote-held without any event for it")
	}
	if tabs[0].sessionLease != nil {
		t.Fatal("tab A lease not released by the session-scoped hook")
	}
	if tabs[1].sessionLease == nil {
		t.Fatal("tab B lease released by a hook scoped to session A (S5 regression: all-tabs release)")
	}

	// Release: exactly that session clears.
	app.handleRemoteWriteAuthority("gc-device-1", false, pathA)
	if RemoteDeviceHoldsSession(pathA) {
		t.Fatal("session A still marked remote-held after release")
	}

	// A held=true without a session releases nothing app-wide (the global
	// effect must never resurrect): tab B's lease survives this no-op.
	app.handleRemoteWriteAuthority("gc-device-1", true, "")
	if tabs[1].sessionLease == nil {
		t.Fatal("tab B lease released by a session-less hold (all-tabs release resurrected)")
	}
	// A scoped hold for B releases tab B (that session IS taken over).
	app.handleRemoteWriteAuthority("gc-device-1", true, pathB)
	if tabs[1].sessionLease != nil {
		t.Fatal("tab B lease not released by its own session's hold")
	}
	// Legacy device-wide release (empty session) clears the whole set.
	app.handleRemoteWriteAuthority("gc-device-1", false, "")
	if RemoteDeviceHoldsSession(pathB) {
		t.Fatal("legacy device-wide release left sessions marked held")
	}
}
