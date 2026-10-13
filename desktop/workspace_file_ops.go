package main

// 任务 855：文件面板重命名 / 删除操作的后端入口。
//
// 边界口径（与面板既有写操作一致，参照 9167/9174 已授权写目录体系）：
// 面板操作的对象是用户自己的工作区文件，路径经 workspacePathForBase 围栏
// 限制在工作区根内（等价于资源管理器打开该目录的能力范围）；会话授权的
// 外部文件夹引用（ExternalFolderRef）是只读引用，对它拒绝一切破坏性操作。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type WorkspaceFileOpFailure struct {
	Path string `json:"path"`
	Err  string `json:"err"`
}

type WorkspaceFileOpsResult struct {
	Succeeded []string                 `json:"succeeded"`
	Failed    []WorkspaceFileOpFailure `json:"failed"`
}

func newWorkspaceFileOpsResult() WorkspaceFileOpsResult {
	return WorkspaceFileOpsResult{Succeeded: []string{}, Failed: []WorkspaceFileOpFailure{}}
}

func (r WorkspaceFileOpsResult) fail(path string, err error) WorkspaceFileOpsResult {
	r.Failed = append(r.Failed, WorkspaceFileOpFailure{Path: path, Err: err.Error()})
	return r
}

// normalizeWorkspaceEntryRel cleans a panel path (dirs carry a trailing "/")
// into the slash-free relative form the path helpers expect.
func normalizeWorkspaceEntryRel(rel string) (string, error) {
	rel = strings.TrimSpace(strings.Trim(filepath.ToSlash(rel), "/"))
	if rel == "" || rel == "." || rel == ".." {
		return "", os.ErrInvalid
	}
	return rel, nil
}

// workspaceEntryAbsoluteForTab resolves a panel path to an absolute path and
// its workspace base, rejecting external folder-reference tokens and any
// escape from the workspace root.
func (a *App) workspaceEntryAbsoluteForTab(tabID, rel string) (path, base string, err error) {
	cleanRel, err := normalizeWorkspaceEntryRel(rel)
	if err != nil {
		return "", "", fmt.Errorf("invalid workspace path %q", rel)
	}
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return "", "", fmt.Errorf("workspace tab is not available")
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		if _, _, isExternal := browser.ExternalFolderRefLocalPath(cleanRel); isExternal {
			return "", "", fmt.Errorf("%q is a session folder reference, not a workspace file", cleanRel)
		}
	}
	base, err = workspaceBaseFromRoot(root)
	if err != nil {
		return "", "", err
	}
	path, inWorkspace, err := workspacePathForBase(base, cleanRel)
	if err != nil {
		return "", "", err
	}
	if !inWorkspace {
		return "", "", os.ErrPermission
	}
	return path, base, nil
}

// RenameWorkspaceEntry renames one workspace entry in place (same directory).
// It returns the new workspace-relative slash path.
func (a *App) RenameWorkspaceEntry(rel, newName string) (string, error) {
	return a.RenameWorkspaceEntryForTab("", rel, newName)
}

func (a *App) RenameWorkspaceEntryForTab(tabID, rel, newName string) (string, error) {
	if a == nil {
		return "", fmt.Errorf("no active app")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" || newName == "." || newName == ".." {
		return "", fmt.Errorf("a file name is required")
	}
	if strings.ContainsAny(newName, `/\`) || strings.ContainsRune(newName, 0) {
		return "", fmt.Errorf("the new name must not contain path separators")
	}
	oldPath, base, err := a.workspaceEntryAbsoluteForTab(tabID, rel)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(oldPath); err != nil {
		return "", err
	}
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	// Case-only renames (README.md → readme.md) are not a collision on
	// case-insensitive filesystems; on case-sensitive systems the duplicate
	// check below stays strict.
	sameFile := newPath == oldPath
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		sameFile = sameFile || strings.EqualFold(newPath, oldPath)
	}
	if !sameFile {
		if _, err := os.Lstat(newPath); err == nil {
			return "", fmt.Errorf("%q already exists in this folder", newName)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return "", err
	}
	newRel, err := filepath.Rel(base, newPath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(newRel), nil
}

// TrashWorkspaceEntries moves workspace entries to the OS trash (recycle-bin
// semantics, restorable). Batch: every entry is attempted, failures aggregate.
func (a *App) TrashWorkspaceEntries(rels []string) WorkspaceFileOpsResult {
	return a.TrashWorkspaceEntriesForTab("", rels)
}

func (a *App) TrashWorkspaceEntriesForTab(tabID string, rels []string) WorkspaceFileOpsResult {
	res := newWorkspaceFileOpsResult()
	if a == nil {
		return res.fail(strings.Join(rels, ","), fmt.Errorf("no active app"))
	}
	type trashedEntry struct{ rel, path string }
	entries := make([]trashedEntry, 0, len(rels))
	for _, rel := range rels {
		path, _, err := a.workspaceEntryAbsoluteForTab(tabID, rel)
		if err != nil {
			res = res.fail(rel, err)
			continue
		}
		if _, err := os.Lstat(path); err != nil {
			res = res.fail(rel, err)
			continue
		}
		entries = append(entries, trashedEntry{rel, path})
	}
	if len(entries) == 0 {
		return res
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.path)
	}
	if err := trashWorkspacePaths(paths); err != nil {
		// The platform call is batch-wide; report the failure against every
		// entry it covered so the panel can surface all of them.
		for _, e := range entries {
			res = res.fail(e.rel, err)
		}
		return res
	}
	for _, e := range entries {
		res.Succeeded = append(res.Succeeded, e.rel)
	}
	return res
}

// DeleteWorkspaceEntriesPermanent permanently removes workspace entries
// (os.RemoveAll, recursive for directories). The frontend must run the
// double-confirm dialog with the non-restorable warning before calling.
func (a *App) DeleteWorkspaceEntriesPermanent(rels []string) WorkspaceFileOpsResult {
	return a.DeleteWorkspaceEntriesPermanentForTab("", rels)
}

func (a *App) DeleteWorkspaceEntriesPermanentForTab(tabID string, rels []string) WorkspaceFileOpsResult {
	res := newWorkspaceFileOpsResult()
	if a == nil {
		return res.fail(strings.Join(rels, ","), fmt.Errorf("no active app"))
	}
	for _, rel := range rels {
		path, _, err := a.workspaceEntryAbsoluteForTab(tabID, rel)
		if err != nil {
			res = res.fail(rel, err)
			continue
		}
		if _, err := os.Lstat(path); err != nil {
			res = res.fail(rel, err)
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			res = res.fail(rel, err)
			continue
		}
		res.Succeeded = append(res.Succeeded, rel)
	}
	return res
}
