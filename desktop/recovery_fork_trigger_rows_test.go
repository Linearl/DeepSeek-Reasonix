package main

import (
	"reflect"
	"runtime"
	"testing"

	"reasonix/internal/control"
)

// TestForkTriggerRowsMatchDesktopFunctions pins the desktop rows of the
// tl1443 fork trigger table to the real methods: renaming one breaks
// compilation here instead of degrading fork_trigger to "other".
func TestForkTriggerRowsMatchDesktopFunctions(t *testing.T) {
	rows := []struct {
		fn   any
		want string
	}{
		{(*App).tabSnapshotLoop, "tab_autosave"},
		{(*App).closeTabRuntime, "tab_close"},
		{(*App).keepOnlyVisibleTab, "tab_close"},
		{(*App).keepOnlyRemoteVisibleTab, "tab_close"},
		{(*App).removeVisibleTabRuntimeAdmissionHeld, "tab_close"},
		{(*App).snapshotMergedWorktreeCloseTab, "tab_close"},
		{(*App).snapshotAllTabs, "app_lifecycle"},
		{(*App).rebindViaDetachedReattach, "session_switch"},
		{(*App).rebindViaFreshBuild, "session_switch"},
		{(*App).SetModelForTab, "model_rebind"},
		{(*App).SetEffortForTab, "model_rebind"},
		{(*App).snapshotSettingsRebuildSource, "model_rebind"},
		{(*App).ensureTabControllerWorkspace, "workspace_rebind"},
		{(*App).snapshotTopicRuntimeBindings, "archive"},
		{(*App).prepareRemovedSessionRuntimes, "session_remove"},
		{(*App).removeWorkspaceSessions, "session_remove"},
		{(*App).releaseDetachedSession, "idle_release"},
		{(*takeoverMirror).demote, "takeover"},
		{(*App).shutdownBody, "shutdown"},
	}
	for _, row := range rows {
		name := runtime.FuncForPC(reflect.ValueOf(row.fn).Pointer()).Name()
		if got := control.ForkTriggerForFunction(name); got != row.want {
			t.Fatalf("ForkTriggerForFunction(%s) = %q, want %q", name, got, row.want)
		}
	}
}
