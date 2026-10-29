package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// tl1240: batch endpoint tests. The batch path must behave per-topic like the
// single path (same trash layout, same title cleanup, same active-work/busy
// gates) while paying the shared fixed costs (gates, directory scan, fallback,
// notification) once for the whole batch.

func writeBatchTopicFixture(t *testing.T, projectRoot, topicID, title string) (dir, sessionPath string) {
	t.Helper()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, title); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir = config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath = writeTopicSession(t, dir, topicID+".jsonl", topicID, title, projectRoot)
	return dir, sessionPath
}

func TestBatchTrashTopicsArchivesMultipleTopics(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topics := []struct{ id, title string }{
		{"topic_batch_one", "Batch one"},
		{"topic_batch_two", "Batch two"},
		{"topic_batch_three", "Batch three"},
	}
	dir := ""
	var sessionPaths []string
	for _, topic := range topics {
		topicDir, sessionPath := writeBatchTopicFixture(t, projectRoot, topic.id, topic.title)
		dir = topicDir
		sessionPaths = append(sessionPaths, sessionPath)
	}

	app := NewApp()
	// A duplicated id must collapse into one item (first-seen order kept).
	result, err := app.BatchTrashTopics([]string{
		topics[0].id, topics[1].id, topics[2].id, " ", topics[0].id,
	})
	if err != nil {
		t.Fatalf("BatchTrashTopics: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("batch items = %d, want 3 (deduped): %#v", len(result.Items), result.Items)
	}
	if result.Succeeded != 3 || result.Failed != 0 {
		t.Fatalf("batch succeeded=%d failed=%d, want 3/0", result.Succeeded, result.Failed)
	}
	for i, topic := range topics {
		item := result.Items[i]
		if item.TopicID != topic.id || !item.OK || item.Error != "" {
			t.Fatalf("item %d = %#v, want ok for %s", i, item, topic.id)
		}
		if _, err := os.Stat(sessionPaths[i]); !os.IsNotExist(err) {
			t.Fatalf("topic %s session should be removed from active history, stat err = %v", topic.id, err)
		}
		trashPath := filepath.Join(dir, sessionTrashDir, topic.id+".jsonl", topic.id+".jsonl")
		if _, err := os.Stat(trashPath); err != nil {
			t.Fatalf("topic %s session should be moved to trash: %v", topic.id, err)
		}
		if got := loadTopicTitle(projectRoot, topic.id); got != "" {
			t.Fatalf("topic %s title should be removed, got %q", topic.id, got)
		}
	}
}

func TestBatchTrashTopicsSingleTopicMatchesSinglePath(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	dir, sessionPath := writeBatchTopicFixture(t, projectRoot, "topic_batch_single", "Batch single")

	result, err := NewApp().BatchTrashTopics([]string{"topic_batch_single"})
	if err != nil {
		t.Fatalf("BatchTrashTopics: %v", err)
	}
	if result.Succeeded != 1 || result.Failed != 0 || len(result.Items) != 1 || !result.Items[0].OK {
		t.Fatalf("single-topic batch result = %#v, want one ok item", result)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("session should be removed from active history, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionTrashDir, "topic_batch_single.jsonl", "topic_batch_single.jsonl")); err != nil {
		t.Fatalf("session should be moved to trash: %v", err)
	}
	if got := loadTopicTitle(projectRoot, "topic_batch_single"); got != "" {
		t.Fatalf("topic title should be removed, got %q", got)
	}
}

func TestBatchTrashTopicsEmptyInputIsRejected(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if _, err := app.BatchTrashTopics(nil); err == nil {
		t.Fatal("empty batch input should be rejected")
	}
	if _, err := app.BatchTrashTopics([]string{"", "  "}); err == nil {
		t.Fatal("blank-only batch input should be rejected")
	}
}

func TestBatchTrashTopicsBusyWhenRemovalGateHeld(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	dir, sessionPath := writeBatchTopicFixture(t, projectRoot, "topic_batch_busy", "Batch busy")

	app := NewApp()
	if !app.sessionRemovalMu.TryLock() {
		t.Fatal("failed to hold sessionRemovalMu for the busy test")
	}
	defer app.sessionRemovalMu.Unlock()
	started := time.Now()
	result, err := app.BatchTrashTopics([]string{"topic_batch_busy"})
	if !errors.Is(err, errTopicArchiveBusy) {
		t.Fatalf("batch busy error = %v, want %v", err, errTopicArchiveBusy)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("busy batch waited %s instead of returning busy", elapsed)
	}
	if result.Succeeded != 0 || result.Failed != 0 {
		t.Fatalf("busy batch should report no outcomes, got %#v", result)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("busy batch should preserve the session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionTrashDir, "topic_batch_busy.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("busy batch should not create a trash entry, stat err = %v", err)
	}
}

func TestBatchTrashTopicsReportsPartialFailure(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	_, okPath := writeBatchTopicFixture(t, projectRoot, "topic_batch_ok", "Batch ok")
	runningID := "topic_batch_running"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, runningID, "Batch running"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	runningPath := writeTopicSession(t, dir, "topic-batch-running.jsonl", runningID, "Batch running", projectRoot)
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: runningPath, Label: "test", WorkspaceRoot: projectRoot})
	defer ctrl.Close()
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"running": {
				ID:            "running",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       runningID,
				TopicTitle:    "Batch running",
				Ctrl:          ctrl,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"running"},
		activeTabID: "running",
	}
	ctrl.Submit("long turn")
	<-runner.started
	defer close(runner.release)

	result, err := app.BatchTrashTopics([]string{runningID, "topic_batch_ok"})
	if err != nil {
		t.Fatalf("BatchTrashTopics should isolate the per-topic failure, got: %v", err)
	}
	if result.Succeeded != 1 || result.Failed != 1 || len(result.Items) != 2 {
		t.Fatalf("partial batch = %#v, want 1 ok / 1 failed", result)
	}
	failed := result.Items[0]
	if failed.TopicID != runningID || failed.OK || !failed.Forceable || failed.Error == "" {
		t.Fatalf("failed item = %#v, want forceable failure for %s", failed, runningID)
	}
	succeeded := result.Items[1]
	if succeeded.TopicID != "topic_batch_ok" || !succeeded.OK || succeeded.Error != "" {
		t.Fatalf("ok item = %#v, want ok for topic_batch_ok", succeeded)
	}
	if _, err := os.Stat(runningPath); err != nil {
		t.Fatalf("running topic session should be preserved: %v", err)
	}
	if got := loadTopicTitle(projectRoot, runningID); got != "Batch running" {
		t.Fatalf("failed topic title = %q, want Batch running", got)
	}
	if _, err := os.Stat(okPath); !os.IsNotExist(err) {
		t.Fatalf("ok topic session should be removed despite sibling failure, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionTrashDir, "topic_batch_ok.jsonl", "topic_batch_ok.jsonl")); err != nil {
		t.Fatalf("ok topic session should be moved to trash: %v", err)
	}
}

// The merged batch scan must build each directory's index once, not once per
// topic — the structural half of the "single + (N-1)×(teardown+move)" target.
func TestTopicTrashTargetsFromIndexesSharesIndexBuilds(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	dir, _ := writeBatchTopicFixture(t, projectRoot, "topic_scan_one", "Scan one")
	writeTopicSession(t, dir, "topic-scan-two.jsonl", "topic_scan_two", "Scan two", projectRoot)

	app := NewApp()
	builds := map[string]int{}
	index := memoizedTopicSessionDirIndexWith(func(dir string) (topicSessionDirIndex, error) {
		builds[dir]++
		return topicSessionIndexForDir(dir)
	})
	for _, topicID := range []string{"topic_scan_one", "topic_scan_two"} {
		targets, err := app.topicTrashTargetsFromDirIndexes(topicID, index)
		if err != nil {
			t.Fatalf("topicTrashTargetsFromIndexes(%s): %v", topicID, err)
		}
		if len(targets) != 1 {
			t.Fatalf("topic %s targets = %d, want 1", topicID, len(targets))
		}
	}
	for dirPath, count := range builds {
		if count != 1 {
			t.Fatalf("dir %s index built %d times, want 1", dirPath, count)
		}
	}
}

func TestBatchTrashTopicsTimingAgainstSinglePath(t *testing.T) {
	isolateDesktopUserDirs(t)

	const n = 8
	buildFixture := func(prefix string) (dir string, ids []string) {
		projectRoot := t.TempDir()
		if err := addProject(projectRoot, ""); err != nil {
			t.Fatalf("add project: %v", err)
		}
		dir = config.SessionDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir sessions: %v", err)
		}
		for i := 0; i < n; i++ {
			topicID := prefix + "_" + string(rune('a'+i))
			if err := setTopicTitle(projectRoot, topicID, "Timing "+topicID); err != nil {
				t.Fatalf("set topic title: %v", err)
			}
			writeTopicSession(t, dir, topicID+".jsonl", topicID, "Timing "+topicID, projectRoot)
			ids = append(ids, topicID)
		}
		return dir, ids
	}

	singleDir, singleIDs := buildFixture("topic_timing_single")
	_ = singleDir
	singleStarted := time.Now()
	for _, topicID := range singleIDs {
		if err := NewApp().TrashTopic(topicID); err != nil {
			t.Fatalf("single TrashTopic(%s): %v", topicID, err)
		}
	}
	singleElapsed := time.Since(singleStarted)

	_, batchIDs := buildFixture("topic_timing_batch")
	batchStarted := time.Now()
	result, err := NewApp().BatchTrashTopics(batchIDs)
	if err != nil {
		t.Fatalf("BatchTrashTopics: %v", err)
	}
	batchElapsed := time.Since(batchStarted)
	if result.Succeeded != n || result.Failed != 0 {
		t.Fatalf("timing batch = %#v, want %d ok", result, n)
	}
	// Readout only: unit-fixture timings are dominated by rename-fast small
	// files, so a hard assertion would be CI-noise bait. The readout records
	// the batch-vs-single ratio for the acceptance report; the structural scan
	// sharing is asserted by TestTopicTrashTargetsFromIndexesSharesIndexBuilds.
	t.Logf("tl1240 timing readout: %d singles = %v, one batch(%d) = %v (batch totalMs=%d)",
		n, singleElapsed, n, batchElapsed, result.TotalMs)
}

func TestBatchMoveToGroupFilesAllTopicsInOneWrite(t *testing.T) {
	isolateDesktopUserDirs(t)

	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.Projects[projectIndexByRoot(f.Projects, root)].Topics = []string{"m1", "m2", "m3"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.SaveSessionGroups("project", root, []desktopGroup{
		{ID: "grp-old", Title: "Old", TopicIDs: []string{"m1", "m2"}},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := app.BatchMoveToGroup("project", root, []string{"m1", "m2", "m3"}, "", "Two")
	if err != nil {
		t.Fatalf("BatchMoveToGroup: %v", err)
	}
	if result.Succeeded != 3 || result.Failed != 0 || len(result.Items) != 3 {
		t.Fatalf("batch move result = %#v, want 3 ok", result)
	}
	for _, item := range result.Items {
		if !item.OK || item.AlreadyFiled || item.Error != "" {
			t.Fatalf("move item = %#v, want a real move", item)
		}
	}
	if result.Group.ID != "collab-two" || result.Group.Title != "Two" {
		t.Fatalf("target group = %#v, want collab-two/Two", result.Group)
	}
	groups, err := app.ListProjectGroups("project", root)
	if err != nil {
		t.Fatal(err)
	}
	membership := map[string][]string{}
	for _, g := range groups {
		for _, topicID := range g.TopicIDs {
			membership[topicID] = append(membership[topicID], g.Title)
		}
	}
	for _, topicID := range []string{"m1", "m2", "m3"} {
		if len(membership[topicID]) != 1 || membership[topicID][0] != "Two" {
			t.Fatalf("topic %s belongs to %v, want exactly [Two]", topicID, membership[topicID])
		}
	}
	if len(membership) != 3 {
		t.Fatalf("unexpected extra memberships: %#v", membership)
	}

	// Re-filing the same batch is a no-op: every item reports AlreadyFiled and
	// the groups file is untouched.
	again, err := app.BatchMoveToGroup("project", root, []string{"m1", "m2", "m3"}, "collab-two", "")
	if err != nil {
		t.Fatalf("second BatchMoveToGroup: %v", err)
	}
	for _, item := range again.Items {
		if !item.OK || !item.AlreadyFiled {
			t.Fatalf("second move item = %#v, want alreadyFiled", item)
		}
	}
}

func TestBatchMoveToGroupValidation(t *testing.T) {
	isolateDesktopUserDirs(t)

	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if _, err := app.BatchMoveToGroup("project", root, nil, "", "Two"); err == nil {
		t.Fatal("empty topic list should be rejected")
	}
	if _, err := app.BatchMoveToGroup("project", root, []string{"m1"}, "", ""); err == nil {
		t.Fatal("missing group args should be rejected")
	}
}
