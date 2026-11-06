package acp

import (
	"reflect"
	"runtime"
	"testing"

	"reasonix/internal/control"
)

// TestForkTriggerRowMatchesACPSnapshot pins the ACP row of the tl1443 fork
// trigger table to the real function (rename = compile error here).
func TestForkTriggerRowMatchesACPSnapshot(t *testing.T) {
	name := runtime.FuncForPC(reflect.ValueOf(snapshotACPController).Pointer()).Name()
	if got := control.ForkTriggerForFunction(name); got != "acp" {
		t.Fatalf("ForkTriggerForFunction(%s) = %q, want acp", name, got)
	}
}
