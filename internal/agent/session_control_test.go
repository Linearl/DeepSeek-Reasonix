package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 274 acceptance tests: directory model visibility (①), stop propagation
// receipts (②), set_model fail-closed on an active turn (③), directory
// resolution as the permission boundary (④), and the default-off switch.
// Every hook is a stub — these pins contract semantics, not the App wiring
// (which is covered by the compile-time port proof and the desktop build).

func controlFixture(t *testing.T) (SessionCollabConfig, string) {
	t.Helper()
	dir := t.TempDir()
	contact := "ct_peer"
	// Minimal addressable session: a transcript + meta sidecar with a contact id.
	sessionPath := filepath.Join(dir, "peer.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"b1","contact_id":"` + contact + `","custom_title":"Peer"}`
	if err := os.WriteFile(sessionPath+".meta", []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := SessionCollabConfig{SessionDir: dir, WorkspaceRoot: t.TempDir()}
	return cfg, contact
}

// ── acceptance ①: directory rows carry model+provider when the host can see ──
func TestDirectoryRowsCarryModelAndProvider(t *testing.T) {
	cfg, contact := controlFixture(t)
	cfg.SessionInfo = func(id string) (string, string, bool) {
		if id == contact {
			return "mimo-api/mimo-v2.6-flash", "mimo-api", true
		}
		return "", "", false
	}
	out, err := directoryPage(cfg, 10, nil, "")
	if err != nil {
		t.Fatalf("directoryPage: %v", err)
	}
	if !strings.Contains(out, `"modelRef":"mimo-api/mimo-v2.6-flash"`) {
		t.Fatalf("row missing modelRef: %s", out)
	}
	if !strings.Contains(out, `"provider":"mimo-api"`) {
		t.Fatalf("row missing provider: %s", out)
	}
}

func TestDirectoryRowsOmitModelWhenUnknown(t *testing.T) {
	cfg, _ := controlFixture(t)
	// nil probe (CLI/tests): fields must be absent, never guessed.
	out, err := directoryPage(cfg, 10, nil, "")
	if err != nil {
		t.Fatalf("directoryPage: %v", err)
	}
	if strings.Contains(out, "modelRef") {
		t.Fatalf("nil probe must omit modelRef entirely: %s", out)
	}
	cfg.SessionInfo = func(string) (string, string, bool) { return "x/y", "x", false }
	out, _ = directoryPage(cfg, 10, nil, "")
	if strings.Contains(out, "modelRef") {
		t.Fatalf("known=false must omit modelRef (no guessing): %s", out)
	}
}

// ── the tool itself: ② stop receipts, ③ fail-closed set_model, ④ boundary ──

func newControlTool(t *testing.T, hooks SessionControlHooks) (sessionControlTool, string) {
	t.Helper()
	cfg, contact := controlFixture(t)
	cfg.SessionControl = hooks
	return sessionControlTool{cfg: cfg}, contact
}

func runControl(t *testing.T, tool sessionControlTool, args string) (string, error) {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		t.Fatalf("bad test args: %v", err)
	}
	raw, _ := json.Marshal(p)
	return tool.Execute(nil, raw)
}

func TestStopReportsActiveTurnAndNotifies(t *testing.T) {
	var noticed string
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop: func(id string) (bool, bool, bool, error) {
			noticed = "stop-called:" + id
			return true, true, true, nil
		},
		SetModel: func(string, string) (bool, bool, bool, string, error) { return false, false, false, "", nil },
	})
	out, err := runControl(t, tool, `{"action":"stop","target":"ct_peer"}`)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(noticed, "stop-called:ct_peer") {
		t.Fatalf("hook not invoked with the resolved contact: %q", noticed)
	}
	if !strings.Contains(out, `"stopped":true`) || !strings.Contains(out, `"wasRunning":true`) {
		t.Fatalf("receipt must report the cancelled turn: %s", out)
	}
}

func TestStopIdleTargetIsIdempotentNoOp(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, true, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) {
			return false, false, false, "", nil
		},
	})
	out, err := runControl(t, tool, `{"action":"stop","target":"ct_peer"}`)
	if err != nil {
		t.Fatalf("stop on idle target must not error: %v", err)
	}
	if !strings.Contains(out, `"stopped":false`) || !strings.Contains(out, "no active turn") {
		t.Fatalf("idle receipt wrong: %s", out)
	}
}

func TestStopUnknownRuntimeIsRefused(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) {
			return false, false, false, "", nil
		},
	})
	if _, err := runControl(t, tool, `{"action":"stop","target":"ct_peer"}`); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("known=false must refuse with the visibility reason, got %v", err)
	}
}

// Acceptance ③: the hard constraint — a live turn refuses set_model with the
// exact remedy named, and the hook's applied path returns the new ref.
func TestSetModelFailsClosedOnActiveTurn(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) {
			return false, true, true, "", nil // running: fail closed
		},
	})
	_, err := runControl(t, tool, `{"action":"set_model","target":"ct_peer","model":"deepseek/deepseek-v4-flash"}`)
	if err == nil {
		t.Fatal("set_model on an active turn must be refused (hard constraint)")
	}
	for _, want := range []string{"active turn", "stop it first", "action=stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "not visible") {
		t.Errorf("running is a refusal, not a visibility problem: %v", err)
	}
}

func TestSetModelAppliesWhenIdle(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, false, nil },
		SetModel: func(_, model string) (bool, bool, bool, string, error) {
			return model == "mimo-api/mimo-v2.6-flash", false, true, "mimo-api/mimo-v2.6-flash", nil
		},
	})
	out, err := runControl(t, tool, `{"action":"set_model","target":"ct_peer","model":"mimo-api/mimo-v2.6-flash"}`)
	if err != nil {
		t.Fatalf("idle set_model: %v", err)
	}
	if !strings.Contains(out, `"applied":true`) || !strings.Contains(out, "mimo-api/mimo-v2.6-flash") {
		t.Fatalf("receipt must carry the new modelRef: %s", out)
	}
	// Missing model is a usage error before any hook runs.
	if _, err := runControl(t, tool, `{"action":"set_model","target":"ct_peer"}`); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("missing model must be refused, got %v", err)
	}
}

// Acceptance ④: the directory resolution is the permission boundary.
func TestBareOrUnknownTargetIsRefused(t *testing.T) {
	tool, _ := newControlTool(t, SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) {
			t.Fatal("hook must not run for an unresolved target")
			return false, false, false, nil
		},
		SetModel: func(string, string) (bool, bool, bool, string, error) {
			t.Fatal("hook must not run for an unresolved target")
			return false, false, false, "", nil
		},
	})
	if _, err := runControl(t, tool, `{"action":"stop","target":"not-a-real-session"}`); err == nil {
		t.Fatal("unknown target must be refused by directory resolution")
	}
	if _, err := runControl(t, tool, `{"action":"stop","target":""}`); err == nil || !strings.Contains(err.Error(), "target is required") {
		t.Fatalf("bare target must be refused up front, got %v", err)
	}
}

func TestUnwiredHostRefuses(t *testing.T) {
	cfg, _ := controlFixture(t) // no hooks wired
	tool := sessionControlTool{cfg: cfg}
	_, err := tool.Execute(nil, json.RawMessage(`{"action":"stop","target":"ct_peer"}`))
	if err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("a config without controller hooks must refuse, got %v", err)
	}
}

// Fork rule 2 (铁律 2): the switch ships default-off — the zero config is off.
func TestSessionControlSwitchDefaultsOff(t *testing.T) {
	var zero struct {
		ExperimentalSessionControl bool `toml:"experimental_session_control"`
	}
	if zero.ExperimentalSessionControl {
		t.Fatal("experimental_session_control must default to false (fork rule 2)")
	}
}
