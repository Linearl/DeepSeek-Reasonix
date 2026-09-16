package main

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
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
// (task 144): it creates the topic, files it into the requested group, and
// records the purpose for later stamping, because the transcript that carries
// the contact_id does not exist until the session first runs.
func (a *App) createCollabSession(workspaceRoot, title, purpose, group, groupID string) (string, error) {
	scope, root := "global", ""
	if strings.TrimSpace(workspaceRoot) != "" {
		scope, root = "project", workspaceRoot
	}
	meta, err := a.CreateTopic(scope, root, title)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(group) != "" || strings.TrimSpace(groupID) != "" {
		if err := a.AddTopicToGroup(scope, root, meta.ID, groupID, group); err != nil {
			return meta.ID, err
		}
	}
	if dir := config.SessionCollabMailDir(); dir != "" {
		if err := sessioncollab.NewPendingPurposeStore(dir).Set(meta.ID, purpose); err != nil {
			return meta.ID, err
		}
	}
	return meta.ID, nil
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
	var result SessionCollabDrainResult
	for _, target := range p.app.sessionCollabTargets() {
		delivered, refused, err := p.deliverToTab(target)
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
		if err != nil {
			entry.Error = err.Error()
			log.Printf("[session-collab] tab %s contact %s: %v", target.tabID, target.contactID, err)
		}
		result.Targets = append(result.Targets, entry)
	}
	return result
}

type sessionCollabTarget struct {
	tabID     string
	contactID string
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

// deliverToTab hands every pending message to the target and acks only what is
// settled. A delivery failure is NOT acked, so the next pass retries it, and the
// sender is told once. Acking a message before it is in the target's inbox would
// make delivery at-most-once — the failure mode where a user's task disappears
// with nothing but a local log line.
func (p *sessionCollabPump) deliverToTab(target sessionCollabTarget) (delivered, refused int, err error) {
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return 0, 0, nil
	}
	mail := sessioncollab.NewMailStore(mailDir)
	return runCollabDelivery(mail, target.contactID, collabDelivery{
		enqueue: func(msg sessioncollab.MailMessage, body string) (bool, error) {
			return p.deliverOne(target.tabID, msg, body)
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
// actually injected, so the caller can tell the sender when it degraded.
func (p *sessionCollabPump) deliverOne(tabID string, msg sessioncollab.MailMessage, body string) (bool, error) {
	if msg.Delivery != string(sessioncollab.DeliverySteer) {
		_, err := p.app.EnqueueInboxFollowup(tabID, body, body, "collab:"+msg.ID)
		return false, err
	}
	receipt, err := p.app.EnqueueInboxSteer(tabID, body, body, "collab:"+msg.ID)
	if err != nil {
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
func sessionCollabDeliveryText(msg sessioncollab.MailMessage, effectiveHop int) string {
	var b strings.Builder
	b.WriteString("[跨会话消息]")
	if msg.From != "" {
		b.WriteString(" 来自 contact_id=")
		b.WriteString(msg.From)
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

// AddressableSessionView is one registered session for the desktop surface
// (task 141). ContactID is the stable address; TopicID and the path are shown
// only so a human can recognise the row.
type AddressableSessionView struct {
	ContactID   string `json:"contactId"`
	Purpose     string `json:"purpose,omitempty"`
	Title       string `json:"title,omitempty"`
	TopicID     string `json:"topicId,omitempty"`
	SessionPath string `json:"sessionPath"`
	Scope       string `json:"scope,omitempty"`
	Workspace   string `json:"workspaceRoot,omitempty"`
	Open        bool   `json:"open"`
}

// ListAddressableSessions enumerates sessions that can be addressed by
// contact_id. Renaming a topic never changes the row's ContactID, so a
// reference taken before the rename still resolves.
func (a *App) ListAddressableSessions() []AddressableSessionView {
	open := map[string]bool{}
	for _, target := range a.sessionCollabTargets() {
		open[target.contactID] = true
	}
	var out []AddressableSessionView
	seen := map[string]bool{}
	add := func(ids []sessioncollab.Identity, scope string) {
		for _, id := range ids {
			if seen[id.ContactID] {
				continue
			}
			seen[id.ContactID] = true
			out = append(out, AddressableSessionView{
				ContactID:   id.ContactID,
				Purpose:     id.Purpose,
				Title:       id.Title,
				TopicID:     id.TopicID,
				SessionPath: id.SessionPath,
				Scope:       scope,
				Workspace:   id.Workspace,
				Open:        open[id.ContactID],
			})
		}
	}
	load := func(sessionPath string) (contact, purpose, topic, title string, ok bool) {
		m, found, err := agent.LoadBranchMeta(sessionPath)
		if err != nil || !found || m.ContactID == "" {
			return "", "", "", "", false
		}
		return m.ContactID, m.Purpose, m.TopicID, m.CustomTitle, true
	}
	// Global sessions first, then every known project, so the list is complete
	// even when the session is not currently open in a tab.
	add(sessioncollab.ScanDir(config.SessionDir(), "", load), "global")
	for _, root := range a.knownProjectRoots() {
		add(sessioncollab.ScanDir(config.ProjectSessionDir(root), root, load), "project")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ContactID < out[j].ContactID })
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
