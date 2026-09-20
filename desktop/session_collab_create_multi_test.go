package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// writeCollabTestConfig installs a two-provider config so model pins can be
// validated: "fake" exposes "chat", "other" exposes "chat" as well — the same
// model name on two endpoints, which is exactly why a bare id cannot be
// accepted (task 162 acceptance 3).
func writeCollabTestConfig(t *testing.T) {
	t.Helper()
	t.Setenv("FAKE_KEY", "sk-test")
	t.Setenv("OTHER_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := `
default_model = "fake/chat"

[[providers]]
name = "fake"
kind = "openai"
base_url = "https://fake.invalid/v1"
model = "chat"
api_key_env = "FAKE_KEY"

[[providers]]
name = "other"
kind = "openai"
base_url = "https://other.invalid/v1"
model = "chat"
api_key_env = "OTHER_KEY"
`
	if err := os.WriteFile(config.UserConfigPath(), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestCreateCollabSessionBatchCreatesEveryItemAndReportsFailures pins task 166:
// one call creates several sessions, a bad item does not roll back the good ones,
// and each item can override the top-level group.
func TestCreateCollabSessionBatchCreatesEveryItemAndReportsFailures(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	result, err := app.createCollabSession(agent.CreateCollabSessionRequest{
		Group: "批量组",
		Sessions: []agent.CreateCollabSessionItem{
			{Title: "前端专家", Purpose: "React 前端"},
			{Title: "后端专家", Purpose: "Go 后端", Group: "另一组"},
			{Title: "", Purpose: "missing title"},
		},
	})
	if err != nil {
		t.Fatalf("batch create returned an error instead of partial success: %v", err)
	}
	if len(result.Created) != 2 {
		t.Fatalf("created = %d, want 2 (a bad item must not roll back the others): %+v", len(result.Created), result)
	}
	if len(result.Failed) != 1 || !strings.Contains(result.Failed[0].Reason, "title and purpose") {
		t.Fatalf("failed = %+v, want one title/purpose refusal", result.Failed)
	}
	for _, item := range result.Created {
		if item.ContactID == "" || item.SessionPath == "" || item.TopicID == "" {
			t.Fatalf("created item is not registered/addressable: %+v", item)
		}
	}
	if result.Created[0].Group != "批量组" {
		t.Fatalf("first item group = %q, want the top-level default", result.Created[0].Group)
	}
	if result.Created[1].Group != "另一组" {
		t.Fatalf("second item group = %q, want its own override", result.Created[1].Group)
	}
}

// TestCreateCollabSessionModelPinValidation pins task 162: a provider/model ref
// is accepted, and every ambiguous or unknown ref is refused with an actionable
// reason instead of silently picking a provider.
func TestCreateCollabSessionModelPinValidation(t *testing.T) {
	isolateDesktopUserDirs(t)
	writeCollabTestConfig(t)
	app := NewApp()

	create := func(title, model string) (agent.CreateCollabSessionResult, error) {
		return app.createCollabSession(agent.CreateCollabSessionRequest{
			Title: title, Purpose: "model pin", Group: "模型组", Model: model,
		})
	}

	ok, err := create("pinned", "fake/chat")
	if err != nil {
		t.Fatalf("explicit provider/model ref was refused: %v", err)
	}
	if ok.Model != "fake/chat" {
		t.Fatalf("result model = %q, want the pinned ref", ok.Model)
	}
	stored, found := agent.LoadSessionModel(ok.SessionPath)
	if !found || stored != "fake/chat" {
		t.Fatalf("session meta model = %q/%v, want the pinned ref so the session starts on it", stored, found)
	}

	for _, tc := range []struct{ name, model, want string }{
		{"bare id", "chat", "must be a `provider/model` ref"},
		{"unknown provider", "nope/chat", "is not configured"},
		{"unknown model", "fake/missing", "does not expose model"},
	} {
		result, err := create("bad-"+tc.name, tc.model)
		if err == nil {
			t.Fatalf("%s (%q) was accepted; a session must not be created on a guessed model", tc.name, tc.model)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s error = %q, want it to mention %q", tc.name, err.Error(), tc.want)
		}
		if len(result.Created) != 0 {
			t.Fatalf("%s created %d sessions despite the refusal", tc.name, len(result.Created))
		}
	}

	// No pin at all: behaviour is unchanged (the session resolves its default at
	// first open), which is what makes this parameter additive.
	def, err := create("default", "")
	if err != nil {
		t.Fatalf("unpinned create failed: %v", err)
	}
	if def.Model != "" {
		t.Fatalf("unpinned result model = %q, want empty (no pin written)", def.Model)
	}
	if stored, found := agent.LoadSessionModel(def.SessionPath); found && stored != "" {
		t.Fatalf("unpinned session wrote model %q; the default must stay a first-open decision", stored)
	}
}

// TestCreateCollabSessionQueuesFirstMessage pins task 167: `message` reaches the
// new session's mailbox in the same call, defaults to steer, and reports a
// messageId.
func TestCreateCollabSessionQueuesFirstMessage(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	result, err := app.createCollabSession(agent.CreateCollabSessionRequest{
		Title: "启动即干活", Purpose: "first message", Group: "启动组",
		Message: "请先读 docs/tasklist/02-待办-UI与交互.md 的任务 160。",
	})
	if err != nil {
		t.Fatalf("create with first message: %v", err)
	}
	if result.MessageID == "" {
		t.Fatalf("messageId missing: %+v", result)
	}
	if len(result.Created) != 1 || result.Created[0].MessageID != result.MessageID {
		t.Fatalf("per-item messageId = %+v, want the queued id", result.Created)
	}
	if result.Delivery != "steer" {
		t.Fatalf("delivery = %q, want steer by default so the new session starts", result.Delivery)
	}
	// The new session has no control runtime yet (nobody opened it), so the pump
	// cannot inject — the message must still be sitting in its mailbox, which is
	// what "not lost while the target is closed" means (task 167 acceptance 3).
	store := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	pending, refused, cerr := store.Claim(result.Created[0].ContactID)
	if cerr != nil {
		t.Fatalf("read mailbox: %v", cerr)
	}
	mailbox := append(append([]sessioncollab.MailMessage(nil), pending...), refused...)
	if len(mailbox) != 1 || !strings.Contains(mailbox[0].Body, "任务 160") {
		t.Fatalf("mailbox entries = %+v, want the first message body", mailbox)
	}
	// Explicit followup keeps its own semantics (queue, do not interrupt).
	followupResult, err := app.createCollabSession(agent.CreateCollabSessionRequest{
		Title: "排队型", Purpose: "followup", Group: "启动组",
		Message: "不着急", Delivery: "followup",
	})
	if err != nil {
		t.Fatalf("create with followup: %v", err)
	}
	if followupResult.Delivery != "followup" {
		t.Fatalf("delivery = %q, want the explicit followup", followupResult.Delivery)
	}
}
