package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// SessionCollabConfig carries host knobs for the task 19 collaboration tools.
type SessionCollabConfig struct {
	Enabled       bool
	SessionDir    string
	WorkspaceRoot string
	// CurrentSessionPath is the calling session's transcript path (for reply routing).
	CurrentSessionPath string
	// MailDir overrides the shared collab mailbox root; empty uses config's.
	MailDir string
	// CurrentContactID is filled on first ensure for the calling session.
	CurrentContactID string
}

func NewSetSessionPurposeTool(cfg SessionCollabConfig) tool.Tool {
	return setSessionPurposeTool{cfg: cfg}
}

type setSessionPurposeTool struct{ cfg SessionCollabConfig }

func (setSessionPurposeTool) Name() string { return "set_session_purpose" }

func (setSessionPurposeTool) Description() string {
	return "Register this session's stable contact_id purpose (one-line duty) for multi-session collaboration. Experimental. Renaming the topic does not change the contact id."
}

func (setSessionPurposeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"purpose":{"type":"string","description":"One-line duty, e.g. 'React frontend expert'."}},"required":["purpose"]}`)
}

func (setSessionPurposeTool) ReadOnly() bool { return false }

func (t setSessionPurposeTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Purpose string `json:"purpose"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Purpose) == "" {
		return "", fmt.Errorf("purpose is required")
	}
	session := t.cfg.CurrentSessionPath
	if session == "" {
		return "", fmt.Errorf("set_session_purpose: current session path is unknown")
	}
	contact, err := SetSessionPurpose(session, p.Purpose)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]string{"contactId": contact, "purpose": strings.TrimSpace(p.Purpose)})
	return string(out), nil
}

func NewListAddressableSessionsTool(cfg SessionCollabConfig) tool.Tool {
	return listAddressableSessionsTool{cfg: cfg}
}

type listAddressableSessionsTool struct{ cfg SessionCollabConfig }

func (listAddressableSessionsTool) Name() string { return "list_addressable_sessions" }

func (listAddressableSessionsTool) Description() string {
	return "List sessions registered for multi-session collaboration: contact_id, purpose, title, and path. Experimental. Use contact_id with talk_to_session."
}

func (listAddressableSessionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (listAddressableSessionsTool) ReadOnly() bool { return true }

func (t listAddressableSessionsTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	if len(ids) == 0 {
		return "No addressable sessions yet. Call set_session_purpose from a session to register it.\n", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Addressable sessions (%d)\n\n", len(ids))
	b.WriteString("| contact_id | purpose | title | path |\n|---|---|---|---|\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "| `%s` | %s | %s | `%s`\n", id.ContactID, id.Purpose, id.Title, filepath.Base(id.SessionPath))
	}
	return b.String(), nil
}

func NewTalkToSessionTool(cfg SessionCollabConfig) tool.Tool {
	return talkToSessionTool{cfg: cfg}
}

type talkToSessionTool struct{ cfg SessionCollabConfig }

func (talkToSessionTool) Name() string { return "talk_to_session" }

func (talkToSessionTool) Description() string {
	return "Send a message to another registered session by contact_id (task 19 / 142-143). delivery=followup queues for the target's next turn; delivery=steer asks for mid-turn injection and degrades to a queued follow-up when the target has no injectable turn (the sender is told). hop must be 0 for a new chain; pass hop+1 when relaying. Experimental."
}

func (talkToSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"Target contact_id from list_addressable_sessions."},"message":{"type":"string"},"hop":{"type":"integer","description":"0 for a new chain; 1-5 when relaying."},"delivery":{"type":"string","enum":["followup","steer"],"description":"followup (default) queues; steer injects mid-turn, degrading to followup when it cannot."},"card_id":{"type":"string","description":"Optional task card id to stamp on the message."}},"required":["to","message"]}`)
}

func (talkToSessionTool) ReadOnly() bool { return false }

func (t talkToSessionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		To       string `json:"to"`
		Message  string `json:"message"`
		Hop      int    `json:"hop"`
		Delivery string `json:"delivery"`
		CardID   string `json:"card_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.To) == "" || strings.TrimSpace(p.Message) == "" {
		return "", fmt.Errorf("to and message are required")
	}
	if p.Hop < 0 || p.Hop > sessioncollab.MaxHop+1 {
		return "", fmt.Errorf("invalid hop %d", p.Hop)
	}
	delivery, err := sessioncollab.ValidateDelivery(p.Delivery)
	if err != nil {
		return "", err
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	target, ok := sessioncollab.ResolveContact(ids, p.To)
	if !ok {
		return "", fmt.Errorf("%w: contact_id %q is not registered (use list_addressable_sessions)", sessioncollab.ErrNotFound, p.To)
	}
	fromContact := t.cfg.CurrentContactID
	if fromContact == "" && t.cfg.CurrentSessionPath != "" {
		fromContact = SessionContactID(t.cfg.CurrentSessionPath)
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	mail := sessioncollab.NewMailStore(mailDir)
	msg, err := mail.Deliver(sessioncollab.MailMessage{
		From:        fromContact,
		FromSession: t.cfg.CurrentSessionPath,
		To:          target.ContactID,
		Body:        strings.TrimSpace(p.Message),
		Delivery:    string(delivery),
		Hop:         p.Hop,
		CardID:      p.CardID,
		ReplyTo:     fromContact,
	})
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"status":    "queued",
		"messageId": msg.ID,
		"to":        target.ContactID,
		"toPurpose": target.Purpose,
		"delivery":  msg.Delivery,
		"hop":       msg.Hop,
		"queued":    true,
	})
	return string(out), nil
}

func workspaceRootForMail(cfg SessionCollabConfig, target sessioncollab.Identity) string {
	if strings.TrimSpace(target.Workspace) != "" {
		return target.Workspace
	}
	if strings.TrimSpace(cfg.WorkspaceRoot) != "" {
		return cfg.WorkspaceRoot
	}
	return filepath.Dir(cfg.SessionDir)
}

// scanAddressable collects registered sessions from the global session dir and
// the current project's session dir. Global sessions carry workspaceRoot "" so
// delivery resolves the shared global mailbox; project sessions carry their
// project root so delivery lands in that project's mailbox.
func scanAddressable(sessionDir, workspaceRoot string) []sessioncollab.Identity {
	load := func(sessionPath string) (contact, purpose, topic, title string, ok bool) {
		m, found, err := LoadBranchMeta(sessionPath)
		if err != nil || !found {
			return "", "", "", "", false
		}
		return m.ContactID, m.Purpose, m.TopicID, m.CustomTitle, m.ContactID != ""
	}
	var out []sessioncollab.Identity
	seen := map[string]bool{}
	add := func(ids []sessioncollab.Identity) {
		for _, id := range ids {
			key := strings.ToLower(id.SessionPath)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, id)
		}
	}
	add(sessioncollab.ScanDir(sessionDir, "", load))
	if workspaceRoot != "" {
		projSessions := config.ProjectSessionDir(workspaceRoot)
		if !samePath(projSessions, sessionDir) {
			add(sessioncollab.ScanDir(projSessions, workspaceRoot, load))
		}
	}
	return out
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(absA, absB)
	}
	return absA == absB
}
