package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	// Shell marks a task-1145 registry shell (registered in the desktop
	// registry, no main transcript on disk). omitempty keeps every pre-1145
	// answer byte-identical; when true, SessionPath is empty — there is no
	// file to trash, the delete converges on the registry entry.
	Shell bool `json:"shell,omitempty"`
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
	return "Move sessions to the desktop trash (manual restore, same as the desktop Delete action) so they leave the contact directory. Single mode: pass `target` (contact_id / topic_id / title) — the caller's own session is refused. Batch mode (task tl1305): pass `targets` (array, same resolution per item) and/or `filter` ({group, title_prefix, inactive_days}, AND-combined) — one call reports and trashes many; failures are isolated per item and never abort the batch. Every batch candidate gets an impact report (open tab / in-flight turn / shell); the calling session and sessions with an in-flight turn are ALWAYS protected (reported, skipped), an open tab protects unless include_open_tabs=true, and protect_hours spares sessions active within the window. Dry-run first (omit confirm): a full per-item impact report with zero writes. Experimental."
}

func (deleteSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "target":{"type":"string","description":"Single mode: contact_id, topic_id, or exact title of the session to trash. Mutually exclusive with targets/filter."},
  "targets":{"type":"array","items":{"type":"string"},"description":"Batch mode: contact_ids / topic_ids / titles to trash, each resolved like target. Combine with filter for a union."},
  "filter":{"type":"object","description":"Batch mode: select candidates by predicate instead of listing them. Fields AND-combine; at least one field required.","properties":{"group":{"type":"string","description":"Sidebar group title (or flat group id) the session's topic belongs to."},"title_prefix":{"type":"string","description":"Case-insensitive title prefix, e.g. 'Heartbeat: '."},"inactive_days":{"type":"number","description":"Last activity (file mtime) at least this many days ago."}}},
  "include_open_tabs":{"type":"boolean","description":"Batch mode: also trash candidates whose tab is open (default false = open tabs are protected). An in-flight turn is protected regardless."},
  "protect_hours":{"type":"number","description":"Batch mode: spare sessions whose last activity is within this many hours (0 = no time window; the safety net for 'everything older than the heartbeat backlog')."},
  "confirm":{"type":"boolean","description":"Must be true to execute. Omit to get the impact report only (dry run)."}
},"required":[]}`)
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

// deleteSessionBatchLimit caps one batch call. A runaway filter (an empty
// prefix would match the whole directory) must fail loudly instead of trashing
// the contact directory in a single call — the caller splits the work instead.
const deleteSessionBatchLimit = 200

// deleteSessionFilter is the tl1305 batch predicate. Fields AND-combine; at
// least one must be set (a no-filter batch would be an unbounded directory
// wipe).
type deleteSessionFilter struct {
	Group        string  `json:"group"`
	TitlePrefix  string  `json:"title_prefix"`
	InactiveDays float64 `json:"inactive_days"`
}

// batchRefCount distinguishes "targets/filter absent" from "present but
// empty": the single-target contract (and its byte-identical output) applies
// only when neither batch key appears at all.
type deleteSessionArgs struct {
	Target         string               `json:"target"`
	Targets        []string             `json:"targets"`
	Filter         *deleteSessionFilter `json:"filter"`
	IncludeOpenTab bool                 `json:"include_open_tabs"`
	ProtectHours   float64              `json:"protect_hours"`
	Confirm        bool                 `json:"confirm"`
}

func (t deleteSessionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p deleteSessionArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	batch := len(p.Targets) > 0 || p.Filter != nil
	if batch && strings.TrimSpace(p.Target) != "" {
		return "", fmt.Errorf("delete_session: pass either `target` (single) or `targets`/`filter` (batch), not both — the single contract reports one session, the batch contract reports many")
	}
	if batch {
		return t.executeBatch(p)
	}
	return t.executeSingle(p.Target, p.Confirm)
}

// executeSingle is the pre-tl1305 contract, byte-identical: resolve one target,
// dry-run its impact, and only trash on confirm=true.
func (t deleteSessionTool) executeSingle(target string, confirm bool) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("target is required")
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	id, err := ResolveTarget(ids, target)
	if err != nil {
		// 任务 1145：注册表空壳触达（软开关，默认关）。目录（磁盘主转录）解析
		// 不到时，若宿主探针认定该 ref 是一个注册表空壳 topic（只认 topic_id），
		// 走空壳清理面而不是 not found——树里可见（注册表有）但目录查不到的
		// 条目从此不再死循环。探针未注入（开关关）时保持原错误逐字节不变。
		if t.cfg.RegistryShellProbe != nil && t.cfg.RegistryShellTrash != nil {
			if shellID, shellTitle, ok := t.cfg.RegistryShellProbe(target); ok {
				return t.executeShell(shellID, shellTitle, confirm)
			}
		}
		return "", err
	}
	// Guard: the calling session must not delete itself. That is a self-
	// destructive path the desktop exposes through a different UI; a secretary
	// accidentally trashing its own transcript mid-turn would be unrecoverable
	// from the collaboration surface.
	if own := t.cfg.currentSessionPath(); own != "" && strings.EqualFold(id.SessionPath, own) {
		return "", fmt.Errorf("refusing to delete the calling session (%q) — close it from the desktop instead", target)
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
			return "", fmt.Errorf("delete_session: cannot verify the inbox of %q (%v) — the unconsumed-mail gate fails closed (宁紧勿松), archive refused; check the inbox panel and retry", target, err)
		}
		unconsumed = n
		if confirm && n > 0 {
			return "", fmt.Errorf("delete_session: %q still has %d unconsumed inbox message(s) (queued≠read) — the task-509 pre-archive gate refuses; have the recipient consume them first (drain_inbox / inbox panel), then re-run", target, n)
		}
	}
	// Dry run and real delete are separate calls with an explicit dryRun flag,
	// so a "just look at the impact" request can never move the session (audit
	// F154-2: a prior version called the host unconditionally and the host
	// always trashed).
	if !confirm {
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

// deleteBatchItem is one candidate's row in the batch report — the dry run
// carries the impact + protection verdict, the confirmed call carries the
// outcome. status is "" on the dry run, then trashed | skipped | failed.
type deleteBatchItem struct {
	Target       string               `json:"target"`
	Title        string               `json:"title,omitempty"`
	SessionPath  string               `json:"sessionPath,omitempty"`
	Status       string               `json:"status,omitempty"`
	Reason       string               `json:"reason,omitempty"`
	Shell        bool                 `json:"shell,omitempty"`
	OpenTab      bool                 `json:"openTab,omitempty"`
	HasTurn      bool                 `json:"hasTurn,omitempty"`
	TurnInFlight bool                 `json:"turnInFlight,omitempty"`
	Protected    bool                 `json:"protected,omitempty"`
	Result       *DeleteSessionResult `json:"result,omitempty"`
}

type deleteBatchSummary struct {
	Matched    int `json:"matched"`
	Trashed    int `json:"trashed,omitempty"`
	Skipped    int `json:"skipped,omitempty"`
	Failed     int `json:"failed,omitempty"`
	WouldTrash int `json:"wouldTrash,omitempty"`
}

// executeBatch is the tl1305 face: many targets and/or a filter predicate, one
// call, per-item isolation. The protection ladder (calling session / in-flight
// turn / open tab / time window) runs on the impact report the host already
// computes for a dry run, so batch and single deletes always see the same
// truth. Failures never abort the batch — a mistyped target among 45 correct
// ones must not resurrect the manual-cleanup drudgery this tool exists to end.
func (t deleteSessionTool) executeBatch(p deleteSessionArgs) (string, error) {
	if t.del == nil {
		return "", fmt.Errorf("delete_session: this host cannot delete sessions")
	}
	if len(p.Targets) == 0 && p.Filter != nil && p.Filter.empty() {
		return "", fmt.Errorf("delete_session: filter needs at least one of group / title_prefix / inactive_days — an empty predicate would select the whole directory")
	}
	if len(p.Targets) > deleteSessionBatchLimit {
		return "", fmt.Errorf("delete_session: %d targets exceed the %d-per-call limit — split the work or narrow the filter", len(p.Targets), deleteSessionBatchLimit)
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	own := t.cfg.currentSessionPath()
	nowMS := time.Now().UnixMilli()

	// Phase 1 — candidates: explicit targets resolve like the single face
	// (including the 1145 registry-shell probe); filter candidates come off the
	// directory scan. Dedup on the resolved session path keeps a target that is
	// also filter-matched to one row.
	candidates := make([]deleteBatchCandidate, 0, len(p.Targets)+8)
	seen := map[string]bool{}
	addCandidate := func(c deleteBatchCandidate) {
		key := strings.ToLower(c.sessionPath)
		if key != "" && seen[key] {
			return
		}
		if key != "" {
			seen[key] = true
		}
		candidates = append(candidates, c)
	}
	for _, ref := range p.Targets {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		c := deleteBatchCandidate{target: ref}
		id, err := ResolveTarget(ids, ref)
		switch {
		case err == nil:
			c.identity = &id
			c.sessionPath = id.SessionPath
		default:
			// 1145: a registry shell has no transcript to resolve — same
			// soft-switched face as the single path, only when enabled.
			if t.cfg.RegistryShellProbe != nil && t.cfg.RegistryShellTrash != nil {
				if shellID, shellTitle, ok := t.cfg.RegistryShellProbe(ref); ok {
					c.shellID, c.shellTitle = shellID, shellTitle
				} else {
					c.resolveErr = err
				}
			} else {
				c.resolveErr = err
			}
		}
		addCandidate(c)
	}
	if p.Filter != nil {
		matched := 0
		for _, id := range ids {
			if !t.filterMatches(*p.Filter, id, nowMS) {
				continue
			}
			matched++
			if matched > deleteSessionBatchLimit {
				return "", fmt.Errorf("delete_session: the filter matches more than %d sessions — narrow it (add title_prefix / shorten inactive_days) or raise the work in batches", deleteSessionBatchLimit)
			}
			addCandidate(deleteBatchCandidate{target: id.Title, identity: &id, sessionPath: id.SessionPath, fromFilter: true})
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("delete_session: no sessions match — check the targets and filter (group names must match the sidebar, title_prefix is case-insensitive)")
	}
	if len(candidates) > deleteSessionBatchLimit {
		return "", fmt.Errorf("delete_session: the call resolved to %d sessions, above the %d-per-call limit — narrow the filter or split the work", len(candidates), deleteSessionBatchLimit)
	}

	// Phase 2 — impact + protection ladder, one host dry run per candidate.
	items := make([]deleteBatchItem, 0, len(candidates))
	runnable := make([]deleteBatchCandidate, 0, len(candidates))
	summary := deleteBatchSummary{Matched: len(candidates)}
	for _, c := range candidates {
		item := deleteBatchItem{Target: c.target, Shell: c.shellID != ""}
		if c.resolveErr != nil {
			item.Status = "failed"
			item.Reason = c.resolveErr.Error()
			summary.Failed++
			items = append(items, item)
			continue
		}
		if c.shellID != "" {
			// Registry shells carry no transcript: nothing to protect, nothing
			// to dry-run — the shell face reports its own impact.
			item.Title = c.shellTitle
			runnable = append(runnable, c)
			items = append(items, item)
			continue
		}
		dryImpact, _, err := t.del(c.identity.ContactID, c.sessionPath, true)
		if err != nil {
			item.SessionPath = c.sessionPath
			item.Status = "failed"
			item.Reason = err.Error()
			summary.Failed++
			items = append(items, item)
			continue
		}
		if strings.TrimSpace(dryImpact.Title) == "" {
			dryImpact.Title = c.identity.Title
		}
		item.Title = dryImpact.Title
		item.SessionPath = c.sessionPath
		item.OpenTab = dryImpact.OpenTab
		item.HasTurn = dryImpact.HasTurn
		item.TurnInFlight = dryImpact.TurnInFlight
		// Protection ladder, hardest first. The calling session and a live
		// turn are non-negotiable (a secretary must not trash its own
		// transcript or kill work in flight); open tabs and the time window
		// are the caller's dials.
		switch {
		case own != "" && strings.EqualFold(c.sessionPath, own):
			item.Protected = true
			item.Reason = "protected: the calling session (close it from the desktop instead)"
		case dryImpact.TurnInFlight:
			item.Protected = true
			item.Reason = "protected: a turn is in flight — wait for it to finish"
		case dryImpact.OpenTab && !p.IncludeOpenTab:
			item.Protected = true
			item.Reason = "protected: the session has an open tab (pass include_open_tabs=true to override)"
		case p.ProtectHours > 0 && c.identity.UpdatedAt >= nowMS-int64(p.ProtectHours*3600_000):
			item.Protected = true
			item.Reason = fmt.Sprintf("protected: last activity within the %.1fh window", p.ProtectHours)
		}
		if item.Protected {
			item.Status = "skipped"
			summary.Skipped++
		} else {
			runnable = append(runnable, c)
		}
		items = append(items, item)
	}
	summary.WouldTrash = len(runnable)

	if !p.Confirm {
		out := map[string]any{
			"status":  "dry_run",
			"items":   items,
			"summary": summary,
			"note":    "re-run with confirm=true to trash the unprotected sessions",
		}
		outJSON, _ := json.Marshal(out)
		return string(outJSON), nil
	}

	// Phase 3 — execute the runnable set. Per-item isolation: a failure is
	// recorded and the batch moves on (失败项不阻断). Shell items go through the
	// single-path shell face so the registry convergence stays in one place.
	next := 0
	for i := range items {
		item := &items[i]
		if item.Status != "" {
			continue // skipped / failed rows settled in phase 2 — never enqueued
		}
		c := runnable[next]
		next++
		if c.shellID != "" {
			if err := t.cfg.RegistryShellTrash(c.shellID, false); err != nil {
				item.Status = "failed"
				item.Reason = fmt.Sprintf("registry shell removal failed: %v", err)
				summary.Failed++
				continue
			}
			item.Status = "trashed"
			item.Result = &DeleteSessionResult{
				ContactID:    c.shellID,
				SessionPath:  "",
				Trashed:      true,
				RestoreUntil: "registry entry removed; there was no session file to restore",
			}
			summary.Trashed++
			continue
		}
		impact, result, err := t.del(c.identity.ContactID, c.sessionPath, false)
		if err != nil {
			item.Status = "failed"
			item.Reason = err.Error()
			summary.Failed++
			continue
		}
		if strings.TrimSpace(impact.Title) == "" {
			impact.Title = item.Title
		}
		item.Status = "trashed"
		res := result
		item.Result = &res
		summary.Trashed++
	}
	out := map[string]any{
		"status":  "batch_done",
		"items":   items,
		"summary": summary,
	}
	outJSON, _ := json.Marshal(out)
	return string(outJSON), nil
}

// deleteBatchCandidate is one resolved batch target before protection is
// judged. Exactly one of identity / shellID / resolveErr is meaningful.
type deleteBatchCandidate struct {
	target      string
	identity    *sessioncollab.Identity
	sessionPath string
	shellID     string
	shellTitle  string
	resolveErr  error
	fromFilter  bool
}

// empty reports a predicate with no field set.
func (f deleteSessionFilter) empty() bool {
	return strings.TrimSpace(f.Group) == "" &&
		strings.TrimSpace(f.TitlePrefix) == "" &&
		f.InactiveDays <= 0
}

// filterMatches applies the AND-combined predicate to one directory identity.
// Group matching rides the host probes when present (task 285: SessionGroup
// answers the sidebar group; SessionGroupMatch also accepts a flat group id);
// without probes only the literal title comparison is possible and a group
// filter cannot match anything — reported, never guessed.
func (t deleteSessionTool) filterMatches(f deleteSessionFilter, id sessioncollab.Identity, nowMS int64) bool {
	if prefix := strings.TrimSpace(f.TitlePrefix); prefix != "" {
		if !strings.HasPrefix(strings.ToLower(id.Title), strings.ToLower(prefix)) {
			return false
		}
	}
	if group := strings.TrimSpace(f.Group); group != "" {
		switch {
		case t.cfg.SessionGroupMatch != nil:
			if !t.cfg.SessionGroupMatch(id.TopicID, group) {
				return false
			}
		case t.cfg.SessionGroup != nil:
			name, known := t.cfg.SessionGroup(id.TopicID)
			if !known || !strings.EqualFold(name, group) {
				return false
			}
		default:
			// No host probe: a group predicate cannot be honoured. Fail closed —
			// matching everything would trash sessions the caller never meant.
			return false
		}
	}
	if days := f.InactiveDays; days > 0 {
		if id.UpdatedAt > nowMS-int64(days*24*60*60*1000) {
			return false
		}
	}
	return true
}

// executeShell is the task-1145 registry-shell face of delete_session: same
// dry-run → confirm contract, but the target is a registry entry with no main
// transcript on disk, so the impact carries shell=true and an empty session
// path (nothing on disk to trash — the write converges on the registry).
func (t deleteSessionTool) executeShell(shellID, shellTitle string, confirm bool) (string, error) {
	impact := DeleteSessionImpact{Title: shellTitle, Shell: true}
	if !confirm {
		out, _ := json.Marshal(map[string]any{
			"status": "dry_run",
			"impact": impact,
			// The next step must exist: confirm really does converge the shell.
			"note": "registry shell (registered, no session file on disk) — re-run with confirm=true to remove it from the desktop registry (tree, groups, title table)",
		})
		return string(out), nil
	}
	if err := t.cfg.RegistryShellTrash(shellID, false); err != nil {
		return "", fmt.Errorf("delete_session: removing registry shell %q failed: %w", shellID, err)
	}
	out, _ := json.Marshal(map[string]any{
		"status": "trashed",
		"result": DeleteSessionResult{
			ContactID:    shellID,
			SessionPath:  "",
			Trashed:      true,
			RestoreUntil: "registry entry removed; there was no session file to restore",
		},
		"impact": impact,
	})
	return string(out), nil
}
