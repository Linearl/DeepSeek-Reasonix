package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// DeleteSessionImpact is the host's pre-delete report so the caller knows what
// it is about to destroy before agreeing (task 154 sub-item A).
type DeleteSessionImpact struct {
	Title       string `json:"title"`
	ContactID   string `json:"contactId,omitempty"`
	SessionPath string `json:"sessionPath"`
	Archived    bool   `json:"archived"`
	// OpenTab is true when a visible desktop tab is bound to this session.
	OpenTab bool `json:"openTab"`
	// HasTurn is true when a live controller is mid-turn on this session.
	HasTurn bool `json:"hasTurn"`
}

// DeleteSessionResult reports the outcome of a move-to-trash.
type DeleteSessionResult struct {
	ContactID   string `json:"contactId"`
	SessionPath string `json:"sessionPath"`
	Trashed     bool   `json:"trashed"`
	// RestoreUntil is a human-readable recovery note. The desktop trash has no
	// automatic 30-day purge, so this is "manual" — the user restores it from
	// the Trash page. The previous "30d" was a claim no code enforced.
	RestoreUntil string `json:"restoreUntil"`
}

// DeleteSessionFunc is the host capability that moves a session to trash
// (matching the desktop's Delete, which is a manual-restore trash). Nil =
// not supported.
//
// dryRun=true MUST NOT delete: it only fills the impact report so the caller
// can see open tab / in-flight turn before confirming. dryRun=false performs the
// real trash after releasing this process's own runtime bindings.
type DeleteSessionFunc func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error)

// NewDeleteSessionTool lets a secretary retire a session from the contact
// directory without opening the desktop (task 154 sub-item A). It refuses to
// delete the calling session's own transcript — that is a different, self-
// destructive operation with its own UI.
func NewDeleteSessionTool(cfg SessionCollabConfig, del DeleteSessionFunc) tool.Tool {
	return deleteSessionTool{cfg: cfg, del: del}
}

type deleteSessionTool struct {
	cfg SessionCollabConfig
	del DeleteSessionFunc
}

func (deleteSessionTool) Name() string { return "delete_session" }

func (deleteSessionTool) Description() string {
	return "Move a session to the desktop trash (manual restore, same as the desktop Delete action) so it leaves the contact directory. Takes a contact_id / topic_id / title. The caller's own session is refused. Dry-run first: the impact report shows whether the session has an open tab or an in-flight turn, so you can tell the user before confirming. Experimental."
}

func (deleteSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"contact_id, topic_id, or exact title of the session to trash."},"confirm":{"type":"boolean","description":"Must be true to execute. Omit to get the impact report only (dry run)."}},"required":["target"]}`)
}

func (deleteSessionTool) ReadOnly() bool { return false }

func (t deleteSessionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target  string `json:"target"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Target) == "" {
		return "", fmt.Errorf("target is required")
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	id, err := ResolveTarget(ids, p.Target)
	if err != nil {
		return "", err
	}
	// Guard: the calling session must not delete itself. That is a self-
	// destructive path the desktop exposes through a different UI; a secretary
	// accidentally trashing its own transcript mid-turn would be unrecoverable
	// from the collaboration surface.
	if t.cfg.CurrentSessionPath != "" && strings.EqualFold(id.SessionPath, t.cfg.CurrentSessionPath) {
		return "", fmt.Errorf("refusing to delete the calling session (%q) — close it from the desktop instead", p.Target)
	}
	if t.del == nil {
		return "", fmt.Errorf("delete_session: this host cannot delete sessions")
	}
	// Dry run and real delete are separate calls with an explicit dryRun flag,
	// so a "just look at the impact" request can never move the session (audit
	// F154-2: a prior version called the host unconditionally and the host
	// always trashed).
	if !p.Confirm {
		impact, _, err := t.del(id.ContactID, id.SessionPath, true)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"status": "dry_run",
			"impact": impact,
			"note":   "re-run with confirm=true to move it to trash",
		})
		return string(out), nil
	}
	impact, result, err := t.del(id.ContactID, id.SessionPath, false)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"status": "trashed",
		"result": result,
		"impact": impact,
	})
	return string(out), nil
}
