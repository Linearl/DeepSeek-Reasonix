package main

import (
	"log"
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

func (p *sessionCollabPump) drain() SessionCollabDrainResult {
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
		if _, err := p.app.EnqueueInboxFollowup(target.tabID, body, body, "collab:"+msg.ID); err != nil {
			return delivered, len(rejected), err
		}
		delivered++
	}
	return delivered, len(rejected), nil
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
	if msg.ReplyTo != "" {
		b.WriteString("回复方式：完成后用 talk_to_session 回信到 contact_id=" + msg.ReplyTo +
			"，hop 传 " + strconv.Itoa(msg.Hop+1) + "。")
	} else {
		b.WriteString("这是单向通知，无需回复。")
	}
	return b.String()
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
