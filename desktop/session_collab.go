package main

import (
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
	"reasonix/internal/config"
	"reasonix/internal/control"
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

type sessionCollabPump struct {
	app *App

	mu      sync.Mutex
	started bool
	stop    chan struct{}
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
	go p.loop(stop)
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
func (a *App) DrainSessionCollabMail() SessionCollabDrainResult {
	if a.sessionCollab == nil {
		return SessionCollabDrainResult{}
	}
	return a.sessionCollab.drain()
}

func (p *sessionCollabPump) drainOnce() {
	result := p.drain()
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
func (a *App) createCollabSession(workspaceRoot, title, purpose, group, groupID string) (agent.CreateCollabSessionResult, error) {
	scope, root := "global", ""
	if strings.TrimSpace(workspaceRoot) != "" {
		scope, root = "project", workspaceRoot
	}
	meta, err := a.CreateTopic(scope, root, title)
	if err != nil {
		return agent.CreateCollabSessionResult{}, err
	}
	if strings.TrimSpace(group) != "" || strings.TrimSpace(groupID) != "" {
		if err := a.AddTopicToGroup(scope, root, meta.ID, groupID, group); err != nil {
			return agent.CreateCollabSessionResult{TopicID: meta.ID}, err
		}
	}

	// Create the transcript now, not on first open. The directory enumerates
	// .jsonl files, so without a file the session is invisible to
	// list_addressable_sessions and talk_to_session.
	dir := desktopSessionDir(root)
	sessionPath, ferr := createEmptySessionFile(dir, "collab")
	if ferr != nil {
		return agent.CreateCollabSessionResult{TopicID: meta.ID}, fmt.Errorf("session file for %q: %w", title, ferr)
	}
	// Stamp contact_id + purpose + topic + scope onto the branch meta so the
	// directory sees a complete record immediately. This is the "创建即注册"
	// step: no pending-purpose round-trip through the pump.
	if _, perr := agent.SetSessionPurpose(sessionPath, purpose); perr != nil {
		return agent.CreateCollabSessionResult{TopicID: meta.ID, SessionPath: sessionPath}, fmt.Errorf("register purpose for %q: %w", title, perr)
	}
	if uerr := agent.UpdateBranchMeta(sessionPath, false, func(m *agent.BranchMeta) error {
		m.TopicID = meta.ID
		m.TopicTitle = meta.Title
		m.Scope = scope
		m.WorkspaceRoot = root
		return nil
	}); uerr != nil {
		return agent.CreateCollabSessionResult{TopicID: meta.ID, SessionPath: sessionPath}, fmt.Errorf("bind topic %q to session: %w", meta.ID, uerr)
	}

	contactID := agent.SessionContactID(sessionPath)
	// A brand-new topic is a tree change; re-emit so the sidebar and session
	// catalog pick up the file we just wrote (sub-item B: no manual refresh).
	a.emitProjectTreeChanged()
	return agent.CreateCollabSessionResult{
		TopicID:     meta.ID,
		ContactID:   contactID,
		SessionPath: sessionPath,
		Purpose:     purpose,
		Group:       group,
		GroupID:     groupID,
	}, nil
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
	impact := agent.DeleteSessionImpact{
		ContactID:   contactID,
		SessionPath: sessionPath,
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
		}
		break
	}
	a.mu.Unlock()

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

func (p *sessionCollabPump) drain() SessionCollabDrainResult {
	p.applyPendingPurposes()
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return SessionCollabDrainResult{}
	}
	mail := sessioncollab.NewMailStore(mailDir)
	pendingContacts := mail.PendingContacts()

	// Deliverability is not "is there a visible tab": a session whose runtime is
	// alive with no tab (detached) can take work exactly like an open one, and a
	// session with neither is opened so the work can land. Only if the open fails
	// does the message wait — never the other way round.
	covered := map[string]bool{}
	var result SessionCollabDrainResult

	for _, target := range p.app.sessionCollabLiveTargets(pendingContacts) {
		covered[target.contactID] = true
		delivered, refused, err := p.deliverToTarget(target)
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
		id, ok := roster[contact]
		if !ok || id.Archived || strings.TrimSpace(id.SessionPath) == "" {
			continue
		}
		if _, err := p.app.OpenTopicSession(id.Scope, id.Workspace, id.TopicID, id.SessionPath); err != nil {
			log.Printf("[session-collab] cannot open session for contact %s (%s): %v", contact, id.Title, err)
			continue
		}
		log.Printf("[session-collab] opened session %q to accept a message for contact %s", id.Title, contact)
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
	mail := sessioncollab.NewMailStore(mailDir)
	return runCollabDelivery(mail, target.contactID, collabDelivery{
		enqueue: func(msg sessioncollab.MailMessage, body string) (bool, error) {
			return p.deliverOne(target, msg, body)
		},
		notify:    p.notifySenderOnce,
		deriveHop: p.verifyHop,
		render:    sessionCollabDeliveryText,
	})
}

// runCollabDelivery is the whole delivery contract in one place: claim without
// consuming, derive the hop, hand over, ack exactly what settled, and report
// anything that did not. Every exit path either acks a message or leaves it for
// the next pass — there is no branch that silently drops it.
func runCollabDelivery(mail *sessioncollab.MailStore, contactID string, d collabDelivery) (delivered, refused int, err error) {
	pending, rejected, claimErr := mail.Claim(contactID)
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
			d.notify(msg, "refused_provenance", sessionCollabBadProvenanceText(msg, verr))
			acked = append(acked, msg.ID)
			refused++
			continue
		}
		steered, derr := d.enqueue(msg, d.render(msg, effectiveHop))
		if derr != nil {
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
	}

	if aerr := mail.Ack(contactID, acked...); aerr != nil {
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
	parent, ok := sessioncollab.NewMailStore(mailDir).ParentThread(msg.From, msg.ThreadID)
	if !ok {
		return 0, fmt.Errorf("thread %s is not in sender %s's mailbox", msg.ThreadID, msg.From)
	}
	derived := parent.Hop + 1
	if derived > sessioncollab.MaxHop {
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
	store := sessioncollab.NewMailStore(mailDir)
	if !store.MarkNotified(msg.From, msg.ID+":"+kind) {
		log.Printf("[session-collab] %s notice for %s already sent", kind, msg.ID)
		return
	}
	log.Printf("[session-collab] %s for message %s from %s", kind, msg.ID, msg.From)
	// A status note answers the original thread so a synchronous sender, which
	// matches on threadId, sees it instead of waiting out its timeout.
	if _, err := store.Deliver(sessioncollab.MailMessage{
		To:       msg.From,
		Body:     note,
		Hop:      msg.Hop,
		ThreadID: msg.ThreadID,
		ReplyTo:  "",
	}); err != nil {
		log.Printf("[session-collab] %s notice to %s failed: %v", kind, msg.From, err)
	}
}

func sessionCollabRefusedHopText(msg sessioncollab.MailMessage) string {
	return "跨会话消息被拒绝并丢弃：协作链已达 hop 上限（" + strconv.Itoa(sessioncollab.MaxHop) +
		"）。如需继续，请新起一条链，不要在上一条链上继续接力。（messageId=" + msg.ID + "）"
}

func sessionCollabBadProvenanceText(msg sessioncollab.MailMessage, cause error) string {
	return "跨会话消息被拒绝并丢弃：无法核实它的链路来源（" + cause.Error() +
		"）。当你回复某条消息时，请把收到的 threadId 原样传回。（messageId=" + msg.ID + "）"
}

func sessionCollabDeliveryFailedText(msg sessioncollab.MailMessage, cause error) string {
	return "跨会话消息投递失败，将自动重试：目标会话当前不可用（" + cause.Error() +
		"）。消息仍在队列中，不会丢失。（messageId=" + msg.ID + "）"
}

// deliverOne hands one message to the target. It reports whether a steer
// actually injected, so the caller can tell the sender when it degraded. A
// detached target is reached through its controller — there is no visible tab
// to address by id.
func (p *sessionCollabPump) deliverOne(target sessionCollabTarget, msg sessioncollab.MailMessage, body string) (bool, error) {
	idem := "collab:" + msg.ID
	if target.detached {
		if target.ctrl == nil {
			return false, fmt.Errorf("detached runtime has no controller")
		}
		if msg.Delivery != string(sessioncollab.DeliverySteer) {
			_, err := p.app.enqueueInboxWithController(target.tabID, target.ctrl, sessioninbox.IntentFollowup, body, body, nil, idem, false, "", "")
			return false, err
		}
		receipt, err := p.app.enqueueInboxWithController(target.tabID, target.ctrl, sessioninbox.IntentSteer, body, body, nil, idem, true, "", "")
		if err != nil {
			return false, err
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
	if msg.Delivery != string(sessioncollab.DeliverySteer) && !target.activeTab {
		_, err := p.app.EnqueueInboxFollowup(target.tabID, body, body, idem)
		return false, err
	}
	receipt, err := p.app.EnqueueInboxSteer(target.tabID, body, body, idem)
	if err != nil {
		// Steer rejected: fall back to a queued follow-up so the message is not lost.
		if msg.Delivery != string(sessioncollab.DeliverySteer) {
			_, ferr := p.app.EnqueueInboxFollowup(target.tabID, body, body, idem)
			if ferr != nil {
				return false, ferr
			}
			return false, nil
		}
		return false, err
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
// keeps the original hop so a status note never inflates the collaboration
// chain, and it is best-effort: a failure here must not drop the message that
// was already delivered.
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
	if _, err := sessioncollab.NewMailStore(mailDir).Deliver(sessioncollab.MailMessage{
		To:      msg.From,
		Body:    note,
		Hop:     msg.Hop,
		ReplyTo: "",
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
	if msg.From != "" {
		b.WriteString(" 来自 contact_id=")
		b.WriteString(msg.From)
	}
	if msg.To != "" {
		b.WriteString(" → 发至 contact_id=")
		b.WriteString(msg.To)
	}
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
	} else {
		b.WriteString("这是单向通知，无需回复。")
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
			activeTab: tab.ID == a.activeTabID,
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
		out = append(out, sessionCollabTarget{tabID: tab.ID, contactID: contact})
	}
	return out
}
