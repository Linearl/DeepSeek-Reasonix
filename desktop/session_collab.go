package main

import (
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
// tab's inbox, where the existing idle dispatcher runs it. Delivery is
// at-least-once with a cursor, so a desktop that was closed loses nothing: the
// next pass resumes from the cursor.
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
func (a *App) createCollabSession(workspaceRoot, title, purpose, group string) (string, error) {
	scope, root := "global", ""
	if strings.TrimSpace(workspaceRoot) != "" {
		scope, root = "project", workspaceRoot
	}
	meta, err := a.CreateTopic(scope, root, title)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(group) != "" {
		if err := a.AddTopicToGroup(scope, root, meta.ID, group); err != nil {
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

func (p *sessionCollabPump) deliverToTab(target sessionCollabTarget) (delivered, refused int, err error) {
	mailDir := config.SessionCollabMailDir()
	if mailDir == "" {
		return 0, 0, nil
	}
	mail := sessioncollab.NewMailStore(mailDir)
	messages, rejected, err := mail.Claim(target.contactID)
	if err != nil {
		return 0, 0, err
	}
	for _, msg := range rejected {
		// A chain that exhausted hop is reported, never silently dropped.
		log.Printf("[session-collab] refused hop-exhausted message from %s to %s", msg.From, target.contactID)
	}
	for _, msg := range messages {
		body := sessionCollabDeliveryText(msg)
		if err := p.deliverOne(target.tabID, msg, body); err != nil {
			return delivered, len(rejected), err
		}
		delivered++
	}
	return delivered, len(rejected), nil
}

// deliverOne hands one message to the target. A steer that cannot reach the
// target's live turn degrades to a queued follow-up, and the sender is told so
// the degradation is never silent (task 143).
func (p *sessionCollabPump) deliverOne(tabID string, msg sessioncollab.MailMessage, body string) error {
	if msg.Delivery != string(sessioncollab.DeliverySteer) {
		_, err := p.app.EnqueueInboxFollowup(tabID, body, body, "collab:"+msg.ID)
		return err
	}
	receipt, err := p.app.EnqueueInboxSteer(tabID, body, body, "collab:"+msg.ID)
	if err != nil {
		return err
	}
	if sessionCollabReceiptSteered(receipt.Disposition) {
		return nil
	}
	log.Printf("[session-collab] steer degraded to follow-up for %s (disposition=%s)", msg.To, receipt.Disposition)
	p.notifyDegradedSteer(msg, receipt.Disposition)
	return nil
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
// to carry the reply address, or an async exchange cannot close its loop.
func sessionCollabDeliveryText(msg sessioncollab.MailMessage) string {
	var b strings.Builder
	b.WriteString("[跨会话消息]")
	if msg.From != "" {
		b.WriteString(" 来自 contact_id=")
		b.WriteString(msg.From)
	}
	if msg.Hop > 0 {
		b.WriteString(" (hop=")
		b.WriteString(strconv.Itoa(msg.Hop))
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
			"，hop 传 " + strconv.Itoa(msg.Hop+1))
		if msg.ID != "" {
			b.WriteString("，并把 thread_id 设为 " + msg.ID + "（发起方可能正在同步等待）")
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
