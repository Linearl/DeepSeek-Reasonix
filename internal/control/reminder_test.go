package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/reminder"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/store"
)

func reminderTestController(t *testing.T) (*Controller, string) {
	t.Helper()
	// Manual temp dir with best-effort cleanup: the controller's background
	// pump goroutines can still hold inbox blobs for a few milliseconds after
	// the assertions pass, and t.TempDir's strict RemoveAll then fails the
	// test on Windows ("directory is not empty") even though the test itself
	// is green.
	dir, err := os.MkdirTemp("", "remindertest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	t.Cleanup(func() { c.Close() })
	return c, session
}

func waitForReminder(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// TestReminderDueDeliversIntoInbox covers 验收③: a due reminder is injected as
// a durable followup into the owning session (Source=reminder) — the entry the
// 任务579 idle-turn bridge consumes. It never goes through talk_to_session:
// the injection is this controller's own enqueue, and the reminder package
// imports no cross-session delivery face (pinned in reminder_test.go there).
func TestReminderDueDeliversIntoInbox(t *testing.T) {
	c, session := reminderTestController(t)
	c.SetRemindersEnabled(true)

	r, err := c.ScheduleReminder("check the oven", time.Now().Add(80*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// 验收① half one: set is durable before returning.
	if _, err := os.Stat(filepath.Join(store.SessionRemindersDir(session), "reminders.json")); err != nil {
		t.Fatalf("queue file missing after set: %v", err)
	}

	waitForReminder(t, 5*time.Second, func() bool {
		snap := c.InboxSnapshot()
		for _, it := range snap.Items {
			if it.Source == reminderSource {
				return true
			}
		}
		return false
	})
	snap := c.InboxSnapshot()
	var found bool
	for _, it := range snap.Items {
		if it.Source != reminderSource {
			continue
		}
		found = true
		if it.Intent != sessioninbox.IntentFollowup {
			t.Fatalf("reminder item intent = %v, want followup", it.Intent)
		}
		_, env, err := c.ReadInboxItem(it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(env.SubmitText, "[scheduled reminder]") || !strings.Contains(env.SubmitText, "check the oven") {
			t.Fatalf("injected text = %q", env.SubmitText)
		}
		if env.Extra["reminder_id"] != "" {
			t.Fatalf("unexpected extra: %v", env.Extra)
		}
	}
	if !found {
		t.Fatal("reminder item not in inbox")
	}
	// Consumed at the reminder layer: the durable queue entry is removed once
	// the inbox owns the message. Wait for the full handoff (entry removal
	// runs on the delivery goroutine after the enqueue lands).
	waitForReminder(t, 5*time.Second, func() bool {
		entries, err := c.ListReminders()
		return err == nil && len(entries) == 0
	})
	_ = r
}

// TestReminderRestartScanDeliversLateWithAnnotation covers 验收①: an entry set
// before a restart is picked up by the new controller's bind scan and
// delivered with the explicit catch-up annotation.
func TestReminderRestartScanDeliversLateWithAnnotation(t *testing.T) {
	c, session := reminderTestController(t)
	c.SetRemindersEnabled(true)
	due := time.Now().Add(80 * time.Millisecond)
	if _, err := c.ScheduleReminder("post-restart alarm", due); err != nil {
		t.Fatal(err)
	}
	// Simulate the restart: the old controller dies without firing, then a
	// fresh controller binds the same session path.
	c.Close()

	dir := filepath.Dir(session)
	session2 := session
	c2 := New(Options{SessionPath: session2, SessionDir: dir, Sink: event.Discard})
	defer c2.Close()
	// Wait until the due time is genuinely in the past, then flip the switch:
	// the first scan must deliver it late with the annotation.
	time.Sleep(150 * time.Millisecond)
	c2.SetRemindersEnabled(true)

	waitForReminder(t, 5*time.Second, func() bool {
		snap := c2.InboxSnapshot()
		for _, it := range snap.Items {
			if it.Source != reminderSource {
				continue
			}
			entries, err := c2.ListReminders()
			return err == nil && len(entries) == 0
		}
		return false
	})
	snap := c2.InboxSnapshot()
	for _, it := range snap.Items {
		if it.Source != reminderSource {
			continue
		}
		_, env, err := c2.ReadInboxItem(it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(env.SubmitText, "delivered late") {
			t.Fatalf("catch-up annotation missing: %q", env.SubmitText)
		}
		if !strings.Contains(env.SubmitText, "post-restart alarm") {
			t.Fatalf("message missing: %q", env.SubmitText)
		}
	}
	// Let the controller's delivery goroutine finish its disk work before the
	// TempDir cleanup races it on Windows.
	time.Sleep(150 * time.Millisecond)
}

// TestReminderCancelPreventsDelivery covers the cancel action's backend face:
// a cancelled reminder's due time passes silently.
func TestReminderCancelPreventsDelivery(t *testing.T) {
	c, _ := reminderTestController(t)
	c.SetRemindersEnabled(true)

	r, err := c.ScheduleReminder("never fires", time.Now().Add(80*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	found, err := c.CancelReminder(r.ID)
	if err != nil || !found {
		t.Fatalf("cancel = %v %v", found, err)
	}
	time.Sleep(250 * time.Millisecond)
	snap := c.InboxSnapshot()
	for _, it := range snap.Items {
		if it.Source == reminderSource {
			t.Fatalf("cancelled reminder delivered: %+v", it)
		}
	}
}

// TestRemindersSwitchOff covers 验收④: off = no scan (an overdue durable entry
// stays parked), ScheduleReminder refuses; on = the scan delivers it.
func TestRemindersSwitchOff(t *testing.T) {
	c, session := reminderTestController(t)

	// Park one durable entry while on, then flip off before it is due.
	c.SetRemindersEnabled(true)
	if _, err := c.ScheduleReminder("parked alarm", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c.SetRemindersEnabled(false)

	if _, err := c.ScheduleReminder("should refuse", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("ScheduleReminder succeeded with the switch off")
	}

	// Force the parked entry overdue on disk; the off-controller must not
	// deliver it (no scan, no timers).
	entries, _ := reminder.List(session)
	if len(entries) != 1 {
		t.Fatalf("durable entries = %+v", entries)
	}
	entries[0].DueAt = time.Now().Add(-time.Second).UnixMilli()
	overdue, err := json.Marshal(struct {
		Entries []reminder.Reminder `json:"entries"`
	}{entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.SessionRemindersDir(session), "reminders.json"), overdue, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	snap := c.InboxSnapshot()
	for _, it := range snap.Items {
		if it.Source == reminderSource {
			t.Fatal("switch-off controller delivered a reminder")
		}
	}

	// Flipping back on re-scans and delivers the now-overdue entry (annotated).
	c.SetRemindersEnabled(true)
	waitForReminder(t, 5*time.Second, func() bool {
		snap := c.InboxSnapshot()
		for _, it := range snap.Items {
			if it.Source != reminderSource {
				continue
			}
			entries, err := c.ListReminders()
			return err == nil && len(entries) == 0
		}
		return false
	})
	time.Sleep(150 * time.Millisecond)
}

// TestConfigSessionRemindersEnabledNilMeansOn pins the switch default: absent
// key = on; only an explicit false turns it off (可回退).
func TestConfigSessionRemindersEnabledNilMeansOn(t *testing.T) {
	var nilCfg *config.Config
	if !nilCfg.SessionRemindersEnabled() {
		t.Fatal("nil config must default on")
	}
	var cfg config.Config
	if !cfg.SessionRemindersEnabled() {
		t.Fatal("absent key must default on")
	}
	off := false
	cfg.Agent.ExperimentalSessionReminders = &off
	if cfg.SessionRemindersEnabled() {
		t.Fatal("explicit false must turn the switch off")
	}
	on := true
	cfg.Agent.ExperimentalSessionReminders = &on
	if !cfg.SessionRemindersEnabled() {
		t.Fatal("explicit true must stay on")
	}
	// The toml key must round-trip so users can flip it in config.toml.
	var decoded config.Config
	if _, err := toml.Decode("[agent]\nexperimental_session_reminders = false\n", &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SessionRemindersEnabled() {
		t.Fatal("decoded false must turn the switch off")
	}
}
