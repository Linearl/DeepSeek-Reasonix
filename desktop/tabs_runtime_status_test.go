package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
)

func TestProjectTreeShowsDetachedRuntimeStatus(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_detached_status"
	topicTitle := "Detached status"
	if err := setTopicTitle("", topicID, topicTitle); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "detached.jsonl", topicID, topicTitle, "", "detached prompt", time.Now())

	app := NewApp()
	sink := &tabEventSink{tabID: "detached", app: app}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "detached", Sink: sink})
	defer ctrl.Close()
	app.detachedSessions[sessionRuntimeKey(path)] = &WorkspaceTab{
		ID:            "detached",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       topicID,
		TopicTitle:    topicTitle,
		SessionPath:   path,
		Ctrl:          ctrl,
		Ready:         true,
		sink:          sink,
		disabledMCP:   map[string]ServerView{},
	}

	ctrl.Submit("keep detached runtime running")
	<-runner.started
	nodes := app.ListProjectTree()
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one global topic", nodes)
	}
	topic := nodes[0].Children[0]
	if topic.TopicID != topicID || topic.Status != topicStatusThinking || !topic.Running {
		t.Fatalf("detached topic status = %+v, want thinking/running for %q", topic, topicID)
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
}

// 任务 757 裁决：本测试原名 TestProjectTreeSplitsMultipleRuntimeSessionsInSameTopic
// （dc11dc67e，2026-06-14「sidebar sessions own runtimes」时代），要求同 topic 多
// 运行时会话拆成 per-session 子行、父行中性。任务 352（3347f023b，2026-10-01，用户
// 截图驱动）已定判普通树恒一逻辑行：runtime 路径与 catalog 路径同合同，多记录聚合
// 到唯一逻辑行（任一 running→Running、running 态状态优先、open 记录取代表
// SessionPath），文件 stem 子行是用户可见噪音，永不展开（守卫见
// project_tree_window_copies_test.go）。352 落地时漏改本测试，形成基线预存红。
// 本测试按 352 终态语义重写：名字即合同——多运行时会话合并，不拆分。
func TestProjectTreeMergesMultipleRuntimeSessionsInSameTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_multi_runtime_status"
	topicTitle := "Multi runtime status"
	if err := setTopicTitle("", topicID, topicTitle); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	sessionA := writeTopicSessionWithPrompt(t, dir, "session-a.jsonl", topicID, topicTitle, "", "session A prompt", time.Now().Add(-time.Hour))
	sessionB := writeTopicSessionWithPrompt(t, dir, "session-b.jsonl", topicID, topicTitle, "", "session B prompt", time.Now())

	app := NewApp()
	runnerA := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	runnerB := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	asks := make(chan struct{}, 1)
	ctrlA := control.New(control.Options{Runner: runnerA, SessionDir: dir, SessionPath: sessionA, Label: "a", Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.AskRequest {
			asks <- struct{}{}
		}
	})})
	ctrlB := control.New(control.Options{Runner: runnerB, SessionDir: dir, SessionPath: sessionB, Label: "b", Sink: event.Discard})
	defer ctrlA.Close()
	defer ctrlB.Close()

	detached := &WorkspaceTab{
		ID:             detachedRuntimeTabID(sessionRuntimeKey(sessionA)),
		Scope:          "global",
		WorkspaceRoot:  globalTabWorkspaceRoot(),
		TopicID:        topicID,
		TopicTitle:     topicTitle,
		SessionPath:    sessionA,
		Ctrl:           ctrlA,
		Ready:          true,
		ActivityStatus: topicStatusWaitingConfirmation,
		disabledMCP:    map[string]ServerView{},
	}
	visible := &WorkspaceTab{
		ID:             "visible",
		Scope:          "global",
		WorkspaceRoot:  globalTabWorkspaceRoot(),
		TopicID:        topicID,
		TopicTitle:     topicTitle,
		SessionPath:    sessionB,
		Ctrl:           ctrlB,
		Ready:          true,
		ActivityStatus: topicStatusThinking,
		disabledMCP:    map[string]ServerView{},
	}
	app.detachedSessions[sessionRuntimeKey(sessionA)] = detached
	app.tabs[visible.ID] = visible
	app.tabOrder = []string{visible.ID}
	app.activeTabID = visible.ID

	ctrlA.Submit("block A")
	ctrlB.Submit("block B")
	<-runnerA.started
	<-runnerB.started
	askCtx, cancelAsk := context.WithCancel(t.Context())
	askDone := make(chan struct{})
	go func() {
		defer close(askDone)
		_, _ = ctrlA.Ask(askCtx, []event.AskQuestion{{ID: "choice", Prompt: "Choose"}})
	}()
	defer func() { cancelAsk(); <-askDone }()
	select {
	case <-asks:
	case <-time.After(3 * time.Second):
		t.Fatal("waiting prompt was not committed")
	}

	nodes := app.ListProjectTree()
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one global topic", nodes)
	}
	// 352 合同：同 topic 的多个运行时会话聚合为唯一逻辑行——任一 running 则行
	// running；running 态状态优先，session-a 按路径排序在前，等待确认先展示；
	// per-session 子行永不展开（文件 stem 行是 352 修掉的噪音，守卫见
	// project_tree_window_copies_test.go）。
	topic := nodes[0].Children[0]
	if !topic.Running {
		t.Fatalf("merged topic should aggregate child runtimes: %+v", topic)
	}
	if topic.Status != topicStatusWaitingConfirmation {
		t.Fatalf("merged topic status = %q, want waiting confirmation from the first running record", topic.Status)
	}
	if len(topic.Children) != 0 {
		t.Fatalf("topic children = %#v, want no per-session rows (task 352: runtime path never emits session children)", topic.Children)
	}

	close(runnerA.release)
	close(runnerB.release)
	cancelAsk()
	waitNotRunning(t, ctrlA)
	waitNotRunning(t, ctrlB)
}

func TestProjectTreeShowsBackgroundJobStatus(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_background_job"
	topicTitle := "Background job"
	if err := setTopicTitle("", topicID, topicTitle); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "job.jsonl", topicID, topicTitle, "", "job prompt", time.Now())

	jm := jobs.NewManager(event.Discard)
	release := make(chan struct{})
	jm.StartForSession(agent.BranchID(path), "bash", "sleep", func(ctx context.Context, _ io.Writer) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "", nil
		}
	})
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "job", Jobs: jm})
	defer ctrl.Close()
	app := NewApp()
	app.tabs["job"] = &WorkspaceTab{
		ID:            "job",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID:       topicID,
		TopicTitle:    topicTitle,
		SessionPath:   path,
		Ctrl:          ctrl,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	app.tabOrder = []string{"job"}
	app.activeTabID = "job"

	nodes := app.ListProjectTree()
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one global topic", nodes)
	}
	topic := nodes[0].Children[0]
	if topic.Status != topicStatusBackgroundJob || !topic.Running {
		t.Fatalf("background job topic status = %+v, want background_job/running", topic)
	}
	tabs := app.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("tabs = %d, want 1", len(tabs))
	}
	if !tabs[0].Running || tabs[0].PendingPrompt || tabs[0].Cancellable || tabs[0].BackgroundJobs != 1 {
		t.Fatalf("tab runtime = running:%v pending:%v cancellable:%v background:%d, want background-only running tab", tabs[0].Running, tabs[0].PendingPrompt, tabs[0].Cancellable, tabs[0].BackgroundJobs)
	}
	raw, err := json.Marshal(tabs[0])
	if err != nil {
		t.Fatalf("marshal tab meta: %v", err)
	}
	if !strings.Contains(string(raw), `"cancellable":false`) {
		t.Fatalf("tab metadata should serialize explicit cancellable=false: %s", raw)
	}

	close(release)
	waitNoJobs(t, ctrl)
	nodes = app.ListProjectTree()
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree after job finish = %#v, want one global topic", nodes)
	}
	topic = nodes[0].Children[0]
	if topic.Status != "" || topic.Running {
		t.Fatalf("background job topic status after finish = %+v, want idle", topic)
	}
}

func TestBackgroundJobNoticeForcesProjectTreeRefresh(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.ctx = context.Background()
	legacyInvalidations := 0
	app.projectTreeChangedHook = func() { legacyInvalidations++ }
	events := make(chan ProjectTreeRuntimeSnapshot, 1)
	app.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if name == "project-tree:runtime-changed" && len(payload) == 1 {
			if snapshot, ok := payload[0].(ProjectTreeRuntimeSnapshot); ok {
				events <- snapshot
			}
		}
	}
	app.tabs["job"] = &WorkspaceTab{
		ID:          "job",
		Scope:       "global",
		TopicID:     "topic_background_notice",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	sink := &tabEventSink{tabID: "job", app: app}

	sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "background bash finished: bash-1"})
	if legacyInvalidations != 0 {
		t.Fatalf("catalog/legacy invalidations = %d, want 0 for runtime-only status", legacyInvalidations)
	}
	select {
	case snapshot := <-events:
		if snapshot.Revision == 0 || snapshot.Topics == nil {
			t.Fatalf("runtime snapshot = %+v, want versioned non-nil projection", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("background job finish notice emitted no project-tree runtime snapshot")
	}
}

func waitNoJobs(t *testing.T, ctrl control.SessionAPI) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(ctrl.Jobs()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("controller still has jobs: %+v", ctrl.Jobs())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTopicActivityStatusPresentsReadinessSeparatelyFromPause(t *testing.T) {
	readiness := event.Event{
		Kind:    event.TurnDone,
		Err:     &agent.FinalReadinessError{Attempts: 3, Reason: "missing verification"},
		Outcome: event.TurnOutcomeFinalReadiness,
	}
	if status, ok := topicActivityStatusFromEvent(readiness); !ok || status != topicStatusAwaitingDelivery {
		t.Fatalf("readiness turn end = (%q, %v), want (%q, true)", status, ok, topicStatusAwaitingDelivery)
	}
	recoveryPause := event.Event{
		Kind:    event.TurnDone,
		Err:     &agent.RecoveryPauseError{Message: "automatic recovery paused"},
		Outcome: event.TurnOutcomeRecoveryPaused,
	}
	if status, ok := topicActivityStatusFromEvent(recoveryPause); !ok || status != topicStatusPaused {
		t.Fatalf("recovery pause turn end = (%q, %v), want (%q, true)", status, ok, topicStatusPaused)
	}
	if status, ok := topicActivityStatusFromEvent(event.Event{Kind: event.TurnDone, Err: io.EOF}); !ok || status != topicStatusError {
		t.Fatalf("ordinary turn error = (%q, %v), want (%q, true)", status, ok, topicStatusError)
	}
	if status, ok := topicActivityStatusFromEvent(event.Event{Kind: event.TurnDone}); !ok || status != "" {
		t.Fatalf("clean turn end = (%q, %v), want cleared status", status, ok)
	}
}

func TestTopicActivityStatusClearsOnCompactionDone(t *testing.T) {
	if status, ok := topicActivityStatusFromEvent(event.Event{Kind: event.CompactionStarted}); !ok || status != topicStatusThinking {
		t.Fatalf("compaction start = (%q, %v), want (%q, true)", status, ok, topicStatusThinking)
	}
	if status, ok := topicActivityStatusFromEvent(event.Event{Kind: event.CompactionDone}); !ok || status != "" {
		t.Fatalf("compaction done = (%q, %v), want cleared status (no thinking spinner)", status, ok)
	}
}

func TestCatalogRuntimeStatusPreservesDeliveryCheckWhenIdle(t *testing.T) {
	got := catalogRuntimeStatus(topicStatusAwaitingDelivery, control.RuntimeStatus{})
	if got != topicStatusAwaitingDelivery {
		t.Fatalf("idle delivery check = %q, want %q", got, topicStatusAwaitingDelivery)
	}
	got = catalogRuntimeStatus(topicStatusAwaitingDelivery, control.RuntimeStatus{Running: true})
	if got != topicStatusThinking {
		t.Fatalf("running delivery check = %q, want %q", got, topicStatusThinking)
	}
	got = catalogRuntimeStatus(topicStatusPaused, control.RuntimeStatus{})
	if got != topicStatusPaused {
		t.Fatalf("idle recovery pause = %q, want %q", got, topicStatusPaused)
	}
}
