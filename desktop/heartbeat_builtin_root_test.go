package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/config"
)

// TestHeartbeatRunScopeNormalizesBuiltinRoots pins the rule behind the task-197
// sidebar defect: a project-scope run pointed at one of the host's own
// directories must run as a global-scope run, because the sidebar can never
// carry a project node for those roots (task 186) and would index the created
// topic nowhere.
func TestHeartbeatRunScopeNormalizesBuiltinRoots(t *testing.T) {
	isolateDesktopUserDirs(t)
	builtin := globalWorkspaceRoot()
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatal(err)
	}
	userRoot := t.TempDir()

	for _, tc := range []struct {
		name          string
		scope         string
		workspaceRoot string
		wantScope     string
		wantRoot      string
	}{
		{name: "builtin root downgrades to global", scope: "project", workspaceRoot: builtin, wantScope: "global", wantRoot: ""},
		{name: "session dir downgrades to global", scope: "project", workspaceRoot: config.SessionDir(), wantScope: "global", wantRoot: ""},
		{name: "user project root is untouched", scope: "project", workspaceRoot: userRoot, wantScope: "project", wantRoot: userRoot},
		{name: "empty scope defaults to global", scope: "", wantScope: "global", wantRoot: ""},
		{name: "explicit global is untouched", scope: "global", workspaceRoot: builtin, wantScope: "global", wantRoot: builtin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotScope, gotRoot := heartbeatRunScope(tc.scope, tc.workspaceRoot)
			if gotScope != tc.wantScope || gotRoot != tc.wantRoot {
				t.Fatalf("heartbeatRunScope(%q, %q) = (%q, %q), want (%q, %q)",
					tc.scope, tc.workspaceRoot, gotScope, gotRoot, tc.wantScope, tc.wantRoot)
			}
		})
	}
}

// TestHeartbeatExecuteTaskListsBuiltinRootTopic is the task-197 acceptance check
// for "the session shows up in the left list and can be reopened": the field
// task that failed runs with scope=project and the global workspace as its
// root, and the created topic has to end up in a sidebar data source that
// carries that root — otherwise closing the tab loses the conversation.
func TestHeartbeatExecuteTaskListsBuiltinRootTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	builtin := globalWorkspaceRoot()
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	seed := HeartbeatTask{
		ID:            "iterintake-probe",
		Title:         "Iteration intake",
		Prompt:        "run the sweep",
		Scope:         "project",
		WorkspaceRoot: builtin,
		ApprovalMode:  "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	publishControllerAfterDelay(t, app, ctrl, 50*time.Millisecond)

	got := engine.executeTask(seed)

	if got.TopicID == "" {
		t.Fatal("run should have created a topic")
	}
	if got.LastRunAt == 0 {
		t.Fatal("run should have been recorded as executed")
	}
	if len(ctrl.submitted) != 1 || ctrl.submitted[0] != "run the sweep" {
		t.Fatalf("submitted prompts = %v, want [run the sweep]", ctrl.submitted)
	}
	if ctrl.approvalMode != "auto" {
		t.Fatalf("approval mode = %q, want the task's configured auto", ctrl.approvalMode)
	}
	// The Global section is the node that carries the builtin root, so that is
	// where the topic has to be indexed.
	if !topicIndexedInProjectsFile("", got.TopicID) {
		f := loadProjectsFile()
		t.Fatalf("globalTopics = %v, want the builtin-root topic %q listed", f.GlobalTopics, got.TopicID)
	}
	if !projectTreeContainsTopic(app.ListProjectTree(), got.TopicID) {
		t.Fatalf("sidebar tree has no node for the builtin-root topic %q", got.TopicID)
	}
	if _, err := os.Stat(filepath.Join(globalWorkspaceRoot())); err != nil {
		t.Fatalf("builtin root vanished: %v", err)
	}
}
