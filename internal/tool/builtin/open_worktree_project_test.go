package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

// Task 128: the session can create an isolated worktree project, get a
// writable root back, and has no empty shell left behind on failure.
func TestOpenIsolatedWorktreeProjectCreatesWritableRoot(t *testing.T) {
	src := t.TempDir()
	managed := t.TempDir()
	gitToolMust(t, src, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitToolMust(t, src, "add", ".")
	gitToolMust(t, src, "commit", "-m", "seed")

	tl := openIsolatedWorktreeProject{workDir: src, managedRoot: managed}
	decl, err := tl.DeclareWriteAccess(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("DeclareWriteAccess: %v", err)
	}
	if len(decl.Directories) != 1 || filepath.Clean(decl.Directories[0]) != filepath.Clean(managed) {
		t.Fatalf("declaration = %+v, want managed root", decl)
	}

	out, err := tl.Execute(context.Background(), json.RawMessage(`{"project_name":"wave2"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result struct {
		ProjectName  string   `json:"projectName"`
		WorktreeRoot string   `json:"worktreeRoot"`
		Branch       string   `json:"branch"`
		WritePaths   []string `json:"writePaths"`
		Next         string   `json:"next"`
		Division     string   `json:"division"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.ProjectName != "wave2" || result.WorktreeRoot == "" {
		t.Fatalf("result = %s", out)
	}
	if !strings.HasPrefix(result.Branch, "reasonix/delivery-") {
		t.Fatalf("branch = %q", result.Branch)
	}
	if len(result.WritePaths) != 1 || result.WritePaths[0] != result.WorktreeRoot {
		t.Fatalf("writePaths = %v", result.WritePaths)
	}
	if !strings.Contains(result.Division, "create_worktree") {
		t.Fatalf("division note missing: %s", result.Division)
	}
	// New root is a real directory the session can write into.
	target := filepath.Join(result.WorktreeRoot, "session_note.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write into new project root: %v", err)
	}
}

func TestOpenIsolatedWorktreeProjectRegisteredBuiltin(t *testing.T) {
	if _, ok := tool.LookupBuiltin("open_isolated_worktree_project"); !ok {
		t.Fatal("open_isolated_worktree_project is not a registered builtin")
	}
}

// recordingOpener captures the task-128 host registration contract: the tool
// must hand the freshly created worktree root to the host hook exactly once.
type recordingOpener struct {
	roots []string
	err   error
}

func (r *recordingOpener) OpenIsolatedWorktreeProject(_ context.Context, worktreeRoot string) error {
	r.roots = append(r.roots, worktreeRoot)
	return r.err
}

// Task 128 acceptance: call → returns root → registration effective. With the
// desktop host hook bound on the context, the created worktree root reaches
// the host and the result reports hostRegistered.
func TestOpenIsolatedWorktreeProjectRegistersWithHost(t *testing.T) {
	src := t.TempDir()
	managed := t.TempDir()
	gitToolMust(t, src, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitToolMust(t, src, "add", ".")
	gitToolMust(t, src, "commit", "-m", "seed")

	opener := &recordingOpener{}
	ctx := tool.WithWorktreeProjectOpener(context.Background(), opener)
	tl := openIsolatedWorktreeProject{workDir: src, managedRoot: managed}
	out, err := tl.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result struct {
		WorktreeRoot   string `json:"worktreeRoot"`
		HostRegistered bool   `json:"hostRegistered"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(opener.roots) != 1 || opener.roots[0] != result.WorktreeRoot {
		t.Fatalf("host registration got %v, want exactly [%s]", opener.roots, result.WorktreeRoot)
	}
	if !result.HostRegistered {
		t.Fatalf("hostRegistered = false, want true; output: %s", out)
	}
}

// Task 128 acceptance: no empty shell. When host registration fails after a
// successful allocation, nothing is registered (the failure path returns
// path-only text) and the worktree stays usable on disk.
func TestOpenIsolatedWorktreeProjectRegistrationFailureLeavesNoShell(t *testing.T) {
	src := t.TempDir()
	managed := t.TempDir()
	gitToolMust(t, src, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitToolMust(t, src, "add", ".")
	gitToolMust(t, src, "commit", "-m", "seed")

	opener := &recordingOpener{err: fmt.Errorf("sidebar unavailable")}
	ctx := tool.WithWorktreeProjectOpener(context.Background(), opener)
	tl := openIsolatedWorktreeProject{workDir: src, managedRoot: managed}
	out, err := tl.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute must degrade to path-only text, got error: %v", err)
	}
	if !strings.Contains(out, "host registration failed") {
		t.Fatalf("output should explain the registration failure: %s", out)
	}
	if strings.Contains(out, `"hostRegistered"`) {
		t.Fatalf("failed registration must not report a registered payload: %s", out)
	}
	// Exactly one registration attempt was made for the created root.
	if len(opener.roots) != 1 || opener.roots[0] == "" {
		t.Fatalf("registration attempts = %v", opener.roots)
	}
	// The allocation survives so the path in the text is real.
	if info, err := os.Stat(opener.roots[0]); err != nil || !info.IsDir() {
		t.Fatalf("worktree root missing after failed registration: %v", err)
	}
}

// Task 128 acceptance: the write-authorization chain. The tool declares the
// managed storage root; after a session-scope grant (or automatically under
// task-127 full access), the freshly created worktree root — which did not
// exist at grant time for later allocations — is covered.
func TestOpenIsolatedWorktreeProjectWriteAuthChain(t *testing.T) {
	src := t.TempDir()
	managed := t.TempDir()
	gitToolMust(t, src, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitToolMust(t, src, "add", ".")
	gitToolMust(t, src, "commit", "-m", "seed")

	tl := openIsolatedWorktreeProject{workDir: src, managedRoot: managed}
	decl, err := tl.DeclareWriteAccess(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("DeclareWriteAccess: %v", err)
	}
	abs, _, _, err := sandbox.NormalizeWriteDirs(decl.Directories, src, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("gate normalization must accept the managed root: %v", err)
	}

	// Without a grant the managed root is missing from the session roots.
	roots := sandbox.NewWritableRootSet([]string{src})
	if missing := roots.Missing(abs); len(missing) == 0 {
		t.Fatal("managed root must be missing before any grant")
	}
	if roots.Covers(managed) {
		t.Fatal("ungranted managed root must not be covered")
	}

	// Session-scope approval (task 127 flow or the user's session choice):
	// the grant refreshes the live root set immediately.
	roots.GrantVerifiedSession(abs)
	if !roots.Covers(managed) {
		t.Fatal("after a session grant, the managed root must be covered")
	}
	// Create the worktree AFTER the grant, like a follow-up allocation: still
	// covered, because every worktree lands under the granted storage root.
	out, err := tl.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result struct {
		WorktreeRoot string `json:"worktreeRoot"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !roots.Covers(result.WorktreeRoot) {
		t.Fatalf("new worktree root %s must be writable under the granted managed root", result.WorktreeRoot)
	}

	// Task-127 full access path: unbounded sets cover everything without any
	// grant, so the creation+write flow never stops to ask.
	unbounded := sandbox.NewWritableRootSet([]string{src})
	unbounded.SetUnbounded(true)
	if !unbounded.Covers(managed) {
		t.Fatal("unbounded (full access) set must cover the managed root without a grant")
	}
}

func TestOpenIsolatedWorktreeProjectInvalidSourceLeavesNoShell(t *testing.T) {
	// Not a git repo: Create fails; managed storage must stay empty.
	src := t.TempDir()
	managed := t.TempDir()
	tl := openIsolatedWorktreeProject{workDir: src, managedRoot: managed}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("non-git source must fail")
	}
	entries, err := os.ReadDir(managed)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed create left %d entries in managed storage", len(entries))
	}
}
