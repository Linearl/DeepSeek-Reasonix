package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// tl1311 acceptance: the agent-side reopen rides session_control as a third
// action. Same stub discipline as session_control_test.go — these pins
// contract semantics (receipts, visibility refusals, self-target scoping),
// not the App wiring.

func TestReopenReportsRemountedAuthority(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop:     func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) { return false, false, false, "", nil },
		Reopen:   func(string) (bool, bool, error) { return true, true, nil },
	})
	out, err := runControl(t, tool, `{"action":"reopen","target":"ct_peer"}`)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !strings.Contains(out, `"reopened":true`) {
		t.Fatalf("receipt must report the remount: %s", out)
	}
	if !strings.Contains(out, "untouched") {
		t.Fatalf("receipt must state the transcript/inbox are untouched: %s", out)
	}
}

// reopened=false with known=true is an honest nothing-to-repair / declined
// receipt (authority already valid, or a live holder keeps the lease) —
// never an error, never a guessed success.
func TestReopenDeclinedIsHonestReceipt(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop:     func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) { return false, false, false, "", nil },
		Reopen:   func(string) (bool, bool, error) { return false, true, nil },
	})
	out, err := runControl(t, tool, `{"action":"reopen","target":"ct_peer"}`)
	if err != nil {
		t.Fatalf("a declined reopen must not error: %v", err)
	}
	if !strings.Contains(out, `"reopened":false`) || !strings.Contains(out, "nothing to repair") {
		t.Fatalf("declined receipt wrong: %s", out)
	}
}

func TestReopenUnknownRuntimeIsRefused(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop:     func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) { return false, false, false, "", nil },
		Reopen:   func(string) (bool, bool, error) { return false, false, nil },
	})
	if _, err := runControl(t, tool, `{"action":"reopen","target":"ct_peer"}`); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("known=false must refuse with the visibility reason, got %v", err)
	}
}

func TestReopenHostErrorPropagates(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop:     func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) { return false, false, false, "", nil },
		Reopen: func(string) (bool, bool, error) {
			return false, true, errors.New("lease registry unavailable")
		},
	})
	if _, err := runControl(t, tool, `{"action":"reopen","target":"ct_peer"}`); err == nil || !strings.Contains(err.Error(), "lease registry") {
		t.Fatalf("hook error must reach the caller, got %v", err)
	}
}

// reopen is not wired: a host that wired only the task-274 hooks gets a
// reopen-specific refusal, and stop/set_model keep working.
func TestReopenUnwiredRefusesWithoutTouchingSiblingActions(t *testing.T) {
	stopCalled := false
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop:     func(string) (bool, bool, bool, error) { stopCalled = true; return false, false, true, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) { return false, false, false, "", nil },
	})
	if _, err := runControl(t, tool, `{"action":"reopen","target":"ct_peer"}`); err == nil || !strings.Contains(err.Error(), "reopen is not wired") {
		t.Fatalf("unwired reopen must refuse with its own reason, got %v", err)
	}
	if _, err := runControl(t, tool, `{"action":"stop","target":"ct_peer"}`); err != nil || !stopCalled {
		t.Fatalf("stop must keep working beside an unwired reopen, err=%v stopCalled=%v", err, stopCalled)
	}
}

// The tl1311 self-target scoping: reopen MAY act on the calling session
// (an agent repairing its own wedge), set_model still refuses self (task 387).
func TestReopenAllowsSelfWhileSetModelRefusesSelf(t *testing.T) {
	cfg, contact := controlFixture(t)
	cfg.CurrentContactID = contact
	calls := 0
	cfg.SessionControl = SessionControlHooks{
		Stop:     func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) { return false, false, false, "", nil },
		Reopen:   func(string) (bool, bool, error) { calls++; return true, true, nil },
	}
	tool := sessionControlTool{cfg: cfg}
	out, err := runControl(t, tool, `{"action":"reopen","target":"`+contact+`"}`)
	if err != nil || !strings.Contains(out, `"reopened":true`) || calls != 1 {
		t.Fatalf("self reopen must be allowed (out=%s err=%v calls=%d)", out, err, calls)
	}
	if _, err := runControl(t, tool, `{"action":"set_model","target":"`+contact+`","model":"x/y"}`); err == nil || !strings.Contains(err.Error(), "calling session itself") {
		t.Fatalf("self set_model must stay refused (task 387), got %v", err)
	}
	if calls != 1 {
		t.Fatalf("the refused set_model must not reach the reopen hook, calls=%d", calls)
	}
}

// The schema advertises the third action so the model can discover it.
func TestReopenActionInSchema(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{})
	var schema struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	found := false
	for _, a := range schema.Properties.Action.Enum {
		if a == "reopen" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reopen missing from the action enum: %v", schema.Properties.Action.Enum)
	}
}
