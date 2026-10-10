package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
)

func TestFileRefsUseActiveTabWorkspaceRoot(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	launchRoot := robustTempDir(t)
	projectRoot := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(launchRoot, "launch-only.txt"), []byte("wrong"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "frontend", "wailsjs", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(projectRoot, "frontend", "wailsjs", "runtime", "runtime.js")
	if err := os.WriteFile(projectFile, []byte("right workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(launchRoot); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	tab := &WorkspaceTab{ID: "project", Scope: "project", WorkspaceRoot: projectRoot}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.activeTabID = tab.ID

	listed := app.ListDir("")
	if !hasDirEntry(listed, "frontend") {
		t.Fatalf("ListDir should list active project root, got %+v", listed)
	}
	if hasDirEntry(listed, "launch-only.txt") {
		t.Fatalf("ListDir leaked launch cwd entries, got %+v", listed)
	}

	found := app.SearchFileRefs("runtime.js")
	if !hasDirEntry(found, "frontend/wailsjs/runtime/runtime.js") {
		t.Fatalf("SearchFileRefs should search active project root, got %+v", found)
	}
	preview := app.ReadFile("frontend/wailsjs/runtime/runtime.js")
	if preview.Err != "" || preview.Body != "right workspace" {
		t.Fatalf("ReadFile active project preview = %+v, want project file", preview)
	}
}

func TestFileRefsForTabIgnoreActiveParentWorkspace(t *testing.T) {
	parentRoot := robustTempDir(t)
	childRoot := filepath.Join(parentRoot, "child")
	if err := os.MkdirAll(childRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentRoot, "parent-only.txt"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentRoot, "shared.txt"), []byte("parent shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childRoot, "child-only.txt"), []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childRoot, "shared.txt"), []byte("child shared"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"parent": {ID: "parent", Scope: "project", WorkspaceRoot: parentRoot},
			"child":  {ID: "child", Scope: "project", WorkspaceRoot: childRoot},
		},
		activeTabID: "parent",
	}

	listed := app.ListDirForTab("child", "")
	if !hasDirEntry(listed, "child-only.txt") || hasDirEntry(listed, "parent-only.txt") {
		t.Fatalf("ListDirForTab(child) = %+v, want only child workspace entries", listed)
	}
	found := app.SearchFileRefsForTab("child", "child-only")
	if !hasDirEntry(found, "child-only.txt") {
		t.Fatalf("SearchFileRefsForTab(child) = %+v, want child-only.txt", found)
	}
	preview := app.ReadFileForTab("child", "shared.txt")
	if preview.Err != "" || preview.Body != "child shared" {
		t.Fatalf("ReadFileForTab(child) = %+v, want child workspace file", preview)
	}
	path, ok, err := app.workspaceOrExternalPathForTab("child", "shared.txt")
	if err != nil || !ok || path != filepath.Join(childRoot, "shared.txt") {
		t.Fatalf("workspaceOrExternalPathForTab(child) = (%q, %v, %v)", path, ok, err)
	}

	legacy := app.ReadFile("shared.txt")
	if legacy.Err != "" || legacy.Body != "parent shared" {
		t.Fatalf("ReadFile legacy active-tab behavior = %+v, want parent workspace file", legacy)
	}
}

func TestFileRefsIncludeRegisteredExternalFolderChildren(t *testing.T) {
	workspace := robustTempDir(t)
	external := filepath.Join(robustTempDir(t), "Folder With Spaces")
	if err := os.MkdirAll(filepath.Join(external, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "src", "outside.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectedExternal := external
	if resolved, err := filepath.EvalSymlinks(external); err == nil {
		expectedExternal = resolved
	}
	expectedDisplayPath := filepath.ToSlash(expectedExternal)

	ctrl := &control.Controller{}
	token, _, err := ctrl.RegisterExternalFolderRef(external)
	if err != nil {
		t.Fatalf("RegisterExternalFolderRef: %v", err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"project": {ID: "project", WorkspaceRoot: workspace, Ctrl: ctrl},
			"other":   {ID: "other", WorkspaceRoot: robustTempDir(t)},
		},
		activeTabID: "other",
	}

	listed := app.ListDirForTab("project", token+"/src/")
	if len(listed) != 1 ||
		listed[0].Name != "outside.txt" ||
		listed[0].Path != token+"/src/outside.txt" ||
		listed[0].DisplayPath != expectedDisplayPath+"/src/outside.txt" {
		t.Fatalf("ListDir external src = %+v, want outside token/display path", listed)
	}

	found := app.SearchFileRefsForTab("project", "outside")
	var externalHit *DirEntry
	for i := range found {
		if found[i].Path == token+"/src/outside.txt" {
			externalHit = &found[i]
			break
		}
	}
	if externalHit == nil || externalHit.DisplayName != "Folder With Spaces/src/outside.txt" || externalHit.DisplayPath != expectedDisplayPath+"/src/outside.txt" {
		t.Fatalf("SearchFileRefs external hit = %+v, all results %+v", externalHit, found)
	}

	preview := app.ReadFileForTab("project", token+"/src/outside.txt")
	if preview.Err != "" || preview.Body != "outside" {
		t.Fatalf("ReadFile external token preview = %+v, want outside file body", preview)
	}
}

func TestDeleteSessionCancelsActiveRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "active.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	app := NewApp()
	activeCtrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test"})
	keepPath := filepath.Join(dir, "keep.jsonl")
	if err := os.WriteFile(keepPath, []byte(`{"role":"user","content":"keep"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write keep session: %v", err)
	}
	keepCtrl := control.New(control.Options{SessionDir: dir, SessionPath: keepPath, Label: "keep"})
	defer keepCtrl.Close()
	app.setTestCtrl(activeCtrl, "")
	app.tabs["keep"] = &WorkspaceTab{ID: "keep", Scope: "global", Ctrl: keepCtrl, Ready: true}
	app.tabOrder = []string{"test", "keep"}

	if err := app.DeleteSession(filepath.Base(path)); err != nil {
		t.Fatalf("DeleteSession(active basename): %v", err)
	}
	if _, ok := app.tabs["test"]; ok {
		t.Fatalf("deleted active session runtime should be removed")
	}
	if got := app.activeTabID; got != "keep" {
		t.Fatalf("active tab after delete = %q, want keep", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active session should be moved out of active history, stat err = %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, "active.jsonl", "active.jsonl")
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("active session should be moved to trash: %v", err)
	}
}

func TestDeleteSessionCancelsPreReadyBlankBuild(t *testing.T) {
	isolateDesktopUserDirs(t)

	globalRoot := globalTabWorkspaceRoot()
	dir := desktopSessionDir(globalRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "pre-ready-blank.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write blank session: %v", err)
	}
	cancelled := false
	blank := &WorkspaceTab{
		ID:            "blank",
		Scope:         "global",
		WorkspaceRoot: globalRoot,
		SessionPath:   path,
		buildCancel:   func() { cancelled = true },
		disabledMCP:   map[string]ServerView{},
	}
	keep := &WorkspaceTab{
		ID:            "keep",
		Scope:         "global",
		WorkspaceRoot: globalRoot,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"blank": blank, "keep": keep},
		tabOrder:    []string{"blank", "keep"},
		activeTabID: "blank",
	}

	if err := app.DeleteSession(filepath.Base(path)); err != nil {
		t.Fatalf("DeleteSession(pre-ready blank): %v", err)
	}
	if !cancelled {
		t.Fatal("pre-ready blank build was not cancelled")
	}
	if !blank.removed {
		t.Fatal("pre-ready blank tab was not marked removed")
	}
	if _, ok := app.tabs["blank"]; ok {
		t.Fatal("pre-ready blank tab should be removed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("blank session should be moved out of active history, stat err = %v", err)
	}
}

func TestDeleteLastTopicSessionFallbackDoesNotReuseDeletedTopic(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_delete_last"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Delete last"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := writeTopicSession(t, dir, "delete-last.jsonl", topicID, "Delete last", projectRoot)
	ctrl := controllerWithContent(t, path)
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"only": {
				ID:            "only",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    "Delete last",
				Ctrl:          ctrl,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"only"},
		activeTabID: "only",
	}

	if err := app.DeleteSession(path); err != nil {
		t.Fatalf("DeleteSession(last topic session): %v", err)
	}

	if _, ok := app.tabs["only"]; ok {
		t.Fatalf("deleted topic session tab should be removed")
	}
	for id, tab := range app.tabs {
		if tab.TopicID == topicID {
			t.Fatalf("fallback tab %q reused deleted topic %q", id, topicID)
		}
		if strings.TrimSpace(tab.TopicID) != "" {
			t.Fatalf("fallback tab %q topic ID = %q, want transient unindexed blank", id, tab.TopicID)
		}
	}
	trashPath := filepath.Join(dir, sessionTrashDir, "delete-last.jsonl", "delete-last.jsonl")
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("deleted session should be moved to trash: %v", err)
	}
}

func TestDeleteSessionFallbackKeepsTopicWithRemainingHistory(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_delete_keep_history"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Keep history"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := writeTopicSession(t, dir, "delete-one.jsonl", topicID, "Keep history", projectRoot)
	remainingPath := writeTopicSessionWithPrompt(t, dir, "remaining.jsonl", topicID, "Keep history", projectRoot, "remaining turn", time.Now().Add(-time.Minute))
	ctrl := controllerWithContent(t, path)
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"only": {
				ID:            "only",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    "Keep history",
				Ctrl:          ctrl,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"only"},
		activeTabID: "only",
	}

	if err := app.DeleteSession(path); err != nil {
		t.Fatalf("DeleteSession(topic with remaining history): %v", err)
	}

	found := false
	for _, tab := range app.tabs {
		if tab.TopicID == topicID {
			found = true
			if got := filepath.Clean(tab.currentSessionPath()); got != filepath.Clean(remainingPath) {
				t.Fatalf("fallback session path = %q, want remaining history %q", got, remainingPath)
			}
		}
	}
	if !found {
		t.Fatalf("fallback should keep topic %q when another session remains", topicID)
	}
}

func TestDeleteSessionWithStuckJobReturnsAfterSingleGrace(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "stuck-delete.jsonl")
	keepPath := filepath.Join(dir, "keep.jsonl")
	for _, p := range []string{path, keepPath} {
		if err := os.WriteFile(p, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write session %s: %v", p, err)
		}
	}

	grace := 500 * time.Millisecond
	teardownNotices := make(chan event.Event, 2)
	jm := jobs.NewManager(teardownNoticeSink(teardownNotices), jobs.WithTeardownGrace(grace))
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "test", Jobs: jm})
	keepCtrl := control.New(control.Options{SessionDir: dir, SessionPath: keepPath, Label: "keep"})
	releaseJob := startNonCooperativeSessionJob(t, jm, path)
	defer func() {
		releaseJob()
		ctrl.Close()
		keepCtrl.Close()
	}()

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.tabs["keep"] = &WorkspaceTab{ID: "keep", Scope: "global", Ctrl: keepCtrl, Ready: true}
	app.tabOrder = []string{"test", "keep"}

	start := time.Now()
	if err := app.DeleteSession(filepath.Base(path)); err != nil {
		t.Fatalf("DeleteSession(stuck job): %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > grace+2*time.Second {
		t.Fatalf("DeleteSession took %s, want one teardown grace plus bounded metadata I/O", elapsed)
	}
	assertSingleTeardownTimeoutNotice(t, teardownNotices, grace)
	if !agent.IsCleanupPending(path) {
		t.Fatalf("stuck delete should mark cleanup pending")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stuck session file should remain until delayed cleanup: %v", err)
	}
}

func TestDeleteSessionTrashConflictKeepsRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "active-conflict.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, sessionTrashDir, filepath.Base(path)), 0o755); err != nil {
		t.Fatalf("create trash conflict: %v", err)
	}

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "test"})
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	defer ctrl.Close()
	ctrl.Submit("work")
	<-runner.started

	err := app.DeleteSession(filepath.Base(path))
	if err != nil {
		t.Fatalf("DeleteSession should succeed after cleaning empty trash dir: %v", err)
	}
	if _, ok := app.tabs["test"]; ok {
		t.Fatalf("deleted session runtime should be removed from tabs")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file should be moved out of active history, stat err = %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(path), filepath.Base(path))
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("session should be moved to trash: %v", err)
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
}

func TestDeleteSessionValidTrashRemovesEmptyLiveStub(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "stale-live.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write live stub: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(path), filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(trashPath), 0o755); err != nil {
		t.Fatalf("create trash dir: %v", err)
	}
	if err := os.WriteFile(trashPath, []byte(`{"role":"user","content":"trashed"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write trash session: %v", err)
	}

	activePath := filepath.Join(dir, "active.jsonl")
	if err := os.WriteFile(activePath, []byte(`{"role":"user","content":"active"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write active session: %v", err)
	}
	activeCtrl := control.New(control.Options{SessionDir: dir, SessionPath: activePath, Label: "active"})
	defer activeCtrl.Close()
	app := &App{
		tabs:        map[string]*WorkspaceTab{"active": {ID: "active", Scope: "global", Ctrl: activeCtrl, Ready: true}},
		activeTabID: "active",
		tabOrder:    []string{"active"},
	}

	if err := app.DeleteSession(filepath.Base(path)); err != nil {
		t.Fatalf("DeleteSession should remove stale live stub: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("live stub should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("existing trash should remain authoritative: %v", err)
	}
}

func TestDeleteSessionValidTrashRemovesDuplicateLiveSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "duplicate-recovery.jsonl")
	content := []byte(`{"role":"user","content":"same recovery"}` + "\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write live session: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(path), filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(trashPath), 0o755); err != nil {
		t.Fatalf("create trash dir: %v", err)
	}
	if err := os.WriteFile(trashPath, content, 0o644); err != nil {
		t.Fatalf("write trash session: %v", err)
	}

	activePath := filepath.Join(dir, "active.jsonl")
	if err := os.WriteFile(activePath, []byte(`{"role":"user","content":"active"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write active session: %v", err)
	}
	activeCtrl := control.New(control.Options{SessionDir: dir, SessionPath: activePath, Label: "active"})
	defer activeCtrl.Close()
	app := &App{
		tabs:        map[string]*WorkspaceTab{"active": {ID: "active", Scope: "global", Ctrl: activeCtrl, Ready: true}},
		activeTabID: "active",
		tabOrder:    []string{"active"},
	}

	if err := app.DeleteSession(filepath.Base(path)); err != nil {
		t.Fatalf("DeleteSession should remove duplicate live session: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("duplicate live session should be removed, stat err = %v", err)
	}
	if got, err := os.ReadFile(trashPath); err != nil || string(got) != string(content) {
		t.Fatalf("existing trash should remain authoritative, got %q err=%v", string(got), err)
	}
}

func TestRestoreSessionRejectsOpenEmptyLiveStub(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "restore-open.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"trashed"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write trash source: %v", err)
	}
	if err := deleteSessionFile(dir, path); err != nil {
		t.Fatalf("trash source: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(path), filepath.Base(path))
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write live stub: %v", err)
	}
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "open"})
	defer ctrl.Close()
	app := &App{
		tabs:        map[string]*WorkspaceTab{"open": {ID: "open", Scope: "global", Ctrl: ctrl, Ready: true}},
		tabOrder:    []string{"open"},
		activeTabID: "open",
	}

	err := app.RestoreSession(trashPath)
	if err == nil || !strings.Contains(err.Error(), "session is open") {
		t.Fatalf("RestoreSession error = %v, want open-session rejection", err)
	}
	if info, statErr := os.Stat(path); statErr != nil || info.Size() != 0 {
		t.Fatalf("open live stub should remain empty, info=%v err=%v", info, statErr)
	}
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("trash session should remain after rejected restore: %v", err)
	}
}

func TestDeleteSessionValidTrashRenamesDifferentLiveConflict(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "real-live.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"new work"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write live session: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(path), filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(trashPath), 0o755); err != nil {
		t.Fatalf("create trash dir: %v", err)
	}
	if err := os.WriteFile(trashPath, []byte(`{"role":"user","content":"trashed"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write trash session: %v", err)
	}

	activeCtrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "active"})
	defer activeCtrl.Close()
	app := NewApp()
	app.setTestCtrl(activeCtrl, "")

	if err := app.DeleteSession(filepath.Base(path)); err != nil {
		t.Fatalf("DeleteSession should move different live session to a unique trash item: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("live session should be moved out of active history, stat err = %v", err)
	}
	if got, err := os.ReadFile(trashPath); err != nil || !strings.Contains(string(got), "trashed") {
		t.Fatalf("original trash session should remain, got %q err=%v", string(got), err)
	}
	trashed, err := listTrashedSessionFiles(dir)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	var renamedPath string
	for _, candidate := range trashed {
		if candidate != trashPath && filepath.Base(candidate) == filepath.Base(path) {
			renamedPath = candidate
			break
		}
	}
	if renamedPath == "" {
		t.Fatalf("renamed trash copy not found in %#v", trashed)
	}
	if filepath.Base(filepath.Dir(renamedPath)) == filepath.Base(path) {
		t.Fatalf("renamed trash copy reused fixed trash item dir: %s", renamedPath)
	}
	if got, err := os.ReadFile(renamedPath); err != nil || !strings.Contains(string(got), "new work") {
		t.Fatalf("renamed trash session = %q err=%v, want live content", string(got), err)
	}
}

func TestDeleteSessionCancelsInactiveOpenRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	activePath := filepath.Join(dir, "active.jsonl")
	inactivePath := filepath.Join(dir, "inactive.jsonl")
	otherPath := filepath.Join(dir, "other.jsonl")
	for _, path := range []string{activePath, inactivePath, otherPath} {
		if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write session %s: %v", path, err)
		}
	}

	activeCtrl := control.New(control.Options{SessionDir: dir, SessionPath: activePath, Label: "active"})
	inactiveCtrl := control.New(control.Options{SessionDir: dir, SessionPath: inactivePath, Label: "inactive"})
	defer activeCtrl.Close()
	defer inactiveCtrl.Close()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"active":   {ID: "active", Scope: "global", Ctrl: activeCtrl, Ready: true},
			"inactive": {ID: "inactive", Scope: "global", Ctrl: inactiveCtrl, Ready: true},
		},
		tabOrder:    []string{"active", "inactive"},
		activeTabID: "active",
	}
	installSessionCatalogForTest(t, app, dir, "global", "")
	if err := app.DeleteSession(filepath.Base(inactivePath)); err != nil {
		t.Fatalf("DeleteSession(inactive open basename): %v", err)
	}
	if _, ok := app.tabs["inactive"]; ok {
		t.Fatalf("deleted inactive session runtime should be removed")
	}
	if _, err := os.Stat(inactivePath); !os.IsNotExist(err) {
		t.Fatalf("inactive open session should be moved out of active history, stat err = %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, "inactive.jsonl", "inactive.jsonl")
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("inactive open session should be moved to trash: %v", err)
	}

	sessions := app.ListSessions()
	current := map[string]bool{}
	open := map[string]bool{}
	for _, s := range sessions {
		current[filepath.Base(s.Path)] = s.Current
		open[filepath.Base(s.Path)] = s.Open
	}
	if !current[filepath.Base(activePath)] {
		t.Fatalf("ListSessions should mark active session current, got %#v", current)
	}
	if current[filepath.Base(otherPath)] {
		t.Fatalf("ListSessions marked unopened session current, got %#v", current)
	}
	if !open[filepath.Base(activePath)] {
		t.Fatalf("ListSessions should mark active and inactive open sessions open, got %#v", open)
	}
	if open[filepath.Base(inactivePath)] || open[filepath.Base(otherPath)] {
		t.Fatalf("ListSessions marked unopened session open, got %#v", open)
	}
}
