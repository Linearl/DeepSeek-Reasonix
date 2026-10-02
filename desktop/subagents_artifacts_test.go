package main

// Task 447 capsule: read-only Wails surface over persisted sub-agent
// artifacts. Covers ListSubagentsByParent (directory listing: whitelist,
// ordering, limit, HasTranscript) and ReadSubagentSession (transcript read
// through the preview pipeline; ref ownership enforcement).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// writeSubagentMeta writes a subagent sidecar with explicit fields so tests
// can assert the whitelisted projection (unlike sessions_test.go's
// writeSubagentArtifact, which writes zero-time metadata only).
func writeSubagentMeta(t *testing.T, dir, ref, parentSession string, createdAt time.Time, name, model, outcome string, withTranscript bool) {
	t.Helper()
	subagentDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withTranscript {
		if err := os.WriteFile(filepath.Join(subagentDir, ref+".jsonl"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta := agent.SubagentMeta{
		Ref:           ref,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt.Add(time.Minute),
		Status:        agent.SubagentCompleted,
		Outcome:       outcome,
		Kind:          "task",
		Name:          name,
		WorkspaceRoot: dir,
		ParentSession: parentSession,
		Model:         model,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subagentDir, ref+".meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newCapsuleTestSession(t *testing.T) (app *App, dir, sessionPath string) {
	t.Helper()
	// Isolate the session store per test: TestMain pins REASONIX_STATE_HOME
	// for the whole binary, and newTestSubagentApp only redirects HOME/XDG —
	// without this, every test here would share one subagents directory.
	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	app = newTestSubagentApp(t)
	dir = config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath = filepath.Join(dir, "capsule-parent.jsonl")
	if err := os.WriteFile(sessionPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write parent session: %v", err)
	}
	return app, dir, sessionPath
}

func TestListSubagentsByParentWhitelistsAndSortsNewestFirst(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	base := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	older := "sa_20261002_100000_000000000_000000000001"
	newer := "sa_20261002_100001_000000000_000000000002"
	otherParent := "sa_20261002_100002_000000000_000000000003"
	writeSubagentMeta(t, dir, older, agent.BranchID(sessionPath), base, "explore", "m/old", "partial", true)
	writeSubagentMeta(t, dir, newer, agent.BranchID(sessionPath), base.Add(time.Second), "research", "m/new", "", true)
	writeSubagentMeta(t, dir, otherParent, "some-other-parent", base.Add(2*time.Second), "hidden", "m/x", "", true)

	views, err := app.ListSubagentsByParent(sessionPath)
	if err != nil {
		t.Fatalf("ListSubagentsByParent: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 owned artifacts, got %d: %+v", len(views), views)
	}
	if views[0].Ref != newer || views[1].Ref != older {
		t.Fatalf("expected newest first [%s %s], got [%s %s]", newer, older, views[0].Ref, views[1].Ref)
	}
	newest := views[0]
	if newest.Name != "research" || newest.Model != "m/new" || newest.Kind != "task" {
		t.Fatalf("unexpected whitelist fields: %+v", newest)
	}
	if newest.Status != string(agent.SubagentCompleted) {
		t.Fatalf("status = %q", newest.Status)
	}
	if newest.Outcome != "" {
		t.Fatalf("empty outcome must stay omitted/empty, got %q", newest.Outcome)
	}
	if newest.ParentSession != agent.BranchID(sessionPath) {
		t.Fatalf("parentSession = %q", newest.ParentSession)
	}
	if !newest.HasTranscript {
		t.Fatal("newest artifact has a transcript on disk, HasTranscript must be true")
	}
	if newest.CreatedAt != base.Add(time.Second).UnixMilli() || newest.UpdatedAt != base.Add(time.Minute+time.Second).UnixMilli() {
		t.Fatalf("timestamps not unix ms: created=%d updated=%d", newest.CreatedAt, newest.UpdatedAt)
	}
	if views[1].Outcome != "partial" {
		t.Fatalf("older outcome = %q", views[1].Outcome)
	}
}

func TestListSubagentsByParentFlagsMissingTranscript(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261002_100000_000000000_000000000004"
	writeSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), time.Now().UTC(), "task", "m", "", false)

	views, err := app.ListSubagentsByParent(sessionPath)
	if err != nil {
		t.Fatalf("ListSubagentsByParent: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(views))
	}
	if views[0].HasTranscript {
		t.Fatal("artifact without jsonl must report HasTranscript=false")
	}
}

func TestListSubagentsByParentEmptyPathAndLimit(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)

	views, err := app.ListSubagentsByParent("")
	if err != nil {
		t.Fatalf("empty path must not error: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("empty path must return no artifacts, got %d", len(views))
	}

	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	total := listSubagentsViewLimit + 5
	for i := 0; i < total; i++ {
		ref := "sa_20261002_090000_000000000_" + strings.TrimSpace(padRefIndex(i))
		writeSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), base.Add(time.Duration(i)*time.Second), "task", "m", "", true)
	}
	views, err = app.ListSubagentsByParent(sessionPath)
	if err != nil {
		t.Fatalf("ListSubagentsByParent: %v", err)
	}
	if len(views) != listSubagentsViewLimit {
		t.Fatalf("expected hard limit %d, got %d", listSubagentsViewLimit, len(views))
	}
	// Newest first: the cap must keep the newest tail, not the oldest head.
	wantNewest := "sa_20261002_090000_000000000_" + strings.TrimSpace(padRefIndex(total-1))
	if views[0].Ref != wantNewest {
		t.Fatalf("cap kept wrong tail: want newest %s, got %s", wantNewest, views[0].Ref)
	}
}

func padRefIndex(i int) string {
	raw := []byte("000000000000")
	for pos := len(raw) - 1; pos >= 0 && i > 0; pos-- {
		raw[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(raw)
}

func TestReadSubagentSessionReturnsPreviewMessages(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261002_100000_000000000_000000000005"
	subagentDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := "{\"role\":\"user\",\"content\":\"sub question\"}\n" +
		"{\"role\":\"assistant\",\"content\":\"sub answer\"}\n"
	writeSubagentMeta(t, dir, ref, agent.BranchID(sessionPath), time.Now().UTC(), "task", "m", "", false)
	// Write the transcript after the meta: writeSubagentMeta(withTranscript)
	// writes an empty placeholder file and would truncate the fixture.
	if err := os.WriteFile(filepath.Join(subagentDir, ref+".jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	messages, err := app.ReadSubagentSession(sessionPath, ref)
	if err != nil {
		t.Fatalf("ReadSubagentSession: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 history messages, got %d: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "sub question" {
		t.Fatalf("unexpected first message: %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "sub answer" {
		t.Fatalf("unexpected second message: %+v", messages[1])
	}
}

func TestReadSubagentSessionRejectsForeignAndUnknownRefs(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	owned := "sa_20261002_100000_000000000_000000000006"
	foreign := "sa_20261002_100000_000000000_000000000007"
	writeSubagentMeta(t, dir, owned, agent.BranchID(sessionPath), time.Now().UTC(), "task", "m", "", true)
	writeSubagentMeta(t, dir, foreign, "another-parent", time.Now().UTC(), "task", "m", "", true)

	if _, err := app.ReadSubagentSession(sessionPath, foreign); err == nil {
		t.Fatal("ref owned by another parent must be rejected")
	} else if !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("unexpected error for foreign ref: %v", err)
	}
	if _, err := app.ReadSubagentSession(sessionPath, "sa_does_not_exist"); err == nil {
		t.Fatal("unknown ref must be rejected")
	}
	// Traversal-shaped input can never match the membership list, so it must
	// fail without touching the filesystem outside the store.
	if _, err := app.ReadSubagentSession(sessionPath, "../../secrets"); err == nil {
		t.Fatal("traversal-shaped ref must be rejected")
	}
	if _, err := app.ReadSubagentSession(sessionPath, ""); err == nil {
		t.Fatal("empty ref must be rejected")
	}
}
