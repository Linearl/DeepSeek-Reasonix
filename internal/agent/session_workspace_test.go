package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Task 545: the session-cwd resolver must return exactly the persisted project
// root, and refuse everything that would silently re-anchor a session to the
// reader's own working directory.
func TestSessionWorkspaceRoot(t *testing.T) {
	project := t.TempDir()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120000_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: project}); err != nil {
		t.Fatal(err)
	}

	got, ok := SessionWorkspaceRoot(sessionPath)
	if !ok || got != filepath.Clean(project) {
		t.Fatalf("SessionWorkspaceRoot = (%q, %v), want (%q, true)", got, ok, filepath.Clean(project))
	}
}

func TestSessionWorkspaceRootMissingMeta(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "20261006_120001_test.jsonl")
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("session without branch meta must not resolve a root")
	}
}

// Adversarial input: the persisted root was deleted or moved after the fact.
// Following a stale root would break every relative path in the session, so a
// nonexistent directory must resolve as "no root".
func TestSessionWorkspaceRootStaleDirectory(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120002_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: filepath.Join(dir, "gone")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("stale (deleted) project root must not resolve")
	}
}

// Global-scope sessions persist an empty workspace_root by convention; they
// have no project to follow.
func TestSessionWorkspaceRootGlobalScopeEmptyRoot(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120003_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: ""}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("global-scope session (empty root) must not resolve a root")
	}
}

// The desktop writers only persist absolute roots; a relative value would
// re-anchor to whatever cwd the reader has, which is exactly the bug 545 fixes.
func TestSessionWorkspaceRootRelativeRootRefused(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20261006_120004_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: "some/relative/dir"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("relative workspace_root must not resolve")
	}
}

func TestSessionWorkspaceRootEmptyPath(t *testing.T) {
	if _, ok := SessionWorkspaceRoot(""); ok {
		t.Fatal("empty session path must not resolve")
	}
	if _, ok := SessionWorkspaceRoot("   "); ok {
		t.Fatal("blank session path must not resolve")
	}
}

// The root pointing at a FILE (not a directory) must be refused too.
func TestSessionWorkspaceRootFileRootRefused(t *testing.T) {
	dir := t.TempDir()
	fileRoot := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(dir, "20261006_120005_test.jsonl")
	if err := SaveBranchMeta(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: fileRoot}); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionWorkspaceRoot(sessionPath); ok {
		t.Fatal("file-valued workspace_root must not resolve")
	}
}

// ── Task 546: new-session "reuse the latest session's directory" ──────────

func writeTestSession(t *testing.T, dir, name, workspaceRoot string, updatedAt time.Time, customTitle string) string {
	t.Helper()
	sessionPath := filepath.Join(dir, name)
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := BranchMeta{ID: BranchID(sessionPath), UpdatedAt: updatedAt}
	if customTitle != "" {
		meta.CustomTitle = customTitle
	}
	if workspaceRoot != "" {
		meta.WorkspaceRoot = workspaceRoot
	}
	if err := SaveBranchMetaPreserveUpdated(sessionPath, meta); err != nil {
		t.Fatal(err)
	}
	return sessionPath
}

// The most recently active session wins, even when the stores are scanned in a
// different order (recency decides, not directory iteration order).
func TestLatestSessionWorkspaceRootPicksMostRecentAcrossDirs(t *testing.T) {
	base := t.TempDir()
	projectOld := t.TempDir()
	projectNew := t.TempDir()
	storeA := filepath.Join(base, "a")
	storeB := filepath.Join(base, "b")
	if err := os.MkdirAll(storeA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(storeB, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestSession(t, storeA, "20261006_090000_old.jsonl", projectOld, time.Now().Add(-2*time.Hour), "Old session")
	writeTestSession(t, storeB, "20261006_100000_new.jsonl", projectNew, time.Now().Add(-1*time.Hour), "New session")

	latest, ok := LatestSessionWorkspaceRoot([]string{storeB, storeA})
	if !ok {
		t.Fatal("expected a resolvable latest session")
	}
	if latest.WorkspaceRoot != filepath.Clean(projectNew) {
		t.Fatalf("WorkspaceRoot = %q, want the newer session's project %q", latest.WorkspaceRoot, filepath.Clean(projectNew))
	}
	if !latest.Usable {
		t.Fatal("existing project root must resolve usable")
	}
	if latest.CustomTitle != "New session" {
		t.Fatalf("CustomTitle = %q, want %q", latest.CustomTitle, "New session")
	}
}

// A global-scope session (empty workspace_root by convention) has no project
// directory to reuse: the scan must skip it and settle on the next most recent
// session that declares a root — not on the global workspace.
func TestLatestSessionWorkspaceRootSkipsGlobalScopeSession(t *testing.T) {
	base := t.TempDir()
	project := t.TempDir()
	store := filepath.Join(base, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestSession(t, store, "20261006_110000_global.jsonl", "", time.Now().Add(-30*time.Minute), "")
	writeTestSession(t, store, "20261006_100000_project.jsonl", project, time.Now().Add(-1*time.Hour), "")

	latest, ok := LatestSessionWorkspaceRoot([]string{store})
	if !ok {
		t.Fatal("expected the older project session to be picked")
	}
	if latest.WorkspaceRoot != filepath.Clean(project) {
		t.Fatalf("WorkspaceRoot = %q, want %q (global-scope session must be skipped)", latest.WorkspaceRoot, filepath.Clean(project))
	}
}

// Adversarial input (task 546 acceptance): the most recent session's directory
// was deleted or moved after the fact. The session still wins the selection,
// but surfaces as not-usable so the caller falls back visibly instead of
// silently reusing a dead path — and instead of silently switching to an older
// session the user did not ask for.
func TestLatestSessionWorkspaceRootStaleDirectoryReportedNotSilentlySkipped(t *testing.T) {
	base := t.TempDir()
	goneProject := filepath.Join(base, "gone-project")
	store := filepath.Join(base, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestSession(t, store, "20261006_110000_stale.jsonl", goneProject, time.Now().Add(-30*time.Minute), "")
	olderProject := t.TempDir()
	writeTestSession(t, store, "20261006_100000_older.jsonl", olderProject, time.Now().Add(-1*time.Hour), "")

	latest, ok := LatestSessionWorkspaceRoot([]string{store})
	if !ok {
		t.Fatal("expected the stale-rooted session to be selected")
	}
	if latest.Usable {
		t.Fatal("deleted project root must not report usable")
	}
	if latest.WorkspaceRoot != filepath.Clean(goneProject) {
		t.Fatalf("WorkspaceRoot = %q, want the declared (stale) path %q", latest.WorkspaceRoot, filepath.Clean(goneProject))
	}
}

// Sessions without any declared root (no meta at all, legacy transcripts) are
// skipped; with nothing left the scan reports no candidate.
func TestLatestSessionWorkspaceRootNoCandidate(t *testing.T) {
	base := t.TempDir()
	store := filepath.Join(base, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// A session with no branch meta at all.
	if err := os.WriteFile(filepath.Join(store, "20261006_110000_nometa.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := LatestSessionWorkspaceRoot([]string{store}); ok {
		t.Fatal("sessions without a declared root must not produce a candidate")
	}
	if _, ok := LatestSessionWorkspaceRoot(nil); ok {
		t.Fatal("no stores must not produce a candidate")
	}
	if _, ok := LatestSessionWorkspaceRoot([]string{filepath.Join(base, "missing")}); ok {
		t.Fatal("a missing store must not produce a candidate")
	}
}

// Blank and whitespace-only roots behave like no declared root.
func TestLatestSessionWorkspaceRootBlankRootSkipped(t *testing.T) {
	base := t.TempDir()
	store := filepath.Join(base, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(store, "20261006_110000_blank.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMetaPreserveUpdated(sessionPath, BranchMeta{ID: BranchID(sessionPath), WorkspaceRoot: "   ", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LatestSessionWorkspaceRoot([]string{store}); ok {
		t.Fatal("blank workspace_root must not produce a candidate")
	}
}
