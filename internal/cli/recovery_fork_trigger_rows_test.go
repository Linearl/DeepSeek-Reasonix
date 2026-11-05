package cli

import (
	"reflect"
	"runtime"
	"testing"

	"reasonix/internal/control"
)

// TestForkTriggerRowsMatchCLIFunctions pins the chat TUI rows of the tl1443
// fork trigger table to the real methods (rename = compile error here).
func TestForkTriggerRowsMatchCLIFunctions(t *testing.T) {
	rows := []struct {
		fn   any
		want string
	}{
		{(*chatTUI).runModelSubcommand, "model_rebind"},
		{(*chatTUI).runEffortCommand, "model_rebind"},
		{(*chatTUI).scheduleRuntimeReload, "model_rebind"},
		{(*chatTUI).scheduleCurrentControllerRebuild, "model_rebind"},
		{(*chatTUI).scheduleSkillSessionRefresh, "model_rebind"},
		{adoptCarriedHistoryPreservingProfileAndGrants, "model_rebind"},
		{(*chatTUI).runResumeCommand, "session_switch"},
		{chatTUI.applyResumePick, "session_switch"},
		{chatTUI.update, "compact"},
		{(*chatTUI).runTakeoverCommand, "takeover"},
		{(*cliTakeoverManager).returnMirrorTransaction, "takeover"},
		{chatTUI.shutdownAndQuit, "shutdown"},
	}
	for _, row := range rows {
		name := runtime.FuncForPC(reflect.ValueOf(row.fn).Pointer()).Name()
		if got := control.ForkTriggerForFunction(name); got != row.want {
			t.Fatalf("ForkTriggerForFunction(%s) = %q, want %q", name, got, row.want)
		}
	}
}
