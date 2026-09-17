package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	return "Register a session's duty (one-line purpose) in the contact directory (通讯录). By default it is THIS session; pass `target` to register another session's duty when you know what it does (after reading its tail with read_session_tail, or after it told you). A session can also change its own duty this way — later calls overwrite. Experimental. Renaming the topic does not change the contact id."
}

func (setSessionPurposeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"purpose":{"type":"string","description":"One-line duty, e.g. 'React frontend expert'."},"target":{"type":"string","description":"Optional: contact_id, topic_id, or exact title of ANOTHER session to register. Omit to register this session."}},"required":["purpose"]}`)
}

func (setSessionPurposeTool) ReadOnly() bool { return false }

func (t setSessionPurposeTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Purpose string `json:"purpose"`
		Target  string `json:"target"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Purpose) == "" {
		return "", fmt.Errorf("purpose is required")
	}
	session := t.cfg.CurrentSessionPath
	appliedTo := "self"
	if strings.TrimSpace(p.Target) != "" {
		ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
		id, rerr := ResolveTarget(ids, p.Target)
		if rerr != nil {
			return "", rerr
		}
		if id.Archived {
			return "", fmt.Errorf("session %q is archived; cannot register a duty on it", p.Target)
		}
		session = id.SessionPath
		appliedTo = "other"
	}
	if session == "" {
		return "", fmt.Errorf("set_session_purpose: no session path (pass target, or run inside a session)")
	}
	contact, err := SetSessionPurpose(session, p.Purpose)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]string{"contactId": contact, "purpose": strings.TrimSpace(p.Purpose), "appliedTo": appliedTo})
	return string(out), nil
}

func NewListAddressableSessionsTool(cfg SessionCollabConfig) tool.Tool {
	return listAddressableSessionsTool{cfg: cfg}
}

type listAddressableSessionsTool struct{ cfg SessionCollabConfig }

func (listAddressableSessionsTool) Name() string { return "list_addressable_sessions" }

func (listAddressableSessionsTool) Description() string {
	return "List the contact directory (通讯录): metadata only — title, purpose, contact_id, topic_id. No transcript content (use read_session_tail for that). Newest first; pass limit to page. Live conversations only by default (deleted .trash sessions never appear; pass archived=true to include retired history). Use contact_id, topic_id, or the exact title as `to` in talk_to_session, or search_sessions(query) when you do not know the name. Experimental."
}

func (listAddressableSessionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max sessions to return, newest first (default 200, max 1000). Omit for the first page."},"archived":{"type":"boolean","description":"Include retired archive sessions (default false)."}},"required":[]}`)
}

func (listAddressableSessionsTool) ReadOnly() bool { return true }

func (t listAddressableSessionsTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Limit    int   `json:"limit"`
		Archived *bool `json:"archived"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	return directoryPage(t.cfg, p.Limit, p.Archived, "")
}

// NewSearchSessionsTool finds sessions by keyword in title/purpose, so a large
// directory does not hide the one you need behind the 200-row first page.
func NewSearchSessionsTool(cfg SessionCollabConfig) tool.Tool {
	return searchSessionsTool{cfg: cfg}
}

type searchSessionsTool struct{ cfg SessionCollabConfig }

func (searchSessionsTool) Name() string { return "search_sessions" }

func (searchSessionsTool) Description() string {
	return "Search the contact directory (通讯录) by keyword — case-insensitive substring match on title and purpose (also contact_id / topic_id). Use this instead of paging list_addressable_sessions when you know part of the name. Metadata only; no transcript content. Experimental."
}

func (searchSessionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Keyword or fragment of the session title / purpose."},"limit":{"type":"integer","description":"Max matches (default 50, max 500)."},"archived":{"type":"boolean","description":"Include retired archive sessions (default false)."}},"required":["query"]}`)
}

func (searchSessionsTool) ReadOnly() bool { return true }

func (t searchSessionsTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query    string `json:"query"`
		Limit    int    `json:"limit"`
		Archived *bool  `json:"archived"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 500 {
		p.Limit = 500
	}
	return directoryPage(t.cfg, p.Limit, p.Archived, p.Query)
}

// directoryPage is the shared projection for list and search: live-only by
// default, metadata-only rows, and a total that respects the same filter the
// caller is paging over (so total never counts archive when archive is hidden).
func directoryPage(cfg SessionCollabConfig, limit int, archived *bool, query string) (string, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	includeArchived := archived != nil && *archived
	q := strings.ToLower(strings.TrimSpace(query))

	all := scanAddressable(cfg.SessionDir, cfg.WorkspaceRoot)
	type row struct {
		Title     string `json:"title"`
		Purpose   string `json:"purpose,omitempty"`
		ContactID string `json:"contactId,omitempty"`
		TopicID   string `json:"topicId,omitempty"`
		Archived  bool   `json:"archived,omitempty"`
	}
	rows := make([]row, 0, limit)
	eligible := 0
	for _, id := range all {
		if id.Archived && !includeArchived {
			continue
		}
		if q != "" {
			hay := strings.ToLower(id.Title + "\x00" + id.Purpose + "\x00" + id.ContactID + "\x00" + id.TopicID)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		eligible++
		if len(rows) < limit {
			rows = append(rows, row{
				Title:     id.Title,
				Purpose:   id.Purpose,
				ContactID: id.ContactID,
				TopicID:   id.TopicID,
				Archived:  id.Archived,
			})
		}
	}
	out, _ := json.Marshal(map[string]any{
		"returned": len(rows),
		"total":    eligible,
		"limit":    limit,
		"query":    q,
		// Explicit so a caller never expects content here: that is read_session_tail.
		"content":  "none — use read_session_tail(target) for transcript bytes",
		"note":     "live conversations only; pass archived=true to include retired history. Deleted (.trash) sessions are never listed.",
		"sessions": rows,
	})
	return string(out), nil
}

// NewReadSessionTailTool lets a session peek at another conversation's recent
// turns before assigning it work, so a duty line is never guessed from a title
// alone. Read-only, size-bounded, and resolved through the contact directory so
// it cannot open an arbitrary path.
func NewReadSessionTailTool(cfg SessionCollabConfig) tool.Tool {
	return readSessionTailTool{cfg: cfg}
}

type readSessionTailTool struct{ cfg SessionCollabConfig }

func (readSessionTailTool) Name() string { return "read_session_tail" }

func (readSessionTailTool) Description() string {
	return "Read the last N bytes (default 10 KiB) of another session's transcript from the contact directory (通讯录). Use it before assigning work or registering a duty on someone else, so the duty line is grounded in what that session actually did rather than its title. Read-only. Experimental."
}

func (readSessionTailTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"contact_id, topic_id, or exact title of the session to read."},"max_bytes":{"type":"integer","description":"How many trailing bytes to return (default 10240, max 65536)."}},"required":["target"]}`)
}

func (readSessionTailTool) ReadOnly() bool { return true }

func (t readSessionTailTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target   string `json:"target"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Target) == "" {
		return "", fmt.Errorf("target is required")
	}
	n := p.MaxBytes
	if n <= 0 {
		n = 10 * 1024
	}
	if n > 64*1024 {
		n = 64 * 1024
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	id, err := ResolveTarget(ids, p.Target)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(id.SessionPath)
	if err != nil {
		return "", fmt.Errorf("cannot read session %q: %w", p.Target, err)
	}
	if len(b) > n {
		// Cut at a line boundary when we can, so the reader is not handed a
		// half-written JSON record.
		cut := n
		for i := n - 1; i > 0; i-- {
			if b[i] == '\n' {
				cut = i + 1
				break
			}
		}
		b = b[len(b)-cut:]
	}
	out, _ := json.Marshal(map[string]any{
		"title":     id.Title,
		"purpose":   id.Purpose,
		"contactId": id.ContactID,
		"topicId":   id.TopicID,
		"bytes":     len(b),
		"truncated": len(b) == n,
		"tail":      string(b),
	})
	return string(out), nil
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
	return "Send a message to another session in the contact directory (通讯录, task 19 / 142-143). `to` accepts a contact_id, a topic_id, or the exact title shown by list_addressable_sessions — the title is the human way to pick someone when you have not met them yet, and the target gains a contact_id on first contact. delivery=followup queues for the target's next turn; delivery=steer asks for mid-turn injection and degrades to a queued follow-up when the target has no injectable turn (the sender is told). Experimental."
}

func (talkToSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"Target: contact_id, topic_id, or the exact title from list_addressable_sessions."},"message":{"type":"string"},"hop":{"type":"integer","description":"0 for a new chain. The system derives the real depth from the thread."},"delivery":{"type":"string","enum":["followup","steer"],"description":"followup (default) queues; steer injects mid-turn, degrading to followup when it cannot."},"card_id":{"type":"string","description":"Optional task card id to stamp on the message."},"thread_id":{"type":"string","description":"When answering a message, pass the threadId it carried so the requester can match your reply."}},"required":["to","message"]}`)
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
	target, err := ResolveTarget(ids, p.To)
	if err != nil {
		return "", err
	}
	if target.Archived {
		// Archived is a different situation from unknown: the address is real,
		// the session is just no longer an active participant.
		return "", fmt.Errorf("session %q is archived and no longer accepts messages (restore it first)", p.To)
	}
	// First contact mints the address, so naming a conversation by its title is
	// enough to make it permanently addressable.
	if target.ContactID == "" {
		minted, merr := EnsureContactID(target.SessionPath)
		if merr != nil {
			return "", fmt.Errorf("cannot mint a contact_id for %q: %w", p.To, merr)
		}
		target.ContactID = minted
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
// this machine: the global dir, every project dir, and the archive.
//
// Only the LEGACY `sessions/` trees are scanned — never `sessions-v4/`. With
// session_storage=v4 dual-write the same conversation lives in both; listing
// both would count every session twice. sessions/ remains the authority, so one
// conversation appears once. A stem-level dedup is a second line of defence in
// case a v4 path ever slipped into the scan set.
//
// Archived sessions are marked so callers can report "archived" distinctly from
// "never registered". Deleted sessions live under .trash/ subdirectories, which
// ScanDir does not recurse into — they never appear.
//
// Duplicate contact_ids are dropped after the first and reported through
// DuplicateContacts: two sessions sharing an address would silently route one
// session's mail to the other.
func scanAddressable(sessionDir, workspaceRoot string) []sessioncollab.Identity {
	load := func(sessionPath string) (contact, purpose, topic, title string, ok bool) {
		m, found, err := LoadBranchMeta(sessionPath)
		if err != nil {
			return "", "", "", "", false
		}
		title = strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath))
		if found {
			contact = m.ContactID
			purpose = m.Purpose
			topic = m.TopicID
			if m.CustomTitle != "" {
				title = m.CustomTitle
			} else if m.TopicTitle != "" {
				title = m.TopicTitle
			}
		}
		// Every conversation belongs in the directory; contact_id is minted on
		// first contact rather than being a precondition for existence.
		return contact, purpose, topic, title, true
	}
	var out []sessioncollab.Identity
	seenPath := map[string]bool{}
	seenStem := map[string]bool{}
	seenContact := map[string]string{}
	add := func(ids []sessioncollab.Identity, workspace, scope string, archived bool) {
		for _, id := range ids {
			key := strings.ToLower(id.SessionPath)
			if seenPath[key] {
				continue
			}
			seenPath[key] = true
			// Stem dedup: the same conversation dual-written to v4 shares its
			// basename stem. Count it once.
			stem := strings.ToLower(strings.TrimSuffix(filepath.Base(id.SessionPath), filepath.Ext(id.SessionPath)))
			if stem != "" && seenStem[stem] {
				continue
			}
			seenStem[stem] = true
			if id.ContactID != "" {
				if _, dup := seenContact[id.ContactID]; dup {
					// Keep the first and let the caller surface the collision
					// rather than silently choosing a recipient.
					continue
				}
				seenContact[id.ContactID] = id.SessionPath
			}
			id.Workspace = workspace
			id.Scope = scope
			id.Archived = archived
			out = append(out, id)
		}
	}

	dirs := []struct {
		dir       string
		workspace string
		scope     string
		archived  bool
	}{
		{sessionDir, "", "global", false},
		{config.ProjectSessionDir(workspaceRoot), workspaceRoot, "project", false},
		{config.ArchiveDir(), "", "global", true},
	}
	for _, dir := range dirs {
		if strings.TrimSpace(dir.dir) == "" {
			continue
		}
		add(sessioncollab.ScanDir(dir.dir, dir.workspace, load), dir.workspace, dir.scope, dir.archived)
	}
	for _, projectSessions := range config.AllProjectSessionDirs() {
		root := ""
		// sessions dir is <state>/projects/<slug>/sessions; the slug is enough
		// for OpenTopicSession, which only needs a consistent scope+root pair.
		root = filepath.Dir(filepath.Dir(projectSessions))
		add(sessioncollab.ScanDir(projectSessions, root, load), root, "project", false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// ResolveTarget maps a directory reference — contact_id, topic_id, or an
// exact title — to a session. A session that has not minted a contact_id yet
// gets one now, so the first message to a named conversation is enough to make
// it permanently addressable.
func ResolveTarget(ids []sessioncollab.Identity, ref string) (sessioncollab.Identity, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return sessioncollab.Identity{}, fmt.Errorf("target is required")
	}
	var byContact, byTopic, byTitle []sessioncollab.Identity
	for _, id := range ids {
		switch {
		case id.ContactID != "" && strings.EqualFold(id.ContactID, ref):
			byContact = append(byContact, id)
		case id.TopicID != "" && strings.EqualFold(id.TopicID, ref):
			byTopic = append(byTopic, id)
		case id.Title != "" && strings.EqualFold(id.Title, ref):
			byTitle = append(byTitle, id)
		}
	}
	if len(byContact) == 1 {
		return byContact[0], nil
	}
	if len(byTopic) == 1 {
		return byTopic[0], nil
	}
	if len(byTitle) > 1 {
		names := make([]string, 0, len(byTitle))
		for _, id := range byTitle {
			names = append(names, id.Title+" ("+id.TopicID+")")
		}
		return sessioncollab.Identity{}, fmt.Errorf("title %q matches %d sessions — use topic_id instead: %s",
			ref, len(byTitle), strings.Join(names, ", "))
	}
	if len(byTitle) == 1 {
		return byTitle[0], nil
	}
	return sessioncollab.Identity{}, fmt.Errorf("%w: %q is not in the contact directory (use list_addressable_sessions)", sessioncollab.ErrNotFound, ref)
}
