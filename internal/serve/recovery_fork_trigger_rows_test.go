package serve

import (
	"reflect"
	"runtime"
	"testing"

	"reasonix/internal/control"
)

// TestForkTriggerRowsMatchServeFunctions pins the serve rows of the tl1443
// fork trigger table to the real methods (rename = compile error here).
func TestForkTriggerRowsMatchServeFunctions(t *testing.T) {
	rows := []struct {
		fn   any
		want string
	}{
		{(*Server).switchModelLocked, "model_rebind"},
		{(*Server).reloadExtensions, "model_rebind"},
		{(*Server).rebuildDetachedModelSettings, "model_rebind"},
		{(*Server).reattachDetached, "session_switch"},
		{(*Server).handoffLocked, "takeover"},
		{(*Server).reclaimMirroredLocked, "takeover"},
		{(*Server).snapshotForeground, "takeover"},
	}
	for _, row := range rows {
		name := runtime.FuncForPC(reflect.ValueOf(row.fn).Pointer()).Name()
		if got := control.ForkTriggerForFunction(name); got != row.want {
			t.Fatalf("ForkTriggerForFunction(%s) = %q, want %q", name, got, row.want)
		}
	}
}
