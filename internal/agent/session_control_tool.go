package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/tool"
)

// Task 274: cross-session control — stop a peer's active turn and switch a
// peer's model. The dispatch card's freedom ("talk_to_session action OR a
// standalone tool") is taken as ONE standalone tool: talk_to_session stays a
// pure messaging channel, and both mutating verbs share one resolution,
// permission and fail-closed surface instead of growing two ad-hoc verbs.
//
// Hard constraint (user 2026-09-24): set_model is FAIL-CLOSED while the
// target has an active turn. A mid-turn model swap breaks sampling and
// promptCacheKey consistency (the "切模型后上下文暴涨" family), so the caller
// must stop first or wait — the error text names both exits. The orchestration
// stop → wait for turn end → set_model is the caller's sequence; this tool
// never queues or performs a deferred switch.
//
// Permission boundary: resolution goes through the contact directory exactly
// like talk_to_session — a bare/unknown ref never reaches a controller (the
// collab tool extension pattern's refusal precedent). Registered only while
// experimental_session_control is on (fork rule 2, default off).
type SessionControlHooks struct {
	// Stop cancels the target's active turn through the host's controller
	// (the same cancel chain the session's own stop button uses), leaving a
	// "remotely stopped" transcript notice before the cancel lands.
	// known=false: this host cannot see that runtime.
	Stop func(contactID string) (stopped, wasRunning, known bool, err error)
	// SetModel applies the target's switcher-same setter. wasRunning=true is
	// reported so the tool can fail closed with the exact remedy; applied
	// carries the resulting modelRef for the receipt.
	SetModel func(contactID, model string) (applied, wasRunning, known bool, newRef string, err error)
}

type sessionControlTool struct{ cfg SessionCollabConfig }

// NewSessionControlTool builds the cross-session control tool (task 274).
// hooks come from the injected SessionControl config — nil keeps every call a
// refusal rather than a guessed success (CLI/tests build configs without a
// host runtime).
func NewSessionControlTool(cfg SessionCollabConfig) tool.Tool {
	return sessionControlTool{cfg: cfg}
}

func (sessionControlTool) Name() string { return "session_control" }

func (sessionControlTool) Description() string {
	return "Stop a peer session's active turn or switch its model (task 274, experimental). Actions: stop (cancels the target's running turn through its own cancel chain; the target's transcript gets a remote-stop notice) and set_model (switches the target's model with the SAME setter its own switcher uses). HARD RULE: set_model refuses while the target has an active turn — mid-turn swaps break sampling/cache consistency; stop first (action=stop) or wait for the turn to end, then call again. Resolution goes through the contact directory like talk_to_session — bare/unknown refs are refused. Requires experimental_session_control. set_model NEVER acts on the calling session itself (use the in-session switcher) and every applied change is audited (caller, target, old and new model). Receipts carry the outcome (stopped/no-active-turn/applied-modelRef with the previous model)."
}

func (sessionControlTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["stop","set_model"],"description":"stop = cancel the target's active turn; set_model = switch its model (refused while running)."},"target":{"type":"string","description":"Session to control: contact_id, topic_id, or exact title (directory resolution — bare refs refused)."},"model":{"type":"string","description":"Required for set_model: the model name exactly as the target's switcher shows it (provider/model)."}},"required":["action","target"]}`)
}

// ReadOnly is false: both actions mutate the peer session (cancel a turn,
// swap a model). The gate is the experimental switch plus directory
// resolution, not read-only framing.
func (sessionControlTool) ReadOnly() bool { return false }

func (t sessionControlTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		Target string `json:"target"`
		Model  string `json:"model"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	target := strings.TrimSpace(p.Target)
	if target == "" {
		return "", fmt.Errorf("target is required (name the session: contact_id, topic_id, or exact title)")
	}
	// Directory resolution is the permission boundary (talk_to_session
	// precedent): an unknown or bare ref stops here, never at a controller.
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	resolved, err := ResolveTarget(ids, target)
	if err != nil {
		return "", err
	}
	if resolved.Archived {
		return "", fmt.Errorf("session %q is archived and no longer accepts control (restore it first)", target)
	}
	if resolved.ContactID == "" {
		return "", fmt.Errorf("session %q has no contact_id — message it once first (first contact mints the address)", target)
	}
	// Task 387: a model change is a peer-session operation — "非己" (not self)
	// is a hard rule from the capability-gating list. Acting on the caller's
	// own session is what the in-session switcher is for.
	if caller := strings.TrimSpace(t.cfg.currentContactID()); caller != "" && caller == resolved.ContactID {
		return "", fmt.Errorf("session %q is the calling session itself — use its own model switcher instead (cross-session model change refuses to act on self)", target)
	}
	controlHooks := t.cfg.SessionControl
	if controlHooks.Stop == nil || controlHooks.SetModel == nil {
		return "", fmt.Errorf("session_control is not wired on this host (no controller hooks) — the capability is unavailable here")
	}

	switch action {
	case "stop":
		stopped, wasRunning, known, serr := controlHooks.Stop(resolved.ContactID)
		if serr != nil {
			return "", serr
		}
		if !known {
			return "", fmt.Errorf("session %q is not visible to this host (another process or runtime not stood up) — cannot stop what this host cannot see", target)
		}
		out, _ := json.Marshal(map[string]any{
			"stopped":    stopped,
			"wasRunning": wasRunning,
			"target":     resolved.ContactID,
			"note": func() string {
				if !wasRunning {
					return "no active turn — nothing to stop (idempotent no-op)"
				}
				return "active turn cancelled; the target's transcript carries a remote-stop notice"
			}(),
		})
		return string(out), nil

	case "set_model":
		model := strings.TrimSpace(p.Model)
		if model == "" {
			return "", fmt.Errorf("model is required for set_model (the name exactly as the target's switcher shows it)")
		}
		// Task 387: audit trail — caller, target, old model, new model. The old
		// ref is read BEFORE the switch through the same per-contact model
		// visibility the status rows use.
		oldRef := ""
		if t.cfg.SessionInfo != nil {
			if prev, _, infoKnown := t.cfg.SessionInfo(resolved.ContactID); infoKnown {
				oldRef = prev
			}
		}
		applied, wasRunning, known, newRef, merr := controlHooks.SetModel(resolved.ContactID, model)
		if merr == nil && known && applied {
			slog.Info("cross-session model change",
				"caller", strings.TrimSpace(t.cfg.currentContactID()),
				"target", resolved.ContactID,
				"old_model", oldRef,
				"new_model", newRef,
				"task", "387")
		}
		if merr != nil {
			return "", merr
		}
		if !known {
			return "", fmt.Errorf("session %q is not visible to this host (another process or runtime not stood up) — cannot switch what this host cannot see", target)
		}
		// Hard constraint (user 2026-09-24): fail closed on a live turn.
		// The host hook reports the state it observed; a race that starts the
		// turn after this check is caught again by the setter's own active-work
		// guard (SetModelForTab rebuilds under turnStartMu).
		if wasRunning {
			return "", fmt.Errorf("target %q has an active turn — model switch refused (no mid-turn swap): stop it first (action=stop) or wait for the turn to finish, then call set_model again", target)
		}
		if !applied {
			return "", fmt.Errorf("model %q was not applied on the target — it may not exist in that session's catalog; check its switcher list", model)
		}
		out, _ := json.Marshal(map[string]any{
			"applied":  true,
			"modelRef": newRef,
			"target":   resolved.ContactID,
			"oldModel": oldRef,
			// Task 387: the effort warning — the target's effort level resets to
			// the new model's default when the previous level is not supported
			// (config.NormalizeEffort in SetModelForTab clears it silently); the
			// per-request effort override (task 9866) still applies per turn.
			"note": "model applied through the target's own switcher setter (idle at observation); if the target had an effort level the new model does not support, it resets to the new model's default",
		})
		return string(out), nil

	default:
		return "", fmt.Errorf("action must be stop or set_model (got %q)", p.Action)
	}
}
