package main

// tl 1059: the create-time explicit topicId binding. Covers the agent upsert
// adapter (create accepts a binding that resolves, rejects an unknown topic and
// the newConversationEachRun contradiction; update rejects the field outright —
// the agent surface has no re-bind), the panel save gate (ReplaceConfig
// validates bindings on NEW rows and, since tl1851, CHANGED bindings on
// existing rows — the panel's re-bind), and the first-run injection (a task
// created with a binding resolves to that conversation instead of spawning a
// fresh one).

import (
	"strings"
	"testing"

	"reasonix/internal/tool/builtin"
)

func newBindingTestAdapter(t *testing.T) (heartbeatManagerAdapter, *HeartbeatEngine) {
	t.Helper()
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	return heartbeatManagerAdapter{engine: engine}, engine
}

func mustCreateWithBinding(t *testing.T, a heartbeatManagerAdapter, binding string, newEachRun bool) (builtin.HeartbeatUpsertResult, error) {
	t.Helper()
	view, err := a.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	patch := builtin.HeartbeatTaskPatch{
		Provided: map[string]bool{"title": true, "prompt": true, "interval": true},
		Title:    "bound task", Prompt: "p", Interval: "30m",
	}
	if binding != "" {
		patch.Provided["topicId"] = true
		patch.TopicID = binding
	}
	if newEachRun {
		patch.Provided["newConversationEachRun"] = true
		fresh := true
		patch.NewConversationEachRun = &fresh
	}
	return a.UpsertTask(builtin.HeartbeatUpsertRequest{Patch: patch, ExpectedRevision: view.Revision})
}

// Create with an explicit binding that resolves: the binding lands on disk.
func TestHeartbeatUpsertCreateWithTopicBindingPersists(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(topicID string) bool { return topicID == "topic_live" }
	res, err := mustCreateWithBinding(t, a, "topic_live", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created {
		t.Fatalf("create receipt wrong: %+v", res)
	}
	fresh := mustList(t, a)
	if len(fresh.Tasks) != 1 || fresh.Tasks[0].TopicID != "topic_live" {
		t.Fatalf("explicit binding not persisted: %+v", fresh.Tasks)
	}
}

// Create without a binding keeps today's behaviour: TopicID stays empty and
// the first run self-binds (covered end-to-end by the reuse-session tests).
func TestHeartbeatUpsertCreateWithoutTopicBindingUnchanged(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(string) bool { return false }
	if _, err := mustCreateWithBinding(t, a, "", false); err != nil {
		t.Fatal(err)
	}
	fresh := mustList(t, a)
	if len(fresh.Tasks) != 1 || fresh.Tasks[0].TopicID != "" {
		t.Fatalf("no-binding create must leave topicId empty, got %+v", fresh.Tasks)
	}
}

// A binding that names no known conversation is refused at creation.
func TestHeartbeatUpsertCreateRejectsUnknownTopic(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(string) bool { return false }
	_, err := mustCreateWithBinding(t, a, "topic_ghost", false)
	if err == nil || !strings.Contains(err.Error(), "topicId") || !strings.Contains(err.Error(), "topic_ghost") {
		t.Fatalf("unknown topic binding must be refused with the id named, got %v", err)
	}
	// Nothing was written.
	if fresh := mustList(t, a); len(fresh.Tasks) != 0 {
		t.Fatalf("refused create must not persist a task: %+v", fresh.Tasks)
	}
}

// A fixed binding contradicts per-run-fresh conversations: refused.
func TestHeartbeatUpsertCreateRejectsBindingPlusFreshPerRun(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(string) bool { return true }
	_, err := mustCreateWithBinding(t, a, "topic_live", true)
	if err == nil || !strings.Contains(err.Error(), "newConversationEachRun") {
		t.Fatalf("binding + newConversationEachRun must be refused, got %v", err)
	}
	if fresh := mustList(t, a); len(fresh.Tasks) != 0 {
		t.Fatalf("refused create must not persist a task: %+v", fresh.Tasks)
	}
}

// Update path: topicId stays engine-owned. Even a no-op rewrite of the current
// value is refused, so the model gets one unambiguous rule.
func TestHeartbeatUpsertUpdateRejectsTopicBinding(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(string) bool { return true }
	created, err := mustCreateWithBinding(t, a, "topic_live", false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"id": true, "title": true, "topicId": true},
			ID:       created.TaskID, Title: "renamed", TopicID: "topic_other",
		},
		ExpectedRevision: view.Revision,
	})
	if err == nil || !strings.Contains(err.Error(), "topicId") || !strings.Contains(err.Error(), "cannot be changed") {
		t.Fatalf("agent update must refuse topicId outright, got %v", err)
	}
	fresh := mustList(t, a)
	if len(fresh.Tasks) != 1 || fresh.Tasks[0].Title != "bound task" || fresh.Tasks[0].TopicID != "topic_live" {
		t.Fatalf("refused update must not change the task: %+v", fresh.Tasks)
	}
}

// Panel save (ReplaceConfig): a NEW row with a binding that resolves is
// accepted and persisted (the panel picker's write path).
func TestHeartbeatReplaceConfigAcceptsNewTaskBinding(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(topicID string) bool { return topicID == "topic_live" }
	view := mustList(t, a)
	tasks := []HeartbeatTask{{ID: "panelnew", Title: "panel", Prompt: "p", Interval: "30m", Enabled: true, TopicID: "topic_live"}}
	updated, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Tasks) != 1 || updated.Tasks[0].TopicID != "topic_live" {
		t.Fatalf("panel binding not persisted: %+v", updated.Tasks)
	}
}

// Panel save: an unknown binding on a NEW row is refused before any write.
func TestHeartbeatReplaceConfigRejectsUnknownBinding(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(string) bool { return false }
	view := mustList(t, a)
	tasks := []HeartbeatTask{{ID: "panelnew", Title: "panel", Prompt: "p", Interval: "30m", Enabled: true, TopicID: "topic_ghost"}}
	if _, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: tasks}); err == nil {
		t.Fatal("unknown binding on a new row must be refused")
	}
	if fresh := mustList(t, a); len(fresh.Tasks) != 0 {
		t.Fatalf("refused panel save must not persist: %+v", fresh.Tasks)
	}
}

// First-run injection: a task created with a binding resolves to that
// conversation (resume mode), and the pre-existing conversation keeps its own
// origin stamp (it is the user's session, not a scheduler-created one).
func TestHeartbeatCreatedWithBindingFirstRunInjectsBoundTopic(t *testing.T) {
	_, app := newReuseSessionTestEngine(t)
	engine := &HeartbeatEngine{app: app, pendingTopics: map[string]heartbeatPendingTopic{}}
	engine.topicExists = func(string) bool { return true }

	external, err := app.CreateTopic("global", "", "User working session")
	if err != nil {
		t.Fatal(err)
	}
	created := HeartbeatTask{ID: "bound", Title: "Bound", Prompt: "p", Interval: "30m", Enabled: true, ReuseSession: true, TopicID: external.ID}
	updated, topicID, _, ok := engine.resolveHeartbeatTopic(created, "global", "", "Heartbeat: Bound")
	if !ok || topicID != external.ID || updated.TopicID != external.ID {
		t.Fatalf("first run must inject the bound topic %q, got ok=%v topic=%q", external.ID, ok, topicID)
	}
	// The legacy (no-mode-flags) path honours the binding the same way:
	// "explicit binding beats first-run binding".
	legacy := created
	legacy.ReuseSession = false
	legacy.TopicID = external.ID
	if _, topicID, _, ok := engine.resolveHeartbeatTopic(legacy, "global", "", "Heartbeat: Bound"); !ok || topicID != external.ID {
		t.Fatalf("legacy first run must inject the bound topic %q, got %q", external.ID, topicID)
	}
}

// ── tl1851: the panel's re-bind (更换绑定会话) ──────────────────────────────

// Panel re-bind on an EXISTING row: a binding that resolves is accepted and
// persisted (the mergeHeartbeatDiskRunHistory change stops forcing the disk
// TopicID back; the revision gate guarantees the snapshot is fresh, so a
// differing TopicID is the user's deliberate re-bind).
func TestHeartbeatReplaceConfigRebindsExistingTask(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(topicID string) bool { return topicID == "topic_live" || topicID == "topic_other" }
	created, err := mustCreateWithBinding(t, a, "topic_live", false)
	if err != nil {
		t.Fatal(err)
	}
	view := mustList(t, a)
	if view.Tasks[0].TopicID != "topic_live" {
		t.Fatalf("seed binding missing: %+v", view.Tasks[0])
	}
	rebound := []HeartbeatTask{{
		ID: created.TaskID, Title: "bound task", Prompt: "p", Interval: "30m",
		Enabled: true, TopicID: "topic_other", LastRunAt: view.Tasks[0].LastRunAt,
	}}
	updated, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: rebound})
	if err != nil {
		t.Fatalf("panel re-bind refused: %v", err)
	}
	if updated.Tasks[0].TopicID != "topic_other" {
		t.Fatalf("re-bind not persisted: topic=%q, want topic_other", updated.Tasks[0].TopicID)
	}
	if fresh := mustList(t, a); fresh.Tasks[0].TopicID != "topic_other" {
		t.Fatalf("re-bind not on disk: %+v", fresh.Tasks[0])
	}
}

// Panel re-bind to an id that resolves nowhere is refused before any write —
// same gate a create faces.
func TestHeartbeatReplaceConfigRejectsUnknownRebind(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(topicID string) bool { return topicID == "topic_live" }
	created, err := mustCreateWithBinding(t, a, "topic_live", false)
	if err != nil {
		t.Fatal(err)
	}
	view := mustList(t, a)
	rebound := []HeartbeatTask{{
		ID: created.TaskID, Title: "bound task", Prompt: "p", Interval: "30m",
		Enabled: true, TopicID: "topic_ghost",
	}}
	if _, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: rebound}); err == nil {
		t.Fatal("re-bind to an unknown conversation must be refused")
	}
	if fresh := mustList(t, a); fresh.Tasks[0].TopicID != "topic_live" {
		t.Fatalf("refused re-bind must not persist: %+v", fresh.Tasks[0])
	}
}

// A re-bind combined with per-run-fresh mode is refused, matching the create
// gate (the frontend clears the binding on the mode switch; a crafted payload
// must not sneak both through).
func TestHeartbeatReplaceConfigRejectsRebindPlusFreshPerRun(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(string) bool { return true }
	created, err := mustCreateWithBinding(t, a, "topic_live", false)
	if err != nil {
		t.Fatal(err)
	}
	view := mustList(t, a)
	rebound := []HeartbeatTask{{
		ID: created.TaskID, Title: "bound task", Prompt: "p", Interval: "30m",
		Enabled: true, TopicID: "topic_other", NewConversationEachRun: true,
	}}
	if _, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: rebound}); err == nil {
		t.Fatal("re-bind + newConversationEachRun must be refused")
	}
	if fresh := mustList(t, a); fresh.Tasks[0].TopicID != "topic_live" || fresh.Tasks[0].NewConversationEachRun {
		t.Fatalf("refused re-bind must not persist: %+v", fresh.Tasks[0])
	}
}

// Un-binding (clear) on an existing row is a legitimate re-bind to "none": the
// first run after the clear creates and binds one, unchanged semantics.
func TestHeartbeatReplaceConfigAcceptsUnbind(t *testing.T) {
	a, engine := newBindingTestAdapter(t)
	engine.topicExists = func(string) bool { return true }
	created, err := mustCreateWithBinding(t, a, "topic_live", false)
	if err != nil {
		t.Fatal(err)
	}
	view := mustList(t, a)
	unbound := []HeartbeatTask{{
		ID: created.TaskID, Title: "bound task", Prompt: "p", Interval: "30m", Enabled: true,
	}}
	updated, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: unbound})
	if err != nil {
		t.Fatalf("unbind refused: %v", err)
	}
	if updated.Tasks[0].TopicID != "" {
		t.Fatalf("unbind not persisted: topic=%q", updated.Tasks[0].TopicID)
	}
}

// The run write-back must not roll back a mid-run panel re-bind: the run
// captured the OLD binding at start; on a non-fresh task with a live disk
// binding the disk value wins. Fresh-conversation tasks keep today's
// always-advance behaviour, and a tombstoned dead binding lets the run's
// recreation through (tl1363b self-heal).
func TestMergeHeartbeatRunUpdatesKeepsPanelRebind(t *testing.T) {
	isolateDesktopUserDirs(t)

	// ① Bound task re-bound mid-run: disk T2 beats the run's stale T1.
	tasks := []HeartbeatTask{{ID: "a", Title: "t", Prompt: "p", Interval: "1h", Enabled: true, TopicID: "topic_new"}}
	mergeHeartbeatRunUpdates(tasks, map[string]HeartbeatTask{
		"a": {ID: "a", TopicID: "topic_old", LastRunAt: 500, RunHistory: []HeartbeatRun{{At: 500, TopicID: "topic_old"}}},
	})
	if tasks[0].TopicID != "topic_new" {
		t.Fatalf("panel re-bind rolled back by run write-back: topic=%q", tasks[0].TopicID)
	}
	// Run-state fields still advance: the timestamp and history are the run's.
	if tasks[0].LastRunAt != 500 || len(tasks[0].RunHistory) != 1 {
		t.Fatalf("run state not merged: lastRunAt=%d history=%d", tasks[0].LastRunAt, len(tasks[0].RunHistory))
	}

	// ② First bind (disk empty) still takes the run's topic.
	first := []HeartbeatTask{{ID: "b", Title: "t", Prompt: "p", Interval: "1h", Enabled: true}}
	mergeHeartbeatRunUpdates(first, map[string]HeartbeatTask{
		"b": {ID: "b", TopicID: "topic_first", LastRunAt: 100},
	})
	if first[0].TopicID != "topic_first" {
		t.Fatalf("first-run bind lost: topic=%q", first[0].TopicID)
	}

	// ③ Fresh-conversation task: the topic advance always wins (unchanged).
	fresh := []HeartbeatTask{{ID: "c", Title: "t", Prompt: "p", Interval: "1h", Enabled: true, NewConversationEachRun: true, TopicID: "topic_prev"}}
	mergeHeartbeatRunUpdates(fresh, map[string]HeartbeatTask{
		"c": {ID: "c", TopicID: "topic_next", LastRunAt: 300},
	})
	if fresh[0].TopicID != "topic_next" {
		t.Fatalf("fresh-mode topic advance blocked: topic=%q", fresh[0].TopicID)
	}

	// ④ Tombstoned disk binding: the run's recreated topic passes through
	// (tl1363b dead-binding self-heal must survive the new guard).
	tombstoned := "topic_dead_tombstoned"
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.DeletedTopics = prependUniqueString(f.DeletedTopics, tombstoned)
		return true, nil
	}); err != nil {
		t.Fatalf("seed tombstone: %v", err)
	}
	dead := []HeartbeatTask{{ID: "d", Title: "t", Prompt: "p", Interval: "1h", Enabled: true, TopicID: tombstoned}}
	mergeHeartbeatRunUpdates(dead, map[string]HeartbeatTask{
		"d": {ID: "d", TopicID: "topic_recreated", LastRunAt: 200},
	})
	if dead[0].TopicID != "topic_recreated" {
		t.Fatalf("dead-binding recreation blocked by re-bind guard: topic=%q", dead[0].TopicID)
	}
}
