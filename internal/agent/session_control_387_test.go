package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// Task 387: the set_model face gains the capability-gating list's semantics —
// refusal to act on SELF, an audit line (caller/target/old model/new model),
// and the effort note on the receipt. The invalid-model refusal is the host
// hook's actionable error, verified to pass through untouched.

func Test387SetModelRefusesToActOnSelf(t *testing.T) {
	cfg, contact := controlFixture(t)
	// The calling session IS the peer: CurrentSessionPath points at the peer
	// transcript, so currentContactID() resolves to the same contact.
	cfg.CurrentSessionPath = cfg.SessionDir + "/peer.jsonl"
	cfg.SessionControl = SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, true, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) {
			t.Fatal("SetModel must never run for a self-targeted change")
			return false, false, true, "", nil
		},
	}
	tool := NewSessionControlTool(cfg)
	_, err := tool.Execute(context.Background(), jsonRaw(`{"action":"set_model","target":"`+contact+`","model":"deepseek/deepseek-v4-flash"}`))
	if err == nil {
		t.Fatal("set_model on the CALLING session must be refused (task 387 非己)")
	}
	for _, want := range []string{"calling session itself", "switcher"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("self-refusal must name %q, got: %v", want, err)
		}
	}
}

func Test387SetModelAuditsCallerTargetAndModels(t *testing.T) {
	cfg, contact := controlFixture(t)
	var oldSeen, newSeen string
	cfg.SessionInfo = func(id string) (string, string, bool) {
		if id == contact {
			return "old-provider/old-model", "old-provider", true
		}
		return "", "", false
	}
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })

	cfg.SessionControl = SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, true, nil },
		SetModel: func(id string, model string) (bool, bool, bool, string, error) {
			oldSeen, newSeen = id, model
			return true, false, true, "new-provider/new-model", nil
		},
	}
	tool := NewSessionControlTool(cfg)
	out, err := tool.Execute(context.Background(), jsonRaw(`{"action":"set_model","target":"`+contact+`","model":"new-provider/new-model"}`))
	if err != nil {
		t.Fatalf("set_model: %v", err)
	}
	if oldSeen != contact || newSeen != "new-provider/new-model" {
		t.Fatalf("hook invoked with the resolved contact and model: %q %q", oldSeen, newSeen)
	}
	audit := buf.String()
	for _, want := range []string{"cross-session model change", "caller=", "target=" + contact, "old_model=old-provider/old-model", "new_model=new-provider/new-model"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit line must carry %q, got: %s", want, audit)
		}
	}
	for _, want := range []string{`"modelRef":"new-provider/new-model"`, `"oldModel":"old-provider/old-model"`} {
		if !strings.Contains(out, want) {
			t.Errorf("receipt must carry %s, got: %s", want, out)
		}
	}
}

func Test387SetModelReceiptCarriesEffortNote(t *testing.T) {
	cfg, contact := controlFixture(t)
	cfg.SessionControl = SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, true, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) {
			return true, false, true, "new-provider/new-model", nil
		},
	}
	tool := NewSessionControlTool(cfg)
	out, err := tool.Execute(context.Background(), jsonRaw(`{"action":"set_model","target":"`+contact+`","model":"new-provider/new-model"}`))
	if err != nil {
		t.Fatalf("set_model: %v", err)
	}
	if !strings.Contains(out, "effort") || !strings.Contains(out, "resets") {
		t.Fatalf("receipt must surface the effort-reset warning, got: %s", out)
	}
}

func Test387UnknownModelPassesThroughActionableError(t *testing.T) {
	cfg, contact := controlFixture(t)
	cfg.SessionControl = SessionControlHooks{
		Stop: func(string) (bool, bool, bool, error) { return false, false, true, nil },
		SetModel: func(string, string) (bool, bool, bool, string, error) {
			return false, false, true, "", errStub("unknown model \"nope/nope\" — available models on that session's workspace: a/one, a/two, b/three")
		},
	}
	tool := NewSessionControlTool(cfg)
	_, err := tool.Execute(context.Background(), jsonRaw(`{"action":"set_model","target":"`+contact+`","model":"nope/nope"}`))
	if err == nil {
		t.Fatal("an unknown model must be refused")
	}
	for _, want := range []string{"unknown model", "available models on that session's workspace", "a/one", "b/three"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the actionable refusal must carry %q, got: %v", want, err)
		}
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

func jsonRaw(s string) []byte { return []byte(s) }
