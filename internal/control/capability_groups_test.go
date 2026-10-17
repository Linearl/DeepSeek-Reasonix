package control

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// TestRouteSuggestsGroupWhenSwitchOn (task 955): the controller wiring —
// SetCapabilityGroups(true) puts group directory rows into the route catalog
// and a trigger hit renders a group suggestion in the transient block; the
// default off state stays byte-clean (no group id ever renders).
func TestRouteSuggestsGroupWhenSwitchOn(t *testing.T) {
	runner := &capabilityRecordingRunner{}
	reg := tool.NewRegistry()
	// rename_session is a session-group member (951 appendix A); registered
	// so the group row counts it as a live member.
	reg.Add(capabilityTestTool{name: "rename_session"})

	c := New(Options{Runner: runner, Registry: reg})
	c.SetCapabilityGroups(true)
	if err := c.Run(context.Background(), "帮我建个任务卡记录这次派单"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(runner.input, "tool:group/session") {
		t.Fatalf("route block must suggest the session group for a trigger hit:\n%s", runner.input)
	}
	if !strings.Contains(runner.input, "inspect") {
		t.Fatalf("group suggestion must point at the group card inspect path:\n%s", runner.input)
	}

	// Off state (default): the same input renders no group id.
	runnerOff := &capabilityRecordingRunner{}
	cOff := New(Options{Runner: runnerOff, Registry: reg})
	if err := cOff.Run(context.Background(), "帮我建个任务卡记录这次派单"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(runnerOff.input, "tool:group/") {
		t.Fatalf("off state must not suggest groups:\n%s", runnerOff.input)
	}
}
