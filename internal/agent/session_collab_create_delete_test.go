package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 158.C: `project` (and its alias `workspace_root`) must reach the host, so
// a session can be created for another project — and a conflict between the two
// spellings must fail instead of silently picking one.
func TestCreateCollabSessionRoutesProjectToHost(t *testing.T) {
	dir := t.TempDir()
	var got []CreateCollabSessionRequest
	create := func(req CreateCollabSessionRequest) (CreateCollabSessionResult, error) {
		got = append(got, req)
		return CreateCollabSessionResult{
			TopicID:     "topic-1",
			ContactID:   "sc_new",
			SessionPath: filepath.Join(dir, "new.jsonl"),
			Scope:       "project",
			ProjectRoot: req.ProjectRoot,
		}, nil
	}
	createTool := NewCreateCollabSessionTool(dir, create)

	// 1. No project: the host decides from the caller's own root.
	out, err := createTool.Execute(context.Background(), []byte(`{"title":"audit","purpose":"auditor","group":"g"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProjectRoot != "" || got[0].WorkspaceRoot != dir {
		t.Fatalf("caller root must be passed with an empty project: %+v", got)
	}
	if !strings.Contains(out, `"scope":"project"`) || !strings.Contains(out, `"projectRoot":""`) {
		t.Fatalf("the result must report where it landed: %s", out)
	}

	// 2. Explicit project root: handed to the host verbatim.
	target := `C:\work\other-project`
	out, err = createTool.Execute(context.Background(), []byte(`{"title":"audit","purpose":"auditor","group":"g","project":"C:\\work\\other-project"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ProjectRoot != target {
		t.Fatalf("project root not forwarded: %+v", got)
	}
	var echoed struct {
		ProjectRoot string `json:"projectRoot"`
	}
	if uerr := json.Unmarshal([]byte(out), &echoed); uerr != nil || echoed.ProjectRoot != target {
		t.Fatalf("result must echo the project root, got %s (%v)", out, uerr)
	}

	// 3. workspace_root is the same axis.
	if _, err = createTool.Execute(context.Background(), []byte(`{"title":"audit","purpose":"auditor","group":"g","workspace_root":"C:\\work\\other-project"}`)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].ProjectRoot != target {
		t.Fatalf("workspace_root alias must behave like project: %+v", got)
	}

	// 4. Two different roots: refuse, and do NOT call the host.
	_, err = createTool.Execute(context.Background(), []byte(`{"title":"audit","purpose":"auditor","group":"g","project":"C:\\work\\a","workspace_root":"C:\\work\\b"}`))
	if err == nil || !strings.Contains(err.Error(), "different roots") {
		t.Fatalf("conflicting roots must be refused, got %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("a refused request must not reach the host: %+v", got)
	}

	// 5. Same root in both spellings is accepted (a caller may echo both).
	if _, err = createTool.Execute(context.Background(), []byte(`{"title":"audit","purpose":"auditor","group":"g","project":"C:\\work\\a","workspace_root":"c:\\work\\a"}`)); err != nil {
		t.Fatalf("the same root in both spellings must be accepted: %v", err)
	}
}

// Task 158.D: the dry-run impact must name the conversation exactly as the
// contact directory names it. The host may leave Title empty (it did), so the
// tool falls back to the title its own directory resolution produced.
func TestDeleteSessionDryRunUsesDirectoryTitle(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, target)
	writeEmpty(t, self)
	const title = "158 补口会话"
	if _, err := SetSessionPurpose(target, "secretary"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBranchMeta(target, false, func(m *BranchMeta) error {
		m.TopicTitle = title
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var sawDryRun bool
	del := func(contactID, sessionPath string, dryRun bool) (DeleteSessionImpact, DeleteSessionResult, error) {
		if !dryRun {
			t.Fatalf("a request without confirm must never reach the deleting path")
		}
		sawDryRun = true
		// Deliberately empty: the tool must still produce a usable impact.
		return DeleteSessionImpact{ContactID: contactID, SessionPath: sessionPath}, DeleteSessionResult{}, nil
	}
	deleteTool := NewDeleteSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		CurrentSessionPath: self,
	}, del)

	out, err := deleteTool.Execute(context.Background(), []byte(`{"target":"`+title+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !sawDryRun {
		t.Fatal("dry run did not consult the host")
	}
	var got struct {
		Status string `json:"status"`
		Impact struct {
			Title    string `json:"title"`
			HasTurn  bool   `json:"hasTurn"`
			OpenTab  bool   `json:"openTab"`
			Archived bool   `json:"archived"`
		} `json:"impact"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("impact json: %v (%s)", err, out)
	}
	if got.Status != "dry_run" {
		t.Fatalf("want dry_run, got %s", out)
	}
	if got.Impact.Title != title {
		t.Fatalf("impact must carry the directory title %q, got %q", title, got.Impact.Title)
	}
}

// A session that already holds a turn must not look empty: the reported case
// was a 45s turn with hasTurn=false. The transcript is the source of truth, the
// same file read_session_tail reads.
func TestSessionTranscriptHasContentDetectsTurns(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.jsonl")
	writeEmpty(t, empty)
	if SessionTranscriptHasContent(empty) {
		t.Fatal("a freshly created session file is empty")
	}
	if SessionTranscriptHasContent(filepath.Join(dir, "missing.jsonl")) {
		t.Fatal("a missing file is not content")
	}
	if SessionTranscriptHasContent("") {
		t.Fatal("an empty path is not content")
	}
	turned := filepath.Join(dir, "turned.jsonl")
	if err := os.WriteFile(turned, []byte(`{"type":"append","message":{"role":"user","content":"hello"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !SessionTranscriptHasContent(turned) {
		t.Fatal("a transcript with turns must count as content")
	}
}

// The directory title has one source: branch meta first, file stem last.
func TestSessionDirectoryTitleSources(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "stem-name.jsonl")
	writeEmpty(t, plain)
	if got := SessionDirectoryTitle(plain); got != "stem-name" {
		t.Fatalf("want the file stem, got %q", got)
	}
	if err := UpdateBranchMeta(plain, false, func(m *BranchMeta) error {
		m.TopicTitle = "topic title"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := SessionDirectoryTitle(plain); got != "topic title" {
		t.Fatalf("want the topic title, got %q", got)
	}
	if err := UpdateBranchMeta(plain, false, func(m *BranchMeta) error {
		m.CustomTitle = "renamed by the user"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := SessionDirectoryTitle(plain); got != "renamed by the user" {
		t.Fatalf("a custom title must win, got %q", got)
	}
}
