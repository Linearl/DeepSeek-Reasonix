package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// fakeUpdateController records which action ran and returns canned answers, so
// the tool's dispatch and rendering can be tested without a host.
type fakeUpdateController struct {
	versions      []tool.VersionHealth
	active        string
	staging       string
	listErr       error
	setTargetArgs []string
	setTargetResp string
	execCallers   []string
}

func (f *fakeUpdateController) ListVersions(context.Context) ([]tool.VersionHealth, string, string, error) {
	return f.versions, f.active, f.staging, f.listErr
}
func (f *fakeUpdateController) SetTarget(_ context.Context, target string) (string, error) {
	f.setTargetArgs = append(f.setTargetArgs, target)
	return f.setTargetResp, nil
}
func (f *fakeUpdateController) ExecuteTarget(_ context.Context, callerSession string) (string, error) {
	f.execCallers = append(f.execCallers, callerSession)
	return "restart scheduled", nil
}

func restartUpdateWithContext(t *testing.T, controller tool.AutonomousUpdateController, args string) (string, error) {
	t.Helper()
	ctx := context.Background()
	if controller != nil {
		ctx = tool.WithAutonomousUpdateController(ctx, controller)
	}
	return NewRestartUpdate().Execute(ctx, json.RawMessage(args))
}

func TestRestartUpdateDispatchesThreeActions(t *testing.T) {
	controller := &fakeUpdateController{
		versions: []tool.VersionHealth{
			{Version: "v1.38.3-2", Active: true, Healthy: true},
			{Version: "v1.38.3-1", Healthy: false},
			{Version: "v1.38.3-3-staged", Staging: true, Healthy: true},
		},
		active:  "v1.38.3-2",
		staging: "v1.38.3-3-staged",
	}

	// list_versions renders the tabular report with health bits.
	report, err := restartUpdateWithContext(t, controller, `{"action":"list_versions"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"active version: v1.38.3-2",
		"staged build: v1.38.3-3-staged",
		"v1.38.3-1",
		"UNHEALTHY (missing desktop/CLI binary",
		"[active]",
		"[staging]",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q\n---\n%s", want, report)
		}
	}

	// set_target forwards the target verbatim.
	if _, err := restartUpdateWithContext(t, controller, `{"action":"set_target","target":"staging"}`); err != nil {
		t.Fatal(err)
	}
	if len(controller.setTargetArgs) != 1 || controller.setTargetArgs[0] != "staging" {
		t.Fatalf("set_target args = %v", controller.setTargetArgs)
	}

	// execute forwards the calling session for the busy-guard exemption.
	caller := tool.WithRestartCallerSession(context.Background(), "C:\\sessions\\caller.jsonl")
	ctx := tool.WithAutonomousUpdateController(caller, controller)
	if _, err := NewRestartUpdate().Execute(ctx, json.RawMessage(`{"action":"execute"}`)); err != nil {
		t.Fatal(err)
	}
	if len(controller.execCallers) != 1 || controller.execCallers[0] != `C:\sessions\caller.jsonl` {
		t.Fatalf("execute callers = %v", controller.execCallers)
	}
}

func TestRestartUpdateRejectsBadRequests(t *testing.T) {
	controller := &fakeUpdateController{}
	// Missing action.
	if _, err := restartUpdateWithContext(t, controller, `{}`); err == nil {
		t.Fatal("missing action must be refused")
	}
	// Unknown action.
	if _, err := restartUpdateWithContext(t, controller, `{"action":"uninstall"}`); err == nil {
		t.Fatal("unknown action must be refused")
	}
	// set_target without a target.
	if _, err := restartUpdateWithContext(t, controller, `{"action":"set_target"}`); err == nil {
		t.Fatal("set_target without target must be refused")
	}
	// No controller bound: the tool reports itself unavailable rather than
	// half-failing (the task-81 host-binding contract).
	if _, err := restartUpdateWithContext(t, nil, `{"action":"list_versions"}`); err == nil {
		t.Fatal("without a bound controller the tool must report unavailability")
	}
}

func TestRestartUpdateNotInitRegistered(t *testing.T) {
	// Task 254: registration is conditional in the boot code. If an init()
	// ever auto-registers the tool, the iron-rule-2 gate would be silently
	// bypassed for every host, so guard the property here.
	if _, ok := tool.LookupBuiltin("restart_update"); ok {
		t.Fatal("restart_update must not be in the global builtin registry; boot registers it behind experimental_autonomous_update")
	}
}
