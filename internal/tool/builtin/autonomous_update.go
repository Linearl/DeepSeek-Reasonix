package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// restartUpdate is the agent-facing half of the versions/ mechanism
// (task 254). It is intentionally NOT init-registered like the other builtins:
// the boot code adds it to the registry only when
// experimental_autonomous_update is on, so a flipped-off switch means the model
// never even sees the tool (the same registration rule drain_inbox follows).
//
// The destructive work stays in the host's existing restart paths
// (RestartAndUpdate / SwitchToVersion, tasks 81/210) and their guards; this
// tool only lists, stages a target, and triggers them. execute needs BOTH
// switches on: autonomous_update registers the tool, restart_update lets the
// host swap the version — the host's error names the missing switch.
type restartUpdate struct {
	// callerSession, when set, resolves the calling session at call time
	// (task 254 third face, S4 call-time evaluation): capability-routed
	// calls never cross the agent's context-binding point, so the busy-guard
	// exemption needs a construction-time resolver bound to the executor's
	// session path. Context bindings win when present (agent path).
	callerSession func() string
}

// NewRestartUpdate returns the restart_update tool without a caller-session
// resolver: only the context-injected (agent) path carries the calling
// session. Boot uses NewRestartUpdateWithCallerSession instead.
func NewRestartUpdate() tool.Tool { return restartUpdate{} }

// NewRestartUpdateWithCallerSession returns the tool bound to a caller-session
// resolver. Boot constructs it after the executor exists so capability-routed
// calls — which never cross the agent's context-binding point — still carry
// the calling session for the busy-guard exemption.
func NewRestartUpdateWithCallerSession(resolve func() string) tool.Tool {
	return restartUpdate{callerSession: resolve}
}

func (restartUpdate) Name() string { return "restart_update" }

// ReadOnly is false: set_target/execute can move the install pointer and end
// the process.
func (restartUpdate) ReadOnly() bool { return false }

func (restartUpdate) Description() string {
	return "Inspect and switch this app's install version (desktop only). Actions: list_versions shows the published versions plus the staged build, each with a health bit (missing CLI means the version must not be switched to — servepool would 503) and which one is active; set_target stages an update target — \"staging\" publishes the staged build, an installed version name rolls back to it — and reports what execute will do; execute performs the staged target and relaunches the app. Execute returns once the swap is committed: the restart follows a moment later and a success must never be retried. Requires the autonomous-update AND restart-and-update experiments; refuse politely when the user has not asked for a version change."
}

func (restartUpdate) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"required":["action"],
"properties":{
  "action":{"type":"string","enum":["list_versions","set_target","execute"],"description":"list_versions: report installed versions + staging with health bits. set_target: stage the update target (target required). execute: perform the staged target and relaunch."},
  "target":{"type":"string","maxLength":128,"description":"set_target only: \"staging\" to publish the staged build, or an installed version name from list_versions to roll back to."}
}
}`)
}

func (r restartUpdate) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var request struct {
		Action string `json:"action"`
		Target string `json:"target"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return "", fmt.Errorf("invalid restart_update args: %w", err)
		}
	}
	controller, ok := tool.AutonomousUpdateControllerFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("restart_update is unavailable outside the desktop app")
	}
	// Calling session: the context binding is exact on the agent path; the
	// construction-time resolver covers every other call face (capability
	// dispatch, task 254 third face).
	caller := tool.RestartCallerSessionFromContext(ctx)
	if caller == "" && r.callerSession != nil {
		caller = strings.TrimSpace(r.callerSession())
	}
	switch request.Action {
	case "list_versions":
		return listVersionsReport(ctx, controller)
	case "set_target":
		if strings.TrimSpace(request.Target) == "" {
			return "", fmt.Errorf("restart_update set_target needs target: \"staging\" or an installed version name from list_versions")
		}
		return controller.SetTarget(ctx, strings.TrimSpace(request.Target))
	case "execute":
		return controller.ExecuteTarget(ctx, caller)
	case "":
		return "", fmt.Errorf("restart_update needs action: list_versions, set_target, or execute")
	default:
		return "", fmt.Errorf("unknown restart_update action %q; use list_versions, set_target, or execute", request.Action)
	}
}

// listVersionsReport renders the controller's rows into the model-facing
// report. Keep it tabular and stable: the model compares health bits across
// versions before choosing a target.
func listVersionsReport(ctx context.Context, controller tool.AutonomousUpdateController) (string, error) {
	versions, active, staging, err := controller.ListVersions(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "active version: %s\n", active)
	if staging != "" {
		fmt.Fprintf(&b, "staged build: %s (unpublished; set_target \"staging\" to publish it)\n", staging)
	} else {
		b.WriteString("staged build: none\n")
	}
	b.WriteString("versions (newest first):\n")
	if len(versions) == 0 {
		b.WriteString("  (no installed versions)\n")
	}
	for _, v := range versions {
		health := "healthy"
		if !v.Healthy {
			health = "UNHEALTHY (missing desktop/CLI binary — do not switch to this)"
		}
		marker := ""
		if v.Active {
			marker = " [active]"
		}
		kind := ""
		if v.Staging {
			kind = " [staging]"
		}
		fmt.Fprintf(&b, "  %s%s%s — %s\n", v.Version, marker, kind, health)
	}
	return b.String(), nil
}
