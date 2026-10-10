package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/servepool"
)

// Task 539: the desktop inbox gate answers session-scoped inbox enqueues for
// desktop-held sessions (steer with the durable queued fallback); unknown
// sessions forward to the serve untouched.

func newInboxGateFixture(t *testing.T) (*App, *WorkspaceTab, string) {
	t.Helper()
	root := t.TempDir()
	dir := config.ProjectSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sess-inbox-gate.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Sink: event.Discard})
	tab := &WorkspaceTab{ID: "inbox-gate-tab", SessionPath: path, WorkspaceRoot: root, Ctrl: ctrl}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}}
	return app, tab, path
}

func TestServePoolInboxGateSteersDesktopHeldSession(t *testing.T) {
	app, _, _ := newInboxGateFixture(t)
	body, err := json.Marshal(map[string]string{
		"input":  "先补测试再收尾",
		"intent": "steer",
	})
	if err != nil {
		t.Fatal(err)
	}
	handled, status, contentType, payload := app.servePoolInboxGate(servepool.InboxGateRequest{
		ProjectID:   "proj",
		ProjectRoot: app.tabs["inbox-gate-tab"].WorkspaceRoot,
		SessionName: "sess-inbox-gate",
		Intent:      "steer",
		Body:        body,
	})
	if !handled {
		t.Fatal("gate did not answer a desktop-held session")
	}
	if status != http.StatusAccepted || contentType != "application/json" {
		t.Fatalf("gate response = %d %q, want 202 json", status, contentType)
	}
	var receipt struct {
		ItemID      string `json:"itemID"`
		Disposition string `json:"disposition"`
	}
	if err := json.Unmarshal(payload, &receipt); err != nil || receipt.ItemID == "" {
		t.Fatalf("receipt = %q (err %v)", string(payload), err)
	}
	if receipt.Disposition == "" {
		t.Fatalf("receipt disposition empty: %q", string(payload))
	}
}

func TestServePoolInboxGateForwardsUnknownSession(t *testing.T) {
	app, tab, _ := newInboxGateFixture(t)
	handled, _, _, _ := app.servePoolInboxGate(servepool.InboxGateRequest{
		ProjectID:   "proj",
		ProjectRoot: tab.WorkspaceRoot,
		SessionName: "someone-elses-session",
		Intent:      "steer",
		Body:        []byte(`{"input":"x","intent":"steer"}`),
	})
	if handled {
		t.Fatal("gate answered a session no desktop tab holds; the serve fence must answer")
	}
}

func TestServePoolInboxGateRejectsEmptyInput(t *testing.T) {
	app, tab, _ := newInboxGateFixture(t)
	handled, status, _, _ := app.servePoolInboxGate(servepool.InboxGateRequest{
		ProjectID:   "proj",
		ProjectRoot: tab.WorkspaceRoot,
		SessionName: "sess-inbox-gate",
		Body:        []byte(`{"input":""}`),
	})
	if !handled || status != http.StatusBadRequest {
		t.Fatalf("gate = (%v, %d), want handled 400", handled, status)
	}
}
