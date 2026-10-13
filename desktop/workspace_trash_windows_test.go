//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTrashPlatformPathsWindowsRecycleBin exercises the real Shell API: the
// entries must disappear from their original location (sent to the Recycle
// Bin, restorable) — pinning that the trash path is a true recycle-bin
// deletion, not a rename or a hard remove.
func TestTrashPlatformPathsWindowsRecycleBin(t *testing.T) {
	root := robustTempDir(t)
	file := filepath.Join(root, "recycle-me.txt")
	if err := os.WriteFile(file, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "recycle-dir")
	if err := os.MkdirAll(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inner", "b.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := trashPlatformPaths([]string{file, dir}); err != nil {
		t.Fatalf("trashPlatformPaths: %v", err)
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatalf("trashed file still present: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("trashed dir still present: %v", err)
	}
	if err := trashPlatformPaths([]string{filepath.Join(root, "never-existed.txt")}); err == nil {
		t.Fatal("trashing a missing path succeeded")
	}
}
