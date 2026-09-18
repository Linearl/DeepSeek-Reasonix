package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Task 170: the "改" half of the collaboration CRUD. The tools only resolve a
// target and hand the work to the host, so these tests pin the resolution and
// the refusal paths rather than the desktop's own writes.

func seedSessionWithTopic(t *testing.T, dir, name, topicID, title string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeEmpty(t, path)
	if err := UpdateBranchMeta(path, false, func(m *BranchMeta) error {
		m.TopicID = topicID
		m.TopicTitle = title
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenameSessionToolResolvesByTitleAndReportsHostResult(t *testing.T) {
	dir := t.TempDir()
	path := seedSessionWithTopic(t, dir, "target.jsonl", "topic-1", "Old title")

	var gotTopicID, gotPath, gotTitle string
	tool := NewRenameSessionTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	}, func(topicID, sessionPath, title string) (RenameSessionResult, error) {
		gotTopicID, gotPath, gotTitle = topicID, sessionPath, title
		return RenameSessionResult{TopicID: topicID, SessionPath: sessionPath, Title: title, ListingSynced: true}, nil
	})

	out, err := tool.Execute(nil, []byte(`{"target":"Old title","title":"New title"}`))
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if gotTopicID != "topic-1" {
		t.Fatalf("resolved topic id = %q, want topic-1", gotTopicID)
	}
	if gotPath != path {
		t.Fatalf("resolved path = %q, want %q", gotPath, path)
	}
	if gotTitle != "New title" {
		t.Fatalf("title = %q, want New title", gotTitle)
	}
	var payload struct {
		Status string              `json:"status"`
		Result RenameSessionResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Status != "renamed" || !payload.Result.ListingSynced {
		t.Fatalf("payload = %s", out)
	}
	if payload.Result.Previous != "Old title" {
		t.Fatalf("previous title = %q, want the resolved title", payload.Result.Previous)
	}
}

func TestRenameSessionToolRequiresTitleAndKnownTarget(t *testing.T) {
	dir := t.TempDir()
	seedSessionWithTopic(t, dir, "target.jsonl", "topic-1", "Known")
	tool := NewRenameSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir},
		func(topicID, sessionPath, title string) (RenameSessionResult, error) {
			t.Fatal("host callback must not run for an unusable request")
			return RenameSessionResult{}, nil
		})
	if _, err := tool.Execute(nil, []byte(`{"target":"Known","title":"   "}`)); err == nil {
		t.Fatal("blank title must be refused")
	}
	if _, err := tool.Execute(nil, []byte(`{"target":"nope","title":"New"}`)); err == nil {
		t.Fatal("unknown target must be refused")
	}
}

// A host without the capability must say so instead of reporting success: the
// tool is registered by the host, so this is the guard for a future host that
// registers it with a nil callback.
func TestRenameSessionToolRefusesWithoutHostCapability(t *testing.T) {
	dir := t.TempDir()
	seedSessionWithTopic(t, dir, "target.jsonl", "topic-1", "Known")
	tool := NewRenameSessionTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, nil)
	if _, err := tool.Execute(nil, []byte(`{"target":"Known","title":"New"}`)); err == nil ||
		!strings.Contains(err.Error(), "cannot rename") {
		t.Fatalf("nil host capability must be refused, got %v", err)
	}
}

func TestMoveTopicToGroupToolMovesResolvedTarget(t *testing.T) {
	dir := t.TempDir()
	seedSessionWithTopic(t, dir, "mover.jsonl", "topic-9", "Mover")

	var gotTopicID, gotScope, gotWorkspace, gotGroup string
	tool := NewMoveTopicToGroupTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	}, func(topicID, sessionPath, scope, workspaceRoot, groupID, groupTitle string) (MoveTopicToGroupResult, error) {
		gotTopicID, gotScope, gotWorkspace, gotGroup = topicID, scope, workspaceRoot, groupTitle
		return MoveTopicToGroupResult{TopicID: topicID, Group: groupTitle, RemovedFrom: []string{"Old"}}, nil
	})

	out, err := tool.Execute(nil, []byte(`{"target":"Mover","group":"Team"}`))
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if gotTopicID != "topic-9" || gotGroup != "Team" {
		t.Fatalf("resolved topic=%q group=%q", gotTopicID, gotGroup)
	}
	if gotScope != "global" {
		t.Fatalf("resolved scope = %q, want global for a session in the shared directory", gotScope)
	}
	if gotWorkspace != "" {
		t.Fatalf("global scope must not carry a workspace root, got %q", gotWorkspace)
	}
	var payload struct {
		Status string                 `json:"status"`
		Result MoveTopicToGroupResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Status != "moved" {
		t.Fatalf("status = %q, want moved", payload.Status)
	}
	if len(payload.Result.RemovedFrom) != 1 || payload.Result.RemovedFrom[0] != "Old" {
		t.Fatalf("removedFrom = %v, want the previous group", payload.Result.RemovedFrom)
	}
}

// A session that was never filed as a topic has nothing to group: the tool must
// say that instead of filing an unrelated entry.
func TestMoveTopicToGroupToolRequiresAFiledTopic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.jsonl")
	writeEmpty(t, path)
	tool := NewMoveTopicToGroupTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir},
		func(topicID, sessionPath, scope, workspaceRoot, groupID, groupTitle string) (MoveTopicToGroupResult, error) {
			t.Fatal("host callback must not run for an unfiled session")
			return MoveTopicToGroupResult{}, nil
		})
	if _, err := tool.Execute(nil, []byte(`{"target":"plain","group":"Team"}`)); err == nil ||
		!strings.Contains(err.Error(), "not filed as a topic") {
		t.Fatalf("unfiled session must be refused, got %v", err)
	}
}

func TestMoveTopicToGroupToolRefusesWithoutGroup(t *testing.T) {
	dir := t.TempDir()
	seedSessionWithTopic(t, dir, "mover.jsonl", "topic-9", "Mover")
	tool := NewMoveTopicToGroupTool(SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir},
		func(topicID, sessionPath, scope, workspaceRoot, groupID, groupTitle string) (MoveTopicToGroupResult, error) {
			t.Fatal("host callback must not run without a group")
			return MoveTopicToGroupResult{}, nil
		})
	if _, err := tool.Execute(nil, []byte(`{"target":"Mover"}`)); err == nil {
		t.Fatal("a move without a group must be refused")
	}
}
