package control

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"reasonix/internal/reminder"
)

// reminderState is controller-owned reminder wiring: the in-process timer
// manager plus the enablement snapshot (task 1001).
type reminderState struct {
	mu sync.Mutex
	// manager is built lazily on first use; nil until a session with reminder
	// activity binds.
	manager *reminder.Manager
	// sessionPath is the path the current timers were armed for.
	sessionPath string
	// enabled snapshots the experimental switch (default on). Off = no scan
	// and no timers; the durable queue file is left untouched so flipping the
	// switch back on recovers pending alarms.
	enabled bool
}

// reminderSource tags inbox entries injected by a due reminder so the
// transcript shows where the message came from.
const reminderSource = "reminder"

// SetRemindersEnabled snapshots the experimental switch (task 1001, default
// on). Call once during boot; off = no timers are armed and the scheduling
// tool stays unregistered, so an alarm set before the flip still fires from
// the next enabled boot's scan (the durable promise survives the switch).
// Re-enabling forces a fresh scan: entries that came due while the switch was
// off deliver late with the annotation, same as a restart.
func (c *Controller) SetRemindersEnabled(enabled bool) {
	c.reminders.mu.Lock()
	c.reminders.enabled = enabled
	if enabled {
		// Defeat the same-path short circuit so the re-enable re-scans disk.
		c.reminders.sessionPath = ""
	}
	c.reminders.mu.Unlock()
	if enabled {
		c.rebindReminders()
	}
}

// rebindReminders re-arms the timer set for the current session path. It
// follows rebindInbox's call sites (constructor, NewSession, Resume,
// SetSessionPath): every session identity change re-syncs from the durable
// queue — this is the restart/rebind scan that upholds the cross-restart
// promise (pending entries re-arm, overdue entries deliver late with an
// annotation).
func (c *Controller) rebindReminders() {
	path := c.SessionPath()
	c.reminders.mu.Lock()
	defer c.reminders.mu.Unlock()
	if !c.reminders.enabled {
		return
	}
	if c.reminders.sessionPath == path {
		return
	}
	if c.reminders.manager == nil {
		c.reminders.manager = reminder.NewManager(c.deliverReminder)
	}
	c.reminders.sessionPath = path
	c.reminders.manager.DropAll()
	c.reminders.manager.Sync(path)
}

// deliverReminder is the manager's due-callback: inject the message as a
// durable follow-up into the owning session. The follow-up path rides the
// 任务579 idle-turn bridge (auto-open when idle, bounded retries when the
// runtime is detached) and never enters talk_to_session, so the cross-session
// receipt face — and its to==from self-send detection (tl 1000) — is
// structurally out of reach. Overdue deliveries carry an explicit catch-up
// notice so the model can tell a restart-delayed alarm from an on-time one.
func (c *Controller) deliverReminder(sessionPath string, r reminder.Reminder) {
	// The store entry stays until delivery is durably accepted; check the
	// entry survived any concurrent cancel by re-reading it.
	entries, err := reminder.List(sessionPath)
	if err != nil {
		slog.Warn("reminder: deliver re-read", "session", sessionPath, "id", r.ID, "err", err)
		return
	}
	live := false
	for _, e := range entries {
		if e.ID == r.ID {
			live = true
			break
		}
	}
	if !live {
		return // cancelled between arming and firing: no-op
	}
	text := reminderMessage(r)
	_, err = c.TryEnqueueFollowup(InboxRequest{
		ExpectedSessionPath: sessionPath,
		Display:             text,
		Raw:                 text,
		Submit:              text,
		Source:              reminderSource,
		Idempotency:         "reminder:" + r.ID,
	})
	if err != nil {
		// Session switched away (fence) or controller closed: keep the
		// durable entry so a later bind's scan re-delivers it (overdue,
		// annotated). Log loudly — alarms must not vanish silently.
		slog.Warn("reminder: enqueue followup failed; entry kept for catch-up",
			"session", sessionPath, "id", r.ID, "err", err)
		return
	}
	// Delivered into the durable inbox — the inbox now owns the message.
	if err := reminder.Remove(sessionPath, r.ID); err != nil {
		slog.Warn("reminder: remove consumed entry", "session", sessionPath, "id", r.ID, "err", err)
	}
}

// reminderMessage renders the injected user message.
func reminderMessage(r reminder.Reminder) string {
	due := time.UnixMilli(r.DueAt).Local().Format("2006-01-02 15:04")
	var b strings.Builder
	b.WriteString("[scheduled reminder")
	if r.FiredLate {
		fmt.Fprintf(&b, " — was due at %s while the app was down; delivered late", due)
	}
	fmt.Fprintf(&b, "] %s", r.Message)
	return b.String()
}

// ScheduleReminder durably sets a reminder for the current session and arms
// its timer. Durable commit happens before the timer is armed.
func (c *Controller) ScheduleReminder(message string, due time.Time) (reminder.Reminder, error) {
	path := c.SessionPath()
	if path == "" {
		return reminder.Reminder{}, fmt.Errorf("reminders require a persisted session path")
	}
	c.reminders.mu.Lock()
	enabled := c.reminders.enabled
	mgr := c.reminders.manager
	c.reminders.mu.Unlock()
	if !enabled {
		return reminder.Reminder{}, fmt.Errorf("reminders are disabled (experimental_session_reminders)")
	}
	now := time.Now()
	if err := reminder.ValidateReminder(due, message, now); err != nil {
		return reminder.Reminder{}, err
	}
	r, err := reminder.Set(path, message, due, now)
	if err != nil {
		return reminder.Reminder{}, err
	}
	if mgr == nil {
		// First reminder of this controller instance: build the manager and
		// adopt the current session.
		c.reminders.mu.Lock()
		if c.reminders.manager == nil {
			c.reminders.manager = reminder.NewManager(c.deliverReminder)
			c.reminders.sessionPath = path
		}
		mgr = c.reminders.manager
		c.reminders.mu.Unlock()
	}
	mgr.Track(path, r)
	return r, nil
}

// CancelReminder cancels a pending reminder and disarms its timer.
func (c *Controller) CancelReminder(id string) (bool, error) {
	path := c.SessionPath()
	if path == "" {
		return false, fmt.Errorf("reminders require a persisted session path")
	}
	found, err := reminder.Cancel(path, id)
	if err != nil {
		return false, err
	}
	c.reminders.mu.Lock()
	if c.reminders.manager != nil {
		c.reminders.manager.Forget(id)
	}
	c.reminders.mu.Unlock()
	return found, nil
}

// ListReminders returns the session's pending reminders, earliest due first.
func (c *Controller) ListReminders() ([]reminder.Reminder, error) {
	path := c.SessionPath()
	if path == "" {
		return nil, fmt.Errorf("reminders require a persisted session path")
	}
	return reminder.List(path)
}

// closeReminders stops the timer manager at controller teardown.
func (c *Controller) closeReminders() {
	c.reminders.mu.Lock()
	defer c.reminders.mu.Unlock()
	if c.reminders.manager != nil {
		c.reminders.manager.Close()
	}
}
