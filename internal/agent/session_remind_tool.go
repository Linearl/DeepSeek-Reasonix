package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/reminder"
	"reasonix/internal/tool"
)

// RemindConfig carries the host knobs for the session_remind tool (task
// 1001). Same shape as TaskCardConfig: boot-time snapshots plus a call-time
// resolver, and the schedule/cancel/list faces are host callbacks so the
// agent package never depends on the controller.
type RemindConfig struct {
	Enabled bool
	// CurrentSessionPath / ResolveSessionPath follow the task-card pattern:
	// the transcript path is bound by the control layer after boot, so the
	// snapshot may be empty for desktop sessions.
	CurrentSessionPath string
	ResolveSessionPath func() string
	// Schedule durably sets one reminder for the caller's session.
	Schedule func(sessionPath, message string, dueAt time.Time) (reminder.Reminder, error)
	// Cancel removes a pending reminder; found=false covers unknown ids.
	Cancel func(sessionPath, id string) (bool, error)
	// List returns the session's pending reminders.
	List func(sessionPath string) ([]reminder.Reminder, error)
}

// NewSessionRemindTool returns the merged three-action reminder tool when the
// experiment is on (task 1001: one schema, three actions — the task_card
// merged-tool pattern keeps the model's selection space small).
func NewSessionRemindTool(cfg RemindConfig) tool.Tool {
	if !cfg.Enabled {
		return nil
	}
	return sessionRemindTool{cfg: cfg}
}

type sessionRemindTool struct{ cfg RemindConfig }

func (sessionRemindTool) Name() string   { return "session_remind" }
func (sessionRemindTool) ReadOnly() bool { return false }
func (sessionRemindTool) Description() string {
	return "Set a delayed reminder for THIS session (an in-conversation alarm, task 1001): at the due time the reminder text is injected back into this conversation as a message and a new turn opens, even if the app restarted in between. Routed by `action`: set schedules one (minutes + message); cancel removes a pending reminder by reminder_id; list shows pending reminders. Reminders never block other work — schedule one and keep going. Minutes must be between 1 and 10080 (7 days); for anything longer use the heartbeat system instead."
}
func (sessionRemindTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["set","cancel","list"],"description":"Which reminder operation to run."},"minutes":{"type":"integer","description":"set: delay in minutes from now (1-10080)."},"message":{"type":"string","description":"set: what to remind about; injected verbatim at the due time."},"reminder_id":{"type":"string","description":"cancel: the id returned by set or list."}},"required":["action"]}`)
}

func (t sessionRemindTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action     string `json:"action"`
		Minutes    *int   `json:"minutes"`
		Message    string `json:"message"`
		ReminderID string `json:"reminder_id"`
	}
	if len(args) == 0 {
		return "", fmt.Errorf("action is required (set|cancel|list)")
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	sessionPath := t.cfg.currentSessionPath()
	if sessionPath == "" {
		return "", fmt.Errorf("session_remind requires a persisted session path")
	}
	switch p.Action {
	case "set":
		if p.Minutes == nil {
			return "", fmt.Errorf("minutes is required for action=set")
		}
		if strings.TrimSpace(p.Message) == "" {
			return "", fmt.Errorf("message is required for action=set")
		}
		due := time.Now().Add(time.Duration(*p.Minutes) * time.Minute)
		r, err := t.cfg.Schedule(sessionPath, p.Message, due)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"reminder_id": r.ID,
			"due_at":      time.UnixMilli(r.DueAt).UTC().Format(time.RFC3339),
			"message":     r.Message,
		})
		return string(out), nil
	case "cancel":
		if strings.TrimSpace(p.ReminderID) == "" {
			return "", fmt.Errorf("reminder_id is required for action=cancel")
		}
		found, err := t.cfg.Cancel(sessionPath, p.ReminderID)
		if err != nil {
			return "", err
		}
		if !found {
			return fmt.Sprintf(`{"cancelled":false,"reason":"no pending reminder %q (already fired, cancelled, or unknown id)"}`, p.ReminderID), nil
		}
		return `{"cancelled":true}`, nil
	case "list":
		entries, err := t.cfg.List(sessionPath)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return "No pending reminders.\n", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# Pending reminders (%d)\n\n", len(entries))
		b.WriteString("| reminder_id | due_at | message |\n|---|---|---|\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n",
				e.ID,
				time.UnixMilli(e.DueAt).UTC().Format(time.RFC3339),
				strings.ReplaceAll(e.Message, "\n", " "))
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown action %q (set|cancel|list)", p.Action)
	}
}

// currentSessionPath resolves the caller's transcript path at call time.
func (c RemindConfig) currentSessionPath() string {
	if c.ResolveSessionPath != nil {
		if p := strings.TrimSpace(c.ResolveSessionPath()); p != "" {
			return p
		}
	}
	return strings.TrimSpace(c.CurrentSessionPath)
}
