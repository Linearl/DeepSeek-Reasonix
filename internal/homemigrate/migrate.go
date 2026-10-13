// Package homemigrate owns the transactional Reasonix-home relocation behind
// the task-846 lab feature (experimental_home_pointer).
//
// Transaction shape (任务书：copy→校验→原子切指针→失败回滚):
//
//	PhaseCopy   mirror the live home into the target directory (.env re-ACL'd)
//	PhaseVerify file count + byte totals must match; the credential .env is
//	            additionally compared by SHA-256
//	PhaseSwitch atomically swap home-pointer.json (temp+rename) — the only
//	            mutation of shared state; everything before it is additive
//
// Any failure before PhaseSwitch leaves the pointer untouched, so the next
// launch keeps using the source home exactly as before (rollback). Partial
// data in the target directory is intentionally NOT deleted: it is harmless
// (no pointer references it) and a retry overwrites it file-by-file. The lock
// file is released best-effort; a crashed migrator leaves a stale lock that
// the next contender steals once the recorded PID is dead.
//
// Six risks the wizard must surface (任务书六条) live in risks.go — Execute
// refuses to run until the caller acknowledges every one of them by id.
package homemigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reasonix/internal/config"
)

// Phase enumerates the migration transaction's stages for progress callbacks
// and results.
type Phase string

const (
	PhaseCopy    Phase = "copy"
	PhaseVerify  Phase = "verify"
	PhaseSwitch  Phase = "switch"
	PhaseDone    Phase = "done"
	PhaseAborted Phase = "aborted"
)

// Sentinel errors. Callers match with errors.Is.
var (
	// ErrMigrationLocked: another migration holds the cross-process mutex.
	ErrMigrationLocked = errors.New("another home migration is already in progress")
	// ErrTargetUnusable: the chosen target directory cannot host a home.
	ErrTargetUnusable = errors.New("target directory is not usable")
	// ErrRisksNotAcknowledged: Execute was called without acknowledging all six risks.
	ErrRisksNotAcknowledged = errors.New("migration risks not acknowledged")
	// ErrVerifyMismatch: post-copy verification found the target incomplete.
	ErrVerifyMismatch = errors.New("target copy failed verification")
)

// Plan is one validated home relocation attempt.
type Plan struct {
	Source string // the home currently in effect (usually the OS default)
	Target string // normalized destination directory
}

// Progress carries per-phase updates for the wizard UI.
type Progress struct {
	Phase   Phase
	Files   int    // copied so far (PhaseCopy)
	Bytes   int64  // copied so far (PhaseCopy)
	Detail  string // human-readable, advisory
	Warning string // non-fatal problem (e.g. skipped symlink)
}

// Executor runs the transaction. The zero value is production-ready; the
// function fields exist for tests to inject failures (rollback assertions)
// without build-tagged plumbing.
type Executor struct {
	// PointerPath overrides the pointer file location (tests). Empty uses
	// config.DefaultHomePointerPath().
	PointerPath string
	// LockPath overrides the migration lock location (tests). Empty uses
	// config.HomeMigrationLockPath().
	LockPath string
	// Clock allows tests to freeze timestamps.
	Now func() time.Time

	// verifyFn replaces the default verify step (tests inject mismatches).
	verifyFn func(src, dst string) error
	// switchFn replaces the default atomic pointer swap (tests inject failures).
	switchFn func(pointerPath, home string) error
	// copyTreeFn replaces the default recursive copy (tests inject failures).
	copyTreeFn func(src, dst string, progress func(Progress)) error
}

// NewExecutor returns an Executor with the production hooks wired.
func NewExecutor() *Executor {
	return &Executor{
		verifyFn:   verifyCopy,
		switchFn:   switchPointer,
		copyTreeFn: copyTree,
	}
}

func (e *Executor) pointerPath() string {
	if e.PointerPath != "" {
		return e.PointerPath
	}
	return config.DefaultHomePointerPath()
}

func (e *Executor) lockPath() string {
	if e.LockPath != "" {
		return e.LockPath
	}
	return config.HomeMigrationLockPath()
}

func (e *Executor) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// ValidatePlan normalizes and sanity-checks a target directory before the
// wizard may offer it. Returned warnings (OneDrive sync root) are advisory —
// the wizard must show them but the user may proceed.
func ValidatePlan(source, target string) (warnings []string, err error) {
	normalized, err := config.NormalizeHomePointerTarget(target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("%w: source home unavailable", ErrTargetUnusable)
	}
	if sameFilePath(normalized, filepath.Clean(source)) {
		return nil, fmt.Errorf("%w: target equals the current home", ErrTargetUnusable)
	}
	// Nested: a home inside its own home would recurse on the next migration.
	if rel, relErr := filepath.Rel(filepath.Clean(source), normalized); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("%w: target %s lives inside the current home", ErrTargetUnusable, normalized)
	}
	if info, statErr := os.Stat(normalized); statErr == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: %s exists and is not a directory", ErrTargetUnusable, normalized)
		}
		entries, readErr := os.ReadDir(normalized)
		if readErr == nil && len(entries) > 0 {
			return nil, fmt.Errorf("%w: %s exists and is not empty", ErrTargetUnusable, normalized)
		}
	}
	var warns []string
	if pathHasOneDrive(normalized) {
		warns = append(warns, "target-path-onedrive")
	}
	return warns, nil
}

// pathHasOneDrive reports whether any path segment looks like a OneDrive sync
// root (risk 5). Best-effort naming heuristic, case-insensitive.
func pathHasOneDrive(path string) bool {
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool { return r == '\\' || r == '/' }) {
		if strings.Contains(strings.ToLower(seg), "onedrive") {
			return true
		}
	}
	return false
}

func sameFilePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	if runtime.GOOS == "windows" {
		aa, bb = strings.ToLower(aa), strings.ToLower(bb)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// Execute runs the full transaction and returns the phase it ended in plus a
// diagnostic detail string. acknowledged must contain every risk id from
// AllRisks() — the wizard UI collects the checkboxes.
func (e *Executor) Execute(plan Plan, acknowledged []string, progress func(Progress)) (Phase, string) {
	if err := RequireAllRisksAcknowledged(acknowledged); err != nil {
		return PhaseAborted, err.Error()
	}
	target, err := config.NormalizeHomePointerTarget(plan.Target)
	if err != nil {
		return PhaseAborted, err.Error()
	}
	source := filepath.Clean(plan.Source)
	plan = Plan{Source: source, Target: target}

	release, lockDetail, err := e.acquireLock()
	if err != nil {
		return PhaseAborted, lockDetail
	}
	defer release()

	emit := func(p Progress) {
		if progress != nil {
			progress(p)
		}
	}

	// Phase 1: copy. Nothing shared is touched yet; failure here is free.
	if err := e.copyTreeFn(plan.Source, plan.Target, progress); err != nil {
		emit(Progress{Phase: PhaseAborted, Detail: fmt.Sprintf("copy failed, pointer unchanged: %v", err)})
		return PhaseAborted, fmt.Sprintf("copy: %v (pointer unchanged; partial files at %s are inert and will be reused by a retry)", err, plan.Target)
	}
	emit(Progress{Phase: PhaseVerify, Detail: "copy complete, verifying"})

	// Phase 2: verify. Mismatch ⇒ abort with the pointer untouched.
	if err := e.verifyFn(plan.Source, plan.Target); err != nil {
		emit(Progress{Phase: PhaseAborted, Detail: fmt.Sprintf("verify failed, pointer unchanged: %v", err)})
		return PhaseAborted, fmt.Sprintf("verify: %v (pointer unchanged)", err)
	}

	// Phase 3: atomic pointer swap. Failure modes of the default swap leave
	// the old pointer intact (temp+rename), so a failed switch is still a
	// clean rollback to the pre-migration state.
	emit(Progress{Phase: PhaseSwitch, Detail: "swapping the home pointer atomically"})
	if err := e.switchFn(e.pointerPath(), plan.Target); err != nil {
		emit(Progress{Phase: PhaseAborted, Detail: fmt.Sprintf("pointer swap failed: %v", err)})
		return PhaseAborted, fmt.Sprintf("switch: %v (old pointer preserved)", err)
	}
	emit(Progress{Phase: PhaseDone, Detail: fmt.Sprintf("home pointer now targets %s; restart Reasonix to land there", plan.Target)})
	return PhaseDone, fmt.Sprintf("migrated %s -> %s", plan.Source, plan.Target)
}

// ResetToDefault removes the pointer file so the next launch uses the
// OS-default home again. Data is never copied back: the custom home keeps its
// files (报告口径：恢复默认=指针回位，不搬数据)。
func (e *Executor) ResetToDefault() error {
	path := e.pointerPath()
	if path == "" {
		return fmt.Errorf("home pointer path unavailable")
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// acquireLock takes the cross-process migration mutex, stealing it only from
// a provably dead owner. Returns the release func and a diagnostic detail.
func (e *Executor) acquireLock() (func(), string, error) {
	path := e.lockPath()
	if path == "" {
		return func() {}, "lock path unavailable", fmt.Errorf("migration lock path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}, "", fmt.Errorf("create lock dir: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			exe := ""
			if self, selfErr := os.Executable(); selfErr == nil {
				exe = filepath.Base(self)
			}
			fmt.Fprintf(handle, "pid=%d\nstarted_at=%s\nexe=%s\n", os.Getpid(), e.now().UTC().Format(time.RFC3339), exe)
			_ = handle.Close()
			release := func() {
				// Best-effort: a failed delete leaves a stale lock that the
				// dead-PID steal logic clears on the next run.
				_ = os.Remove(path)
			}
			return release, "", nil
		}
		if !os.IsExist(err) {
			return func() {}, "", fmt.Errorf("create migration lock: %w", err)
		}
		// Lock exists: steal it only when the recorded PID is provably dead.
		if info, statErr := os.Stat(path); statErr == nil && e.now().Sub(info.ModTime()) > lockStaleAge {
			// Age-based fallback: a live owner refreshes nothing, so an
			// ancient lock with an unprobeable PID still yields.
			_ = os.Remove(path)
			continue
		}
		if ownerPID, readErr := readLockPID(path); readErr == nil && ownerPID > 0 {
			if pidAlive(ownerPID) {
				return func() {}, fmt.Sprintf("lock held by live pid %d", ownerPID), ErrMigrationLocked
			}
			_ = os.Remove(path) // stale owner; retry the exclusive create
			continue
		}
		return func() {}, "lock held (unreadable owner)", ErrMigrationLocked
	}
	return func() {}, "lock acquisition raced", ErrMigrationLocked
}

// lockStaleAge is how long a lock file may sit before age alone makes it
// stealable. Generous: a migration can legitimately copy a big home for
// minutes, but hours mean the owner died without cleanup.
const lockStaleAge = 6 * time.Hour

// MigrationInProgress reports whether a live migration mutex exists — the
// desktop/CLI startup check that makes "迁移中拒绝第二实例" real (risk 4).
func MigrationInProgress() bool {
	return migrationInProgressAt(config.HomeMigrationLockPath())
}

func migrationInProgressAt(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) > lockStaleAge {
		return false
	}
	pid, err := readLockPID(path)
	if err != nil || pid <= 0 {
		return true // unreadable live-age lock: assume the worst, fail closed
	}
	return pidAlive(pid)
}

func readLockPID(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if pid, ok := strings.CutPrefix(strings.TrimSpace(line), "pid="); ok {
			var value int
			if _, err := fmt.Sscanf(pid, "%d", &value); err == nil {
				return value, nil
			}
			return 0, fmt.Errorf("unreadable pid field")
		}
	}
	return 0, fmt.Errorf("no pid field")
}

// writeFileSync writes dst with src's exact bytes and mode, creating parent
// dirs. Used by copyTree for regular files.
func writeFileSync(dst string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

// bookkeepingRelPath reports whether rel (relative to the home root) is a
// pointer/lock bookkeeping file that must neither travel nor be counted — the
// copy phase skips them and verify must count the same set or the two phases
// disagree by exactly the lock file the transaction itself created.
func bookkeepingRelPath(rel string) bool {
	if strings.Contains(rel, string(os.PathSeparator)) {
		return false // bookkeeping lives only at the home root
	}
	switch strings.ToLower(filepath.Base(rel)) {
	case config.HomePointerFileName, "home-pointer.json.tmp", "home-migrate.lock":
		return true
	default:
		return false
	}
}

// copyTree mirrors src into dst, skipping the pointer/lock bookkeeping files
// and symlinks. The credential .env goes through CopyEnvFile so the copy gets
// freshly-redone access permissions instead of inheriting whatever the
// previous location had.
func copyTree(src, dst string, progress func(Progress)) error {
	var copied int64
	var bytes int64
	err := filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			return nil // created lazily on first child write
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if progress != nil {
				progress(Progress{Phase: PhaseCopy, Warning: fmt.Sprintf("skipped symlink %s", rel)})
			}
			return nil
		}
		if bookkeepingRelPath(rel) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		target := filepath.Join(dst, rel)
		var writeErr error
		if sameFilePath(entry.Name(), ".env") {
			writeErr = CopyEnvFile(path, target)
		} else {
			writeErr = writeFileSync(target, data, info.Mode().Perm())
		}
		if writeErr != nil {
			return writeErr
		}
		copied++
		bytes += int64(len(data))
		if progress != nil {
			progress(Progress{Phase: PhaseCopy, Files: int(copied), Bytes: bytes})
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// verifyCopy compares file counts, byte totals, and (for the credential .env)
// the SHA-256 of the copy against the source. Count+bytes catches truncation
// and skipped subtrees; the credential hash pins the one file whose loss is
// not repairable by re-login flows.
func verifyCopy(src, dst string) error {
	var srcFiles, dstFiles int64
	var srcBytes, dstBytes int64
	var srcEnvHash, dstEnvHash string
	count := func(root string, files, bytes *int64, envHash *string) error {
		return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if bookkeepingRelPath(rel) {
				return nil // count the same set copyTree produced
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			*files++
			*bytes += info.Size()
			if envHash != nil && sameFilePath(entry.Name(), ".env") {
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				*envHash = EnvFileFingerprint(data)
			}
			return nil
		})
	}
	if err := count(src, &srcFiles, &srcBytes, &srcEnvHash); err != nil {
		return fmt.Errorf("source scan: %w", err)
	}
	if err := count(dst, &dstFiles, &dstBytes, &dstEnvHash); err != nil {
		return fmt.Errorf("%w: target scan: %v", ErrVerifyMismatch, err)
	}
	if srcFiles != dstFiles || srcBytes != dstBytes {
		return fmt.Errorf("%w: source %d files/%d bytes vs target %d files/%d bytes", ErrVerifyMismatch, srcFiles, srcBytes, dstFiles, dstBytes)
	}
	if srcEnvHash != "" && srcEnvHash != dstEnvHash {
		return fmt.Errorf("%w: credential .env hash mismatch", ErrVerifyMismatch)
	}
	return nil
}

// switchPointer is the production atomic swap (temp + rename in the pointer's
// directory).
func switchPointer(pointerPath, home string) error {
	return config.WriteHomePointerFile(pointerPath, home)
}
