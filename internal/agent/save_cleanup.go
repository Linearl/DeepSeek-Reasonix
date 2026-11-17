package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/store"
)

// Cleanup 维护域（tl1633 自 save.go 纯移动拆出）：延迟清理标记、
// 侧车对账、超长文件名改名与 branch reparent。

const (
	cleanupPendingExt = ".cleanup-pending.json"

	guardianSidecarSuffix = ".guardian.jsonl"

	// nameMaxBytes is the single-component filename limit shared by the
	// filesystems Reasonix targets (APFS, ext4, NTFS all cap at 255).
	nameMaxBytes = 255

	// maxSessionBasenameBytes bounds transcript basenames that reconciliation
	// leaves in place. Sidecars append up to ~16 bytes to the transcript name
	// or its stem (".lease.lock", ".cleanup-pending.json", ".guardian.jsonl"),
	// so 224 keeps every sidecar comfortably under nameMaxBytes with headroom
	// for future suffixes. Names past this bound come from the pre-bounded
	// recovery cascade and get renamed by reconcileOverlongSessionFilenames.
	maxSessionBasenameBytes = 224
)

// CleanupPendingMeta records that a session was logically removed but still has
// artifacts waiting for a background job to unwind before physical cleanup.
type CleanupPendingMeta struct {
	Operation string `json:"operation"`
	CreatedAt int64  `json:"createdAt"`
}

// CleanupPendingInfo describes one durable delayed-cleanup marker and the
// session transcript it belongs to.
type CleanupPendingInfo struct {
	SessionPath string
	MarkerPath  string
	Meta        CleanupPendingMeta
}

// CleanupPendingPath returns the durable marker path for a session transcript.
// Pure derivation with no filesystem side effects. The three sink operations
// (Mark/Clear/Is) must go through cleanupPendingMarkerPath instead, which
// anchors and boundary-checks the untrusted session path (tl1681).
func CleanupPendingPath(sessionPath string) string {
	return store.SessionCleanupPending(sessionPath)
}

// cleanupPendingMarkerPath is the tl1681 guard shared by the three delayed-
// cleanup sink operations (MarkCleanupPending / ClearCleanupPending /
// IsCleanupPending). sessionPath arrives from enumeration and delete-flow
// callers and is treated as untrusted: it must never relocate the marker away
// from the canonical transcript's own sidecar slot.
//
// Three rejection gates wrap one meaning-preserving anchor:
//
//   - Anchor: the marker is derived only from fileutil.SanitizeFilePath's
//     output — the tl1373 Clean+Abs sanitizer shape (recognized by the CodeQL
//     go/path-injection query as a taint cut). Clean callers are byte-for-byte
//     unaffected: their marker spelling is identical to the pre-guard form,
//     merely anchored absolute earlier (relative callers resolved against the
//     working directory before, so the same file is targeted).
//   - Gate 1 (../ traversal injection): a raw spelling that Clean would rewrite
//     (dot segments, redundant or doubled separators) is rejected outright.
//     Every production caller passes enumeration output, whose spelling is
//     already clean, so no legitimate flow carries dot segments.
//   - Gate 2 (non-local absolute injection): UNC shares, \\?\ device forms and
//     drive-relative spellings are rejected unless the resulting directory
//     physically sits inside a configured session storage root — a
//     REASONIX_STATE_HOME pointed at a share is a legal setup and stays
//     sanctioned.
//
// Gates 1 and 2 are purely lexical and safe on hot list surfaces. The
// write/delete sinks additionally run markerResolvesWithinSessionRoots (gate
// 3, symlink escape), which costs a bounded physical resolution per call and
// would be too expensive per item in IsCleanupPending's list loops — a
// read-only Stat cannot escape the root anyway.
func cleanupPendingMarkerPath(sessionPath string) (string, error) {
	trimmed := strings.TrimSpace(sessionPath)
	if trimmed == "" {
		return "", nil
	}
	canonical := fileutil.SanitizeFilePath(trimmed)
	marker := store.SessionCleanupPending(canonical)
	if marker == "" {
		return "", nil
	}
	if err := guardCleanupPendingSpelling(trimmed, marker); err != nil {
		return "", err
	}
	return marker, nil
}

// guardCleanupPendingSpelling runs the two lexical gates of
// cleanupPendingMarkerPath against the raw input.
func guardCleanupPendingSpelling(raw, marker string) error {
	// Gate 1: any spelling Clean would rewrite carries a relocation payload.
	// FromSlash first, so Windows callers using forward-slash spellings (the
	// OS accepts them and pre-guard behavior targeted the same file) are not
	// mistaken for traversal.
	if filepath.Clean(raw) != filepath.FromSlash(raw) {
		return fmt.Errorf("cleanup-pending path %q: session path must be a clean spelling without traversal (tl1681)", raw)
	}
	// Gate 2: non-local absolute shapes (UNC, \\?\, drive-relative) must be
	// sanctioned by a configured storage root, checked by physical identity.
	if nonLocalPathSpelling(raw) && !physicallyWithinConfiguredSessionRoots(filepath.Dir(marker)) {
		return fmt.Errorf("cleanup-pending path %q: outside the configured session storage roots (tl1681)", raw)
	}
	return nil
}

// nonLocalPathSpelling reports whether p is a Windows absolute shape that
// reaches beyond the local drive namespace: UNC shares, \\?\ device paths, or
// a drive-relative spelling ("C:evil.jsonl") that the OS anchors against the
// drive's current directory. On Unix every absolute path is a local shape —
// backslashes are ordinary filename bytes there — so the gate is Windows-only
// and never fires elsewhere.
func nonLocalPathSpelling(p string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if vol := filepath.VolumeName(p); len(vol) == 2 && len(p) > 2 && p[2] != '\\' && p[2] != '/' {
		return true
	}
	return false
}

// markerResolvesWithinSessionRoots is gate 3 (tl1681) for the write/delete
// sinks. When the marker's directory lexically sits inside a configured
// session storage root, the directory's physical identity (symlinks and
// junctions resolved, Windows case folding included) must still land inside
// the correspondingly resolved roots — a directory symlink or junction
// planted inside the root cannot smuggle marker writes and removals past the
// storage boundary. Directories outside every configured root (test fixtures,
// external enumerations) have no root to escape from and keep the pre-guard
// behavior.
func markerResolvesWithinSessionRoots(marker string) error {
	dir := filepath.Dir(marker)
	if !lexicallyWithinConfiguredSessionRoots(dir) {
		return nil
	}
	if !physicallyWithinConfiguredSessionRoots(dir) {
		return fmt.Errorf("cleanup-pending path %q: directory resolves outside the configured session storage roots through a symlink (tl1681)", dir)
	}
	return nil
}

// configuredSessionStorageRoots derives the sanctioned session storage roots
// from configuration: the global v3 and v4 roots plus every per-workspace
// sessions/sessions-v4 directory currently on disk. Derived per call so env
// overrides (tests, REASONIX_STATE_HOME) take effect immediately.
func configuredSessionStorageRoots() []string {
	var roots []string
	if d := config.SessionDir(); d != "" {
		roots = append(roots, d)
	}
	if d := config.SessionStoreDir(); d != "" {
		roots = append(roots, d)
	}
	if base := config.MemoryUserDir(); base != "" {
		projects := filepath.Join(base, "projects")
		entries, err := os.ReadDir(projects)
		if err != nil {
			return roots
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			slug := filepath.Join(projects, e.Name())
			roots = append(roots, filepath.Join(slug, "sessions"), filepath.Join(slug, "sessions-v4"))
		}
	}
	return roots
}

// lexicallyWithinConfiguredSessionRoots reports whether dir sits inside one
// of the configured roots by spelling alone (no filesystem resolution).
func lexicallyWithinConfiguredSessionRoots(dir string) bool {
	dir = lexicalPathKey(dir)
	for _, root := range configuredSessionStorageRoots() {
		if dirWithinRootKey(dir, lexicalPathKey(root)) {
			return true
		}
	}
	return false
}

// physicallyWithinConfiguredSessionRoots reports whether dir's physical
// identity (symlinks and junctions resolved, Windows case folded) sits inside
// one of the correspondingly resolved configured roots.
func physicallyWithinConfiguredSessionRoots(dir string) bool {
	dir = lexicalPathKey(resolvePhysicalPath(dir))
	for _, root := range configuredSessionStorageRoots() {
		if dirWithinRootKey(dir, lexicalPathKey(resolvePhysicalPath(root))) {
			return true
		}
	}
	return false
}

// resolvePhysicalPath resolves dir's physical location by climbing components
// and following reparse points via os.Readlink. filepath.EvalSymlinks cannot
// stand in for this on Windows: since Go 1.26, Lstat no longer marks directory
// junctions with ModeSymlink, so EvalSymlinks leaves them unresolved while
// junctions remain the unprivileged directory-alias primitive of that
// platform (mklink /J). Readlink succeeds on both junctions and symlinks, so
// one walk covers both. Only the write/delete sinks call this, so the extra
// per-component syscalls stay off the hot list surfaces.
func resolvePhysicalPath(dir string) string {
	cur := filepath.Clean(dir)
	tail := ""
	for {
		if target, err := os.Readlink(cur); err == nil && target != "" {
			// cur is a reparse point; follow it and keep scanning — the
			// target itself may chain further links. The tail collected so
			// far stays pending: it hangs below cur wherever cur ends up.
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			cur = filepath.Clean(target)
			continue
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
	return filepath.Join(cur, tail)
}

// lexicalPathKey normalizes a path for containment comparison: cleaned,
// absolute, case folded on Windows. It performs no filesystem resolution —
// resolvePhysicalPath supplies the physical form.
func lexicalPathKey(p string) string {
	key := filepath.Clean(strings.TrimSpace(p))
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

// dirWithinRootKey reports whether the normalized dir equals root or lies
// strictly inside it. Both inputs must come from the same normalization form.
func dirWithinRootKey(dir, root string) bool {
	if root == "" || dir == "" {
		return false
	}
	if dir == root {
		return true
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// MarkCleanupPending hides a logically removed session from resume/list surfaces
// until delayed physical cleanup has finished.
func MarkCleanupPending(sessionPath, operation string) error {
	path, err := cleanupPendingMarkerPath(sessionPath)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	if err := markerResolvesWithinSessionRoots(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	meta := CleanupPendingMeta{Operation: strings.TrimSpace(operation), CreatedAt: time.Now().UnixMilli()}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	// The marker controls session visibility during delayed cleanup. Publish it
	// atomically so a crash cannot leave malformed JSON that hides the session
	// and blocks reconciliation on the next startup.
	return fileutil.AtomicWriteFile(path, b, 0o644)
}

// ClearCleanupPending removes a delayed-cleanup marker after physical cleanup.
func ClearCleanupPending(sessionPath string) error {
	path, err := cleanupPendingMarkerPath(sessionPath)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	if err := markerResolvesWithinSessionRoots(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsCleanupPending reports whether a session is hidden pending delayed cleanup.
// A session path that fails the tl1681 guard is reported as not pending: the
// marker subsystem has no jurisdiction over out-of-domain paths, and Mark
// would have refused to publish a marker for them.
func IsCleanupPending(sessionPath string) bool {
	path, err := cleanupPendingMarkerPath(sessionPath)
	if err != nil || path == "" {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// IsVisibleSession reports whether a persisted session should appear on normal
// user/agent-facing list, restore, and retrieval surfaces.
func IsVisibleSession(sessionPath string) bool {
	return strings.TrimSpace(sessionPath) != "" && !IsCleanupPending(sessionPath)
}

// ListCleanupPending returns delayed-cleanup markers left in dir. A missing
// directory is not an error.
func ListCleanupPending(dir string) ([]CleanupPendingInfo, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CleanupPendingInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), cleanupPendingExt) {
			continue
		}
		markerPath := filepath.Join(dir, e.Name())
		var meta CleanupPendingMeta
		b, err := fileencoding.ReadFileUTF8(markerPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if strings.TrimSpace(string(b)) != "" {
			if err := json.Unmarshal(b, &meta); err != nil {
				return nil, fmt.Errorf("read cleanup-pending marker %s: %w", markerPath, err)
			}
		}
		name := strings.TrimSuffix(e.Name(), cleanupPendingExt) + ".jsonl"
		out = append(out, CleanupPendingInfo{
			SessionPath: filepath.Join(dir, name),
			MarkerPath:  markerPath,
			Meta:        meta,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SessionPath < out[j].SessionPath
	})
	return out, nil
}

// ReconcileCleanupPending retries physical cleanup for leftover delayed-cleanup
// markers and stale lock/lease sidecars. It keeps going after individual
// cleanup errors and returns them joined.
func ReconcileCleanupPending(dir string, cleanup func(CleanupPendingInfo) error) error {
	var errs []error
	if err := ReconcileSessionSidecars(dir); err != nil {
		errs = append(errs, err)
	}
	if err := reconcileRecoveryTrashStages(dir); err != nil {
		errs = append(errs, err)
	}
	pending, err := ListCleanupPending(dir)
	if err != nil {
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	for _, item := range pending {
		handled, err := reconcileRecoveryTrashPending(item)
		if !handled {
			if cleanup == nil {
				continue
			}
			err = cleanup(item)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", item.SessionPath, err))
		}
	}
	return errors.Join(errs...)
}

// ReconcileSessionSidecars renames transcripts whose filenames outgrew their
// sidecars and removes stale lock and lease files left beside sessions by
// older runtimes. It never removes .jsonl transcripts; recovered conversations
// may contain useful user history even when their names are ugly.
func ReconcileSessionSidecars(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	var errs []error
	if err := reconcileOverlongSessionFilenames(dir); err != nil {
		errs = append(errs, err)
	}
	// Re-list after the rename pass: it retires old names and their sidecars.
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.Join(errs...)
		}
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		sidecarPath := filepath.Join(dir, name)
		// The lease/lock family appends to the full transcript path
		// (<id>.jsonl.lease.lock), so the reverse parse must strip only the
		// bare sidecar tail and keep .jsonl. The store *Suffix constants are
		// the full on-disk spellings; using them here would strip .jsonl too
		// and send stale-sidecar cleanup at a phantom path (task 680).
		switch {
		case strings.HasSuffix(name, store.SessionLeaseInfoSuffix):
			base := filepath.Join(dir, strings.TrimSuffix(name, ".lease.json"))
			if err := removeStaleSessionLeaseInfoSidecar(base, sidecarPath); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sidecarPath, err))
			}
		case strings.HasSuffix(name, store.SessionLeaseLockSuffix):
			base := filepath.Join(dir, strings.TrimSuffix(name, ".lease.lock"))
			if err := removeStaleSessionLeaseLockSidecar(base, sidecarPath); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sidecarPath, err))
			}
		case strings.HasSuffix(name, store.SessionLockFileSuffix):
			base := filepath.Join(dir, strings.TrimSuffix(name, ".lock"))
			if err := removeStaleSessionLockSidecar(base, sidecarPath); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sidecarPath, err))
			}
		}
	}
	return errors.Join(errs...)
}

func removeStaleSessionLockSidecar(basePath, sidecarPath string) error {
	basePath = canonicalSessionSavePath(basePath)
	if sessionLeaseHeldLocally(basePath) || SessionLeaseHeldByOtherRuntime(basePath) {
		return nil
	}
	lock, err := tryTakeSessionLockFile(sidecarPath)
	if err != nil {
		if errors.Is(err, ErrSessionFileLockHeld) {
			return nil
		}
		return err
	}
	// The removal is atomic with the release (unlink-under-flock on Unix,
	// delete-disposition on the held handle on Windows), so a concurrent
	// saver can never acquire a lock file that is being deleted under it.
	return lock.RemoveAndUnlock()
}

// removeStaleSessionLeaseLockSidecar retires a leftover .lease.lock. The file
// is the lease lock itself, so taking it non-blocking proves no runtime holds
// the lease, and RemoveAndUnlock deletes it atomically with the release.
func removeStaleSessionLeaseLockSidecar(basePath, _ string) error {
	basePath = canonicalSessionSavePath(basePath)
	if sessionLeaseHeldLocally(basePath) {
		return nil
	}
	lock, err := tryTakeSessionLeaseLock(basePath)
	if err != nil {
		if errors.Is(err, ErrSessionLeaseHeld) {
			return nil
		}
		return err
	}
	return lock.RemoveAndUnlock()
}

// removeStaleSessionLeaseInfoSidecar retires a leftover .lease.json while
// holding the lease lock, so no runtime can adopt the info file mid-removal.
// The info file itself is never held open by anyone, so a plain remove under
// the lock is safe on every platform.
func removeStaleSessionLeaseInfoSidecar(basePath, sidecarPath string) error {
	basePath = canonicalSessionSavePath(basePath)
	if sessionLeaseHeldLocally(basePath) {
		return nil
	}
	lockPath := store.SessionLeaseLock(basePath)
	if _, err := os.Stat(lockPath); err == nil {
		unlock, err := tryLockSessionLeaseFile(basePath)
		if err != nil {
			if errors.Is(err, ErrSessionLeaseHeld) {
				return nil
			}
			return err
		}
		removeErr := os.Remove(sidecarPath)
		if unlock != nil {
			unlock()
		}
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	// No lease lock file: holders keep it present (and locked) for their whole
	// lifetime, so the leftover info sidecar has no owner to race with.
	if err := os.Remove(sidecarPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func sessionLeaseHeldLocally(path string) bool {
	_, ok := sessionLeaseOwners.Load(canonicalSessionSavePath(path))
	return ok
}

// sessionLockSidecarFits reports whether basePath's .lock sidecar name stays
// within the filesystem's per-component limit; past it, no process can hold
// (or ever have held) the file lock, because the lock file cannot be created.
func sessionLockSidecarFits(basePath string) bool {
	return len(filepath.Base(store.SessionLockFile(basePath))) <= nameMaxBytes
}

// sessionLeaseSidecarFits is the lease-file analogue of sessionLockSidecarFits.
func sessionLeaseSidecarFits(basePath string) bool {
	return len(filepath.Base(store.SessionLeaseLock(basePath))) <= nameMaxBytes
}

// reconcileOverlongSessionFilenames renames transcripts whose basenames grew
// past maxSessionBasenameBytes — the leftover shape of the pre-bounded
// recovery cascade (#5923), where lock and lease sidecars could no longer be
// created and the session became unsaveable. The conversation bytes are kept
// verbatim under a bounded name derived the same way new recovery branches
// are named; branch meta moves along with its ID rewritten, and sessions
// pointing at the old ID are re-parented so lineage survives the rename.
func reconcileOverlongSessionFilenames(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var errs []error
	renamed := map[string]string{} // old branch ID -> new branch ID
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !store.IsSessionTranscriptName(name) {
			continue
		}
		if len(name) <= maxSessionBasenameBytes {
			continue
		}
		oldPath := filepath.Join(dir, name)
		if IsCleanupPending(oldPath) {
			// Being deleted; renaming would orphan the cleanup marker.
			continue
		}
		newID, err := renameOverlongSession(oldPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", oldPath, err))
		}
		// A non-empty newID means the transcript rename landed even if some
		// sidecar migration failed; record it so children still re-parent —
		// this run is the only one that knows the old-to-new mapping.
		if newID != "" {
			renamed[BranchID(oldPath)] = newID
		}
	}
	if len(renamed) > 0 {
		if err := reparentSessionBranches(dir, renamed); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// renameOverlongSession moves one overlong transcript to its bounded name and
// migrates the sidecars that carry user state. It returns the new branch ID,
// or "" when the session was skipped because a runtime may still own it.
func renameOverlongSession(oldPath string) (string, error) {
	oldID := BranchID(oldPath)
	newID := recoveryParentStem(oldID)
	if newID == oldID {
		return "", nil
	}
	newPath := filepath.Join(filepath.Dir(oldPath), newID+".jsonl")
	if _, err := os.Stat(newPath); err == nil {
		return "", fmt.Errorf("rename target %s already exists", filepath.Base(newPath))
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// 946: bounded in-process wait. A budget-out here means another goroutine
	// in this process holds the session — the same skip semantics as a live
	// lease or a held .lock file: leave it for a later sweep (the warn line
	// already named the wedged path).
	unlockOld, err := lockSessionSavePath(oldPath)
	if err != nil {
		return "", nil
	}
	defer unlockOld()
	unlockNew, err := lockSessionSavePath(newPath)
	if err != nil {
		return "", nil
	}
	defer unlockNew()
	if sessionLeaseHeldLocally(oldPath) {
		return "", nil
	}
	// Names past the sidecar limit cannot have lease or lock holders in any
	// process — the holder files themselves are uncreatable — so probing them
	// would only manufacture ENAMETOOLONG errors and wrongly skip the exact
	// sessions this pass exists to repair.
	if sessionLeaseSidecarFits(oldPath) && SessionLeaseHeldByOtherRuntime(oldPath) {
		return "", nil
	}
	var lockFile *sessionLockFile
	if sessionLockSidecarFits(oldPath) {
		lock, err := tryTakeSessionLockFile(oldPath + ".lock")
		if err != nil {
			if errors.Is(err, ErrSessionFileLockHeld) {
				return "", nil
			}
			return "", err
		}
		lockFile = lock
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		// Nothing moved: the old transcript is intact and the next
		// reconciliation can retry, so its lock file stays in place too.
		if lockFile != nil {
			lockFile.Unlock()
		}
		return "", err
	}
	// The transcript is committed under its new name from here on. Sidecar
	// migration and lock cleanup failures are reported, but the new ID is
	// still returned so the caller re-parents children: the old name is gone,
	// and a later run would have no way to reconstruct this mapping.
	var errs []error
	if err := migrateSessionSidecars(oldPath, newPath, newID); err != nil {
		errs = append(errs, err)
	}
	// Retire the old disposable lease sidecars: any holder was ruled out
	// above, and nothing keeps these files open, so a plain remove is safe.
	if sessionLeaseSidecarFits(oldPath) {
		for _, stale := range []string{store.SessionLeaseLock(oldPath), store.SessionLeaseInfo(oldPath)} {
			if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}
	// The old .lock goes atomically with the release of the lock we hold on it.
	if lockFile != nil {
		if err := lockFile.RemoveAndUnlock(); err != nil {
			errs = append(errs, err)
		}
	}
	return newID, errors.Join(errs...)
}

// migrateSessionSidecars moves the user-state sidecars of a renamed session:
// branch meta (with its ID rewritten to match the new filename), goal state,
// and the checkpoint/job directories. Lock and lease files are disposable and
// are removed by the caller instead.
func migrateSessionSidecars(oldPath, newPath, newID string) error {
	errs := []error{migratePinnedContextSidecar(oldPath, newPath, newID)}
	if len(filepath.Base(oldPath))+len(".meta") <= nameMaxBytes {
		if meta, ok, err := LoadBranchMeta(oldPath); err != nil {
			errs = append(errs, err)
		} else if ok {
			meta.ID = newID
			if err := SaveBranchMetaPreserveUpdated(newPath, meta); err != nil {
				errs = append(errs, err)
			} else if err := os.Remove(BranchMetaPath(oldPath)); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}
	for _, pair := range [][2]string{
		{store.SessionGoalState(oldPath), store.SessionGoalState(newPath)},
		{store.SessionEventLog(oldPath), store.SessionEventLog(newPath)},
		// tl 1172 L2: 归档态日志随会话迁移，否则 fork/改名后历史留在旧名字下。
		{store.SessionEventLogZst(oldPath), store.SessionEventLogZst(newPath)},
		{store.SessionEventLogDamaged(oldPath), store.SessionEventLogDamaged(newPath)},
		{store.SessionTurnEventLog(oldPath), store.SessionTurnEventLog(newPath)},
		{store.SessionTurnEventLogDamaged(oldPath), store.SessionTurnEventLogDamaged(newPath)},
		{store.SessionEventIndex(oldPath), store.SessionEventIndex(newPath)},
		{store.SessionConflictLog(oldPath), store.SessionConflictLog(newPath)},
		{store.SessionRecoveryState(oldPath), store.SessionRecoveryState(newPath)},
		{store.SessionCheckpointDir(oldPath), store.SessionCheckpointDir(newPath)},
		{store.SessionJobsDir(oldPath), store.SessionJobsDir(newPath)},
		{store.SessionInboxDir(oldPath), store.SessionInboxDir(newPath)},
	} {
		// A source name past the filesystem limit cannot exist; renaming it
		// would just manufacture ENAMETOOLONG instead of a clean not-exist.
		if len(filepath.Base(pair[0])) > nameMaxBytes {
			continue
		}
		if err := os.Rename(pair[0], pair[1]); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reparentSessionBranches rewrites ParentID references from renamed branch IDs
// to their bounded replacements so the branch tree stays connected.
func reparentSessionBranches(dir string, renamed map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !store.IsSessionTranscriptName(name) {
			continue
		}
		if len(name)+len(".meta") > nameMaxBytes {
			continue
		}
		path := filepath.Join(dir, name)
		// 946: skip a wedged path instead of failing the sweep — the budget
		// warn already named it, and the next reconcile retries.
		unlock, lockErr := lockSessionSavePath(path)
		if lockErr != nil {
			continue
		}
		meta, ok, err := LoadBranchMeta(path)
		if err == nil && ok {
			if newParent, hit := renamed[meta.ParentID]; hit && newParent != meta.ParentID {
				meta.ParentID = newParent
				err = SaveBranchMetaPreserveUpdated(path, meta)
			}
		}
		unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}
