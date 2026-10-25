package main

// tl 1059: the create-only explicit topicId binding. Covers the agent upsert
// adapter (create accepts a binding that resolves, rejects an unknown topic and
// the newConversationEachRun contradiction; update rejects the field outright —
// the engine's mid-life re-bind protection), the panel save gate (ReplaceConfig
// validates bindings on NEW rows only), and the first-run injection (a task
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
	if err == nil || !strings.Contains(err.Error(), "topicId") || !strings.Contains(err.Error(), "engine-owned") {
		t.Fatalf("update must refuse topicId outright, got %v", err)
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
