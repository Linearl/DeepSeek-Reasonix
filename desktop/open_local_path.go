package main

import (
	"fmt"
	"os"

	"reasonix/internal/localopen"
)

// The path-safety gate moved to internal/localopen (task 944) so builtin
// tools can enforce the SAME gate as the opener family
// without importing package main. The wrappers below keep the desktop call
// sites and tests on their original names; behavior is byte-identical.

func normalizeLocalOpenPath(path string) (string, error) { return localopen.Normalize(path) }

func hasDisallowedWindowsPathSyntax(path string) bool {
	return localopen.HasDisallowedWindowsPathSyntax(path)
}

func openTargetAllowed(path string, isDir bool, mode os.FileMode) bool {
	return localopen.TargetAllowed(path, isDir, mode)
}

func openTargetPathAllowed(path string, info os.FileInfo) bool {
	return localopen.TargetPathAllowed(path, info)
}

// OpenLocalPath opens an arbitrary local absolute path (file or directory)
// with the OS default application. It backs clicking a local path rendered in
// chat markdown (issue #7426) — Windows drive paths, UNC paths and file:///
// URLs included.
func (a *App) OpenLocalPath(path string) error {
	path, err := normalizeLocalOpenPath(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !openTargetPathAllowed(path, info) {
		return fmt.Errorf("refusing to open executable target %q", path)
	}
	return openWorkspacePathWithType(path, info.IsDir())
}
