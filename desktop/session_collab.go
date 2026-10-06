package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/safego"
	"reasonix/internal/secrets"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// Session collaboration pump (task 19 / 142).
//
// talk_to_session only appends to a durable mailbox: a tool cannot drive another
// session's controller. This pump is the delivery half — it claims new mail for
// every open tab that owns a contact_id and enqueues it as a follow-up on that
// tab's inbox, where the existing idle dispatcher runs it.
//
// Scope, stated plainly: this is the ONLY live delivery path. A session with no
// open tab is not woken, and a headless/CLI process has no pump at all, so its
// mail waits. Delivery is at-least-once *for tabs that are open*: a message is
// acked only after it reached the target's inbox, and a redelivery reuses the
// message id as the inbox idempotency key, so a retry cannot duplicate a turn.
const sessionCollabPumpInterval = 4 * time.Second

// Task 485 P2: per-contact retry backoff for failed delivery passes. A
// refused pass is NOT acked, so without backoff the pump retried it every
// tick forever (568 WARN lines in one morning during the lease leak). The
// schedule doubles from collabRetryBackoffBase up to collabRetryBackoffMax;
// on-demand "deliver now" passes bypass it entirely.
const (
	collabRetryBackoffBase = 5 * time.Second
	collabRetryBackoffMax  = 5 * time.Minute
)

// collabRetryDelay is the wait after the failures-th consecutive failed pass
// (failures >= 1): 5s, 10s, 20s, ... capped at 5 minutes. Pure so the
// schedule is pinned by a table test.
func collabRetryDelay(failures int) time.Duration {
	if failures <= 1 {
		return collabRetryBackoffBase
	}
	d := collabRetryBackoffBase
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= collabRetryBackoffMax {
			return collabRetryBackoffMax
		}
	}
	return d
}

type sessionCollabPump struct {
	app *App

	mu      sync.Mutex
	started bool
	stop    chan struct{}

	// retryMu guards the per-contact backoff state (task 485 P2).
	retryMu sync.Mutex
	retry   map[string]*collabRetryState
}

// collabRetryState is one contact's consecutive-failure chain: the count
// drives the doubling schedule, notBefore gates the next pump pass.
type collabRetryState struct {
	failures  int
	notBefore time.Time
}

// contactRetryDue reports whether a pump pass may touch the contact. A
// contact with no recorded failure is always due.
func (p *sessionCollabPump) contactRetryDue(contactID string, now time.Time) bool {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	st, ok := p.retry[contactID]
	return !ok || !now.Before(st.notBefore)
}

// deferContactRetry schedules the next attempt after another consecutive
// failure: first failure waits 5s, then doubling up to the 5-minute cap.
func (p *sessionCollabPump) deferContactRetry(contactID string, now time.Time) {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	failures := 1
	if st, ok := p.retry[contactID]; ok {
		failures = st.failures + 1
	}
	if p.retry == nil {
		p.retry = map[string]*collabRetryState{}
	}
	p.retry[contactID] = &collabRetryState{failures: failures, notBefore: now.Add(collabRetryDelay(failures))}
}

// resetContactRetry clears a contact's backoff after a successful pass.
func (p *sessionCollabPump) resetContactRetry(contactID string) {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	delete(p.retry, contactID)
}

func newSessionCollabPump(app *App) *sessionCollabPump {
	return &sessionCollabPump{app: app}
}

func (p *sessionCollabPump) Start() {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.stop = make(chan struct{})
	stop := p.stop
	p.mu.Unlock()
	// Task 188: the pump owns the delivery path whose last words before the
	// 2026-09-20 silent exit were "[session-collab] opened session" — wrap the
	// loop so a panic inside a delivery pass is recovered with its stack in the
	// rolling log instead of taking the whole process down unseen.
	safego.Go("sessioncollab.pump.loop", func() { p.loop(stop) })
}

func (p *sessionCollabPump) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		return
	}
	p.started = false
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
}

func (p *sessionCollabPump) loop(stop chan struct{}) {
	ticker := time.NewTicker(sessionCollabPumpInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !sessionCollabEnabled() {
				continue
			}
			p.drainOnce()
		}
	}
}

// sessionCollabEnabled reads the live user config. The pump is a no-op while
// the experiment is off, so an untouched install pays nothing.
func sessionCollabEnabled() bool {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return false
	}
	return cfg.Agent.ExperimentalSessionCollab
}

// syncCollabInboxMergeMode re-arms the controller-level drain merge from the
// live config (task 221). Reading it per pump pass keeps a settings change
// effective from the next drain without a restart; the dispatcher itself only
// pays an atomic load per admission.
func syncCollabInboxMergeMode() {
	if cfg, err := config.Load(); err == nil && cfg != nil {
		control.SetCollabInboxMergeMode(cfg.Agent.CollabInboxMerge)
	}
}

// sessionCollabHopLimit resolves the chain ceiling in force (task 204). The config
// layer clamps the experimental value; an unreadable config keeps the package default,
// so a broken file can never widen the ceiling.
func sessionCollabHopLimit() int {
	return config.SessionCollabHopLimitLive()
}

// collabBackgroundDelivery resolves the task-224 gate: when the experimental
// background-delivery switch is on, collab delivery opens the target tab
// inactive so the woken conversation runs without stealing focus, and the
// host pump skips MailStore entirely (drain_inbox becomes the sole consumer).
// minor-1 (audit-2): this gate MUST be the same resolution the boot
// registration used. A live config.Load() here let an ON→OFF flip without a
// restart resume the pump under a still-registered drain_inbox, reopening the
// M-a double-consume race on the read-only Claim cursor. It now reads the
// boot-published snapshot; an unreadable config resolved to false at boot and
// stays false for the process lifetime.
func collabBackgroundDelivery() bool {
	return boot.CollabDrainInboxGate()
}

// collabBackgroundDeliveryFromConfig is the pure branch extracted for M3
// testing. Returns true when experimental_collab_background_delivery is on.
func collabBackgroundDeliveryFromConfig(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Agent.ExperimentalCollabBackgroundDelivery
}

// CollabOpenDetachedFromConfig is the task-264 pure branch (two-mode test).
// On: pump stand-ups build a detached runtime instead of a visible tab.
// Unlike the 224 gate this is a live panel setting — it is read per drain
// pass so a settings flip applies without a restart, and it never changes
// delivery semantics (only where the runtime lands).
func CollabOpenDetachedFromConfig(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Agent.SessionCollabBackground
}

func collabOpenDetached() bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return CollabOpenDetachedFromConfig(cfg)
}

// SessionCollabDrainResult reports one delivery pass so failures stay visible.
type SessionCollabDrainResult struct {
	Delivered int                        `json:"delivered"`
	Refused   int                        `json:"refused"`
	Targets   []SessionCollabDrainTarget `json:"targets"`
}

type SessionCollabDrainTarget struct {
	TabID     string `json:"tabId"`
	ContactID string `json:"contactId"`
	Delivered int    `json:"delivered"`
	Refused   int    `json:"refused"`
	Error     string `json:"error,omitempty"`
}

// DrainSessionCollabMail performs one delivery pass on demand. Exposed for
// tests and for a user-triggered "deliver now"; the pump calls the same code.
// On-demand passes bypass the retry backoff (task 485 P2): an explicit
// "deliver now" is the user saying try, not the pump hammering a busy lease.
func (a *App) DrainSessionCollabMail() SessionCollabDrainResult {
	if a.sessionCollab == nil {
		return SessionCollabDrainResult{}
	}
	return a.sessionCollab.drain(false)
}

func (p *sessionCollabPump) drainOnce() {
	// Task 221: re-arm the drain merge before any delivery, so a settings
	// change lands with the next pass and the queued merge sees it.
	syncCollabInboxMergeMode()
	result := p.drain(true)
	if result.Delivered == 0 && result.Refused == 0 {
		return
	}
	log.Printf("[session-collab] delivered=%d refused=%d across %d tab(s)", result.Delivered, result.Refused, len(result.Targets))
}

// createCollabSession is the host capability behind create_collab_session
// (task 144, hardened by 154): it creates the topic, files it into the requested
// group, AND writes the transcript + contact_id + purpose immediately, so the
// session is addressable in the contact directory before anyone opens it. The
// previous version only minted a topic_id and left a pending purpose for a
// first-run stamp — which meant talk_to_session(to=new_topic) returned not-found
// until the user manually opened the session (incident 2026-09-17).
func (a *App) createCollabSession(req agent.CreateCollabSessionRequest) (agent.CreateCollabSessionResult, error) {
	// Task 156.B (audit F154-6): the global tab carries a non-empty
	// WorkspaceRoot (globalWorkspaceRoot(), app.go), so the old
	// "workspaceRoot != ''" probe misfiled every global-tab collab session
	// as a project topic — which re-created the "global-workspace" project
	// after the user deleted it (ghost-project loop, incident 2026-09-17).
	// Compare against the real global workspace root instead; only a genuinely
	// different project root counts as project scope.
	//
	// A requested project must be one the desktop already knows: creating a
	// session in an unknown root would leave a project nobody opened, which is
	// how the ghost project above appeared in the first place.
	scope, root, serr := resolveCollabTargetScope(req.WorkspaceRoot, req.ProjectRoot, a.registeredCollabProjectRoots())
	if serr != nil {
		return agent.CreateCollabSessionResult{}, serr
	}
	_, defaultApproval, _ := desktopNewSessionDefaults(scope, root)

	items := req.ItemList()
	result := agent.CreateCollabSessionResult{}
	var firstErr error
	for _, item := range items {
		one, err := a.createOneCollabSession(scope, root, item, defaultApproval)
		if err != nil {
			// Partial success is the contract (task 166): an item is created the
			// moment its file and contact_id land, so a later failure never rolls
			// back what already exists — the caller retries only the failures.
			result.Failed = append(result.Failed, agent.CreateCollabSessionFailure{
				Title:  strings.TrimSpace(item.Title),
				Reason: err.Error(),
			})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		result.Created = append(result.Created, one)
	}
	if len(result.Created) == 0 {
		if firstErr == nil {
			firstErr = fmt.Errorf("no session was created")
		}
		return result, firstErr
	}
	// The single form keeps its historical flat fields so existing callers and
	// tests read the same shape they always did.
	if len(items) == 1 {
		first := result.Created[0]
		result.TopicID = first.TopicID
		result.ContactID = first.ContactID
		result.SessionPath = first.SessionPath
		result.Purpose = first.Purpose
		result.Group = first.Group
		result.GroupID = first.GroupID
		result.Model = first.Model
		result.Scope = first.Scope
		result.ProjectRoot = first.ProjectRoot
	}
	// Task 167: a first message rides the existing mailbox path — the same
	// MailStore.Deliver the talk_to_session tool uses, drained by the same pump,
	// so there is no second delivery channel to keep in step.
	if body := strings.TrimSpace(req.Message); body != "" {
		a.queueCollabFirstMessage(&result, body, req.Delivery)
	}
	// A brand-new topic is a tree change; re-emit so the sidebar and session
	// catalog pick up the file we just wrote (sub-item B: no manual refresh).
	a.emitProjectTreeChanged()
	return result, nil
}

// createOneCollabSession creates a single collaborating session: topic, group,
// transcript, contact_id, purpose, approval mode and (when requested) the model
// pin. It is the loop body of the batch form and the whole of the single form.
func (a *App) createOneCollabSession(scope, root string, item agent.CreateCollabSessionItem, defaultApproval string) (agent.CreateCollabSessionItemResult, error) {
	title, purpose := strings.TrimSpace(item.Title), strings.TrimSpace(item.Purpose)
	if title == "" || purpose == "" {
		return agent.CreateCollabSessionItemResult{}, fmt.Errorf("title and purpose are required for every session")
	}
	group, groupID := strings.TrimSpace(item.Group), strings.TrimSpace(item.GroupID)
	if group == "" && groupID == "" {
		return agent.CreateCollabSessionItemResult{}, fmt.Errorf("group or group_id is required: an ungrouped expert session is invisible to the team view")
	}
	// Task 162: an explicit model pin is validated with the same provider/model
	// resolution the settings UI uses. A bare id is refused rather than guessed,
	// because two endpoints may expose the same model name.
	modelRef, merr := resolveRequestedCollabModel(scope, root, item.Model)
	if merr != nil {
		return agent.CreateCollabSessionItemResult{}, merr
	}
	meta, err := a.CreateTopic(scope, root, title)
	if err != nil {
		return agent.CreateCollabSessionItemResult{}, err
	}
	if group != "" || groupID != "" {
		if err := a.AddTopicToGroup(scope, root, meta.ID, groupID, group); err != nil {
			return agent.CreateCollabSessionItemResult{TopicID: meta.ID, Title: title, Purpose: purpose}, err
		}
	}

	// Create the transcript now, not on first open. The directory enumerates
	// .jsonl files, so without a file the session is invisible to
	// list_addressable_sessions and talk_to_session.
	dir := desktopSessionDir(root)
	sessionPath, ferr := createEmptySessionFile(dir, "collab")
	if ferr != nil {
		return agent.CreateCollabSessionItemResult{TopicID: meta.ID, Title: title}, fmt.Errorf("session file for %q: %w", title, ferr)
	}
	// Stamp contact_id + purpose + topic + scope onto the branch meta so the
	// directory sees a complete record immediately. This is the "创建即注册"
	// step: no pending-purpose round-trip through the pump.
	if _, perr := agent.SetSessionPurpose(sessionPath, purpose); perr != nil {
		return agent.CreateCollabSessionItemResult{TopicID: meta.ID, Title: title, SessionPath: sessionPath}, fmt.Errorf("register purpose for %q: %w", title, perr)
	}
	// Task 156.C: inherit the desktop default tool approval mode (Ask/Auto/
	// YOLO from settings) so a collab session that auto-starts a turn from a
	// cross-session message can actually run tools. A hardcoded "ask" here
	// made every remotely-triggered tool call abort with "approval aborted"
	// — nobody is present to approve a turn the user never opened.
	// Task 162 follows the same pattern for the model pin: an explicit ref is
	// written here so the session starts on it instead of on the default.
	if uerr := agent.UpdateBranchMeta(sessionPath, false, func(m *agent.BranchMeta) error {
		m.TopicID = meta.ID
		m.TopicTitle = meta.Title
		m.Scope = scope
		m.WorkspaceRoot = root
		m.ToolApprovalMode = defaultApproval
		if modelRef != "" {
			m.Model = modelRef
		}
		return nil
	}); uerr != nil {
		return agent.CreateCollabSessionItemResult{TopicID: meta.ID, Title: title, SessionPath: sessionPath}, fmt.Errorf("bind topic %q to session: %w", meta.ID, uerr)
	}

	return agent.CreateCollabSessionItemResult{
		Title:       title,
		Purpose:     purpose,
		TopicID:     meta.ID,
		ContactID:   agent.SessionContactID(sessionPath),
		SessionPath: sessionPath,
		Group:       group,
		GroupID:     groupID,
		Model:       modelRef,
		Scope:       scope,
		ProjectRoot: root,
	}, nil
}

// resolveRequestedCollabModel validates an optional `provider/model` ref against
// the configuration that governs the new session (project config for a project
// scope, user config otherwise — the same source desktopNewSessionDefaults
// uses). The empty ref means "no pin": the session keeps resolving its default
// at first open, exactly as before (task 162 acceptance 4).
func resolveRequestedCollabModel(scope, root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", nil
	}
	prov, model, ok := strings.Cut(requested, "/")
	prov, model = strings.TrimSpace(prov), strings.TrimSpace(model)
	if !ok || prov == "" || model == "" {
		return "", fmt.Errorf("model %q must be a `provider/model` ref: a bare model id is ambiguous when two endpoints expose the same name, so the host will not pick one for you", requested)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if strings.TrimSpace(scope) == "project" && strings.TrimSpace(root) != "" {
		if projectCfg, cerr := config.LoadForRootReadOnly(root); cerr == nil {
			cfg = projectCfg
		}
	}
	if _, exists := cfg.Provider(prov); !exists {
		return "", fmt.Errorf("model %q names provider %q, which is not configured in this workspace", requested, prov)
	}
	if _, resolved := cfg.ResolveModel(requested); !resolved {
		return "", fmt.Errorf("provider %q does not expose model %q — check the model id in the provider settings", prov, model)
	}
	return requested, nil
}

// queueCollabFirstMessage hands the caller's first instruction to every created
// session through the collaboration mailbox (task 167). Failures are reported on
// the item, never swallowed: the session exists, so the caller can retry just the
// message with talk_to_session.
func (a *App) queueCollabFirstMessage(result *agent.CreateCollabSessionResult, body, delivery string) {
	mode := strings.TrimSpace(delivery)
	if mode == "" {
		// Creating a session WITH an instruction means "start working": a
		// followup would wait for a turn that never comes. steer keeps the
		// explicit followup available for callers who want it queued instead.
		mode = string(sessioncollab.DeliverySteer)
	}
	mailDir := config.SessionCollabMailDir()
	if strings.TrimSpace(mailDir) == "" {
		result.Failed = append(result.Failed, agent.CreateCollabSessionFailure{Title: "(first message)", Reason: "the collaboration mailbox directory is unavailable"})
		return
	}
	store := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit())
	from := a.collabCallerContactID()
	result.Delivery = mode
	for i := range result.Created {
		item := &result.Created[i]
		if strings.TrimSpace(item.ContactID) == "" {
			result.Failed = append(result.Failed, agent.CreateCollabSessionFailure{Title: item.Title, Reason: "the session has no contact_id, so it cannot receive the first message"})
			continue
		}
		msg, derr := store.Deliver(context.Background(), sessioncollab.MailMessage{
			From:     from,
			To:       item.ContactID,
			Body:     body,
			Delivery: mode,
			Hop:      0,
			ReplyTo:  from,
		})
		if derr != nil {
			result.Failed = append(result.Failed, agent.CreateCollabSessionFailure{Title: item.Title, Reason: "first message could not be queued: " + derr.Error()})
			continue
		}
		item.MessageID = msg.ID
		if result.MessageID == "" {
			result.MessageID = msg.ID
		}
		// Task 365 C5: the steer path never runs the mail pump's onDelivered
		// hook, so the very message that creates the task relationship used
		// to leave the cascade grant unregistered — the child's first
		// sensitive ask missed cascadeDelegateFor and parked locally. Bind
		// the grant here, with the same semantics as onDelivered
		// (target=recipient, source=sender). Registering is idempotent and
		// the TTL re-arms, so a later mail delivery just refreshes it.
		registerCascadeGrant(item.ContactID, from)
	}
	// Deliver now instead of waiting for the next pump tick, so a steer reaches
	// the new session while the caller is still in its turn.
	if a.sessionCollab != nil {
		a.sessionCollab.drainOnce()
	}
}

// collabCallerContactID resolves the CALLING session's collaboration address at
// call time. Task 158.B / S4: the boot-time snapshot is empty for desktop
// sessions, so the address must come from the live tab, never from startup
// state, or every message would read as coming from "(未登记)".
func (a *App) collabCallerContactID() string {
	a.mu.RLock()
	tab := a.tabs[a.activeTabID]
	a.mu.RUnlock()
	if tab == nil {
		return ""
	}
	path := strings.TrimSpace(a.currentSessionPathFor(tab))
	if path == "" {
		return ""
	}
	if id := agent.SessionContactID(path); id != "" {
		return id
	}
	if minted, err := agent.EnsureContactID(path); err == nil {
		return minted
	}
	return ""
}

// registeredCollabProjectRoots is the set of project roots the desktop already
// knows: the saved project list (what the sidebar shows) plus every project a
// live tab is open on. A root outside this set is not a project yet — creating
// a session "in" it would invent a project nobody opened.
//
// The union matters in both directions: a project the user opened but has not
// re-saved is still real, and a project persisted without an open tab is real
// too.
func (a *App) registeredCollabProjectRoots() []string {
	var roots []string
	seen := func(root string) bool {
		for _, r := range roots {
			if sameDesktopPath(r, root) {
				return true
			}
		}
		return false
	}
	for _, p := range loadProjectsFile().Projects {
		if root := strings.TrimSpace(p.Root); root != "" && !seen(root) {
			roots = append(roots, root)
		}
	}
	for _, root := range a.knownProjectRoots() {
		if root != "" && !seen(root) {
			roots = append(roots, root)
		}
	}
	return roots
}

// resolveCollabTargetScope decides WHERE a newly created collaborating session
// lands, and refuses the request when it cannot be honoured.
//
// No requested project: the caller's own scope wins, except that the global
// workspace root is not a project (task 156.B — treating it as one re-created a
// deleted "global-workspace" project).
//
// Requested project: it must be a root the desktop already knows. A typo that
// silently created a project would leave a stray project in the sidebar and a
// session in the wrong place, so the error lists what is available instead.
func resolveCollabTargetScope(callerRoot, requestedRoot string, registered []string) (string, string, error) {
	requested := strings.TrimSpace(requestedRoot)
	if requested == "" {
		if wr := strings.TrimSpace(callerRoot); wr != "" && !sameDesktopPath(wr, globalWorkspaceRoot()) {
			return "project", wr, nil
		}
		return "global", "", nil
	}
	if sameDesktopPath(requested, globalWorkspaceRoot()) {
		return "", "", fmt.Errorf("project %q is the global workspace, not a project — omit `project` to create the session in Global", requested)
	}
	for _, root := range registered {
		if sameDesktopPath(root, requested) {
			return "project", root, nil
		}
	}
	if len(registered) == 0 {
		return "", "", fmt.Errorf("project %q is not a registered project and this desktop has no projects yet — add the project first, or omit `project` to create the session in the calling session's scope", requested)
	}
	// The next step has to exist: these are the roots the caller may pass.
	// Bounded so a large list cannot flood the tool result.
	const maxListed = 10
	listed := registered
	suffix := ""
	if len(listed) > maxListed {
		listed = listed[:maxListed]
		suffix = fmt.Sprintf(" (and %d more)", len(registered)-maxListed)
	}
	return "", "", fmt.Errorf("project %q is not a registered project — pass one of: %s%s, or omit `project` to create the session in the calling session's scope",
		requested, strings.Join(listed, ", "), suffix)
}

// deleteCollabSession is the host capability behind delete_session (154-A).
//
// dryRun=true: inspect only — fill the impact report from the live tab map and
// return without touching the file. A dry run MUST NOT delete (audit F154-2: a
// prior version called the trashing path unconditionally, so "just looking"
// would move the session to trash and then claim dry_run).
//
// dryRun=false: release this process's own runtime bindings first — the same
// sequence the desktop Delete uses — so the removal guard does not refuse a
// lease we ourselves hold. Then trash, and re-emit a tree change so the
// directory and sidebar drop it immediately.
//
// The trash is manual-restore: the desktop has no automatic 30-day purge.
func (a *App) deleteCollabSession(contactID, sessionPath string, dryRun bool) (agent.DeleteSessionImpact, agent.DeleteSessionResult, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return agent.DeleteSessionImpact{}, agent.DeleteSessionResult{}, fmt.Errorf("session path is required")
	}
	dir := sessionDirectoryForPath(sessionPath)
	if dir == "" {
		return agent.DeleteSessionImpact{}, agent.DeleteSessionResult{}, fmt.Errorf("cannot determine the session directory for %q", sessionPath)
	}

	// Impact is computed from live state, independent of dryRun, so the dry run
	// and the real delete report the same truth.
	//
	// Task 158.D: Title comes from the SAME source the contact directory uses
	// (branch meta custom/topic title, else the file stem). The dry run used to
	// leave it empty, so the caller could not tell which conversation it was
	// about to trash — and it matched nothing a user would recognise.
	impact := agent.DeleteSessionImpact{
		ContactID:   contactID,
		SessionPath: sessionPath,
		Title:       agent.SessionDirectoryTitle(sessionPath),
	}
	a.mu.Lock()
	for _, tab := range a.tabs {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(tab.Ctrl.SessionPath()), sessionPath) {
			continue
		}
		impact.OpenTab = true
		if status := tab.Ctrl.RuntimeStatus(); status.Running {
			impact.HasTurn = true
			impact.TurnInFlight = true
		}
		break
	}
	a.mu.Unlock()
	// Task 158.D: no live tab does NOT mean "nothing here". The transcript is
	// read the same way read_session_tail reads it — from the file — so a
	// session that already ran a turn (the reported case: 45s of work) is never
	// reported as empty just because nothing is bound to it right now.
	if !impact.HasTurn && agent.SessionTranscriptHasContent(sessionPath) {
		impact.HasTurn = true
	}

	if dryRun {
		return impact, agent.DeleteSessionResult{}, nil
	}

	// Real delete: release this process's own bindings first, mirroring the
	// desktop Delete (app.go:deleteSession). Without this the removal guard
	// sees a lease we hold and refuses with "in use by another Reasonix window"
	// — and the impact report never comes back. The helper also handles the
	// teardown timeout and finishDestroyHandles (audit §T2-a).
	if err := a.releaseSessionRuntimeForDelete(dir, sessionPath); err != nil {
		return impact, agent.DeleteSessionResult{}, err
	}

	a.removeSessionCatalogPath(sessionPath, "session_deleted")
	a.emitProjectTreeChanged()
	return impact, agent.DeleteSessionResult{
		ContactID:    contactID,
		SessionPath:  sessionPath,
		Trashed:      true,
		RestoreUntil: "manual — restore from the Trash page",
	}, nil
}

// releaseSessionRuntimeForDelete tears down this process's own controller and
// handles for a session, then trashes it. It mirrors the subset of
// app.deleteSession that the collab path needs — the desktop sequence holds
// both locks across the whole teardown + trash, and it MUST call
// finishDestroyHandles: Finish() clears the jobs manager's destroying[stem]
// marker, and that marker is what suppresses background-job notifications for
// the session. Leaving it set means a session restored from trash has its
// future progress events permanently swallowed (audit §T2-a).
func (a *App) releaseSessionRuntimeForDelete(dir, sessionPath string) error {
	release := a.lockRuntimeMutation("delete-collab-session")
	defer release()
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	removed, _ := a.removeSessionRuntimeBindings(dir, sessionPath)
	if err := a.prepareRemovedSessionRuntimes(removed); err != nil {
		a.closeRemainingRemovedSessionRuntimesAdmissionHeld(removed, map[control.SessionAPI]bool{})
		return err
	}
	destroys := a.destroyHandlesForSession(dir, sessionPath, removed)
	timedOut := waitDestroyHandles(destroys)
	a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, map[control.SessionAPI]bool{})
	if timedOut {
		// A slow teardown must not block the caller forever, but the file must
		// not be trashed out from under a still-live job either. Mark cleanup
		// pending and let the same delayed trash the desktop uses finish it.
		if err := agent.MarkCleanupPending(sessionPath, "delete"); err != nil {
			a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, map[control.SessionAPI]bool{})
			return err
		}
		key := filepath.Base(sessionPath)
		go delayedDesktopSessionTrash(dir, sessionPath, key, destroys)
		return nil
	}
	err := trashSessionArtifacts(dir, sessionPath, filepath.Base(sessionPath))
	finishDestroyHandles(destroys)
	if err != nil {
		a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, map[control.SessionAPI]bool{})
		return err
	}
	a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, map[control.SessionAPI]bool{})
	return nil
}

// applyPendingPurposes stamps purpose + contact_id onto sessions whose topic
// was created with a purpose but had no transcript yet. This is the second half
// of "create a session in a group": the first half cannot know the file path.
func (p *sessionCollabPump) applyPendingPurposes() {
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return
	}
	store := sessioncollab.NewPendingPurposeStore(mailDir)
	pending := store.List()
	if len(pending) == 0 {
		return
	}
	a := p.app
	a.mu.Lock()
	type topicTab struct {
		topicID string
		path    string
	}
	var candidates []topicTab
	for _, tab := range a.tabs {
		if tab == nil || tab.removed || tab.Ctrl == nil {
			continue
		}
		topicID := strings.TrimSpace(tab.TopicID)
		path := strings.TrimSpace(tab.Ctrl.SessionPath())
		if topicID == "" || path == "" {
			continue
		}
		candidates = append(candidates, topicTab{topicID: topicID, path: path})
	}
	a.mu.Unlock()

	for _, c := range candidates {
		purpose, ok := pending[c.topicID]
		if !ok {
			continue
		}
		if _, err := agent.SetSessionPurpose(c.path, purpose); err != nil {
			log.Printf("[session-collab] stamp purpose on %s: %v", c.path, err)
			continue
		}
		if err := store.Clear(c.topicID); err != nil {
			log.Printf("[session-collab] clear pending purpose %s: %v", c.topicID, err)
		}
	}
}

func (p *sessionCollabPump) drain(respectBackoff bool) SessionCollabDrainResult {
	p.applyPendingPurposes()
	// Task 224 redo (user ruling): when experimental_collab_background_delivery
	// is on, the host pump does NOT stand up sessions or deliver — no new tab
	// ever appears in the tab bar. Messages stay in MailStore and are consumed
	// exclusively by the agent's drain_inbox tool. Visibility: mailbox only
	// (drain_inbox / get_session_status unreadInbox). Backpressure: none needed
	// — the pump skips entirely, so there is no deliverOne retry loop.
	if collabBackgroundDelivery() {
		return SessionCollabDrainResult{}
	}
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return SessionCollabDrainResult{}
	}
	mail := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit())
	pendingContacts := mail.PendingContacts()

	// Deliverability is not "is there a visible tab": a session whose runtime is
	// alive with no tab (detached) can take work exactly like an open one, and a
	// session with neither is opened so the work can land. Only if the open fails
	// does the message wait — never the other way round.
	covered := map[string]bool{}
	var result SessionCollabDrainResult
	now := time.Now()

	for _, target := range p.app.sessionCollabLiveTargets(pendingContacts) {
		covered[target.contactID] = true
		if respectBackoff && !p.contactRetryDue(target.contactID, now) {
			continue
		}
		delivered, refused, err := p.deliverToTarget(target)
		if err != nil {
			// Task 485 P2: a failed pass used to retry every pump tick (4-5s),
			// each attempt logging a WARN — the 2026-10-05 lease leak turned
			// that into 568 refused lines in half an hour. Back the contact
			// off (doubling, capped); a settled delivery resets it.
			p.deferContactRetry(target.contactID, now)
		} else {
			p.resetContactRetry(target.contactID)
		}
		result.Delivered += delivered
		result.Refused += refused
		if delivered == 0 && refused == 0 && err == nil {
			continue
		}
		entry := SessionCollabDrainTarget{
			TabID:     target.tabID,
			ContactID: target.contactID,
			Delivered: delivered,
			Refused:   refused,
		}
		if target.detached {
			entry.TabID = target.tabID + " (detached)"
		}
		if err != nil {
			entry.Error = err.Error()
			log.Printf("[session-collab] target %s contact %s: %v", entry.TabID, target.contactID, err)
		}
		result.Targets = append(result.Targets, entry)
	}

	// Nothing running for this contact: stand the session up so the next pass
	// can deliver. An open failure is logged, not acked, so the message stays.
	roster := p.app.collabRosterIndex()
	for _, contact := range pendingContacts {
		if covered[contact] {
			continue
		}
		if respectBackoff && !p.contactRetryDue(contact, now) {
			continue
		}
		id, ok := roster[contact]
		if !ok || id.Archived || strings.TrimSpace(id.SessionPath) == "" {
			continue
		}
		// Open the session so the next pass can deliver. When
		// experimental_collab_background_delivery is on, drain() already
		// returned early above — this path only runs with the switch off.
		// Task 264: with session_collab_background on, the stand-up builds a
		// DETACHED runtime (no tab in the bar — the user's final ruling);
		// delivery semantics are unchanged, the next pass lands through the
		// detached branch. Off keeps the baseline: open + auto-activate.
		open := p.app.OpenTopicSession
		if collabOpenDetached() {
			open = p.app.OpenTopicSessionDetached
		}
		if _, err := open(id.Scope, id.Workspace, id.TopicID, id.SessionPath); err != nil {
			// codeql[go/clear-text-logging] the flagged chain only carries the
			// provider env-var NAME from config validation errors, never the
			// key value; RedactError also strips any provider-echoed key text.
			p.deferContactRetry(contact, now)
			log.Printf("[session-collab] cannot open session for contact %s (%s): %v", contact, id.Title, secrets.RedactError(err))
			continue
		}
		log.Printf("[session-collab] opened session %q to accept a message for contact %s (background=%v)", id.Title, contact, collabOpenDetached())
	}
	return result
}

// sessionCollabTarget is one place mail can land: a visible tab, or a detached
// runtime that outlived its tab. Reasonix's model is that closing a tab does not
// necessarily stop the work — detachedSessions holds exactly those runtimes, so
// collaboration must be able to reach them without opening a tab.
type sessionCollabTarget struct {
	tabID     string
	contactID string
	// detached marks a target with no visible tab: the controller is reached
	// through the runtime, not through inboxCtrl(tabID).
	detached bool
	// activeTab marks the currently focused tab. A followup queued into it is
	// invisible in the transcript until the turn finishes, so delivery prefers
	// a mid-turn steer.
	activeTab bool
	ctrl      control.SessionAPI
}

// collabDelivery is the injectable seam for one tab's delivery pass, so the
// loss path (a failing target) is testable without a live desktop.
type collabDelivery struct {
	// enqueue hands one rendered message to the target. steered reports whether
	// a steer actually injected; err means the target could not take it.
	enqueue func(msg sessioncollab.MailMessage, body string) (steered bool, err error)
	// notify writes a status note back to the sender (once per key).
	notify func(msg sessioncollab.MailMessage, kind, text string)
	// deriveHop returns the chain depth derived from the thread, or an error
	// when provenance cannot be verified.
	deriveHop func(msg sessioncollab.MailMessage) (int, error)
	// onDelivered (task 225) registers the task-source grant for a settled
	// delivery: target ← sender, so the target's approvals can cascade back.
	onDelivered func(msg sessioncollab.MailMessage)
	// render builds the text the target reads.
	render func(msg sessioncollab.MailMessage, effectiveHop int) string
}

// deliverToTarget hands every pending message to the target and acks only what
// is settled. A delivery failure is NOT acked, so the next pass retries it, and
// the sender is told once. Acking a message before it is in the target's inbox
// would make delivery at-most-once — the failure mode where a user's task
// disappears with nothing but a local log line.
func (p *sessionCollabPump) deliverToTarget(target sessionCollabTarget) (delivered, refused int, err error) {
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return 0, 0, nil
	}
	mail := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit())
	return runCollabDelivery(mail, target.contactID, collabDelivery{
		enqueue: func(msg sessioncollab.MailMessage, body string) (bool, error) {
			return p.deliverOne(target, msg, body)
		},
		notify:    p.notifySenderOnce,
		deriveHop: p.verifyHop,
		render:    sessionCollabDeliveryText,
		// Task 225: a settled delivery binds the target's approval prompts to
		// this sender for the grant window — the task-source registration the
		// cascade delegate resolves later.
		onDelivered: func(msg sessioncollab.MailMessage) { registerCascadeGrant(msg.To, msg.From) },
	})
}

// runCollabDelivery is the whole delivery contract in one place: claim without
// consuming, derive the hop, hand over, ack exactly what settled, and report
// anything that did not. Every exit path either acks a message or leaves it for
// the next pass — there is no branch that silently drops it.
func runCollabDelivery(mail *sessioncollab.MailStore, contactID string, d collabDelivery) (delivered, refused int, err error) {
	pending, rejected, claimErr := mail.Claim(context.Background(), contactID)
	if claimErr != nil {
		return 0, 0, claimErr
	}

	var acked []string
	var firstErr error
	note := func(e error) {
		if firstErr == nil {
			firstErr = e
		}
	}

	for _, msg := range rejected {
		// Hop exhausted: the sender must learn why, and the message is settled.
		d.notify(msg, "refused_hop", sessionCollabRefusedHopText(msg))
		acked = append(acked, msg.ID)
		refused++
	}

	for _, msg := range pending {
		effectiveHop, verr := d.deriveHop(msg)
		if verr != nil {
			// Task 213: the refusal must name its real cause. An exhausted
			// chain is not a provenance failure, and a cross-wired thread is
			// not the same mistake as an unknown one — one catch-all text sent
			// senders hunting for a threadId bug that does not exist.
			kind, text := sessionCollabRefusalText(msg, verr)
			d.notify(msg, kind, text)
			acked = append(acked, msg.ID)
			refused++
			continue
		}
		steered, derr := d.enqueue(msg, d.render(msg, effectiveHop))
		if derr != nil {
			// 判定放决策点：既识别 deliverOne 包装后的 sentinel，也兜住任何
			// 直接漏出的原始冲突（生产两条路径同语义，测试夹具亦可直注）。
			if errors.Is(derr, errCollabDuplicateDelivery) || errors.Is(derr, sessioninbox.ErrIdempotencyConflict) {
				// 任务461 P10①：重发的同内容已在目标收件箱——静默结算（ack），
				// 不给发送方任何「失败」通知：通知会喂养重试循环（实测每
				// 5-6s 一次 conflict 重投风暴）。
				acked = append(acked, msg.ID)
				delivered++
				continue
			}
			// Not acked: retried next pass. The sender hears about it once.
			d.notify(msg, "delivery_failed", sessionCollabDeliveryFailedText(msg, derr))
			note(derr)
			continue
		}
		if !steered && msg.Delivery == string(sessioncollab.DeliverySteer) {
			d.notify(msg, "steer_degraded", sessionCollabSteerDegradedText(msg))
		}
		acked = append(acked, msg.ID)
		delivered++
		// Task 225: a settled delivery binds the target's approvals to this
		// sender for the grant window.
		if d.onDelivered != nil {
			d.onDelivered(msg)
		}
	}

	if aerr := mail.Ack(context.Background(), contactID, acked...); aerr != nil {
		note(aerr)
	}
	return delivered, refused, firstErr
}

// verifyHop derives the chain depth from the thread the message answers instead
// of trusting the number the sender typed. A reply that names a parent thread
// gets parent.Hop+1 — mechanically, so a relay cannot declare itself a first
// hop. A message that claims to be a reply but carries no resolvable parent is
// refused, because its depth is unverifiable.
//
// Residual, stated plainly: a sender that fabricates a brand-new thread (no
// parent) legitimately starts a new chain at hop 0. That is a new conversation,
// not a hidden relay, but it does mean the ceiling bounds *threaded* chains
// rather than every possible message.
func (p *sessionCollabPump) verifyHop(msg sessioncollab.MailMessage) (int, error) {
	isReply := msg.ThreadID != "" && msg.ThreadID != msg.ID
	if !isReply {
		if msg.Hop > 0 {
			// Claiming depth without a parent to derive it from.
			return 0, fmt.Errorf("hop=%d claimed but threadId does not name a parent", msg.Hop)
		}
		return 0, nil
	}
	if strings.TrimSpace(msg.From) == "" {
		return 0, fmt.Errorf("reply has no sender to resolve thread %s", msg.ThreadID)
	}
	mailDir := config.SessionCollabMailDir()
	// Task 194 + 156.B: the predicates (thread must live in the sender's own mailbox,
	// and must have been opened by the peer being answered) live in sessioncollab so the
	// tool layer and this pump cannot drift apart.
	parent, _, perr := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit()).ResolveReplyParent(msg)
	if perr != nil {
		return 0, perr
	}
	derived := parent.Hop + 1
	if derived > sessionCollabHopLimit() {
		return derived, errSessionCollabHopExhausted
	}
	return derived, nil
}

var errSessionCollabHopExhausted = errors.New("collaboration chain hop limit reached")

// notifySenderOnce writes a status note back to the sender's mailbox at most
// once per (message, kind), so a target that stays unavailable for hours does
// not turn into a mail flood while still never being silent.
func (p *sessionCollabPump) notifySenderOnce(msg sessioncollab.MailMessage, kind, note string) {
	if strings.TrimSpace(msg.From) == "" {
		return
	}
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return
	}
	store := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit())
	if !store.MarkNotified(msg.From, msg.ID+":"+kind) {
		log.Printf("[session-collab] %s notice for %s already sent", kind, msg.ID)
		return
	}
	log.Printf("[session-collab] %s for message %s from %s", kind, msg.ID, msg.From)
	// 任务548 P0-2（死信可达）：状态通知必须走「新链形态」——不拷回原消息的
	// Hop/ThreadID。历史形态把 Hop+ThreadID 原样拷回且 From 留空，通知自身在
	// 发送方的泵会被 verifyHop 二次拒收（threadId 非空 ⇒ 视为 reply，而 reply
	// 无 From ⇒ "reply has no sender"；threadId 为空而 hop>0 ⇒ "claimed but
	// no parent"）——2026-10-06 事故里四封拒收通知全数如此，派活方对「消息
	// 被拒」永久失聪。新链形态（Hop=0、threadId 留空、From=目标 contact）
	// isReply=false 且 hop=0，必过溯源门；原 messageId 与拒因已在正文里。
	// 代价：同步 wait 不再按 threadId 提前命中状态通知，会等满超时后在本通知
	// 落邮箱时读到——换来的是死信信号在一切拒收场景下可达。
	if _, err := store.Deliver(context.Background(), sessioncollab.MailMessage{
		From:    msg.To, // 目标会话（泵所在方）是通知的真实来源
		To:      msg.From,
		Body:    note,
		Hop:     0,
		ReplyTo: "",
		Kind:    "system", // 任务461 P8 ②：平台状态通知自动已读，不顶未读数
	}); err != nil {
		log.Printf("[session-collab] %s notice to %s failed: %v", kind, msg.From, err)
	}
}

func sessionCollabRefusedHopText(msg sessioncollab.MailMessage) string {
	return "跨会话消息被拒绝并丢弃：协作链已达 hop 上限（" + strconv.Itoa(sessionCollabHopLimit()) +
		"）。如需继续，请新起一条链，不要在上一条链上继续接力。（messageId=" + msg.ID + "）"
}

// sessionCollabRefusalText classifies a deriveHop failure (task 213) so each
// refusal names its real cause and points at a next step that exists. The
// exhausted-chain sentinel — whether the pump's own or the store's — is a hop
// refusal, a cross-wired thread is its own kind, and everything else (unknown
// thread id, missing sender, a claimed depth with no parent) stays a
// provenance failure.
func sessionCollabRefusalText(msg sessioncollab.MailMessage, cause error) (string, string) {
	switch {
	case errors.Is(cause, errSessionCollabHopExhausted), errors.Is(cause, sessioncollab.ErrHopLimit):
		return "refused_hop", sessionCollabRefusedHopText(msg)
	case errors.Is(cause, sessioncollab.ErrReplyThreadCrossWired):
		return "refused_cross_wire", sessionCollabCrossWiredText(msg, cause)
	default:
		return "refused_provenance", sessionCollabBadProvenanceText(msg, cause)
	}
}

// sessionCollabCrossWiredText is distinct from the provenance text on purpose:
// the sender did resolve a thread, but it belongs to another peer, so "pass
// the id you received" alone would not tell them which of their ids is wrong.
func sessionCollabCrossWiredText(msg sessioncollab.MailMessage, cause error) string {
	return "跨会话消息被拒绝并丢弃：它的 threadId 串线到另一条链（" + cause.Error() +
		"）。请改用本次收到的那条入向消息的 threadId 回信，或省略 threadId 另起新链，不要复用其他对端的链。（messageId=" + msg.ID + "）"
}

func sessionCollabBadProvenanceText(msg sessioncollab.MailMessage, cause error) string {
	return "跨会话消息被拒绝并丢弃：无法核实它的链路来源（" + cause.Error() +
		"）。当你回复某条消息时，请把收到的 threadId 原样传回。（messageId=" + msg.ID + "）"
}

func sessionCollabDeliveryFailedText(msg sessioncollab.MailMessage, cause error) string {
	return "跨会话消息投递失败，将自动重试：目标会话当前不可用（" + cause.Error() +
		"）。消息仍在队列中，不会丢失。（messageId=" + msg.ID + "）"
}

// errCollabDuplicateDelivery marks an admission the target inbox already
// holds under the same content key — a resend (任务461 P10①). From the
// sender's perspective it is SUCCESS: nothing new is added and nothing failed.
var errCollabDuplicateDelivery = errors.New("collab mail resend: target inbox already holds identical content (idempotent duplicate)")

// collabAdmissionErr classifies one enqueue outcome. An idempotency conflict
// under the pump's content key means the target inbox ALREADY admitted this
// content — the stored envelope differs only by per-copy metadata (the fresh
// msg id of the resend), not by intent. Treating it as a failure made the
// pump leave the mail unacked (re-claimed every pass) and sent the sender a
// "投递失败" note whose retry fed the loop — the 5-6s conflict storm of
// 任务461 P10. A duplicate is settled silently instead.
func collabAdmissionErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sessioninbox.ErrIdempotencyConflict) {
		log.Printf("[session-collab] duplicate delivery deduped at inbox admission (idempotency conflict treated as already-delivered)")
		return errCollabDuplicateDelivery
	}
	return err
}

// deliverOne hands one message to the target. It reports whether a steer
// actually injected, so the caller can tell the sender when it degraded. A
// detached target is reached through its controller — there is no visible tab
// to address by id.
func (p *sessionCollabPump) deliverOne(target sessionCollabTarget, msg sessioncollab.MailMessage, body string) (bool, error) {
	// Task 309: the idempotency key defaults to content-based (from, thread,
	// body) — a retried send mints a fresh msg.ID but must dedup onto the
	// original delivery, and the pump's own redelivery of the same mail lands
	// on the same key too. The store still rejects differing content under
	// one key. [agent] session_collab_mail_idempotent_default=false restores
	// the per-message key (no content dedup).
	idem := "collab:" + msg.ID
	if config.SessionCollabMailIdempotentEnabled() {
		sum := sha256.Sum256([]byte(msg.From + "\x00" + msg.ThreadID + "\x00" + strings.TrimSpace(msg.Body)))
		idem = "collab:" + msg.From + ":" + msg.ThreadID + ":" + hex.EncodeToString(sum[:8])
	}
	// Task 221: stamp the sender into the envelope Source so the drain-time
	// merge can group by sender structurally ("collab:<contactID>") instead of
	// parsing the rendered header text.
	source := "collab:" + msg.From
	if target.detached {
		if target.ctrl == nil {
			return false, fmt.Errorf("detached runtime has no controller")
		}
		if msg.Delivery != string(sessioncollab.DeliverySteer) {
			_, err := p.app.enqueueInboxWithControllerSource(target.tabID, target.ctrl, sessioninbox.IntentFollowup, body, body, nil, idem, false, "", "", source, msg.ReceiptRequested, msg.ID, msg.To)
			return false, collabAdmissionErr(err)
		}
		receipt, err := p.app.enqueueInboxWithControllerSource(target.tabID, target.ctrl, sessioninbox.IntentSteer, body, body, nil, idem, true, "", "", source, msg.ReceiptRequested, msg.ID, msg.To)
		if err != nil {
			return false, collabAdmissionErr(err)
		}
		steered := sessionCollabReceiptSteered(receipt.Disposition)
		if !steered {
			log.Printf("[session-collab] steer degraded to follow-up for %s (disposition=%s)", msg.To, receipt.Disposition)
		}
		return steered, nil
	}
	// A followup queued into a tab that is already running a turn is invisible
	// in the transcript until that turn finishes — and the user sees nothing.
	// When the target is the currently active tab, steer it mid-turn so the
	// message is rendered immediately. The existing steer path already falls
	// back to a queued follow-up when the target cannot take a steer, so this
	// degrades safely.
	ctrl, ctrlErr := p.app.inboxCtrl(target.tabID)
	if ctrlErr != nil {
		return false, ctrlErr
	}
	if msg.Delivery != string(sessioncollab.DeliverySteer) && !target.activeTab {
		_, err := p.app.enqueueInboxWithControllerSource(target.tabID, ctrl, sessioninbox.IntentFollowup, body, body, nil, idem, false, "", "", source, msg.ReceiptRequested, msg.ID, msg.To)
		return false, collabAdmissionErr(err)
	}
	receipt, err := p.app.enqueueInboxWithControllerSource(target.tabID, ctrl, sessioninbox.IntentSteer, body, body, nil, idem, true, "", "", source, msg.ReceiptRequested, msg.ID, msg.To)
	if err != nil {
		// Steer rejected: fall back to a queued follow-up so the message is not lost.
		if msg.Delivery != string(sessioncollab.DeliverySteer) {
			_, ferr := p.app.enqueueInboxWithControllerSource(target.tabID, ctrl, sessioninbox.IntentFollowup, body, body, nil, idem, false, "", "", source, msg.ReceiptRequested, msg.ID, msg.To)
			if ferr != nil {
				return false, collabAdmissionErr(ferr)
			}
			return false, nil
		}
		return false, collabAdmissionErr(err)
	}
	steered := sessionCollabReceiptSteered(receipt.Disposition)
	if !steered {
		log.Printf("[session-collab] steer degraded to follow-up for %s (disposition=%s)", msg.To, receipt.Disposition)
	}
	return steered, nil
}

func sessionCollabSteerDegradedText(msg sessioncollab.MailMessage) string {
	return "你发送的 steer 未能注入目标会话当轮（目标不可注入），已自动降级为排队 follow-up，目标会在下一轮处理。（messageId=" + msg.ID + "）"
}

// sessionCollabReceiptSteered reports whether the admission actually injected
// mid-turn guidance rather than queueing it for later.
func sessionCollabReceiptSteered(disposition string) bool {
	switch disposition {
	case "started", "steer_accepted":
		return true
	default:
		return false
	}
}

// notifyDegradedSteer writes a status note back to the sender's mailbox. It
// is best-effort: a failure here must not drop the message that was already
// delivered.
//
// 当前无调用点（降级通知实际经 collabDelivery.notify → notifySenderOnce 发出，
// 同为新链形态）；保留实现并同修为 Hop=0 新链形态（任务548 P0-2）：拷回原
// 消息的 hop 会让通知在发送方的泵被溯源门拒收（hop>0 且无父 threadId），
// 「降级」信号对发送方不可达。
func (p *sessionCollabPump) notifyDegradedSteer(msg sessioncollab.MailMessage, disposition string) {
	if strings.TrimSpace(msg.From) == "" {
		return
	}
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return
	}
	note := "你发送的 steer 未能注入目标会话当轮（目标不可注入，disposition=" + disposition +
		"），已自动降级为排队 follow-up，目标会在下一轮处理。"
	if _, err := sessioncollab.NewMailStoreWithHopLimit(mailDir, sessionCollabHopLimit()).Deliver(context.Background(), sessioncollab.MailMessage{
		From:    msg.To,
		To:      msg.From,
		Body:    note,
		Hop:     0,
		ReplyTo: "",
		Kind:    "system", // 任务461 P8 ②：平台状态通知自动已读，不顶未读数
	}); err != nil {
		log.Printf("[session-collab] degraded-steer notice to %s failed: %v", msg.From, err)
	}
}

// sessionCollabDeliveryText is what the target session actually reads. It has
// to carry the reply address and the thread id, or an async exchange cannot
// close its loop and a synchronous sender cannot match its answer.
// effectiveHop is the depth derived from the thread, not the sender's claim.
//
// Session titles change automatically (auto-title), so the recipient must be
// able to verify the message landed correctly by ID, not by title. Both sides
// are always included so the receiving session can confirm it is the intended
// target and the sender's identity is unambiguous.
func sessionCollabDeliveryText(msg sessioncollab.MailMessage, effectiveHop int) string {
	var b strings.Builder
	b.WriteString("[跨会话消息]")
	// Both sides are always printed, even when empty: a silent omission of From
	// loses the sender identity and, with it, the reply address (the ReplyTo is
	// the same value). The recipient must be able to see the shape of the
	// conversation, not infer it from absence.
	fromLabel := msg.From
	if fromLabel == "" {
		fromLabel = "(未登记)"
	}
	toLabel := msg.To
	if toLabel == "" {
		toLabel = "(未知)"
	}
	b.WriteString(" 来自 contact_id=")
	b.WriteString(fromLabel)
	b.WriteString(" → 发至 contact_id=")
	b.WriteString(toLabel)
	if effectiveHop > 0 {
		b.WriteString(" (hop=")
		b.WriteString(strconv.Itoa(effectiveHop))
		b.WriteString(")")
	}
	b.WriteString("\n\n")
	b.WriteString(msg.Body)
	b.WriteString("\n\n---\n")
	if msg.CardID != "" {
		b.WriteString("关联任务卡片：" + msg.CardID + "\n")
	}
	if msg.Hop > 0 || msg.From != "" {
		if msg.ID != "" {
			b.WriteString("会话线程：threadId=" + msg.ID + "\n")
		}
	}
	if msg.ReplyTo != "" {
		b.WriteString("回复方式：完成后用 talk_to_session 回信到 contact_id=" + msg.ReplyTo +
			"，hop 传 " + strconv.Itoa(effectiveHop+1))
		if msg.ID != "" {
			b.WriteString("，并把 thread_id 设为 " + msg.ID + "（发起方可能正在同步等待；hop 由系统按 thread 派生核对，自报值无效）")
		}
		b.WriteString("。")
		// Task 173: a demanded reply is stated as a requirement, not a hint —
		// 156.D's text guidance stays for optional replies.
		if msg.RequireReply {
			b.WriteString("\n⚠ 发件人要求回信（require_reply）：完成本信的工作后，必须按上面的回复方式回信；无法完成也请回信说明，不要只在本会话里写下结论。")
		}
	} else if msg.From != "" {
		// Task 487: an empty ReplyTo with a registered From is a system
		// message (the task-309 read receipt looks exactly like this), not an
		// unregistered sender. The old catch-all called the sender "未登记"
		// right under a header that printed its contact_id, and sent the
		// recipient chasing a remediation that had already happened. The
		// sender IS addressable: fall back to From as the reply address.
		b.WriteString("回复方式：完成后用 talk_to_session 回信到 contact_id=" + msg.From +
			"，hop 传 " + strconv.Itoa(effectiveHop+1) +
			"。（发送方未指定专用回信地址，回信到其登记的 contact_id 即可。）")
	} else {
		// Task 156.D: the old wording ("请先让发送方登记") told the *recipient*
		// to fix something only the sender can do. State the fact and what the
		// recipient can actually do. Task 487: this branch is now reachable
		// only when From is empty too, so 「未登记」 is finally true here.
		b.WriteString("这是一条单向通知：发送方未登记 contact_id，本消息无法回信。如需联系发送方，请在发送方所在会话中让它先调用一次 talk_to_session（首次发信会自动登记身份）。")
	}
	return b.String()
}

// AddressableSessionView is one session in the contact directory (task 141).
// ContactID is the stable address (empty until first contact); TopicID and the
// path are shown so a human can recognise the row, and Purpose is optional —
// the title carries the meaning when no duty has been registered.
type AddressableSessionView struct {
	ContactID   string `json:"contactId"`
	Purpose     string `json:"purpose,omitempty"`
	Title       string `json:"title,omitempty"`
	TopicID     string `json:"topicId,omitempty"`
	SessionPath string `json:"sessionPath"`
	Scope       string `json:"scope,omitempty"`
	Workspace   string `json:"workspaceRoot,omitempty"`
	Open        bool   `json:"open"`
	Archived    bool   `json:"archived"`
}

// collabRoster enumerates every session on this machine — global, every project
// dir, and the archive. Purpose is optional metadata; every conversation belongs
// in the directory, the same way MiMo's session-chat lists every conversation.
func (a *App) collabRoster() []sessioncollab.Identity {
	load := func(sessionPath string) (contact, purpose, topic, title string, ok bool) {
		m, found, err := agent.LoadBranchMeta(sessionPath)
		if err != nil {
			return "", "", "", "", false
		}
		// One title source for the whole collaboration surface (task 158.D):
		// the same helper the agent-side directory and the delete dry run use.
		title = agent.SessionDirectoryTitle(sessionPath)
		if found {
			contact = m.ContactID
			purpose = m.Purpose
			topic = m.TopicID
		}
		// Include sessions with no sidecar yet: they are conversations too.
		return contact, purpose, topic, title, true
	}
	var out []sessioncollab.Identity
	seen := map[string]bool{}
	add := func(dir, workspace, scope string, archived bool) {
		for _, id := range sessioncollab.ScanDir(dir, workspace, load) {
			key := strings.ToLower(id.SessionPath)
			if seen[key] {
				continue
			}
			seen[key] = true
			id.Scope = scope
			id.Archived = archived
			out = append(out, id)
		}
	}
	add(config.SessionDir(), "", "global", false)
	for _, dir := range config.AllProjectSessionDirs() {
		add(dir, projectRootForSessionDir(dir), "project", false)
	}
	add(config.ArchiveDir(), "", "global", true)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// projectRootForSessionDir recovers the project root from a
// <state>/projects/<slug>/sessions layout so a scanned session can name the
// workspace it belongs to. Empty when the layout is unexpected.
func projectRootForSessionDir(sessionsDir string) string {
	// sessions dir sits two levels under the project slug directory; the slug is
	// not the original root, but OpenTopicSession only needs a consistent scope +
	// root pair, and the identity already carries the exact session path.
	return filepath.Dir(filepath.Dir(sessionsDir))
}

// ListAddressableSessions enumerates the contact directory for the desktop
// surface. Renaming a topic never changes ContactID, so a reference taken before
// the rename still resolves; sessions without a contact_id yet are still listed
// and can be addressed by topic_id or title.
func (a *App) ListAddressableSessions() []AddressableSessionView {
	open := map[string]bool{}
	for _, target := range a.sessionCollabTargets() {
		open[target.contactID] = true
	}
	out := make([]AddressableSessionView, 0)
	for _, id := range a.collabRoster() {
		out = append(out, AddressableSessionView{
			ContactID:   id.ContactID,
			Purpose:     id.Purpose,
			Title:       id.Title,
			TopicID:     id.TopicID,
			SessionPath: id.SessionPath,
			Scope:       id.Scope,
			Workspace:   id.Workspace,
			Open:        id.ContactID != "" && open[id.ContactID],
			Archived:    id.Archived,
		})
	}
	return out
}

// knownProjectRoots lists configured project roots, newest-agnostic order.
func (a *App) knownProjectRoots() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	var roots []string
	for _, tab := range a.tabs {
		if tab == nil || tab.Scope != "project" {
			continue
		}
		root := strings.TrimSpace(tab.WorkspaceRoot)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

// collabSessionStatus answers the in-process running/idle truth for one
// contact (task 218). Only controllers this App owns are visible; anything
// else — another process, a runtime that was never stood up — is unknown,
// because a guessed idle would invite double-dispatch onto a busy peer. No
// turn-edge timestamp is tracked here yet, so lastTurnAtMS stays 0 and the
// tool falls back to the mailbox's own last-delivery time.
//
// pending (task 218, dispatch-round feedback) counts the session-inbox items
// queued inside the controller — a steer degraded to followup lands there,
// past the collab mailbox cursor, so the file-only counter cannot see it.
func (a *App) collabSessionStatus(contactID string) (running bool, lastTurnAtMS int64, pending int, known bool) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return false, 0, 0, false
	}
	for _, target := range a.sessionCollabLiveTargets(nil) {
		if target.contactID != contactID || target.ctrl == nil {
			continue
		}
		snap := target.ctrl.InboxSnapshot()
		for _, item := range snap.Items {
			if item.State == sessioninbox.StateQueued {
				pending++
			}
		}
		return target.ctrl.RuntimeStatus().Running, 0, pending, true
	}
	return false, 0, 0, false
}

// collabSessionInfo (task 274 ①) reports a contact's current model through
// the same live-target walk as collabSessionStatus: the model is a runtime
// fact, so a runtime this host cannot see answers known=false and the row
// simply omits the fields — never a stale guess. Only attached tabs carry a
// model (detached runtimes expose none on RuntimeStatus), which is reported
// honestly rather than fabricated. provider is the catalog prefix of the
// modelRef ("provider/model"), matching what the switcher shows.
func (a *App) collabSessionInfo(contactID string) (modelRef string, provider string, known bool) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return "", "", false
	}
	for _, target := range a.sessionCollabLiveTargets(nil) {
		if target.contactID != contactID {
			continue
		}
		if target.tabID == "" {
			return "", "", false // detached: no model surface on the runtime
		}
		tab := a.tabByID(target.tabID)
		if tab == nil {
			return "", "", false
		}
		a.mu.RLock()
		model := strings.TrimSpace(tab.model)
		a.mu.RUnlock()
		if model == "" {
			return "", "", false
		}
		prov, _, _ := strings.Cut(model, "/")
		return model, prov, true
	}
	return "", "", false
}

// collabSessionStop (task 274 ②) cancels a peer's active turn through the
// same cancel chain the session's own stop button uses. The remote-stop trace
// is written to the target's transcript BEFORE the cancel lands, so history
// records who ended the turn (the 237-family copy lives in the notice text).
// known=false: this host cannot see that runtime — the caller reports it
// instead of pretending a stop happened.
func (a *App) collabSessionStop(contactID string) (stopped, wasRunning, known bool, err error) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return false, false, false, nil
	}
	for _, target := range a.sessionCollabLiveTargets(nil) {
		if target.contactID != contactID || target.ctrl == nil {
			continue
		}
		if !target.ctrl.RuntimeStatus().Running {
			return false, false, true, nil // idle: idempotent no-op, honest receipt
		}
		target.ctrl.Notice("本会话的运行中 turn 已被发起方对话远程停止（remote stop from the parent conversation）")
		target.ctrl.Cancel()
		return true, true, true, nil
	}
	return false, false, false, nil
}

// collabSessionSetModel (task 274 ③) observes the active-turn state so the
// tool can fail closed FIRST, then routes the change through the tab's own
// switcher setter (SetModelForTab — the same build+swap path the UI uses;
// its rebuildControllerActiveWorkErrorFor guard is the second line of
// defence if a turn starts between this check and the swap).
// detached runtimes have no switcher surface: known=true, applied=false, no
// error — the tool renders the specific refusal.
func (a *App) collabSessionSetModel(contactID, model string) (applied, wasRunning, known bool, newRef string, err error) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return false, false, false, "", nil
	}
	for _, target := range a.sessionCollabLiveTargets(nil) {
		if target.contactID != contactID {
			continue
		}
		if target.ctrl == nil {
			return false, false, false, "", nil
		}
		if target.ctrl.RuntimeStatus().Running {
			// Fail closed (user hard constraint): never swap a running turn's
			// model — sampling and promptCacheKey consistency would break.
			return false, true, true, "", nil
		}
		if target.tabID == "" {
			return false, false, true, "", nil
		}
		if err := a.SetModelForTab(target.tabID, model); err != nil {
			// Task 387: actionable refusal — name the entries the target's own
			// switcher would offer (its workspace config catalog), capped so a
			// huge catalog stays a one-line error.
			if wrapped, wrapErr := a.wrapUnknownModelErr(target.tabID, model, err); wrapErr == nil {
				err = wrapped
			}
			return false, false, true, "", err
		}
		if tab := a.tabByID(target.tabID); tab != nil {
			a.mu.RLock()
			newRef = strings.TrimSpace(tab.model)
			a.mu.RUnlock()
		}
		return true, false, true, newRef, nil
	}
	return false, false, false, "", nil
}

// collabSessionTurnStatus (task 319) exposes the peer's authoritative turn
// lifecycle to the subscription verdict: a turn that reached a NON-completed
// terminal state (failed/interrupted/recovery_required) is the peer dying
// instead of finishing — the signal that a dispatched task went silent.
// known=false: runtime not visible (another process / not stood up) — the
// verdict then never fires rather than guessing a death.

// wrapUnknownModelErr (task 387): an unknown-model refusal gains the list of
// models the target workspace actually offers, so the caller can retry with a
// valid id without opening the target's switcher. Non-unknown errors (lease,
// active work, persistence) pass through untouched.
func (a *App) wrapUnknownModelErr(tabID, model string, err error) (error, error) {
	if err == nil || !strings.Contains(err.Error(), "unknown model") {
		return nil, errors.New("not-unknown")
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil, errors.New("no-tab")
	}
	cfg, cfgErr := config.LoadForRoot(tab.WorkspaceRoot)
	if cfgErr != nil || cfg == nil {
		return nil, errors.New("no-config")
	}
	names := make([]string, 0, 8)
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		for _, m := range p.Models {
			names = append(names, p.Name+"/"+m)
			if len(names) >= 8 {
				break
			}
		}
		if len(names) >= 8 {
			break
		}
	}
	if len(names) == 0 {
		return nil, errors.New("empty-catalog")
	}
	more := ""
	if len(names) >= 8 {
		more = ", …"
	}
	return fmt.Errorf("%w — available models on that session's workspace: %s%s", err, strings.Join(names, ", "), more), nil
}

func (a *App) collabSessionTurnStatus(contactID string) (status string, known bool) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return "", false
	}
	for _, target := range a.sessionCollabLiveTargets(nil) {
		if target.contactID != contactID || target.ctrl == nil {
			continue
		}
		return string(target.ctrl.RuntimeStatus().Status), true
	}
	return "", false
}

// sessionCollabLiveTargets is every place a message can land without first
// standing anything up: visible tabs bound to a session, plus detached runtimes
// whose tab was closed but whose work is still alive. Only the contacts in
// pendingContacts are considered, so an idle pass costs nothing.
func (a *App) sessionCollabLiveTargets(pendingContacts []string) []sessionCollabTarget {
	want := map[string]bool{}
	for _, c := range pendingContacts {
		want[c] = true
	}
	out := append([]sessionCollabTarget(nil), a.sessionCollabTargets()...)
	seen := map[string]bool{}
	for _, t := range out {
		seen[t.contactID] = true
	}

	a.mu.Lock()
	detached := make([]*WorkspaceTab, 0, len(a.detachedSessions))
	for _, tab := range a.detachedSessions {
		if tab != nil {
			detached = append(detached, tab)
		}
	}
	a.mu.Unlock()

	for _, tab := range detached {
		if tab.Ctrl == nil || tab.ReadOnly || tab.Takeover.Spectator {
			continue
		}
		path := strings.TrimSpace(tab.Ctrl.SessionPath())
		if path == "" {
			continue
		}
		contact := agent.SessionContactID(path)
		if contact == "" || seen[contact] {
			continue
		}
		if len(want) > 0 && !want[contact] {
			continue
		}
		seen[contact] = true
		out = append(out, sessionCollabTarget{
			tabID:     tab.ID,
			contactID: contact,
			detached:  true,
			ctrl:      tab.Ctrl,
		})
	}
	return out
}

// collabRosterIndex maps contact_id to the newest identity that owns it.
func (a *App) collabRosterIndex() map[string]sessioncollab.Identity {
	out := map[string]sessioncollab.Identity{}
	for _, id := range a.collabRoster() {
		if strings.TrimSpace(id.ContactID) == "" {
			continue
		}
		out[id.ContactID] = id
	}
	return out
}

// sessionCollabTargets snapshots open tabs that own a registered contact_id.
// Sessions that never registered one are skipped: nothing can address them yet.
func (a *App) sessionCollabTargets() []sessionCollabTarget {
	a.mu.Lock()
	tabs := make([]*WorkspaceTab, 0, len(a.tabs))
	for _, tab := range a.tabs {
		if tab != nil && !tab.removed {
			tabs = append(tabs, tab)
		}
	}
	// Snapshot under the same lock as the tabs: a concurrent close could flip
	// activeTabID between the read and the loop, and an unlocked read would be a
	// data race.
	activeTabID := a.activeTabID
	a.mu.Unlock()

	var out []sessionCollabTarget
	seen := map[string]bool{}
	for _, tab := range tabs {
		if tab.Ctrl == nil || tab.ReadOnly || tab.Takeover.Spectator {
			continue
		}
		path := strings.TrimSpace(tab.Ctrl.SessionPath())
		if path == "" {
			continue
		}
		key := strings.ToLower(path)
		if seen[key] {
			continue
		}
		contact := agent.SessionContactID(path)
		if contact == "" {
			continue
		}
		seen[key] = true
		out = append(out, sessionCollabTarget{
			tabID:     tab.ID,
			contactID: contact,
			activeTab: tab.ID == activeTabID,
		})
	}
	return out
}
