package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/checkpoint"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1724 pins the unified invalid-scope contract across the desktop rewind
// entry points. Before, PreviewRewindForTab and CommitRewindForTab silently
// widened any unknown scope to RewindBoth (forking the conversation on a
// frontend typo) while serve returned 400 for the same input. Now every entry
// point rejects through control.ParseRewindScope with the identical
// control.ErrRewindInvalidScope text.
func TestRewindInvalidScopeSameOutcomeAcrossDesktopEntryPoints(t *testing.T) {
	isolateDesktopUserDirsSchemaOne(t)
	dir := t.TempDir()
	root := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	ckptDir := sessionPath[:len(sessionPath)-len(".jsonl")] + ".ckpt"
	if err := os.MkdirAll(ckptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(root, "a.txt")
	if err := os.WriteFile(filePath, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	diskMode := uint32(fileInfo.Mode().Perm())
	before := "before"
	afterExists := true
	seedCheckpoint(t, ckptDir, checkpoint.Checkpoint{
		SchemaVersion: checkpoint.SchemaV2, Turn: 1, Time: time.Now(), Prompt: "edit", MsgIndex: 3,
		Coverage: checkpoint.CoverageComplete,
		Files: []checkpoint.FileSnap{{
			Path: "a.txt", Content: &before, SHA256: checkpoint.Digest([]byte(before)), Mode: diskMode,
			AfterExisted: &afterExists, AfterSHA256: checkpoint.Digest([]byte("after")), AfterMode: diskMode,
			CaptureSource: checkpoint.CaptureBeforeMutation,
		}},
	})
	session := agent.NewSession("")
	session.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "answer"},
		{Role: provider.RoleUser, Content: "edit"},
		{Role: provider.RoleAssistant, Content: "done"},
	})
	if err := session.Save(sessionPath); err != nil {
		t.Fatal(err)
	}
	ag := agent.New(nil, nil, session, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, Runner: ag, SessionDir: dir, SessionPath: sessionPath, WorkspaceRoot: root, Label: "test"})
	app := NewApp()
	app.setTestCtrl(ctrl, "test")
	app.tabs["test"].WorkspaceRoot = root
	defer func() {
		for _, tab := range app.tabs {
			if tab != nil && tab.Ctrl != nil {
				tab.Ctrl.Close()
			}
		}
	}()

	// Sanity: the fixture admits a valid "both" preview, so the rejections
	// below cannot be mistaken for a broken fixture or missing checkpoint.
	if plan := app.PreviewRewindForTab("test", 1, "both"); !plan.OK {
		t.Fatalf("valid-scope control preview = %+v, want OK fixture", plan)
	}

	wantErr := control.ErrRewindInvalidScope.Error()
	for _, scope := range []string{"bogus", "", "CODE"} {
		plan := app.PreviewRewindForTab("test", 1, scope)
		if plan.OK {
			t.Fatalf("preview scope=%q: OK=true, want rejection", scope)
		}
		if plan.Error != wantErr {
			t.Fatalf("preview scope=%q: error = %q, want shared contract text %q", scope, plan.Error, wantErr)
		}
		if plan.PlanID != "" {
			t.Fatalf("preview scope=%q: leaked plan %q, want no plan", scope, plan.PlanID)
		}

		err := app.RewindForTab("test", 1, scope)
		if err == nil {
			t.Fatalf("RewindForTab scope=%q: nil error, want rejection", scope)
		}
		if err.Error() != wantErr {
			t.Fatalf("RewindForTab scope=%q: error = %q, want shared contract text %q", scope, err.Error(), wantErr)
		}
		result := app.CommitRewindForTab("test", "", 1, scope)
		if result.OK {
			t.Fatalf("commit scope=%q: OK=true, want rejection", scope)
		}
		if result.Error != wantErr {
			t.Fatalf("commit scope=%q: error = %q, want shared contract text %q", scope, result.Error, wantErr)
		}
	}

	// Zero side effects: no file touched, session intact, no fork tab spawned.
	if got, err := os.ReadFile(filePath); err != nil || string(got) != "after" {
		t.Fatalf("file after invalid scopes = %q err=%v, want untouched \"after\"", got, err)
	}
	if got := ctrl.History(); len(got) != 5 {
		t.Fatalf("history after invalid scopes = %d messages, want 5", len(got))
	}
	if len(app.tabs) != 1 {
		t.Fatalf("tabs after invalid scopes = %d, want 1 (no fork tab)", len(app.tabs))
	}
}
