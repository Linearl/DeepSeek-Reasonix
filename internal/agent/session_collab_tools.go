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
	"reasonix/internal/store"
	"reasonix/internal/tool"
)

// SessionCollabConfig carries host knobs for the task 19 collaboration tools.
type SessionCollabConfig struct {
	Enabled       bool
	SessionDir    string
	WorkspaceRoot string
	// CurrentSessionPath is the calling session's transcript path (for reply
	// routing) as it was known when the tools were built.
	CurrentSessionPath string
	// ResolveSessionPath, when set, answers with the calling session's
	// transcript path AT CALL TIME and takes precedence over the snapshot above.
	//
	// Required for a self-directed call (`set_session_purpose` without `target`,
	// or any tool that stamps the caller's own identity): the transcript path is
	// bound by the control layer AFTER boot (desktop/session_prompt.go,
	// control/sessionpath.go), so the boot-time snapshot is empty for every
	// desktop session — the tool then reported "no session path" and the only
	// way out was to pass an explicit target. Resolving late also survives a
	// path that changes under a live session (rewind/recovery copies), which a
	// snapshot can never do.
	ResolveSessionPath func() string
	// MailDir overrides the shared collab mailbox root; empty uses config's.
	MailDir string

	// EventWaitRecheck enables task 244 B3: event_wait re-evaluates its checker
	// once more right before returning and reports the second verdict as
	// recheckSatisfied, exposing a judged-vs-returned window race. Off
	// (default) keeps the return shape byte-identical.
	EventWaitRecheck bool
	// CurrentContactID is filled on first ensure for the calling session.
	CurrentContactID string
	// Task 309 mailbox defaults for talk_to_session (boot-bridged from
	// [agent] config): MailReceiptDefault applies when a call omits
	// `receipt`; DefaultDelivery applies when a call omits `delivery` (empty
	// still validates to steer if this is empty).
	MailReceiptDefault bool
	DefaultDelivery    string
	// HopLimit resolves the live collaboration chain ceiling AT CALL TIME (task 204),
	// mirroring ResolveSessionPath: a boot snapshot would freeze the value for the
	// life of a session. Nil keeps the package default.
	HopLimit func() int
	// SessionStatus, when set, answers the in-process running/idle truth for a
	// contact (task 218): the host knows which controllers own an active turn,
	// a file-only view never can. pending counts session-inbox items queued in
	// the controller (a steer degraded to followup lands there, invisible to
	// the file counter). known=false means the probe cannot see that process
	// (other process, runtime not stood up) — the tool must report unknown
	// rather than guess an idle. Nil makes every state unknown.
	SessionStatus func(contactID string) (running bool, lastTurnAtMS int64, pending int, known bool)
	// Task 243 A2: turn-scoped dispatch echo. Injected by boot as agent method
	// values (executor exists before the collab literal); nil in direct unit
	// construction, which omits the echo field entirely.
	RecordDispatch   func(target string)
	RecentDispatches func() []string
	// Task 274 ①: per-contact model visibility. modelRef is the session's
	// current model (the same value the switcher shows, e.g.
	// "mimo-api/mimo-v2.6-flash"); provider is its catalog prefix. known=false
	// means this host cannot see the session's runtime — the row omits the
	// fields instead of guessing. Nil keeps every row model-less (CLI/tests).
	SessionInfo func(contactID string) (modelRef string, provider string, known bool)
	// SessionControl (task 274 ②③) carries the host's controller hooks for
	// the two mutating verbs: cancel a peer's active turn (with a remote-stop
	// transcript notice) and switch its model through the switcher-same setter.
	// Both funcs nil keeps session_control a refusal (CLI/tests build configs
	// without a host runtime).
	SessionControl SessionControlHooks
	// SessionTurnStatus (task 319) exposes a peer's authoritative turn
	// lifecycle (event.TurnStatus as string: completed/failed/interrupted/
	// recovery_required/...). The subscription verdict classifies an abnormal
	// terminal state — the peer turn that died instead of completing — and
	// notifies the watcher. known=false: runtime not visible here. Nil keeps
	// every status unknown, so turn_abnormal_end can never fire by guessing.
	SessionTurnStatus func(contactID string) (status string, known bool)
	// Task 173: the collaboration panel gates. Tool-level gates (delete /
	// read_tail / create) keep boot from registering the tool at all, so they
	// are not consulted here. The parameter-level gates are checked at call
	// time, and a refusal must name the panel switch and its real settings
	// entry — never a generic "invalid argument".
	AllowRequireReply bool
	AllowSteer        bool
	// DailySendLimit caps this session's outgoing cross-session messages per
	// day (task 173 ⑥). 0 = no cap.
	DailySendLimit int
	// Bus #1: BusContacts answers the addressable task-bus synthetic contacts
	// ("zcode-<role>", e.g. "zcode-worker" the headless pool) AT CALL TIME.
	// Nil resolves from the live user config ([serve.bus_mcp] roles +
	// [serve.bus_worker] contact, via config.BusContactsLive) — production
	// needs no boot wiring; tests inject a fixed table to stay hermetic.
	// talk_to_session falls back to these contacts ONLY after the contact
	// directory misses, so a real session always wins.
	BusContacts func() []string
	// Task 202: the collaboration status stream. CollabStatusPath is the
	// append-only jsonl the engine writes lifecycle events to; empty disables
	// the stream entirely. It is deliberately independent of Enabled: with
	// messaging off, the stream still lets a batch manager decide from file
	// evidence alone. Event identity (contact id or session path basename) is
	// resolved at call time, mirroring currentSessionPath.
	CollabStatusPath string
	// 任务 285（会话信息面）三个宿主探针，全部调用时求值（task 274 探针同款）；
	// nil（CLI/测试）时对应字段缺席或给出可操作拒绝，绝不猜。
	// SessionGroup answers which sidebar session group a topic belongs to.
	SessionGroup func(topicID string) (group string, known bool)
	// 任务 454: SessionGroupMatch answers "is this topic a member of the group
	// named by title OR flat group id" (e.g. both reasonix-for-ai and
	// collab-reasonix address the same group). The directory rows carry only
	// the title, so an id-typed group argument cannot match any row without
	// this host-side membership probe. Nil keeps the title-only match of
	// task 285.
	SessionGroupMatch func(topicID, group string) bool
	// SessionVersions reports a conversation's recovery lineage (the same data
	// the UI version viewer shows). ok=false: single-version conversations
	// have no lineage worth listing.
	SessionVersions func(scope, workspaceRoot, topicID, sessionPath string) (members []SessionVersionInfo, ok bool)
	// AdoptSessionVersion switches the conversation's active version through
	// the host's existing recovery selection path. The tool gates on
	// confirm=true before this probe ever runs.
	AdoptSessionVersion func(scope, workspaceRoot, topicID, sessionPath, versionID string) error
}

// collabStatusEvent appends one event to the configured stream, if any.
func (c SessionCollabConfig) collabStatusEvent(event, summary string, needsDecision bool) {
	AppendCollabStatusEvent(c.CollabStatusPath, collabStatusSessionID(c.currentSessionPath(), c.currentContactID()), "", event, summary, needsDecision)
}

// hopLimit resolves the ceiling in force for this call (task 204).
func (c SessionCollabConfig) hopLimit() int {
	if c.HopLimit == nil {
		return sessioncollab.MaxHop
	}
	return sessioncollab.ClampHopLimit(c.HopLimit())
}

// currentSessionPath resolves the calling session's transcript path, preferring
// the live value over the boot snapshot.
func (c SessionCollabConfig) currentSessionPath() string {
	if c.ResolveSessionPath != nil {
		if p := strings.TrimSpace(c.ResolveSessionPath()); p != "" {
			return p
		}
	}
	return strings.TrimSpace(c.CurrentSessionPath)
}

// currentContactID resolves the calling session's own address — the one a
// reply must be sent to. It falls back to minting the id on first use, exactly
// like the async send path, so "the sender is always addressable" holds for a
// session that only ever sends.
func (c SessionCollabConfig) currentContactID() string {
	if id := strings.TrimSpace(c.CurrentContactID); id != "" {
		return id
	}
	path := c.currentSessionPath()
	if path == "" {
		return ""
	}
	if id := SessionContactID(path); id != "" {
		return id
	}
	if minted, err := EnsureContactID(path); err == nil {
		return minted
	}
	return ""
}

func NewSetSessionPurposeTool(cfg SessionCollabConfig) tool.Tool {
	return setSessionPurposeTool{cfg: cfg}
}

type setSessionPurposeTool struct{ cfg SessionCollabConfig }

func (setSessionPurposeTool) Name() string { return "set_session_purpose" }

func (setSessionPurposeTool) Description() string {
	return "Register a session's duty (one-line purpose) in the contact directory (通讯录). By default it is THIS session; pass `target` to register another session's duty when you know what it does (after reading its tail with read_session_tail, or after it told you). A session can also change its own duty this way — later calls overwrite. Task 348: optionally pass `identity` = {type, domain} (type ∈ human|main|sub|heartbeat|system — the 人/主对话/子对话 three-layer roles field-formatted; Chinese spellings accepted; domain = the 【领域】 segment) and `duties` = [] (the structured duty list behind the free-text purpose). Omitting them leaves the stored values untouched; passing identity replaces the whole block, passing duties replaces the list ([] clears it). Experimental. Renaming the topic does not change the contact id."
}

func (setSessionPurposeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"purpose":{"type":"string","description":"One-line duty, e.g. 'React frontend expert'."},"target":{"type":"string","description":"Optional: contact_id, topic_id, or exact title of ANOTHER session to register. Omit to register this session."},"identity":{"type":"object","description":"Task 348: structured identity (三层架构角色字段化). Omit to keep the stored identity; pass to REPLACE it — type: one of human|main|sub|heartbeat|system (aliases 人/主对话/子对话/系统 accepted; empty clears), domain: the 【领域】 segment e.g. '开发'.","properties":{"type":{"type":"string","enum":["human","main","sub","heartbeat","system"]},"domain":{"type":"string"}}},"duties":{"type":"array","items":{"type":"string"},"description":"Task 348: structured duty list. Omit to keep the stored list; pass [] to clear."}},"required":["purpose"]}`)
}

func (setSessionPurposeTool) ReadOnly() bool { return false }

func (t setSessionPurposeTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Purpose string `json:"purpose"`
		Target  string `json:"target"`
		// Task 348: pointer receivers, so "omitted" and "passed empty" are
		// different calls — omitted keeps the stored value, passed replaces it.
		Identity *struct {
			Type   string `json:"type"`
			Domain string `json:"domain"`
		} `json:"identity"`
		Duties *[]string `json:"duties"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Purpose) == "" {
		return "", fmt.Errorf("purpose is required")
	}
	session := t.cfg.currentSessionPath()
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
	var identity *SessionIdentity
	if p.Identity != nil {
		identity = &SessionIdentity{Type: p.Identity.Type, Domain: p.Identity.Domain}
	}
	contact, err := SetSessionDuty(session, p.Purpose, identity, p.Duties)
	if err != nil {
		return "", err
	}
	// Task 348: same three keys as before for a purpose-only call (byte
	// identical result); the structured keys appear only when they were sent.
	outMap := map[string]any{"contactId": contact, "purpose": strings.TrimSpace(p.Purpose), "appliedTo": appliedTo}
	if identity != nil {
		canonical, cerr := sessioncollab.NormalizeIdentityType(identity.Type)
		if cerr != nil {
			return "", cerr
		}
		outMap["identityType"] = canonical
		outMap["identityDomain"] = strings.TrimSpace(identity.Domain)
	}
	if p.Duties != nil {
		outMap["duties"] = *p.Duties
	}
	out, _ := json.Marshal(outMap)
	return string(out), nil
}

func NewListAddressableSessionsTool(cfg SessionCollabConfig) tool.Tool {
	return listAddressableSessionsTool{cfg: cfg}
}

type listAddressableSessionsTool struct{ cfg SessionCollabConfig }

func (listAddressableSessionsTool) Name() string { return "list_addressable_sessions" }

func (listAddressableSessionsTool) Description() string {
	return "List the contact directory (通讯录): metadata only — title, purpose, contact_id, topic_id, and the sidebar session group when the host knows it (task 285). Task 348: rows registered with structured fields also carry identityType (human|main|sub|heartbeat|system), identityDomain and duties[] — absent for purpose-only sessions. No transcript content (read_session_tail does that). Task 174 merged search in: omit query for the newest-first page; pass query for a keyword filter over title/purpose/contact_id/topic_id; pass group to keep only one session group (task 454: the group title OR its flat id both work — e.g. reasonix-for-ai and collab-reasonix address the same group). Task 175: pass sent=true to read YOUR OWN outgoing log — the misdirected-send check after a batch dispatch. Task 508: pass include_stats=true to add per-row eventsBytes / turns / lastActivityAt (file size and persisted sidecar counters — still zero transcript bytes; this is what a heartbeat rotation audit needs to size up sessions in one call). Use contact_id, topic_id, or the exact title as `to` in talk_to_session. Entries frozen for over a week carry stale=true (task 175) — re-check before trusting the purpose. Experimental."
}

func (listAddressableSessionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max rows to return (directory: newest first, default 200, max 1000; sent: default 20)."},"archived":{"type":"boolean","description":"Include retired archive sessions (default false)."},"query":{"type":"string","description":"Keyword filter over title/purpose/ids (the old search_sessions). Omit for the unfiltered newest-first page."},"group":{"type":"string","description":"Keep only sessions in this sidebar session group (task 285/454; case-insensitive group title or flat group id — the returned rows name the title). Omit for all groups."},"sent":{"type":"boolean","description":"Return your OWN outgoing log instead of the directory (task 175) — id, recipient, thread, first line of each message you sent. Use it to catch a misdirected send."},"include_stats":{"type":"boolean","description":"Task 508: add eventsBytes (size of the PRIMARY event log <id>.events.jsonl only — sidecars like .meta/.turns/.damaged are NOT counted; 0 when the log is missing), turns (persisted meta sidecar counter; 0 = unknown), lastActivityAt (event-log mtime, ms epoch; 0 = unknown) to every row. Default false — without it rows carry no stats keys at all. Still metadata only: one stat per row, no log body is ever read."}},"required":[]}`)
}

func (listAddressableSessionsTool) ReadOnly() bool { return true }

func (t listAddressableSessionsTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Limit        int    `json:"limit"`
		Archived     *bool  `json:"archived"`
		Query        string `json:"query"`
		Group        string `json:"group"`
		Sent         bool   `json:"sent"`
		IncludeStats bool   `json:"include_stats"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	if p.Sent {
		return sentLogPage(t.cfg, p.Limit)
	}
	return directoryPageFiltered(t.cfg, p.Limit, p.Archived, p.Query, p.Group, p.IncludeStats)
}

// sentLogPage renders the caller's own outgoing log (task 175), reachable as
// the directory tool's action instead of a fifteenth tool name (task 174).
// The body is a first-line summary: enough to spot the wrong recipient
// without re-reading the whole dispatch.
func sentLogPage(cfg SessionCollabConfig, limit int) (string, error) {
	me := strings.TrimSpace(cfg.currentContactID())
	if me == "" {
		return "", fmt.Errorf("no session path — your own sent log needs the caller's contact_id (call from a registered session)")
	}
	if limit <= 0 {
		limit = 20
	}
	mailDir := cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	sent := sessioncollab.NewMailStoreWithHopLimit(mailDir, cfg.hopLimit()).ListSent(me, limit)
	type row struct {
		MessageID string `json:"messageId"`
		To        string `json:"to"`
		ToTitle   string `json:"toTitle,omitempty"`
		ThreadID  string `json:"threadId,omitempty"`
		Summary   string `json:"summary"`
		At        int64  `json:"at"`
	}
	rows := make([]row, 0, len(sent))
	for _, m := range sent {
		summary := m.Body
		if i := strings.IndexByte(summary, '\n'); i >= 0 {
			summary = summary[:i]
		}
		rows = append(rows, row{
			MessageID: m.ID, To: m.To, ToTitle: m.ToTitle,
			ThreadID: m.ThreadID, Summary: summary, At: m.At,
		})
	}
	out, _ := json.Marshal(map[string]any{
		"from":  me,
		"count": len(rows),
		"note":  "your own sent log (task 175) — verify the recipient title before trusting a batch dispatch",
		"sent":  rows,
	})
	return string(out), nil
}

// NewGetSessionStatusTool answers "is the peer busy?" (task 218) from cheap
// metadata alone: the in-process running state where the host can see it, the
// mailbox counters everywhere else. No transcript bytes ever cross this
// boundary, so a status sweep stays KB-light no matter how large the directory.
func NewGetSessionStatusTool(cfg SessionCollabConfig) tool.Tool {
	return getSessionStatusTool{cfg: cfg}
}

type getSessionStatusTool struct{ cfg SessionCollabConfig }

func (getSessionStatusTool) Name() string { return "get_session_status" }

func (getSessionStatusTool) Description() string {
	return "Check whether collaboration peers are busy before assigning work (task 218). Without arguments returns every addressable live session; pass targets (mixed contact_id / topic_id / exact title) to query one or many in one call. Each record is lightweight structured metadata — running/idle/queued/unknown state, last activity, unread inbox count — never transcript content; unmatched targets are reported explicitly. state=unknown means THIS process cannot see the session's runtime — never that it is idle or dead; before treating it as idle check lastActivity and read the target's inbox.jsonl tail. Strictly read-only. Experimental."
}

func (getSessionStatusTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"targets":{"type":"array","items":{"type":"string"},"description":"Sessions to query: contact_id, topic_id, or exact title, mixed freely. Omit for every addressable live session."}},"required":[]}`)
}

func (getSessionStatusTool) ReadOnly() bool { return true }

// collabStatusRecords is the single source of the "who is busy?" judgement
// (task 218): directory scan, target resolution and the state classification
// (running > queued > idle; unknown never guessed). get_session_status answers
// one call with it and event_wait polls it (task 228), so the two tools can
// never disagree about the same peer. The third return is the live directory
// size, so callers can state what a "total" means (task 228 audit m5: it is
// the whole addressable directory, not the queried-target count).
func collabStatusRecords(cfg SessionCollabConfig, targets []string) (records []map[string]any, unmatched []string, liveTotal int) {
	all := scanAddressable(cfg.SessionDir, cfg.WorkspaceRoot)
	live := make([]sessioncollab.Identity, 0, len(all))
	for _, id := range all {
		if !id.Archived {
			live = append(live, id)
		}
	}
	liveTotal = len(live)

	pick := func(target string) (sessioncollab.Identity, bool) {
		key := strings.ToLower(strings.TrimSpace(target))
		if key == "" {
			return sessioncollab.Identity{}, false
		}
		for _, id := range live {
			if strings.EqualFold(id.ContactID, key) {
				return id, true
			}
		}
		for _, id := range live {
			if id.TopicID != "" && strings.EqualFold(id.TopicID, key) {
				return id, true
			}
		}
		for _, id := range live {
			if id.Title != "" && strings.EqualFold(id.Title, key) {
				return id, true
			}
		}
		return sessioncollab.Identity{}, false
	}

	mailDir := cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	mail := sessioncollab.NewMailStoreWithHopLimit(mailDir, cfg.hopLimit())

	status := func(id sessioncollab.Identity) map[string]any {
		unread, lastDelivery := 0, int64(0)
		if id.ContactID != "" {
			unread, lastDelivery = mail.InboxStatus(id.ContactID)
		}
		running, lastTurn, probePending, known := false, int64(0), 0, false
		if cfg.SessionStatus != nil && id.ContactID != "" {
			running, lastTurn, probePending, known = cfg.SessionStatus(id.ContactID)
		}
		// Task 218 (dispatch-round feedback): a steer degraded to followup is
		// queued INSIDE the target's session inbox, past the collab mailbox
		// cursor — so the pending the probe sees must join the file counter
		// before any queued/idle decision. Without it an idle peer with a
		// degraded steer parked in its inbox reads idle, and the dispatcher
		// double-sends.
		busy := unread + probePending
		// State precedence (task 218): an active turn wins over pending mail; a
		// runtime the probe cannot see is unknown — never a guessed idle.

		// Direction (task 244 B7): Reasonix's form of MiMo's roster rule
		// "prefer repeating a child over routing into a corpse"
		// (actor/schema.ts:135-140): uncertainty resolves to a state no router
		// treats as free, so a stalled peer is re-contacted or skipped, never
		// dispatched into. Pinned by session_collab_status_test.go (unknown
		// stays unknown) and event_wait's unmatched rule.
		state := "unknown"
		switch {
		case known && running:
			state = "running"
		case known && busy > 0:
			state = "queued"
		case known:
			state = "idle"
		}
		activity := lastTurn
		if activity == 0 {
			activity = lastDelivery
		}
		record := map[string]any{
			"contactId":    id.ContactID,
			"topicId":      id.TopicID,
			"title":        id.Title,
			"scope":        id.Scope,
			"state":        state,
			"lastActivity": activity,
			// Everything waiting to be consumed: the collab mailbox queue plus
			// the controller's session inbox (degraded steers live there).
			"unreadInbox": busy,
		}
		// Task 375: unknown must be self-explanatory — it means THIS process
		// cannot see the target's runtime, never that the session is idle or
		// dead. Twice misread as "won't start" (0920, 0927); the actionable
		// check is lastActivity age plus the target's inbox tail.
		if state == "unknown" {
			record["hint"] = "unknown = this process cannot see the session's runtime (another process owns it, or the runtime is not stood up) — it is NOT evidence the session is idle or dead. Before treating it as idle: compare lastActivity against how recently work was expected, then read the session's inbox.jsonl tail — the mailbox is the authoritative dispatch evidence."
		}
		return record
	}

	records = make([]map[string]any, 0)
	unmatched = make([]string, 0)
	if len(targets) == 0 {
		for _, id := range live {
			records = append(records, status(id))
		}
	} else {
		for _, target := range targets {
			id, ok := pick(target)
			if !ok {
				// A target the directory cannot resolve is reported, never
				// silently dropped — the caller may be one typo away from
				// assigning work to nobody.
				unmatched = append(unmatched, target)
				continue
			}
			records = append(records, status(id))
		}
	}
	return records, unmatched, liveTotal
}

func (t getSessionStatusTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Targets []string `json:"targets"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}

	records, unmatched, liveTotal := collabStatusRecords(t.cfg, p.Targets)
	out, _ := json.Marshal(map[string]any{
		"returned": len(records),
		// Task 228 audit m5: "total" is the whole addressable live directory
		// (what a no-argument sweep would answer), NOT the queried-target
		// count — "returned" is that count. Stated here so the semantics
		// never have to be inferred.
		"total":     liveTotal,
		"totalNote": "total = addressable live sessions in the directory; returned = records in this answer",
		"query":     "session status — state/lastActivity/unreadInbox only, no transcript content",
		"states":    "running=active turn; idle=waiting for input; queued=inbox has unconsumed mail; unknown=process not visible (never a guessed idle)",
		"unmatched": unmatched,
		"sessions":  records,
	})
	return string(out), nil
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
// collabDispatchEcho reads the turn-scoped dispatch ledger through the injected
// probe (task 243 A2); nil probe or empty ledger returns nil so callers can
// omit the field instead of emitting null.
func collabDispatchEcho(cfg SessionCollabConfig) []string {
	if cfg.RecentDispatches == nil {
		return nil
	}
	return cfg.RecentDispatches()
}

// collabDirectoryMaxRows is the hard row ceiling for list/search pages — the
// tool schema's documented "max 1000". The rows hint below deliberately uses
// this constant instead of the caller-controlled limit: a limit-derived hint
// stays flagged by codeql go/uncontrolled-allocation-size even after the
// clamp above, and the constant keeps the allocation provably bounded
// (~100KB of metadata structs worst case, per tool call).
const collabDirectoryMaxRows = 1000

func directoryPage(cfg SessionCollabConfig, limit int, archived *bool, query string) (string, error) {
	return directoryPageFiltered(cfg, limit, archived, query, "", false)
}

// directoryPageFiltered is directoryPage with the task-285 group filter: rows
// carry their sidebar session group (when the host probe knows it) and a
// non-empty group argument keeps only that group. The filter applies BEFORE
// the eligibility count, so `total` respects it exactly like the query filter.
//
// includeStats (task 508) gates the per-row eventsBytes/turns/lastActivityAt
// enrichment. Off — the default and every pre-508 caller — the row shape is
// byte-identical to before; on, each EMITTED row (never a paged-out one)
// gains the three fields. Both sources are metadata, not content: turns comes
// from the meta sidecar already read by the scan, and the other two are one
// os.Stat on the primary event log — the log body is never opened.
func directoryPageFiltered(cfg SessionCollabConfig, limit int, archived *bool, query, group string, includeStats bool) (string, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > collabDirectoryMaxRows {
		limit = collabDirectoryMaxRows
	}
	includeArchived := archived != nil && *archived
	q := strings.ToLower(strings.TrimSpace(query))
	group = strings.TrimSpace(group)

	all := scanAddressable(cfg.SessionDir, cfg.WorkspaceRoot)
	type row struct {
		Title     string `json:"title"`
		Purpose   string `json:"purpose,omitempty"`
		ContactID string `json:"contactId,omitempty"`
		TopicID   string `json:"topicId,omitempty"`
		Archived  bool   `json:"archived,omitempty"`
		// Task 274 ①: the session's current model, when this host can see the
		// runtime. Absent (omitempty) when the probe is nil or unknown — a
		// guessed model would route work onto the wrong provider assumption.
		ModelRef string `json:"modelRef,omitempty"`
		Provider string `json:"provider,omitempty"`
		// Task 175 ③: a purpose frozen since long before the last activity is
		// more misleading than no purpose at all — callers route work by it.
		Stale bool `json:"stale,omitempty"`
		// 任务 285: the sidebar session group, when the host probe knows it.
		Group string `json:"group,omitempty"`
		// 任务 348: structured identity/duties. All omitempty — a purpose-only
		// session (every pre-348 sidecar) emits the exact same row as before,
		// which is the zero-migration acceptance in wire form.
		IdentityType   string   `json:"identityType,omitempty"`
		IdentityDomain string   `json:"identityDomain,omitempty"`
		Duties         []string `json:"duties,omitempty"`
		// 任务 508: stats, present only when the caller passed include_stats=true
		// (pointers stay nil otherwise, so the un-gated row shape is unchanged).
		// When present they are ALWAYS emitted — even as 0 — so a consumer can
		// tell "no event log on disk" (eventsBytes 0) from "stats not requested"
		// (keys absent). eventsBytes/lastActivityAt cover ONLY the primary event
		// log <id>.events.jsonl: no sidecar (.meta/.turns.jsonl/.damaged) is
		// counted, matching heartbeat_session_rotate.py's accounting.
		EventsBytes    *int64 `json:"eventsBytes,omitempty"`
		Turns          *int   `json:"turns,omitempty"`
		LastActivityAt *int64 `json:"lastActivityAt,omitempty"`
	}
	// A duty older than a week, in a codebase where batches live for days, is
	// presumed stale rather than presumed current.
	const purposeStaleAfter = 7 * 24 * time.Hour
	rows := make([]row, 0, collabDirectoryMaxRows)
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
		rowGroup := ""
		if cfg.SessionGroup != nil && id.TopicID != "" {
			if g, known := cfg.SessionGroup(id.TopicID); known {
				rowGroup = g
			}
		}
		if group != "" && !topicInSessionGroup(cfg, id.TopicID, rowGroup, group) {
			continue
		}
		eligible++
		if len(rows) < limit {
			stale := id.UpdatedAt > 0 && time.Since(time.UnixMilli(id.UpdatedAt)) > purposeStaleAfter
			modelRef, provider := "", ""
			// Task 274 ①: model visibility rides the injected probe — absent or
			// unknown keeps the fields empty (omitempty) instead of guessing.
			if cfg.SessionInfo != nil && id.ContactID != "" {
				if ref, prov, known := cfg.SessionInfo(id.ContactID); known {
					modelRef, provider = ref, prov
				}
			}
			rows = append(rows, row{
				Title:          id.Title,
				Purpose:        id.Purpose,
				ContactID:      id.ContactID,
				TopicID:        id.TopicID,
				Archived:       id.Archived,
				Stale:          stale,
				ModelRef:       modelRef,
				Provider:       provider,
				Group:          rowGroup,
				IdentityType:   id.IdentityType,
				IdentityDomain: id.IdentityDomain,
				Duties:         id.Duties,
			})
			if includeStats {
				eventsBytes, lastActivityAt := sessionEventLogStats(id.SessionPath)
				turns := id.Turns
				r := &rows[len(rows)-1]
				r.EventsBytes = &eventsBytes
				r.Turns = &turns
				r.LastActivityAt = &lastActivityAt
			}
		}
	}
	payload := map[string]any{
		"returned": len(rows),
		"total":    eligible,
		"limit":    limit,
		"query":    q,
		"group":    group,
		// Explicit so a caller never expects content here: that is read_session_tail.
		"content":  "none — use read_session_tail(target) for transcript bytes",
		"note":     "live conversations only; pass archived=true to include retired history. Deleted (.trash) sessions are never listed.",
		"sessions": rows,
	}
	// 任务 508: when stats ride the rows, say so — and pin the accounting so a
	// heartbeat audit never mistakes eventsBytes for a directory-wide total
	// (sidecars excluded) or turns for a live replay (persisted counter).
	if includeStats {
		payload["stats"] = "eventsBytes = primary event log (<id>.events.jsonl) size only, sidecars (.meta/.turns/.damaged) excluded, 0 when missing; turns = persisted meta-sidecar counter, 0 = unknown; lastActivityAt = event-log mtime (ms epoch), 0 = unknown. Metadata only — no log body is read."
	}
	// Task 243 A2: the directory doubles as the batch echo — who this turn
	// already dispatched to, so a batch dispatcher can spot its own sends
	// before double-sending (tasks 175/218).
	if echo := collabDispatchEcho(cfg); echo != nil {
		payload["dispatchedThisTurn"] = echo
	}
	out, _ := json.Marshal(payload)
	return string(out), nil
}

// sessionEventLogStats answers task 508's "how big, how active?" for one
// session with filesystem metadata only: the size and mtime of the PRIMARY
// event log (<id>.events.jsonl). The log body is never opened — a 200 MB
// transcript costs the same one stat as an empty one. Sidecars (.meta,
// .turns.jsonl, .damaged, ...) are deliberately NOT counted, so the number
// reconciles exactly with heartbeat_session_rotate.py's `os.path.getsize` on
// the same file. A missing log (fresh session, or one still on the array
// format) reports 0/0 — unknown, never a guessed activity time.
func sessionEventLogStats(sessionPath string) (eventsBytes, lastActivityAtMS int64) {
	info, err := os.Stat(store.SessionEventLog(sessionPath))
	if err != nil {
		return 0, 0
	}
	return info.Size(), info.ModTime().UnixMilli()
}

// topicInSessionGroup applies the task-454 group filter: a row stays when the
// group argument names its sidebar group by title (case-insensitive exact,
// task 285) or by flat group id through the host membership probe — the rows
// themselves only ever carry the title, so the id spelling is invisible to a
// title-only compare. Without the probe (CLI/tests) the filter keeps the
// title-only match of task 285; an empty topic id can only ever match by title.
func topicInSessionGroup(cfg SessionCollabConfig, topicID, rowGroup, group string) bool {
	if strings.EqualFold(rowGroup, group) {
		return true
	}
	if cfg.SessionGroupMatch == nil || strings.TrimSpace(topicID) == "" {
		return false
	}
	return cfg.SessionGroupMatch(topicID, group)
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
	return "Send a message to another session in the contact directory (通讯录, task 19 / 142-143). `to` accepts a contact_id, a topic_id, or the exact title shown by list_addressable_sessions — the title is the human way to pick someone when you have not met them yet, and the target gains a contact_id on first contact. Task-bus synthetic contacts (zcode-<role>: enrolled bus roles, plus the zcode-worker headless pool) are also addressable while the bus is configured — mail lands in the role's shared bus inbox; for the pool to EXECUTE a task, send a kind=bus-task JSON body naming a pending card created with the task card tool. delivery=steer (default, task 309) asks for mid-turn injection and degrades to a queued follow-up when the target has no injectable turn; delivery=followup explicitly queues for the target's next turn. Resending the same content on the same thread inside the dedup window returns the original messageId instead of enqueueing a duplicate (mailbox idempotency, task 309). Your own contact_id is minted automatically on first send, so the target can always reply. When answering a message that was delivered to you, ALWAYS reply through this tool with to = the From contact_id carried in the delivery text — never answer inside your own transcript, the sender cannot see it. Experimental."
}

func (talkToSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"Target: contact_id, topic_id, the exact title from list_addressable_sessions, or an enrolled task-bus contact (zcode-<role>, e.g. zcode-worker)."},"message":{"type":"string"},"hop":{"type":"integer","description":"0 for a new chain. The system derives the real depth from the thread."},"delivery":{"type":"string","enum":["followup","steer"],"description":"steer (default, task 309) injects mid-turn, degrading to followup when it cannot; followup explicitly queues for the next turn."},"receipt":{"type":"boolean","description":"Task 309: request a read receipt — the target sends back a system receipt message when this mail enters its context (turn injection / drain consumption). Default off; delivery-level confirmation already rides the return value."},"card_id":{"type":"string","description":"Optional task card id to stamp on the message."},"thread_id":{"type":"string","description":"When answering a message, pass the message id you RECEIVED — the inbound id from your own mailbox (drain_inbox / delivery text), never the id of a message you sent yourself; a self-sent id fails the thread check (task 194). The requester matches your reply against it."},"require_reply":{"type":"boolean","description":"Set true when the sender needs an answer on this thread (task 173). Requires the panel switch session_collab_allow_require_reply."},"approver":{"type":"string","description":"contact_id (or resolvable title) of the session that answers THIS task's approval prompts (task 225). Default: the sender. Must be a registered session."},"wait":{"type":"boolean","description":"Set true to wait — bounded — for a reply on this thread instead of returning queued at once (the old talk_to_session_sync behavior)."},"timeout_ms":{"type":"integer","description":"wait: how long to wait for the reply (default 30000, max 120000)."}},"required":["to","message"]}`)
}

func (talkToSessionTool) ReadOnly() bool { return false }

// refuseGate is the task-173 parameter-level refusal: it names the panel
// switch that withheld the capability and the one settings entry that exists,
// so the caller can act instead of guessing at an "invalid argument".
func refuseGate(capability, switchName string) error {
	return fmt.Errorf("%s在跨会话通信实验面板中未开启（%s）——请到 设置 → 实验特性 → 跨会话通信 面板开启后重试", capability, switchName)
}

func (t talkToSessionTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		To           string `json:"to"`
		Message      string `json:"message"`
		Hop          int    `json:"hop"`
		Delivery     string `json:"delivery"`
		Receipt      *bool  `json:"receipt"`
		CardID       string `json:"card_id"`
		ThreadID     string `json:"thread_id"`
		RequireReply bool   `json:"require_reply"`
		Approver     string `json:"approver"`
		Wait         bool   `json:"wait"`
		TimeoutMS    int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.To) == "" || strings.TrimSpace(p.Message) == "" {
		return "", fmt.Errorf("to and message are required")
	}
	if limit := t.cfg.hopLimit(); p.Hop < 0 || p.Hop > limit+1 {
		return "", fmt.Errorf("invalid hop %d", p.Hop)
	}
	// Task 309: an omitted delivery uses the configured mailbox default
	// (steer unless [agent] session_collab_default_delivery says otherwise).
	if strings.TrimSpace(p.Delivery) == "" && strings.TrimSpace(t.cfg.DefaultDelivery) != "" {
		p.Delivery = t.cfg.DefaultDelivery
	}
	delivery, err := sessioncollab.ValidateDelivery(p.Delivery)
	if err != nil {
		return "", err
	}
	// Task 309: an omitted receipt uses the configured default (off unless
	// session_collab_mail_receipt_default turns it on).
	receiptRequested := t.cfg.MailReceiptDefault
	if p.Receipt != nil {
		receiptRequested = *p.Receipt
	}
	if delivery == sessioncollab.DeliverySteer && !t.cfg.AllowSteer {
		// Task 173 ④: with the panel switch off, steer degrades to followup —
		// the message still lands, it just loses the mid-turn injection. A
		// refusal here would break every existing steer caller for a setting
		// they have never seen; the degraded flag in the result keeps the
		// outcome honest.
		delivery = sessioncollab.DeliveryFollowup
	}
	if p.RequireReply && !t.cfg.AllowRequireReply {
		return "", refuseGate("require_reply（要求对方回信）", "允许配置回信要求")
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	target, err := ResolveTarget(ids, p.To)
	viaBus := false
	if err != nil {
		// Bus #1: "zcode-<role>" task-bus contacts are not directory sessions —
		// the directory has no row for them, so ResolveTarget reports
		// ErrNotFound. When the ref IS an enrolled bus contact, resolve it
		// there instead; anything else keeps the original directory error. The
		// fallback lives HERE, not inside ResolveTarget: the other callers
		// (read_session_tail, approver, set_session_purpose) are session-file
		// semantics a synthetic mailbox contact cannot satisfy.
		resolved, busOK := t.resolveBusContact(p.To)
		if !busOK {
			if isZcodeRef(p.To) {
				return "", fmt.Errorf("%w; %q looks like a task-bus contact — enroll the role first (reasonix bus enroll --role <name>)", err, p.To)
			}
			return "", err
		}
		target = resolved
		viaBus = true
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
	// Task 156.A: the sender must be addressable on first send. A session
	// that only ever sends (never gets messaged first) would otherwise stay
	// "(未登记)" forever and its messages degrade to one-way notices.
	// Task 158.B: resolve the sender's own address at call time — the boot-time
	// snapshot is empty for desktop sessions, which is what made a self-directed
	// set_session_purpose fail and every unregistered sender read as "(未登记)".
	fromContact := t.cfg.currentContactID()
	fromSession := t.cfg.currentSessionPath()
	mailDir := t.mailDirFor(target, viaBus)
	mail := sessioncollab.NewMailStoreWithHopLimit(mailDir, t.cfg.hopLimit())
	msg := sessioncollab.MailMessage{
		From:             fromContact,
		FromSession:      fromSession,
		To:               target.ContactID,
		Body:             strings.TrimSpace(p.Message),
		Delivery:         string(delivery),
		Hop:              p.Hop,
		CardID:           p.CardID,
		ReplyTo:          fromContact,
		ThreadID:         strings.TrimSpace(p.ThreadID),
		RequireReply:     p.RequireReply,
		ReceiptRequested: receiptRequested, // task 309: read receipt, consumed at turn-injection/drain time on the recipient side.
	}
	// Task 225 (user ruling): an explicit approver overrides the task-source
	// default for this task's approval prompts. It must resolve to a real
	// directory entry — a typo'd approver would silently strand every
	// approval the target raises.
	var approver string
	if strings.TrimSpace(p.Approver) != "" {
		ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
		resolved, aerr := ResolveTarget(ids, p.Approver)
		if aerr != nil {
			return "", fmt.Errorf("approver %q 无法解析为可寻址会话（task 225）：%w", p.Approver, aerr)
		}
		if resolved.Archived {
			return "", fmt.Errorf("approver %q 已归档，不能作为审批者（task 225）", p.Approver)
		}
		approver = resolved.ContactID
		if approver == "" {
			approver = resolved.SessionPath
		}
		msg.Approver = approver
	}
	// Task 173 ⑥: the daily cap counts only what actually left this session,
	// so the check sits right before Deliver — a refused call writes nothing.
	if t.cfg.DailySendLimit > 0 {
		if sent := mail.CountSentFromToday(fromContact); sent >= t.cfg.DailySendLimit {
			return "", fmt.Errorf("已达跨会话单日发信上限（%d 封/天，今日已发 %d 封）——这是防消息风暴的运行时限制，明天自动恢复；紧急请联系用户调整 设置 → 实验特性 → 跨会话通信 的单日发信上限", t.cfg.DailySendLimit, sent)
		}
	}
	// Task 194-P0: an unresolvable thread_id used to be accepted here, written into
	// the peer's inbox and only then dropped by the delivery pump, so the sender saw
	// "queued" and learned nothing. Validate before writing: the call reports it.
	if _, _, terr := mail.ResolveReplyParent(msg); terr != nil {
		return "", terr
	}
	// task 461 P1: the send rides a cancel-free context — a cancelled turn
	// must still settle the request on disk and report "wait ended early",
	// never an NDR for mail that never left (send/wait are separate phases;
	// the wait itself stays cancellable). The lock wait remains bounded by
	// the filelock default budget (5s).
	delivered, derr := mail.Deliver(cancelFreeCtx(ctx), msg)
	if derr != nil {
		// Task 309 NDR: a refused delivery reads like a mailbox bounce —
		// recipient, reason, and a quoted excerpt of the original body, so
		// the sender can decide whether to resend without opening any log.
		excerpt := strings.TrimSpace(p.Message)
		if runes := []rune(excerpt); len(runes) > 80 {
			excerpt = string(runes[:80]) + "…"
		}
		return "", fmt.Errorf("退信（NDR）：致 %s；原因：%v；原文「%s」", target.Title, derr, excerpt)
	}
	msg = delivered
	// Task 175: the sender keeps its own sent log — the inbox only shows what
	// arrived, so a misdirected send used to be invisible on this side until a
	// confused peer answered. Recorded after the real id/at are known.
	mail.RecordSent(cancelFreeCtx(ctx), msg, target.Title)
	// Task 175: put the recipient in the caller's face. The historical failure
	// was a correct-looking "queued" for the WRONG peer; delivered_to carries
	// the id plus its human-readable title so the mismatch reads at a glance.
	deliveredTo := target.ContactID
	if target.Title != "" {
		deliveredTo = target.ContactID + "｜" + target.Title
	}
	// Task 202: engine-written status events. A delivery is progress evidence
	// even with messaging disabled on the reader side; require_reply means the
	// batch manager owes a decision, wait=true means this session now blocks.
	t.cfg.collabStatusEvent(CollabStatusDelivered, fmt.Sprintf("to %s (%s): %.160s", deliveredTo, msg.Delivery, p.Message), false)
	if p.RequireReply {
		t.cfg.collabStatusEvent(CollabStatusNeedsDecision, fmt.Sprintf("awaiting reply from %s", deliveredTo), true)
	}
	// Task 243 A2: record this dispatch on the turn ledger, then echo the
	// whole turn's ledger back — display-only, never a refusal (no semantic
	// de-duplication; the model decides what a repeat means).
	if t.cfg.RecordDispatch != nil {
		record := target.ContactID
		if target.Title != "" {
			record += " (" + target.Title + ")"
		}
		t.cfg.RecordDispatch(record)
	}
	payload := map[string]any{
		"status":       "queued",
		"messageId":    msg.ID,
		"threadId":     msg.ID,
		"from":         fromContact,
		"to":           target.ContactID,
		"delivered_to": deliveredTo,
		"toPurpose":    target.Purpose,
		"delivery":     msg.Delivery,
		"hop":          msg.Hop,
		"queued":       true,
	}
	// Task 375: when the target's runtime state is unknown to this process,
	// say so on the receipt — queued is the mailbox acknowledgment, NOT proof
	// of delivery; the authoritative signal is the target's inbox.jsonl.
	// Guidance only: the probe result changes nothing about the delivery.
	// A nil SessionStatus probe means every state is unknown (CLI-style host)
	// — exactly the situation the hint exists for.
	if t.cfg.SessionStatus == nil {
		payload["targetStatusHint"] = "the target's runtime state is unknown to this process (no status probe is wired here). queued means the message is in the durable mailbox — it is NOT proof the target has picked it up; read the target's inbox.jsonl tail for authoritative dispatch evidence."
	} else if _, _, _, known := t.cfg.SessionStatus(target.ContactID); !known {
		payload["targetStatusHint"] = "the target's runtime state is unknown to this process (another process owns it, or the runtime is not stood up). queued means the message is in the durable mailbox — it is NOT proof the target has picked it up; read the target's inbox.jsonl tail for authoritative dispatch evidence."
	}
	if echo := collabDispatchEcho(t.cfg); echo != nil {
		payload["dispatchedThisTurn"] = echo
	}
	out, _ := json.Marshal(payload)
	// Task 174: the sync twin collapsed into wait=true. Without it this is the
	// exact async contract the tests pinned; with it, the bounded wait runs
	// after the durable delivery has already been acknowledged.
	if p.Wait {
		t.cfg.collabStatusEvent(CollabStatusBlockingWait, fmt.Sprintf("waiting (bounded) for a reply on thread %s", msg.ID), true)
		return t.waitReply(ctx, msg.ID, p.TimeoutMS)
	}
	return string(out), nil
}

// resolveBusContact maps a "zcode-<role>" reference to a synthetic task-bus
// contact (bus #1) when that contact is live — an enrolled [serve.bus_mcp]
// role, or the [serve.bus_worker] pool contact. Membership against the
// contact table is the only gate: the table itself is shape-validated in
// config (busContactsFrom), so a hand-edited config can never mint a
// traversing mailbox path here, and an unenrolled role stays ErrNotFound.
// Matching is case-insensitive like the directory's own; the canonical
// lower-case contact from the table wins.
// cancelFreeCtx drops cancellation (and deadlines) from ctx while keeping it
// non-nil: the sync send contract (task 156/158, pinned by
// TestTalkToSessionSyncStopsWaitingWhenContextEnds) settles the request on
// disk even when the turn ends mid-call, and reports the phase split in the
// result instead of an NDR. Bounded waiting is preserved by the filelock
// default budget, not by cancellation.
func cancelFreeCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (t talkToSessionTool) resolveBusContact(ref string) (sessioncollab.Identity, bool) {
	contacts := t.busContacts()
	if len(contacts) == 0 {
		return sessioncollab.Identity{}, false
	}
	key := strings.ToLower(strings.TrimSpace(ref))
	for _, contact := range contacts {
		if strings.EqualFold(contact, key) {
			return sessioncollab.Identity{
				ContactID: contact,
				Title:     contact,
				Purpose:   "zcode task-bus contact",
			}, true
		}
	}
	return sessioncollab.Identity{}, false
}

// busContacts answers the addressable bus contacts: the injected probe when
// the host wired one (tests), else the live user config. A nil probe reading
// live config means production needs no boot wiring for the fallback.
func (t talkToSessionTool) busContacts() []string {
	if t.cfg.BusContacts != nil {
		return t.cfg.BusContacts()
	}
	return config.BusContactsLive()
}

// isZcodeRef reports whether a target reference sits in the synthetic-contact
// namespace. Messaging only — it decides whether a failed resolution should
// point at bus enroll; the actual gate is contact-table membership.
func isZcodeRef(ref string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ref)), "zcode-")
}

// mailDirFor picks the mailbox root for one delivery. Session targets use the
// shared collab directory (or the host override). A bus synthetic contact
// must land in the bus's OWN resolution of that directory
// (config.BusMailDirLive mirrors busmcp.New), otherwise a deployment with a
// custom [serve.bus_mcp].mail_dir would silently strand agent mail in a box
// no zcode role reads. An explicit cfg.MailDir override (tests, custom hosts)
// wins over both. viaBus marks a bus-fallback target so a real directory
// session that happens to carry a zcode-prefixed contact id keeps routing to
// the session stream.
func (t talkToSessionTool) mailDirFor(target sessioncollab.Identity, viaBus bool) string {
	if strings.TrimSpace(t.cfg.MailDir) != "" {
		return t.cfg.MailDir
	}
	if viaBus {
		if dir := config.BusMailDirLive(); dir != "" {
			return dir
		}
	}
	return config.SessionCollabMailDir()
}

// waitReply polls the caller's own inbox for an answer on the delivered
// message's thread (task 174; the old talk_to_session_sync behavior verbatim).
func (t talkToSessionTool) waitReply(ctx context.Context, messageID string, timeoutMS int) (string, error) {
	queued := fmt.Sprintf(`{"status":"queued","messageId":%q,"threadId":%q}`, messageID, messageID)
	var sent struct {
		MessageID string `json:"messageId"`
	}
	_ = json.Unmarshal([]byte(queued), &sent)
	if sent.MessageID == "" {
		return queued, nil
	}
	// Task 156.A: mint the sender address on first send (same as async). Task
	// 158.B: resolved at call time, not from the boot snapshot.
	me := t.cfg.currentContactID()
	if me == "" {
		return queued, nil // nothing to receive an answer on
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 120*time.Second {
		timeout = 120 * time.Second
	}
	// Task 158.A: this is a REAL wait, not a fire-and-forget send — it polls
	// the requester's own inbox for a message whose threadId is the id we just
	// delivered, bounded by `timeout` (max 120s) and by the caller's context.
	started := time.Now()
	reply, ok := sessioncollab.NewMailStore(mailDir).AwaitReplyContext(ctx, me, sent.MessageID, timeout)
	waited := int(time.Since(started) / time.Millisecond)
	if !ok {
		note := "request was delivered; the reply will arrive in your inbox later"
		if ctx != nil && ctx.Err() != nil {
			// The turn was cancelled while waiting. The result is the same
			// "no reply yet" a timeout reports — the request is already
			// queued — but the reason must not read as an unexplained
			// timeout, or the caller re-sends a message that is in flight.
			note = "wait ended early (" + ctx.Err().Error() +
				"); the request was delivered and the reply will still arrive in your inbox"
		}
		out, _ := json.Marshal(map[string]any{
			"status":    "timeout",
			"messageId": sent.MessageID,
			"threadId":  sent.MessageID,
			"waitedMs":  waited,
			"timeoutMs": int(timeout / time.Millisecond),
			"note":      note,
		})
		return string(out), nil
	}
	out, _ := json.Marshal(map[string]any{
		"status":    "replied",
		"messageId": sent.MessageID,
		"threadId":  sent.MessageID,
		"waitedMs":  waited,
		"from":      reply.From,
		"body":      reply.Body,
	})
	return string(out), nil
}

// NewTalkToSessionSyncTool is the synchronous variant. It delivers the same
// durable message and then waits — bounded — for an answer carrying the thread
// id. A timeout is reported as a status, not an error: the request is already
// queued, so failing the call would misreport what happened.
//
// Task 174: boot no longer registers this tool — talk_to_session(wait=true) is
// the same capability — but the constructor stays for direct callers and tests.
func NewTalkToSessionSyncTool(cfg SessionCollabConfig) tool.Tool {
	return talkToSessionSyncTool{cfg: cfg}
}

type talkToSessionSyncTool struct{ cfg SessionCollabConfig }

func (talkToSessionSyncTool) Name() string { return "talk_to_session_sync" }

func (talkToSessionSyncTool) Description() string {
	return "Send a message to another registered session and wait for its reply (task 19 / 142). The request is delivered durably first; if no reply arrives within the timeout this returns status=timeout with the message id, and the answer still lands in your inbox later. Prefer talk_to_session (async) for long tasks. Your own contact_id is minted automatically on first send. When answering a message that was delivered to you, ALWAYS reply through this tool (async form) with to = the From contact_id — never answer inside your own transcript. Experimental."
}

func (talkToSessionSyncTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"to":{"type":"string","description":"Target contact_id."},"message":{"type":"string"},"hop":{"type":"integer"},"card_id":{"type":"string"},"timeout_ms":{"type":"integer","description":"How long to wait for the reply (default 30000, max 120000)."},"require_reply":{"type":"boolean","description":"Set true when the sender needs an answer on this thread (task 173). Requires the panel switch session_collab_allow_require_reply."}},"required":["to","message"]}`)
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
	// Task 156.A: mint the sender address on first send (same as async). Task
	// 158.B: resolved at call time, not from the boot snapshot.
	me := t.cfg.currentContactID()
	if me == "" {
		return queued, nil // nothing to receive an answer on
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	// Task 158.A: this is a REAL wait, not a fire-and-forget send — it polls
	// the requester's own inbox for a message whose threadId is the id we just
	// delivered, bounded by `timeout` (max 120s) and by the caller's context.
	started := time.Now()
	reply, ok := sessioncollab.NewMailStore(mailDir).AwaitReplyContext(ctx, me, sent.MessageID, timeout)
	waited := int(time.Since(started) / time.Millisecond)
	if !ok {
		note := "request was delivered; the reply will arrive in your inbox later"
		if ctx != nil && ctx.Err() != nil {
			// The turn was cancelled while waiting. The result is the same
			// "no reply yet" a timeout reports — the request is already
			// queued — but the reason must not read as an unexplained
			// timeout, or the caller re-sends a message that is in flight.
			note = "wait ended early (" + ctx.Err().Error() +
				"); the request was delivered and the reply will still arrive in your inbox"
		}
		out, _ := json.Marshal(map[string]any{
			"status":    "timeout",
			"messageId": sent.MessageID,
			"threadId":  sent.MessageID,
			"waitedMs":  waited,
			"timeoutMs": int(timeout / time.Millisecond),
			"note":      note,
		})
		return string(out), nil
	}
	out, _ := json.Marshal(map[string]any{
		"status":    "replied",
		"messageId": sent.MessageID,
		"threadId":  sent.MessageID,
		"waitedMs":  waited,
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
	load := func(sessionPath string) sessioncollab.MetaInfo {
		m, found, err := LoadBranchMeta(sessionPath)
		if err != nil {
			return sessioncollab.MetaInfo{}
		}
		// Every conversation belongs in the directory; contact_id is minted on
		// first contact rather than being a precondition for existence.
		info := sessioncollab.MetaInfo{
			Title: SessionDirectoryTitle(sessionPath),
			OK:    true,
		}
		if found {
			info.ContactID = m.ContactID
			info.Purpose = m.Purpose
			info.TopicID = m.TopicID
			// 任务 348: structured fields ride the same sidecar read — no second
			// file access, and absent for every purpose-only (pre-348) session.
			info.IdentityType = m.IdentityType
			info.IdentityDomain = m.IdentityDomain
			info.Duties = m.Duties
			// 任务 508: the persisted turn count rides the same read. 0 (older
			// sidecars, purpose-only sessions) means unknown — never a guess.
			info.Turns = m.Turns
		}
		return info
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
		add(sessioncollab.ScanDirMeta(dir.dir, dir.workspace, load), dir.workspace, dir.scope, dir.archived)
	}
	for _, projectSessions := range config.AllProjectSessionDirs() {
		root := ""
		// sessions dir is <state>/projects/<slug>/sessions; the slug is enough
		// for OpenTopicSession, which only needs a consistent scope+root pair.
		root = filepath.Dir(filepath.Dir(projectSessions))
		add(sessioncollab.ScanDirMeta(projectSessions, root, load), root, "project", false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// collabRefMatches is the two-sided compare: exact (case-insensitive) first so
// titles the normalizer cannot represent — CJK-only names fold to an empty
// key — keep their precise match, then the non-empty normalized form so
// "PR 1741" reaches "pr_1741". An empty normalized side never participates,
// which is what keeps two distinct CJK titles out of each other's ambiguity.
func collabRefMatches(key, ref, normKey, normRef string) bool {
	if strings.EqualFold(key, ref) {
		return true
	}
	return normKey != "" && normRef != "" && normKey == normRef
}

// ResolveTarget maps a directory reference — contact_id, topic_id, or an
// exact title — to a session. A session that has not minted a contact_id yet
// gets one now, so the first message to a named conversation is enough to make
// it permanently addressable.
// normalizeCollabRef folds a directory reference to the canonical match form
// (task 243 A6, sub-report 01-④3): trim, lower-case, and collapse every
// non-alphanumeric run to a single underscore — the MiMo topicKey rule, so
// "PR 1741" and "pr_1741" address the same session. Applied to BOTH sides of
// every directory comparison; a normalization collision surfaces as the
// existing multi-match ambiguity error, never as a silent wrong pick.
func normalizeCollabRef(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.TrimRight(b.String(), "_")
}

func ResolveTarget(ids []sessioncollab.Identity, ref string) (sessioncollab.Identity, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return sessioncollab.Identity{}, fmt.Errorf("target is required")
	}
	// Task 243 A6: every key compares on the normalized form (two-sided), so
	// "PR 1741" reaches a session titled "pr_1741" and vice versa; purpose
	// joins title as the fourth find-or-reuse key — a hit relays to the
	// existing session (never a new one) exactly like title does.
	norm := normalizeCollabRef(ref)
	var byContact, byTopic, byTitle, byPurpose []sessioncollab.Identity
	for _, id := range ids {
		switch {
		case id.ContactID != "" && collabRefMatches(id.ContactID, ref, normalizeCollabRef(id.ContactID), norm):
			byContact = append(byContact, id)
		case id.TopicID != "" && collabRefMatches(id.TopicID, ref, normalizeCollabRef(id.TopicID), norm):
			byTopic = append(byTopic, id)
		case id.Title != "" && collabRefMatches(id.Title, ref, normalizeCollabRef(id.Title), norm):
			byTitle = append(byTitle, id)
		case id.Purpose != "" && collabRefMatches(id.Purpose, ref, normalizeCollabRef(id.Purpose), norm):
			byPurpose = append(byPurpose, id)
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
	if len(byPurpose) > 1 {
		names := make([]string, 0, len(byPurpose))
		for _, id := range byPurpose {
			names = append(names, id.Title+" ("+id.TopicID+")")
		}
		return sessioncollab.Identity{}, fmt.Errorf("purpose %q matches %d sessions — use topic_id instead: %s",
			ref, len(byPurpose), strings.Join(names, ", "))
	}
	if len(byPurpose) == 1 {
		return byPurpose[0], nil
	}
	return sessioncollab.Identity{}, fmt.Errorf("%w: %q is not in the contact directory (use list_addressable_sessions)", sessioncollab.ErrNotFound, ref)
}

// SessionDirectoryTitle returns the display title of a session exactly as the
// contact directory computes it: the branch-meta custom title, else the topic
// title, else the file stem.
//
// Exported so every collaboration surface (the agent-side directory, the
// desktop roster, the delete dry-run impact) names a conversation the same way.
// Two sources for one title is how a dry run ends up reporting "" for a session
// the user can see in the sidebar (task 158.D).
func SessionDirectoryTitle(sessionPath string) string {
	title := strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath))
	if m, found, err := LoadBranchMeta(sessionPath); err == nil && found {
		if m.CustomTitle != "" {
			title = m.CustomTitle
		} else if m.TopicTitle != "" {
			title = m.TopicTitle
		}
	}
	return title
}

// SessionTranscriptHasContent reports whether a transcript holds anything
// beyond the empty placeholder a session is created with (an empty file plus
// its sidecar). It answers "would deleting this discard work?" from the FILE —
// the same source read_session_tail reads — so a session that already ran a
// turn is never reported as empty merely because no tab or runtime is bound to
// it at this instant.
func SessionTranscriptHasContent(sessionPath string) bool {
	if strings.TrimSpace(sessionPath) == "" {
		return false
	}
	info, err := os.Stat(sessionPath)
	if err != nil {
		return false
	}
	return info.Size() > 0
}
