package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/reminder"
)

func remindConfigForTest(schedule func(sessionPath, message string, dueAt time.Time) (reminder.Reminder, error),
	cancel func(sessionPath, id string) (bool, error),
	list func(sessionPath string) ([]reminder.Reminder, error)) RemindConfig {
	return RemindConfig{
		Enabled:            true,
		CurrentSessionPath: "/w/s.jsonl",
		Schedule:           schedule,
		Cancel:             cancel,
		List:               list,
	}
}

func mustExecute(t *testing.T, tl toolLike, args string) string {
	t.Helper()
	out, err := tl.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("execute %s: %v", args, err)
	}
	return out
}

// toolLike avoids importing the tool package just for the test double.
type toolLike interface {
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// TestSessionRemindToolSet covers action=set: minutes convert to an absolute
// due time, the id round-trips, and required-argument gaps fail loudly.
func TestSessionRemindToolSet(t *testing.T) {
	var gotPath, gotMsg string
	var gotDue time.Time
	cfg := remindConfigForTest(
		func(sessionPath, message string, dueAt time.Time) (reminder.Reminder, error) {
			gotPath, gotMsg, gotDue = sessionPath, message, dueAt
			return reminder.Reminder{ID: "rem_abc", DueAt: dueAt.UnixMilli(), Message: message}, nil
		}, nil, nil)
	tl := NewSessionRemindTool(cfg)

	out := mustExecute(t, tl, `{"action":"set","minutes":30,"message":"stretch legs"}`)
	if !strings.Contains(out, `"reminder_id":"rem_abc"`) {
		t.Fatalf("set output = %s", out)
	}
	if gotPath != "/w/s.jsonl" || gotMsg != "stretch legs" {
		t.Fatalf("schedule args = %q %q", gotPath, gotMsg)
	}
	if d := time.Until(gotDue); d < 29*time.Minute || d > 31*time.Minute {
		t.Fatalf("due delta = %v, want ~30m", d)
	}

	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"set","message":"no minutes"}`)); err == nil {
		t.Fatal("set without minutes must fail")
	}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"set","minutes":5}`)); err == nil {
		t.Fatal("set without message must fail")
	}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing action must fail")
	}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"purge"}`)); err == nil {
		t.Fatal("unknown action must fail")
	}
}

// TestSessionRemindToolCancel covers action=cancel on both faces: a live id
// cancels, an unknown id reports cancelled=false with the reason spelled out.
func TestSessionRemindToolCancel(t *testing.T) {
	cfg := remindConfigForTest(nil,
		func(sessionPath, id string) (bool, error) { return id == "rem_live", nil }, nil)
	tl := NewSessionRemindTool(cfg)

	if out := mustExecute(t, tl, `{"action":"cancel","reminder_id":"rem_live"}`); !strings.Contains(out, `"cancelled":true`) {
		t.Fatalf("cancel output = %s", out)
	}
	out, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"cancel","reminder_id":"rem_gone"}`))
	if err != nil || !strings.Contains(out, `"cancelled":false`) {
		t.Fatalf("cancel unknown = %q %v", out, err)
	}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"cancel"}`)); err == nil {
		t.Fatal("cancel without id must fail")
	}
}

// TestSessionRemindToolList covers action=list: empty renders the explicit
// no-pending line; entries render as rows carrying the id.
func TestSessionRemindToolList(t *testing.T) {
	empty := remindConfigForTest(nil, nil, func(sessionPath string) ([]reminder.Reminder, error) {
		return nil, nil
	})
	if out := mustExecute(t, NewSessionRemindTool(empty), `{"action":"list"}`); !strings.Contains(out, "No pending reminders") {
		t.Fatalf("empty list output = %s", out)
	}

	full := remindConfigForTest(nil, nil, func(sessionPath string) ([]reminder.Reminder, error) {
		return []reminder.Reminder{{
			ID: "rem_x", DueAt: time.Now().Add(time.Hour).UnixMilli(), Message: "hello\nworld",
		}}, nil
	})
	out := mustExecute(t, NewSessionRemindTool(full), `{"action":"list"}`)
	if !strings.Contains(out, "`rem_x`") || !strings.Contains(out, "hello world") {
		t.Fatalf("list output = %s", out)
	}
}

// TestSessionRemindToolGate pins the switch-off face: the constructor returns
// nil so boot registers nothing (unregistered, not registered-but-refusing).
func TestSessionRemindToolGate(t *testing.T) {
	cfg := RemindConfig{Enabled: false}
	if tl := NewSessionRemindTool(cfg); tl != nil {
		t.Fatal("disabled config must return nil tool")
	}
}

// TestSessionRemindToolNeedsSessionPath: without a resolvable session path the
// tool refuses instead of silently scheduling against an empty path.
func TestSessionRemindToolNeedsSessionPath(t *testing.T) {
	cfg := RemindConfig{
		Enabled: true,
		Schedule: func(sessionPath, message string, dueAt time.Time) (reminder.Reminder, error) {
			return reminder.Reminder{ID: "rem_never"}, nil
		},
	}
	tl := NewSessionRemindTool(cfg)
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"action":"set","minutes":5,"message":"x"}`)); err == nil {
		t.Fatal("empty session path must fail")
	}
}
