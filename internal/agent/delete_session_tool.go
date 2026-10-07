package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
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
	// HasTurn is true when deleting this session would interrupt or discard
	// work: a controller is mid-turn on it, OR its transcript already holds
	// turns. The transcript half is read from the file, the way read_session_tail
	// reads it, so "no open tab" can never be reported as "nothing here"
	// (task 158.D: a session with 45s of work came back hasTurn=false).
	HasTurn bool `json:"hasTurn"`
	// TurnInFlight is the narrower fact behind HasTurn — a live controller is
	// mid-turn right now — kept separate so a caller can tell "busy" from
	// "has history".
	TurnInFlight bool `json:"turnInFlight,omitempty"`
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

// unconsumedMailCount is the task-509 pre-archive gate probe: how many inbox
// entries of the target contact are NOT covered by its seen cursor
// (queued≠read — memory 跨会话消息投递复核法). A read error is returned, not
// swallowed: the gate must fail closed (宁紧勿松), so an unverifiable inbox
// refuses the archive exactly like an unconsumed one. A contact with no id
// never participated in the mail system — the same convention the
// get_session_status mail probe uses — so there is nothing to protect.
func (t deleteSessionTool) unconsumedMailCount(contactID string) (int, error) {
	if strings.TrimSpace(contactID) == "" {
		return 0, nil
	}
	mailDir := t.cfg.MailDir
	if strings.TrimSpace(mailDir) == "" {
		mailDir = config.SessionCollabMailDir()
	}
	unread, err := sessioncollab.NewMailStore(mailDir).Peek(contactID)
	if err != nil {
		return 0, err
	}
	return len(unread), nil
}

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
	if own := t.cfg.currentSessionPath(); own != "" && strings.EqualFold(id.SessionPath, own) {
		return "", fmt.Errorf("refusing to delete the calling session (%q) — close it from the desktop instead", p.Target)
	}
	if t.del == nil {
		return "", fmt.Errorf("delete_session: this host cannot delete sessions")
	}
	// 任务 509 未消费消息前置门（软开关，默认关）：开关注册在 [agent]
	// session_collab_delete_unread_gate。开启时，目标收件箱还有 seen 游标未
	// 覆盖的条目（queued≠已读）就拒绝 confirm 归档，dry-run 则如实上报条数；
	// 关闭 = 行为与既有版本逐字节一致。归档 ≠ 删除（manual-restore trash），
	// 但排队未读的消息会随归档退出活跃协作面——自动归档路径必须先消费。
	unconsumed := -1 // -1 = gate off / not probed
	if t.cfg.DeleteUnreadGate {
		n, err := t.unconsumedMailCount(id.ContactID)
		if err != nil {
			return "", fmt.Errorf("delete_session: cannot verify the inbox of %q (%v) — the unconsumed-mail gate fails closed (宁紧勿松), archive refused; check the inbox panel and retry", p.Target, err)
		}
		unconsumed = n
		if p.Confirm && n > 0 {
			return "", fmt.Errorf("delete_session: %q still has %d unconsumed inbox message(s) (queued≠read) — the task-509 pre-archive gate refuses; have the recipient consume them first (drain_inbox / inbox panel), then re-run", p.Target, n)
		}
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
		// The impact must name the conversation the caller is about to trash,
		// and it must be the SAME name the contact directory shows: fall back to
		// the resolved identity's title when the host did not fill it in.
		if strings.TrimSpace(impact.Title) == "" {
			impact.Title = id.Title
		}
		out := map[string]any{
			"status": "dry_run",
			"impact": impact,
			"note":   "re-run with confirm=true to move it to trash",
		}
		// 任务 509: with the gate on, the dry run also answers "will confirm be
		// refused?" so the orchestrator can consume mail before asking.
		if unconsumed >= 0 {
			out["mailGate"] = map[string]any{"unconsumed": unconsumed, "refusesConfirm": unconsumed > 0}
		}
		outJSON, _ := json.Marshal(out)
		return string(outJSON), nil
	}
	impact, result, err := t.del(id.ContactID, id.SessionPath, false)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(impact.Title) == "" {
		impact.Title = id.Title
	}
	out := map[string]any{
		"status": "trashed",
		"result": result,
		"impact": impact,
	}
	// 任务 509: the gate passed with zero unconsumed — record that in the tool
	// result so the archive decision carries its own evidence (留痕).
	if unconsumed >= 0 {
		out["mailGate"] = map[string]any{"unconsumed": unconsumed}
	}
	outJSON, _ := json.Marshal(out)
	return string(outJSON), nil
}
