package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	goruntime "runtime"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/control"
	"reasonix/internal/fileref"
	fileenc "reasonix/internal/fileutil/encoding"
	"reasonix/internal/proc"
)

func trimUTF8PartialSuffix(data []byte) []byte {
	if utf8.Valid(data) {
		return data
	}
	for i := len(data) - 1; i >= 0 && len(data)-i <= utf8.UTFMax; i-- {
		if !utf8.RuneStart(data[i]) {
			continue
		}
		if !utf8.Valid(data[:i]) || utf8.FullRune(data[i:]) {
			return data
		}
		return data[:i]
	}
	return data
}

func previewMediaKind(path string) (kind string, mime string) {
	mime = previewMediaMIMEs[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		return "", ""
	}
	if strings.HasPrefix(mime, "image/") {
		return "image", mime
	}
	if mime == "application/pdf" {
		return "pdf", mime
	}
	return "", ""
}

func workspaceEntryRel(rel, name string) string {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." {
		return name
	}
	return rel + "/" + name
}

func skipWorkspaceEntry(rel, name string, isDir bool) bool {
	return fileref.SkipEntryForPanel(workspaceEntryRel(rel, name), name, isDir)
}

func (a *App) activeWorkspaceBase() (string, error) {
	return workspaceBaseFromRoot(a.activeWorkspaceRoot())
}

func (a *App) workspaceTargetForTab(tabID string) (string, control.SessionAPI, bool) {
	tabID = strings.TrimSpace(tabID)
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		if tabID == "" {
			return ".", nil, true
		}
		return "", nil, false
	}
	return tab.WorkspaceRoot, tab.Ctrl, true
}

func workspaceBaseFromRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" || root == "." {
		return os.Getwd()
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Clean(root), nil
}

func workspacePathForBase(base, rel string) (string, bool, error) {
	base = filepath.Clean(base)
	if rel == "" {
		return "", false, os.ErrInvalid
	}
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, rel)
	}
	path = filepath.Clean(path)
	r, err := filepath.Rel(base, path)
	if err != nil {
		return "", false, err
	}
	if r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
		return "", false, os.ErrPermission
	}
	return path, true, nil
}

// ListDir lists one directory level (directories first, then files, each
// alphabetical) for the "@" file-reference menu. rel resolves against the active
// tab workspace. The menu navigates one level at a time, never recursively —
// bounded for huge trees.
func (a *App) ListDir(rel string) []DirEntry {
	return a.ListDirForTab("", rel)
}

// ListDirForTab is the tab-scoped variant used by multi-tab frontend surfaces.
func (a *App) ListDirForTab(tabID, rel string) []DirEntry {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return []DirEntry{}
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		if entries, handled := browser.ListExternalFolderRefDir(rel); handled {
			return externalFolderDirEntries(entries)
		}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return []DirEntry{}
	}
	dir := base
	if rel != "" {
		path, ok, err := workspacePathForBase(base, rel)
		if err != nil || !ok {
			return []DirEntry{}
		}
		dir = path
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return []DirEntry{}
	}
	dirs, files := []DirEntry{}, []DirEntry{}
	for _, e := range es {
		name := e.Name()
		if skipWorkspaceEntry(rel, name, e.IsDir()) {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, DirEntry{Name: name, IsDir: true})
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, DirEntry{Name: name, IsDir: false})
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	return append(dirs, files...)
}

// SearchFileRefs finds workspace files by basename for bare "@token" completion.
func (a *App) SearchFileRefs(query string) []DirEntry {
	return a.SearchFileRefsForTab("", query)
}

// SearchFileRefsForTab is the tab-scoped variant used by multi-tab frontend surfaces.
func (a *App) SearchFileRefsForTab(tabID, query string) []DirEntry {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return []DirEntry{}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return []DirEntry{}
	}
	results := fileref.Search(base, query, fileRefSearchLimit)
	out := make([]DirEntry, 0, len(results))
	for _, r := range results {
		out = append(out, DirEntry{Name: r.Path, IsDir: r.IsDir})
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		out = append(out, externalFolderDirEntries(browser.SearchExternalFolderRefs(query, fileRefSearchLimit))...)
	}
	return out
}

type externalFolderRefBrowser interface {
	ListExternalFolderRefDir(tokenPath string) ([]control.ExternalFolderRefEntry, bool)
	SearchExternalFolderRefs(query string, limit int) []control.ExternalFolderRefEntry
	ExternalFolderRefLocalPath(tokenPath string) (path, displayPath string, ok bool)
}

func externalFolderRefBrowserFromController(ctrl control.SessionAPI) externalFolderRefBrowser {
	if browser, ok := ctrl.(externalFolderRefBrowser); ok {
		return browser
	}
	return nil
}

func externalFolderDirEntries(entries []control.ExternalFolderRefEntry) []DirEntry {
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, DirEntry{
			Name:        e.Name,
			Path:        e.Path,
			IsDir:       e.IsDir,
			DisplayName: e.DisplayName,
			DisplayPath: e.DisplayPath,
		})
	}
	return out
}

func (a *App) workspaceOrExternalPathForTab(tabID, rel string) (string, bool, error) {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return "", false, os.ErrNotExist
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		if path, _, ok := browser.ExternalFolderRefLocalPath(rel); ok {
			return path, true, nil
		}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return "", false, err
	}
	return workspacePathForBase(base, rel)
}

// ReadFile returns a small text preview for a file under the current workspace
// or a session-authorized external folder ref.
func (a *App) ReadFile(rel string) FilePreview {
	return a.ReadFileForTab("", rel)
}

// ReadFileForTab returns a preview resolved against the requested tab.
func (a *App) ReadFileForTab(tabID, rel string) FilePreview {
	out := FilePreview{Path: rel}
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		out.Err = "invalid path"
		return out
	}
	info, err := os.Stat(path)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	if info.IsDir() {
		out.Err = "path is a directory"
		return out
	}
	if !info.Mode().IsRegular() {
		out.Err = "path is not a regular file"
		return out
	}
	out.Size = info.Size()
	if kind, mime := previewMediaKind(path); kind != "" {
		token := a.ensureMediaTokenStore().create(path, info.Name(), mime, kind, info.Size(), info.ModTime())
		out.Kind = kind
		out.Mime = mime
		out.URL = "/__reasonix_workspace_media/" + token + "/" + url.PathEscape(info.Name())
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	defer f.Close()

	buf := make([]byte, filePreviewLimit+1)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		out.Err = err.Error()
		return out
	}
	data := buf[:n]
	if len(data) > filePreviewLimit {
		data = data[:filePreviewLimit]
		out.Truncated = true
	}

	// Check for BOM first (just the first 2-3 bytes — always complete
	// even at a truncation boundary). BOM-prefixed files skip the NUL
	// check since UTF-16 normally contains 0x00 for ASCII characters.
	bomKind := fileenc.DetectQuick(data)
	if bomKind != fileenc.UTF8 {
		enc, _ := fileenc.Detect(data)
		if enc == fileenc.LossyUTF8 {
			out.Binary = true
			return out
		}
		decoded := fileenc.Decode(data, enc)
		out.Body = string(decoded)
		return out
	}

	// No BOM — NUL in raw bytes is a binary signal.
	if bytes.Contains(data, []byte{0}) {
		out.Binary = true
		return out
	}

	// Trim any partial multi-byte rune at the truncation boundary BEFORE
	// encoding detection. Without this, a large UTF-8 file truncated
	// mid-character would fail utf8.Valid and be misdetected as GB18030
	// or LossyUTF8, producing mojibake or a false binary classification.
	if out.Truncated {
		data = trimUTF8PartialSuffix(data)
	}
	enc, _ := fileenc.Detect(data)
	if enc == fileenc.LossyUTF8 {
		out.Binary = true
		return out
	}
	out.Body = string(fileenc.Decode(data, enc))
	return out
}

// OpenWorkspacePath opens a workspace or authorized external-ref file/folder in
// the OS default app.
func (a *App) OpenWorkspacePath(rel string) error {
	return a.OpenWorkspacePathForTab("", rel)
}

// OpenWorkspacePathForTab opens a path resolved against the requested tab.
func (a *App) OpenWorkspacePathForTab(tabID, rel string) error {
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		return os.ErrInvalid
	}
	return openWorkspacePath(path)
}

// RevealWorkspacePath shows a workspace or authorized external-ref file in the
// native file manager.
func (a *App) RevealWorkspacePath(rel string) error {
	return a.RevealWorkspacePathForTab("", rel)
}

// RevealWorkspacePathForTab reveals a path resolved against the requested tab.
func (a *App) RevealWorkspacePathForTab(tabID, rel string) error {
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		return os.ErrInvalid
	}
	return revealPath(path)
}

// RevealPath shows an arbitrary absolute path in the native file manager.
func (a *App) RevealPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return os.ErrInvalid
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return revealPath(path)
}

var revealPath = defaultRevealPath

func defaultRevealPath(path string) error {
	switch goruntime.GOOS {
	case "darwin":
		return proc.VisibleCommand("open", "-R", path).Start()
	case "windows":
		// explorer.exe lives in %SystemRoot%, which isn't always on PATH (the
		// launch environment can strip it), so resolve it directly rather than
		// relying on a PATH lookup.
		explorer := "explorer.exe"
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = os.Getenv("windir")
		}
		if root != "" {
			explorer = filepath.Join(root, "explorer.exe")
		}
		return proc.VisibleCommand(explorer, "/select,", path).Start()
	default:
		dir := path
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			dir = filepath.Dir(path)
		}
		return proc.VisibleCommand("xdg-open", dir).Start()
	}
}

func (a *App) withActiveWorkspace(fn func() (string, error)) (string, error) {
	var result string
	err := a.withActiveWorkspaceDo(func() error {
		var err error
		result, err = fn()
		return err
	})
	return result, err
}

func (a *App) withActiveWorkspaceDo(fn func() error) error {
	root := a.activeWorkspaceRoot()
	if root != "" && root != "." {
		prev, err := os.Getwd()
		if err != nil {
			return err
		}
		if err := os.Chdir(root); err != nil {
			return err
		}
		defer func() { _ = os.Chdir(prev) }()
	}
	return fn()
}

// SavePastedImage stores a browser clipboard image data URL under the active
// tab's workspace .reasonix/attachments and returns the relative @-reference path.
func (a *App) SavePastedImage(dataURL string) (string, error) {
	return a.withActiveWorkspace(func() (string, error) {
		return control.SaveImageDataURLIn(a.activeWorkspaceRoot(), dataURL)
	})
}

// SaveClipboardImage reads the native OS clipboard image under the active tab's
// workspace .reasonix/attachments and returns the relative @-reference path.
func (a *App) SaveClipboardImage() (string, error) {
	return a.withActiveWorkspace(control.SaveClipboardImage)
}

// SavePastedFile stores a dropped non-image file (the browser exposes its bytes
// as a data URL but not a real path) under the active tab's workspace
// .reasonix/attachments and returns the relative @-reference path.
func (a *App) SavePastedFile(name, dataURL string) (string, error) {
	return a.withActiveWorkspace(func() (string, error) {
		return control.SaveAttachmentDataURL(name, dataURL)
	})
}

// PickExportFile opens the native save dialog and returns the selected path. It
// returns "" when the user cancels.
func (a *App) PickExportFile(defaultFilename, mimeType string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	defaultFilename = safeExportFilename(defaultFilename)
	ext := strings.ToLower(filepath.Ext(defaultFilename))
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "Export session",
		DefaultDirectory:     dialogDefaultDirectory(a.activeWorkspaceRoot()),
		DefaultFilename:      defaultFilename,
		CanCreateDirectories: true,
		Filters:              exportFileFilters(mimeType, ext),
	})
	if err != nil || path == "" {
		return "", err
	}
	if ext != "" && filepath.Ext(path) == "" {
		path += ext
	}
	return path, nil
}

// SaveExportFile writes an exported session payload to a path previously picked
// by PickExportFile. An empty path is treated as a cancelled export.
func (a *App) SaveExportFile(path, payload string, base64Encoded bool) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	var data []byte
	var err error
	if base64Encoded {
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return fmt.Errorf("decode export payload: %w", err)
		}
	} else {
		data = []byte(payload)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return exportOperationError("save export file", path, err)
	}
	return nil
}

// SaveExportImageFiles writes one or more base64-encoded image parts. A single
// image keeps the native save dialog's normal overwrite semantics. Multi-part
// exports use numbered sibling paths and never overwrite an existing sibling;
// every payload is staged before any target is committed, and a failed commit
// removes only files created by this call.
func (a *App) SaveExportImageFiles(path string, payloads []string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if len(payloads) == 0 {
		return errors.New("no image payloads to export")
	}
	if len(payloads) == 1 {
		return a.SaveExportFile(path, payloads[0], true)
	}

	targets := make([]string, len(payloads))
	for i := range payloads {
		targets[i] = numberedExportPath(path, i, len(payloads))
	}

	return saveExclusiveExportPayloads(targets, len(payloads), func(index int) ([]byte, error) {
		decoded, err := base64.StdEncoding.DecodeString(payloads[index])
		if err != nil {
			return nil, fmt.Errorf("decode export image part %d: %w", index+1, err)
		}
		return decoded, nil
	})
}

type stagedExportFile struct {
	targetPath string
	tempPath   string
}

type committedExportFile struct {
	path string
	info os.FileInfo
}

const exportTempCreateAttempts = 100

func numberedExportPath(path string, partIndex, partCount int) string {
	if partCount <= 1 {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	return fmt.Sprintf("%s-%d-of-%d%s", stem, partIndex+1, partCount, ext)
}

func saveExclusiveExportFiles(targets []string, payloads [][]byte) error {
	return saveExclusiveExportPayloads(targets, len(payloads), func(index int) ([]byte, error) {
		return payloads[index], nil
	})
}

func saveExclusiveExportPayloads(targets []string, payloadCount int, payloadAt func(int) ([]byte, error)) error {
	if len(targets) == 0 || len(targets) != payloadCount || payloadAt == nil {
		return errors.New("invalid export image batch")
	}
	for _, target := range targets {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("export file already exists: %s", filepath.Base(target))
		} else if !errors.Is(err, os.ErrNotExist) {
			return exportOperationError("inspect export target", target, err)
		}
	}

	staged := make([]stagedExportFile, 0, len(targets))
	defer func() {
		for _, file := range staged {
			_ = os.Remove(file.tempPath)
		}
	}()
	for i, target := range targets {
		payload, err := payloadAt(i)
		if err != nil {
			return err
		}
		file, finalMode, err := createExportTempFile(filepath.Dir(target))
		if err != nil {
			return exportOperationError("stage export file", target, err)
		}
		tempPath := file.Name()
		staged = append(staged, stagedExportFile{targetPath: target, tempPath: tempPath})
		if _, err = file.Write(payload); err == nil {
			err = file.Sync()
		}
		// Keep staged payloads private while they are incomplete, then restore
		// the same umask-adjusted mode used by SaveExportFile before publishing.
		if err == nil {
			err = file.Chmod(finalMode)
		}
		if err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return exportOperationError("stage export file", target, err)
		}
	}

	committed := make([]committedExportFile, 0, len(staged))
	for _, file := range staged {
		info, err := commitStagedExportFile(file.tempPath, file.targetPath)
		if err != nil {
			rollbackCommittedExportFiles(committed)
			return exportOperationError("save export file", file.targetPath, err)
		}
		committed = append(committed, committedExportFile{path: file.targetPath, info: info})
	}
	return nil
}

// createExportTempFile reserves a cryptographically random sibling path with
// the same requested mode as a normal export. It immediately narrows the mode
// while bytes are staged; the caller restores finalMode only after the payload
// has been completely written and synced.
func createExportTempFile(dir string) (*os.File, os.FileMode, error) {
	for range exportTempCreateAttempts {
		var suffix [12]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, 0, fmt.Errorf("generate export temp name: %w", err)
		}
		path := filepath.Join(dir, ".reasonix-export-"+hex.EncodeToString(suffix[:]))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err == nil {
			err = file.Chmod(0o600)
		}
		if err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, 0, err
		}
		return file, info.Mode().Perm(), nil
	}
	return nil, 0, errors.New("could not reserve a unique export temp file")
}

func commitStagedExportFile(tempPath, targetPath string) (os.FileInfo, error) {
	stagedInfo, err := os.Lstat(tempPath)
	if err != nil {
		return nil, err
	}
	// A hard link publishes a fully written staged file atomically and fails if
	// the target already exists. Some filesystems do not support hard links, so
	// fall back to an exclusive create while preserving the no-overwrite rule.
	if err := os.Link(tempPath, targetPath); err == nil {
		current, statErr := os.Lstat(targetPath)
		if statErr != nil {
			removeExportFileIfSame(targetPath, stagedInfo)
			return nil, statErr
		}
		if !os.SameFile(current, stagedInfo) {
			return nil, errors.New("export target changed while it was being saved")
		}
		return stagedInfo, nil
	}

	source, err := os.Open(tempPath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	info, statErr := target.Stat()
	if statErr == nil {
		_, err = io.Copy(target, source)
	}
	if err == nil && statErr == nil {
		err = target.Sync()
	}
	if closeErr := target.Close(); err == nil && statErr == nil {
		err = closeErr
	}
	if statErr != nil {
		err = statErr
	}
	if err != nil {
		removeExportFileIfSame(targetPath, info)
		return nil, err
	}
	return info, nil
}

func rollbackCommittedExportFiles(files []committedExportFile) {
	for _, file := range files {
		removeExportFileIfSame(file.path, file.info)
	}
}

func removeExportFileIfSame(path string, created os.FileInfo) {
	if created == nil {
		return
	}
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(current, created) {
		_ = os.Remove(path)
	}
}

func exportOperationError(operation, path string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("%s %s: %w", operation, filepath.Base(path), pathErr.Err)
	}
	return fmt.Errorf("%s %s: %w", operation, filepath.Base(path), err)
}

func safeExportFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "reasonix-session.md"
	}
	return filepath.Base(name)
}

func exportFileFilters(mimeType, ext string) []runtime.FileFilter {
	switch mimeType {
	case "text/markdown":
		return []runtime.FileFilter{{DisplayName: "Markdown (*.md)", Pattern: "*.md"}}
	case "application/json":
		return []runtime.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}}
	case "application/pdf":
		return []runtime.FileFilter{{DisplayName: "PDF (*.pdf)", Pattern: "*.pdf"}}
	case "image/png":
		return []runtime.FileFilter{{DisplayName: "PNG image (*.png)", Pattern: "*.png"}}
	}
	if ext != "" {
		return []runtime.FileFilter{{DisplayName: strings.ToUpper(strings.TrimPrefix(ext, ".")) + " files (*" + ext + ")", Pattern: "*" + ext}}
	}
	return []runtime.FileFilter{{DisplayName: "All files (*.*)", Pattern: "*.*"}}
}

// AttachmentDataURL returns a safe data URL for a stored image attachment.
func (a *App) AttachmentDataURL(path string) (string, error) {
	return a.withActiveWorkspace(func() (string, error) {
		return control.ImageDataURLIn(a.activeWorkspaceRoot(), path)
	})
}

// DroppedItem is one OS-dropped file resolved into a composer context entry: an
// in-tree file becomes a workspace @reference (read in place, no copy), while an
// outside directory becomes a session-scoped workspace @reference; an image or
// out-of-tree file is copied into .reasonix/attachments.
type DroppedItem struct {
	Kind        string `json:"kind"` // "workspace" | "attachment"
	Path        string `json:"path"`
	IsDir       bool   `json:"isDir,omitempty"`
	DisplayPath string `json:"displayPath,omitempty"`
	PreviewURL  string `json:"previewUrl,omitempty"`
}

// AttachDropped turns an absolute path from the native file-drop bridge into a
// composer context entry. Images are stored as attachments so the chip shows a
// thumbnail; in-workspace files are referenced relatively (no copy); directories
// outside the workspace are registered as current-session folder references;
// files outside the workspace are copied into .reasonix/attachments.
func (a *App) AttachDropped(path string) (DroppedItem, error) {
	var item DroppedItem
	err := a.withActiveWorkspaceDo(func() error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isImageExt(path) {
			if rel, err := control.SaveImageFileIn(a.activeWorkspaceRoot(), path); err == nil {
				preview, _ := control.ImageDataURLIn(a.activeWorkspaceRoot(), rel)
				item = DroppedItem{Kind: "attachment", Path: rel, PreviewURL: preview}
				return nil
			}
		}
		if rel, ok := workspaceRelativeIn(path, a.activeWorkspaceRoot()); ok {
			item = DroppedItem{Kind: "workspace", Path: rel, IsDir: info.IsDir()}
			return nil
		}
		if info.IsDir() {
			tab, ctrl := a.tabAndCtrlByID("")
			if err := a.ensureTabControllerWorkspace(tab); err != nil {
				return err
			}
			if tab != nil {
				ctrl = a.controllerForTab(tab)
			}
			if ctrl == nil {
				return fmt.Errorf("workspace is not ready")
			}
			token, displayPath, err := ctrl.RegisterExternalFolderRef(path)
			if err != nil {
				return err
			}
			item = DroppedItem{Kind: "workspace", Path: token, IsDir: true, DisplayPath: displayPath}
			return nil
		}
		rel, err := control.SaveAttachmentFileIn(a.activeWorkspaceRoot(), path)
		if err != nil {
			return err
		}
		item = DroppedItem{Kind: "attachment", Path: rel}
		return nil
	})
	if err != nil {
		return DroppedItem{}, err
	}
	return item, nil
}

func isImageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func workspaceRelativeIn(path, workspaceRoot string) (string, bool) {
	root := workspaceRoot
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", false
		}
		root = abs
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// PickWorkspace opens a folder chooser and, on a pick, opens a new project tab
// scoped to that folder. Returns the chosen path ("" if cancelled).
func (a *App) PickWorkspace() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur, _ := os.Getwd()
	a.mu.RLock()
	if tab := a.activeTabLocked(); tab != nil && tab.WorkspaceRoot != "" {
		cur = tab.WorkspaceRoot
	}
	a.mu.RUnlock()
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose working folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return a.SwitchWorkspace(dir)
}

func dialogDefaultDirectory(preferred string) string {
	if dir := nearestExistingDirectory(preferred); dir != "" {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil {
		if dir := nearestExistingDirectory(cwd); dir != "" {
			return dir
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if dir := nearestExistingDirectory(home); dir != "" {
			return dir
		}
	}
	return ""
}

func nearestExistingDirectory(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	for {
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return path
			}
			path = filepath.Dir(path)
			continue
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}
