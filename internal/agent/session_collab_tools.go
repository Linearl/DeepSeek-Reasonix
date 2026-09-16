package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	b.WriteString("| contact_id | purpose | title | archived | path |\n|---|---|---|---|---|\n")
	for _, id := range ids {
		archived := ""
		if id.Archived {
			archived = "yes"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | `%s`\n", id.ContactID, id.Purpose, id.Title, archived, filepath.Base(id.SessionPath))
	}
	if dups := duplicateContactIDs(t.cfg.SessionDir, t.cfg.WorkspaceRoot); len(dups) > 0 {
		// A shared address silently routes one session's mail to another, so it
		// is reported instead of being resolved by picking a winner.
		fmt.Fprintf(&b, "\n⚠️ 重复的 contact_id（同名会话文件副本）——这些地址不再唯一，未列入上表：%s\n", strings.Join(dups, ", "))
	}
	return b.String(), nil
}

// duplicateContactIDs reports contact ids shared by more than one session file.
func duplicateContactIDs(sessionDir, workspaceRoot string) []string {
	load := func(sessionPath string) (string, string, string, string, bool) {
		m, found, err := LoadBranchMeta(sessionPath)
		if err != nil || !found || m.ContactID == "" {
			return "", "", "", "", false
		}
		return m.ContactID, "", "", "", true
	}
	count := map[string]int{}
	dirs := []string{sessionDir, config.ProjectSessionDir(workspaceRoot), config.ArchiveDir()}
	dirs = append(dirs, config.AllProjectSessionDirs()...)
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		for _, id := range sessioncollab.ScanDir(dir, "", load) {
			count[id.ContactID]++
		}
	}
	var out []string
	for id, n := range count {
		if n > 1 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
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
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"Target contact_id from list_addressable_sessions."},"message":{"type":"string"},"hop":{"type":"integer","description":"0 for a new chain; 1-5 when relaying."},"delivery":{"type":"string","enum":["followup","steer"],"description":"followup (default) queues; steer injects mid-turn, degrading to followup when it cannot."},"card_id":{"type":"string","description":"Optional task card id to stamp on the message."},"thread_id":{"type":"string","description":"When answering a message, pass the threadId it carried so the requester can match your reply."}},"required":["to","message"]}`)
}

func (talkToSessionTool) ReadOnly() bool { return false }

func (t talkToSessionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		To       string `json:"to"`
		Message  string `json:"message"`
		Hop      int    `json:"hop"`
		Delivery string `json:"delivery"`
		CardID   string `json:"card_id"`
		ThreadID string `json:"thread_id"`
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
	if target.Archived {
		// Archived is a different situation from unknown: the address is real,
		// the session is just no longer an active participant.
		return "", fmt.Errorf("contact_id %q is archived and no longer accepts messages (restore the session first)", p.To)
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
		ThreadID:    strings.TrimSpace(p.ThreadID),
	})
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"status":    "queued",
		"messageId": msg.ID,
		"threadId":  msg.ID,
		"to":        target.ContactID,
		"toPurpose": target.Purpose,
		"delivery":  msg.Delivery,
		"hop":       msg.Hop,
		"queued":    true,
	})
	return string(out), nil
}

// NewTalkToSessionSyncTool is the synchronous variant. It delivers the same
// durable message and then waits — bounded — for an answer carrying the thread
// id. A timeout is reported as a status, not an error: the request is already
// queued, so failing the call would misreport what happened.
func NewTalkToSessionSyncTool(cfg SessionCollabConfig) tool.Tool {
	return talkToSessionSyncTool{cfg: cfg}
}

type talkToSessionSyncTool struct{ cfg SessionCollabConfig }

func (talkToSessionSyncTool) Name() string { return "talk_to_session_sync" }

func (talkToSessionSyncTool) Description() string {
	return "Send a message to another registered session and wait for its reply (task 19 / 142). The request is delivered durably first; if no reply arrives within the timeout this returns status=timeout with the message id, and the answer still lands in your inbox later. Prefer talk_to_session (async) for long tasks. Experimental."
}

func (talkToSessionSyncTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"Target contact_id."},"message":{"type":"string"},"hop":{"type":"integer"},"card_id":{"type":"string"},"timeout_ms":{"type":"integer","description":"How long to wait for the reply (default 30000, max 120000)."}},"required":["to","message"]}`)
}

func (talkToSessionSyncTool) ReadOnly() bool { return false }

func (t talkToSessionSyncTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		To        string `json:"to"`
		Message   string `json:"message"`
		Hop       int    `json:"hop"`
		CardID    string `json:"card_id"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 120*time.Second {
		timeout = 120 * time.Second
	}
	// Reuse the async path verbatim so delivery semantics cannot drift.
	queued, err := talkToSessionTool{cfg: t.cfg}.Execute(ctx, args)
	if err != nil {
		return "", err
	}
	var sent struct {
		MessageID string `json:"messageId"`
	}
	_ = json.Unmarshal([]byte(queued), &sent)
	if sent.MessageID == "" {
		return queued, nil
	}
	me := t.cfg.CurrentContactID
	if me == "" && t.cfg.CurrentSessionPath != "" {
		me = SessionContactID(t.cfg.CurrentSessionPath)
	}
	if me == "" {
		return queued, nil // nothing to receive an answer on
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	reply, ok := sessioncollab.NewMailStore(mailDir).AwaitReply(me, sent.MessageID, timeout)
	if !ok {
		out, _ := json.Marshal(map[string]any{
			"status":    "timeout",
			"messageId": sent.MessageID,
			"threadId":  sent.MessageID,
			"waitedMs":  int(timeout / time.Millisecond),
			"note":      "request was delivered; the reply will arrive in your inbox later",
		})
		return string(out), nil
	}
	out, _ := json.Marshal(map[string]any{
		"status":    "replied",
		"messageId": sent.MessageID,
		"threadId":  sent.MessageID,
		"from":      reply.From,
		"body":      reply.Body,
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

// scanAddressable collects registered sessions from every session directory on
// this machine: the global dir, every project dir, and the archive. Scanning all
// projects (not just the open ones) is what makes "list all addressable
// sessions" true; archived sessions are marked so callers can report "archived"
// distinctly from "never registered".
//
// Duplicate contact_ids are dropped after the first and reported through
// DuplicateContacts: two sessions sharing an address would silently route one
// session's mail to the other.
func scanAddressable(sessionDir, workspaceRoot string) []sessioncollab.Identity {
	load := func(sessionPath string) (contact, purpose, topic, title string, ok bool) {
		m, found, err := LoadBranchMeta(sessionPath)
		if err != nil || !found {
			return "", "", "", "", false
		}
		return m.ContactID, m.Purpose, m.TopicID, m.CustomTitle, m.ContactID != ""
	}
	var out []sessioncollab.Identity
	seenPath := map[string]bool{}
	seenContact := map[string]string{}
	add := func(ids []sessioncollab.Identity, archived bool) {
		for _, id := range ids {
			key := strings.ToLower(id.SessionPath)
			if seenPath[key] {
				continue
			}
			seenPath[key] = true
			if prev, dup := seenContact[id.ContactID]; dup {
				// Keep the first and let the caller surface the collision rather
				// than silently choosing a recipient.
				_ = prev
				continue
			}
			seenContact[id.ContactID] = id.SessionPath
			id.Archived = archived
			out = append(out, id)
		}
	}

	dirs := []string{sessionDir}
	if workspaceRoot != "" {
		dirs = append(dirs, config.ProjectSessionDir(workspaceRoot))
	}
	dirs = append(dirs, config.AllProjectSessionDirs()...)
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		add(sessioncollab.ScanDir(dir, "", load), false)
	}
	// Archived sessions are still addressable history: report them as archived
	// rather than pretending they never existed.
	add(sessioncollab.ScanDir(config.ArchiveDir(), "", load), true)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}
