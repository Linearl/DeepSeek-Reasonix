package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/control"
)

func newFileOpsTestApp(t *testing.T) (*App, string) {
	t.Helper()
	root := robustTempDir(t)
	app := &App{tabs: map[string]*WorkspaceTab{
		"tab": {ID: "tab", Scope: "project", WorkspaceRoot: root},
	}}
	return app, root
}

func writeFileOpsFixture(t *testing.T, root, rel string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenameWorkspaceEntryForTab(t *testing.T) {
	app, root := newFileOpsTestApp(t)
	old := writeFileOpsFixture(t, root, "notes/old-name.txt")

	newRel, err := app.RenameWorkspaceEntryForTab("tab", "notes/old-name.txt", "new-name.txt")
	if err != nil {
		t.Fatalf("RenameWorkspaceEntryForTab: %v", err)
	}
	if newRel != "notes/new-name.txt" {
		t.Fatalf("new rel = %q, want notes/new-name.txt", newRel)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatalf("old path still present: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "notes", "new-name.txt")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}

	// Trailing-slash dir paths from the panel tree rename the directory.
	if err := os.MkdirAll(filepath.Join(root, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RenameWorkspaceEntryForTab("tab", "folder/", "renamed"); err != nil {
		t.Fatalf("dir rename: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "renamed")); err != nil {
		t.Fatalf("renamed dir missing: %v", err)
	}

	// Case-only rename on case-insensitive filesystems, plain duplicate on
	// case-sensitive ones: either way the result must exist under the new
	// spelling when the target spelling was free.
	if _, err := app.RenameWorkspaceEntryForTab("tab", "notes/new-name.txt", "OTHER.txt"); err != nil {
		t.Fatalf("case rename: %v", err)
	}
}

func TestRenameWorkspaceEntryRejections(t *testing.T) {
	app, root := newFileOpsTestApp(t)
	writeFileOpsFixture(t, root, "a.txt")
	writeFileOpsFixture(t, root, "b.txt")

	cases := []struct {
		label string
		rel   string
		name  string
	}{
		{"empty name", "a.txt", "  "},
		{"dot name", "a.txt", "."},
		{"dotdot name", "a.txt", ".."},
		{"separator", "a.txt", `sub\x`},
		{"unix separator", "a.txt", "sub/x"},
		{"collision", "a.txt", "b.txt"},
		{"missing source", "gone.txt", "x.txt"},
		{"path escape", "../outside.txt", "x.txt"},
	}
	for _, tc := range cases {
		if _, err := app.RenameWorkspaceEntryForTab("tab", tc.rel, tc.name); err == nil {
			t.Fatalf("%s: rename accepted", tc.label)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatalf("rejected rename damaged the source: %v", err)
	}
}

func TestTrashWorkspaceEntriesForTabGoesThroughPlatformTrash(t *testing.T) {
	app, root := newFileOpsTestApp(t)
	fileA := writeFileOpsFixture(t, root, "a.txt")
	dirB := filepath.Join(root, "sub")
	writeFileOpsFixture(t, root, "sub/b.txt")

	var platformCalls [][]string
	previous := workspaceTrashPlatform
	workspaceTrashPlatform = func(paths []string) error {
		clone := append([]string(nil), paths...)
		platformCalls = append(platformCalls, clone)
		return nil
	}
	t.Cleanup(func() { workspaceTrashPlatform = previous })

	res := app.TrashWorkspaceEntriesForTab("tab", []string{"a.txt", "sub/", "missing.txt"})
	if len(platformCalls) != 1 {
		t.Fatalf("platform trash calls = %d, want exactly one batch call", len(platformCalls))
	}
	wantPaths := []string{fileA, dirB}
	if len(platformCalls[0]) != len(wantPaths) {
		t.Fatalf("batch paths = %v, want %v", platformCalls[0], wantPaths)
	}
	for i, want := range wantPaths {
		if platformCalls[0][i] != want {
			t.Fatalf("batch path[%d] = %q, want %q", i, platformCalls[0][i], want)
		}
	}
	if len(res.Succeeded) != 2 || len(res.Failed) != 1 || res.Failed[0].Path != "missing.txt" {
		t.Fatalf("result = %+v, want two succeeded and missing.txt failed", res)
	}
	// The spy does not delete: entries must still be on disk, proving the
	// panel trash path performs no self-managed removal.
	if _, err := os.Lstat(fileA); err != nil {
		t.Fatalf("trash path removed the file outside the platform API: %v", err)
	}
	if _, err := os.Lstat(dirB); err != nil {
		t.Fatalf("trash path removed the dir outside the platform API: %v", err)
	}
}

func TestTrashWorkspaceEntriesReportsPlatformFailurePerEntry(t *testing.T) {
	app, root := newFileOpsTestApp(t)
	writeFileOpsFixture(t, root, "a.txt")
	previous := workspaceTrashPlatform
	workspaceTrashPlatform = func([]string) error { return errors.New("platform refused") }
	t.Cleanup(func() { workspaceTrashPlatform = previous })

	res := app.TrashWorkspaceEntriesForTab("tab", []string{"a.txt"})
	if len(res.Succeeded) != 0 || len(res.Failed) != 1 || res.Failed[0].Path != "a.txt" {
		t.Fatalf("result = %+v, want a.txt failed", res)
	}
	if !strings.Contains(res.Failed[0].Err, "platform refused") {
		t.Fatalf("failure detail lost: %+v", res.Failed[0])
	}
}

func TestDeleteWorkspaceEntriesPermanentForTab(t *testing.T) {
	app, root := newFileOpsTestApp(t)
	fileA := writeFileOpsFixture(t, root, "a.txt")
	dirB := filepath.Join(root, "sub")
	writeFileOpsFixture(t, root, "sub/deep/b.txt")

	res := app.DeleteWorkspaceEntriesPermanentForTab("tab", []string{"a.txt", "sub/", "missing.txt"})
	if len(res.Succeeded) != 2 || len(res.Failed) != 1 {
		t.Fatalf("result = %+v, want two succeeded one failed", res)
	}
	if _, err := os.Lstat(fileA); !os.IsNotExist(err) {
		t.Fatalf("permanent delete left a.txt: %v", err)
	}
	if _, err := os.Lstat(dirB); !os.IsNotExist(err) {
		t.Fatalf("permanent delete left dir: %v", err)
	}
}

func TestWorkspaceEntryOpsRejectExternalFolderRef(t *testing.T) {
	external := filepath.Join(robustTempDir(t), "external")
	if err := os.MkdirAll(filepath.Join(external, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctrl := &control.Controller{}
	token, _, err := ctrl.RegisterExternalFolderRef(external)
	if err != nil {
		t.Fatalf("RegisterExternalFolderRef: %v", err)
	}
	app := &App{tabs: map[string]*WorkspaceTab{
		"tab": {ID: "tab", Scope: "project", WorkspaceRoot: robustTempDir(t), Ctrl: ctrl},
	}}

	rel := token + "/src/keep.txt"
	if _, err := app.RenameWorkspaceEntryForTab("tab", rel, "moved.txt"); err == nil {
		t.Fatal("rename accepted an external folder ref")
	}
	if res := app.TrashWorkspaceEntriesForTab("tab", []string{rel}); len(res.Failed) != 1 {
		t.Fatalf("trash accepted an external folder ref: %+v", res)
	}
	if res := app.DeleteWorkspaceEntriesPermanentForTab("tab", []string{rel}); len(res.Failed) != 1 {
		t.Fatalf("permanent delete accepted an external folder ref: %+v", res)
	}
	if _, err := os.Lstat(filepath.Join(external, "src")); err != nil {
		t.Fatalf("external folder was damaged: %v", err)
	}
}

// TestTrashPlatformFilesUseNativeTrashAPI pins the task-855 iron rule at the
// source level: the platform trash files implement recycle-bin deletion via
// the OS APIs (SHFileOperationW / trashItemAtURL / gio trash) and never fall
// back to direct removal.
func TestTrashPlatformFilesUseNativeTrashAPI(t *testing.T) {
	markers := map[string][]string{
		"workspace_trash_windows.go": {"SHFileOperationW", "fofAllowUndo"},
		"workspace_trash_darwin.m":   {"trashItemAtURL"},
		"workspace_trash_linux.go":   {"gio", "trash"},
	}
	for name, wants := range markers {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(body)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s lost the native trash marker %q", name, want)
			}
		}
		for _, ban := range []string{"os.RemoveAll", "os.Remove(", "os.Rename("} {
			if strings.Contains(text, ban) {
				t.Fatalf("%s must not self-delete via %s", name, ban)
			}
		}
	}
}
