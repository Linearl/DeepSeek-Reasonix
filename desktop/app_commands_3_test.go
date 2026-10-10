package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
)

func TestTrashTopicRejectsBackgroundJob(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_stuck_trash"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Stuck trash"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, dir, "stuck-topic.jsonl", topicID, "Stuck trash", projectRoot)

	jm := jobs.NewManager(event.Discard)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: sessionPath, Label: "test", Jobs: jm, WorkspaceRoot: projectRoot})
	releaseJob := startNonCooperativeSessionJob(t, jm, sessionPath)
	defer func() {
		releaseJob()
		ctrl.Close()
	}()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"stuck": {
				ID:            "stuck",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    "Stuck trash",
				Ctrl:          ctrl,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
			"keep": {
				ID:            "keep",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       "topic_keep",
				TopicTitle:    "Keep",
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"stuck", "keep"},
		activeTabID: "stuck",
	}

	if err := app.TrashTopic(topicID); !errors.Is(err, errTopicHasActiveWork) {
		t.Fatalf("TrashTopic(background job) error = %v, want %v", err, errTopicHasActiveWork)
	}
	if _, ok := app.tabs["stuck"]; !ok {
		t.Fatal("rejected archive should keep the background-job topic tab")
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("rejected archive should not mark session cleanup pending")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("rejected archive should preserve the live session: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, "stuck-topic.jsonl", "stuck-topic.jsonl")
	if _, err := os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatalf("rejected archive created a trash entry, stat err = %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "Stuck trash" {
		t.Fatalf("rejected archive topic title = %q, want Stuck trash", got)
	}
}

func teardownNoticeSink(out chan<- event.Event) event.Sink {
	return event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && strings.Contains(e.Detail, "background job teardown timed out") {
			out <- e
		}
	})
}

func assertSingleTeardownTimeoutNotice(t *testing.T, notices <-chan event.Event, grace time.Duration) {
	t.Helper()
	var notice event.Event
	select {
	case notice = <-notices:
	default:
		t.Fatal("missing background-job teardown timeout notice")
	}
	var waited time.Duration
	for field := range strings.FieldsSeq(notice.Detail) {
		if !strings.HasPrefix(field, "waited=") {
			continue
		}
		parsed, err := time.ParseDuration(strings.TrimSuffix(strings.TrimPrefix(field, "waited="), ";"))
		if err != nil {
			t.Fatalf("parse teardown waited field %q: %v", field, err)
		}
		waited = parsed
		break
	}
	if waited < grace-10*time.Millisecond || waited > grace+250*time.Millisecond {
		t.Fatalf("teardown notice waited %s, want one %s grace; detail: %s", waited, grace, notice.Detail)
	}
	select {
	case extra := <-notices:
		t.Fatalf("duplicate teardown timeout notice: %+v", extra)
	default:
	}
}

func TestWaitDestroyHandlesWaitsConcurrently(t *testing.T) {
	started := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()

	handle := func(id int) control.SessionDestroyHandle {
		return control.SessionDestroyHandle{Wait: func() jobs.TeardownResult {
			started <- id
			<-release
			return jobs.TeardownResult{TimedOut: []jobs.TeardownJob{{ID: strconv.Itoa(id)}}}
		}}
	}
	done := make(chan bool, 1)
	go func() { done <- waitDestroyHandles([]control.SessionDestroyHandle{handle(1), handle(2)}) }()

	seen := map[int]bool{}
	for len(seen) < 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("destroy waits did not start concurrently; started=%v", seen)
		}
	}
	releaseAll()
	select {
	case timedOut := <-done:
		if !timedOut {
			t.Fatal("waitDestroyHandles lost timed-out result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitDestroyHandles did not return after all waits completed")
	}
}

func TestRestoreSessionRejectsDestroyingSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "trash-me.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := deleteSessionFile(dir, sessionPath); err != nil {
		t.Fatalf("deleteSessionFile: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(sessionPath), filepath.Base(sessionPath))

	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: filepath.Join(dir, "active.jsonl"), Label: "active", Jobs: jm})
	defer ctrl.Close()
	destroy := ctrl.BeginDestroySession(sessionPath)
	defer destroy.Finish()

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	if err := app.RestoreSession(trashPath); err == nil || !strings.Contains(err.Error(), "cleanup is still in progress") {
		t.Fatalf("RestoreSession while destroying error = %v, want cleanup-in-progress", err)
	}
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("trashed session should remain after rejected restore: %v", err)
	}

	destroy.Finish()
	if err := app.RestoreSession(trashPath); err != nil {
		t.Fatalf("RestoreSession after finish: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session should be restored: %v", err)
	}
}

func TestDesktopSessionAPIsUseControllerSessionDir(t *testing.T) {
	isolateDesktopUserDirs(t)
	dirA := filepath.Join(t.TempDir(), "workspace-a-sessions")
	dirB := filepath.Join(t.TempDir(), "workspace-b-sessions")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatalf("mkdir dirA: %v", err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatalf("mkdir dirB: %v", err)
	}
	pathA := filepath.Join(dirA, "a.jsonl")
	pathB := filepath.Join(dirB, "b.jsonl")
	if err := os.WriteFile(pathA, []byte(`{"role":"user","content":"workspace A"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write pathA: %v", err)
	}
	if err := os.WriteFile(pathB, []byte(`{"role":"user","content":"workspace B"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write pathB: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dirA, SessionPath: pathA, Label: "test"}), "")
	defer app.activeCtrl().Close()
	installSessionCatalogForTest(t, app, dirA, "global", "")
	sessions := app.ListSessions()
	if len(sessions) != 1 || sessions[0].Path != pathA || sessions[0].TurnsState != "unknown" ||
		!strings.Contains(sessions[0].Preview, "being indexed") {
		t.Fatalf("ListSessions should read the active controller session dir only, got %+v", sessions)
	}
	if err := app.RenameSession(pathA, "A title"); err != nil {
		t.Fatalf("RenameSession in active session dir: %v", err)
	}
	meta, ok, err := agent.LoadBranchMeta(pathA)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta after RenameSession ok=%v err=%v", ok, err)
	}
	if meta.CustomTitle != "A title" {
		t.Fatalf("custom title should be written to branch meta, got %q", meta.CustomTitle)
	}
	reconcileSessionCatalogForTest(t, app, dirA, "global", "")
	sessions = app.ListSessions()
	if len(sessions) != 1 || sessions[0].Title != "A title" {
		t.Fatalf("ListSessions should return custom title from branch meta, got %+v", sessions)
	}
	if titles := loadSessionTitles(dirA); titles["a.jsonl"] != "A title" {
		t.Fatalf("title should be written beside the active session, got %+v", titles)
	}
	if titles := loadSessionTitles(dirB); len(titles) != 0 {
		t.Fatalf("inactive workspace title sidecar should remain untouched, got %+v", titles)
	}
}

func TestListSessionsMarksAutoBotSessionAsChannel(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "bot-channel.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"from channel"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	cfg := config.Default()
	cfg.Bot.Connections = []config.BotConnectionConfig{{
		ID: "weixin-weixin", Provider: "weixin", Domain: "weixin", Label: "微信", Enabled: true, Status: "connected",
		SessionMappings: []config.BotConnectionSessionMapping{{
			RemoteID: "wx-chat-1", SessionID: "path:" + path, SessionSource: "auto",
		}},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dir, SessionPath: filepath.Join(dir, "active.jsonl"), Label: "test"}), "")
	defer app.activeCtrl().Close()
	installSessionCatalogForTest(t, app, dir, "global", "")
	sessions := app.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("ListSessions len = %d, want 1: %+v", len(sessions), sessions)
	}
	got := sessions[0]
	if got.Kind != "channel" || got.Channel != "weixin" || got.ChannelLabel != "微信" || got.RemoteID != "wx-chat-1" || got.SessionSource != "auto" {
		t.Fatalf("channel session meta = %+v", got)
	}
}

func TestDeleteSessionClearsAutoBotSessionMapping(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "bot-channel.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"from channel"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	other := filepath.Join(dir, "other-channel.jsonl")
	cfg := config.Default()
	cfg.Bot.Connections = []config.BotConnectionConfig{{
		ID: "weixin-weixin", Provider: "weixin", Domain: "weixin", Label: "微信", Enabled: true, Status: "connected",
		SessionMappings: []config.BotConnectionSessionMapping{
			{RemoteID: "remove-auto", SessionID: "path:" + path, SessionSource: "auto"},
			{RemoteID: "keep-explicit", SessionID: "path:" + path},
			{RemoteID: "keep-other-auto", SessionID: "path:" + other, SessionSource: "auto"},
		},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: filepath.Join(dir, "active.jsonl"), Label: "test"})
	app.setTestCtrl(ctrl, "")
	defer app.activeCtrl().Close()

	if err := app.DeleteSession(path); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	mappings := got.Bot.Connections[0].SessionMappings
	if len(mappings) != 2 {
		t.Fatalf("session mappings = %+v, want explicit and other auto mappings preserved", mappings)
	}
	for _, mapping := range mappings {
		if mapping.RemoteID == "remove-auto" {
			t.Fatalf("deleted session auto mapping was preserved: %+v", mappings)
		}
	}
}

func TestOpenChannelSessionForTabIsReadOnly(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "bot-channel.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"from channel"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: filepath.Join(dir, "active.jsonl"), Label: "test"})
	app.setTestCtrl(ctrl, "")
	defer app.activeCtrl().Close()

	if _, err := app.OpenChannelSessionForTab("test", path); err != nil {
		t.Fatalf("OpenChannelSessionForTab: %v", err)
	}
	if meta := app.tabMeta(app.activeTab(), true); !meta.ReadOnly {
		t.Fatalf("channel tab should be read-only: %+v", meta)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	app.SubmitToTab("test", "must not append")
	app.RunShellForTab("test", "echo must-not-run")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("read-only channel transcript changed:\nbefore=%s\nafter=%s", before, after)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString(`{"role":"user","content":"external follow-up"}` + "\n"); err != nil {
		f.Close()
		t.Fatalf("append external message: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close append: %v", err)
	}
	app.snapshotAllTabs()
	afterSnapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after snapshot: %v", err)
	}
	if !strings.Contains(string(afterSnapshot), "external follow-up") {
		t.Fatalf("read-only channel snapshot overwrote external append:\n%s", afterSnapshot)
	}
}

func TestUserTriggeredCommandsReturnErrorsWhenUnavailable(t *testing.T) {
	tests := []struct {
		name string
		app  *App
		call func(*App) error
		want string
	}{
		{
			name: "submit read-only",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global", ReadOnly: true}},
				activeTabID: "test",
			},
			call: func(app *App) error { return app.SubmitToTab("test", "hello") },
			want: "read-only",
		},
		{
			name: "submit workspace unavailable",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global", StartupErr: "boom"}},
				activeTabID: "test",
			},
			call: func(app *App) error { return app.SubmitToTab("test", "hello") },
			want: "workspace failed to start: boom",
		},
		{
			name: "run shell workspace unavailable",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global"}},
				activeTabID: "test",
			},
			call: func(app *App) error { return app.RunShellForTab("test", "echo hi") },
			want: "workspace is still starting",
		},
		{
			name: "steer workspace unavailable",
			app: &App{
				tabs:        map[string]*WorkspaceTab{"test": {ID: "test", Scope: "global"}},
				activeTabID: "test",
			},
			call: func(app *App) error { return app.SteerForTab("test", "please continue") },
			want: "workspace is still starting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(tt.app)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want to contain %q", err, tt.want)
			}
		})
	}
}

func TestSubmitEntryPointsRejectEmptyProviderInput(t *testing.T) {
	app := NewApp()
	for _, tt := range []struct {
		name string
		call func() error
	}{
		{name: "plain", call: func() error { return app.SubmitToTab("missing", " \n\t ") }},
		{name: "display", call: func() error { return app.SubmitDisplayToTab("missing", "visible prompt", " ") }},
		{name: "delivery recovery", call: func() error {
			return app.SubmitDeliveryRecoveryToTab("missing", "visible prompt", "")
		}},
		{name: "invocations", call: func() error {
			return app.SubmitInvocationsToTab("missing", "/skill visible", "", nil)
		}},
		{name: "edited display", call: func() error {
			return app.SubmitEditedDisplayToTab("missing", "visible prompt", "\n", "original prompt")
		}},
		{name: "initial goal", call: func() error {
			_, err := app.SubmitInitialGoalToTab("missing", "goal", "visible prompt", "", nil, "normal", "auto")
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, errEmptyTurnInput) {
				t.Fatalf("error = %v, want errEmptyTurnInput", err)
			}
		})
	}
}

func TestInvocationEntryPointsAllowEmptyExplicitTaskForSkillOnlyTurn(t *testing.T) {
	invocations := []InvocationRequest{{Name: "skill", Kind: "skill"}}
	if err := validateInvocationTurnInput("", invocations); err != nil {
		t.Fatalf("skill-only invocation input rejected: %v", err)
	}
	if err := validateInvocationTurnInput("", nil); !errors.Is(err, errEmptyTurnInput) {
		t.Fatalf("empty input without invocations = %v, want errEmptyTurnInput", err)
	}
}

func TestCloseReadOnlyChannelTabDoesNotSnapshotTranscript(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "bot-channel.jsonl")
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"from channel"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: filepath.Join(dir, "active.jsonl"), Label: "test"})
	app.setTestCtrl(ctrl, "")
	defer ctrl.Close()

	if _, err := app.OpenChannelSessionForTab("test", path); err != nil {
		t.Fatalf("OpenChannelSessionForTab: %v", err)
	}
	app.mu.Lock()
	app.tabs["survivor"] = &WorkspaceTab{ID: "survivor", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	app.tabOrder = []string{"test", "survivor"}
	app.activeTabID = "test"
	app.mu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString(`{"role":"user","content":"external close follow-up"}` + "\n"); err != nil {
		f.Close()
		t.Fatalf("append external message: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close append: %v", err)
	}

	if err := app.CloseTab("test"); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}
	afterClose, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after close: %v", err)
	}
	if !strings.Contains(string(afterClose), "external close follow-up") {
		t.Fatalf("closing read-only channel tab overwrote external append:\n%s", afterClose)
	}
}

func TestResumeSessionRejectsCleanupPending(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	activePath := filepath.Join(dir, "active.jsonl")
	pendingPath := filepath.Join(dir, "pending.jsonl")
	for _, path := range []string{activePath, pendingPath} {
		if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := agent.MarkCleanupPending(pendingPath, "delete"); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: activePath, Label: "test"})
	app.setTestCtrl(ctrl, "")
	defer app.activeCtrl().Close()

	if _, err := app.ResumeSession(pendingPath); err == nil || !strings.Contains(err.Error(), "pending cleanup") {
		t.Fatalf("ResumeSession cleanup-pending error = %v, want pending cleanup", err)
	}
	if got := app.activeCtrl().SessionPath(); filepath.Clean(got) != filepath.Clean(activePath) {
		t.Fatalf("active session path after rejected resume = %q, want %q", got, activePath)
	}
	if _, err := app.OpenChannelSessionForTab("test", pendingPath); err == nil || !strings.Contains(err.Error(), "pending cleanup") {
		t.Fatalf("OpenChannelSessionForTab cleanup-pending error = %v, want pending cleanup", err)
	}
	if meta := app.tabMeta(app.activeTab(), true); meta.ReadOnly {
		t.Fatalf("rejected channel open should not make tab read-only: %+v", meta)
	}
}

func TestResumeSessionRejectsPathOutsideControllerSessionDir(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	activePath := filepath.Join(dirA, "active.jsonl")
	outsidePath := filepath.Join(dirB, "outside.jsonl")
	for _, path := range []string{activePath, outsidePath} {
		if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dirA, SessionPath: activePath, Label: "test"}), "")
	defer app.activeCtrl().Close()

	if _, err := app.ResumeSession(outsidePath); err == nil {
		t.Fatal("ResumeSession should reject a transcript outside the active session dir")
	}
	if _, err := app.PreviewSession(outsidePath); err == nil {
		t.Fatal("PreviewSession should reject a transcript outside the active session dir")
	}
}

func BenchmarkDesktopListSessionsScoped(b *testing.B) {
	dirA := filepath.Join(b.TempDir(), "workspace-a-sessions")
	dirB := filepath.Join(b.TempDir(), "workspace-b-sessions")
	for _, dir := range []string{dirA, dirB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatalf("mkdir %s: %v", dir, err)
		}
		for i := range 120 {
			path := filepath.Join(dir, fmt.Sprintf("session-%03d.jsonl", i))
			body := fmt.Sprintf(`{"role":"user","content":"session %03d"}`+"\n", i)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				b.Fatalf("write session: %v", err)
			}
		}
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{SessionDir: dirA, SessionPath: filepath.Join(dirA, "session-000.jsonl"), Label: "test"}), "")
	defer app.activeCtrl().Close()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		sessions := app.ListSessions()
		if len(sessions) != 120 {
			b.Fatalf("ListSessions len = %d, want 120", len(sessions))
		}
	}
}

type appendingDesktopRunner struct {
	session *agent.Session
	started chan string
}

func (r *appendingDesktopRunner) Run(_ context.Context, input string) error {
	r.started <- input
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	r.session.Add(provider.Message{Role: provider.RoleAssistant, Content: "ok"})
	return nil
}

func TestForkCreatesActiveTabWithoutSwitchingSourceController(t *testing.T) {
	isolateDesktopUserDirsSchemaOne(t)

	workspace := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(workspace, "reasonix.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := agent.NewSessionPath(dir, "test")
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &appendingDesktopRunner{session: sess, started: make(chan string, 2)}
	ctrl := control.New(control.Options{
		Runner:        runner,
		Executor:      exec,
		Sink:          event.Discard,
		SessionDir:    dir,
		SessionPath:   path,
		Label:         "test",
		WorkspaceRoot: workspace,
	})
	app := NewApp()
	app.setTestCtrl(ctrl, "deepseek/test")
	app.tabs["test"].Scope = "project"
	app.tabs["test"].WorkspaceRoot = workspace
	app.tabs["test"].TopicID = "topic_source"
	app.tabs["test"].TopicTitle = "Source topic"
	defer ctrl.Close()

	ctrl.Submit("first")
	<-runner.started
	waitNotRunning(t, ctrl)
	ctrl.Submit("second")
	<-runner.started
	waitNotRunning(t, ctrl)
	if got := len(ctrl.History()); got != 5 {
		t.Fatalf("source history len before fork = %d, want 5", got)
	}

	meta, err := app.Fork(1)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if !meta.Active || meta.ID == "" || meta.ID == "test" {
		t.Fatalf("fork meta = %+v, want a new active tab", meta)
	}
	if got := app.activeTabID; got != meta.ID {
		t.Fatalf("active tab = %q, want fork tab %q", got, meta.ID)
	}
	if got := ctrl.SessionPath(); got != path {
		t.Fatalf("source controller session path = %q, want %q", got, path)
	}
	if got := len(ctrl.History()); got != 5 {
		t.Fatalf("source history len after fork = %d, want 5", got)
	}
	if got, want := meta.TopicTitle, "Source topic · 分叉"; got != want {
		t.Fatalf("fork topic title = %q, want %q", got, want)
	}

	var forkPath string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read session dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		candidate := filepath.Join(dir, entry.Name())
		if candidate == path {
			continue
		}
		m, ok, err := agent.LoadBranchMeta(candidate)
		if err != nil {
			t.Fatalf("load fork meta: %v", err)
		}
		if ok && m.TopicID == meta.TopicID {
			forkPath = candidate
			if m.ParentID != agent.BranchID(path) || m.ForkTurn != 1 || m.ForkMessageIndex != 3 {
				t.Fatalf("fork branch meta = %+v, want parent %q turn 1 index 3", m, agent.BranchID(path))
			}
			if m.Scope != "project" || m.WorkspaceRoot != workspace || m.TopicTitle != "Source topic · 分叉" {
				t.Fatalf("fork topic meta = %+v", m)
			}
		}
	}
	if forkPath == "" {
		t.Fatalf("fork session with topic %q not found in %s", meta.TopicID, dir)
	}
}
