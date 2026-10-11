package session

// Task 805: v4→v3 backfill execution. PlanV4Import decides, RunV4Import
// executes. The five safety boundaries follow the 773 importer contract
// (docs/report/zcode交付/zcode交付-773-v5迁移器-20261011.md §3), right-sized
// for a target-side write:
//
//	1. pre-run backup    — every legacy file the run will modify is copied to
//	                       import-v4-backup-<UTC timestamp>/ before the first
//	                       write; a backup failure aborts the run with zero
//	                       writes.
//	2. dry-run default   — PlanV4Import stops before any write; the run
//	                       re-plans and only executes append/rebuild entries.
//	3. failure rollback  — size and existence of every touched legacy file
//	                       (transcript + sidecars) are journaled before the
//	                       write; on failure each file is truncated/deleted
//	                       back to its journaled state.
//	4. post-run verify   — every written transcript is re-read and compared
//	                       row-by-row against the v4 projection, the v4 source
//	                       tree fingerprint must be unchanged, and the
//	                       accounting must close.
//	5. read-only source  — the v4 store is only ever opened ReadOnly; the
//	                       before/after fingerprint proves zero mutation.
//
// Appends go through the fork's own save machinery (agent.LoadSession →
// AddBatch → Save), so the native event log, the checkpoint and the sidecars
// stay consistent — a backfilled session is indistinguishable from one the
// app saved itself.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// V4ImportBackup records the pre-run backup of the legacy files the run touches.
type V4ImportBackup struct {
	Dir     string `json:"dir,omitempty"`
	Files   int    `json:"files,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// V4ImportRunOptions carries the executed-run switches on top of the plan
// options.
type V4ImportRunOptions struct {
	V4ImportOptions
	// NoBackup skips the pre-run copy of the legacy files (not recommended).
	NoBackup bool
	// BackupRoot overrides where the timestamped backup directory is created.
	// Empty defaults to the parent of the first v3 directory (never inside a
	// session store).
	BackupRoot string
	// Progress receives one line per completed step when non-nil.
	Progress func(format string, args ...any)
}

// V4ImportReport is the receipt written after every run. It doubles as the
// backfill ledger entry.
type V4ImportReport struct {
	SchemaVersion int            `json:"schemaVersion"`
	Command       string         `json:"command"`
	Mode          string         `json:"mode"`
	StartedAt     time.Time      `json:"startedAt"`
	FinishedAt    time.Time      `json:"finishedAt"`
	V4Root        string         `json:"v4Root"`
	Backup        V4ImportBackup `json:"backup"`
	Entries       []V4PlanEntry  `json:"entries"`
	TouchedFiles  []string       `json:"touchedFiles,omitempty"`
	AppendCount   int            `json:"appendCount"`
	RebuildCount  int            `json:"rebuildCount"`
	SkipCount     int            `json:"skipCount"`
	ConflictCount int            `json:"conflictCount"`
	FailCount     int            `json:"failCount"`
	Verified      bool           `json:"verified"`
	SourceBefore  string         `json:"sourceFingerprintBefore"`
	SourceAfter   string         `json:"sourceFingerprintAfter"`
	SourceUnmoved bool           `json:"sourceUnchanged"`
	Accounting    string         `json:"accounting"`
	Losses        []string       `json:"losses"`
}

// v4ImportLossNotes states honestly what a backfill does and does not carry.
var v4ImportLossNotes = []string{
	"content the v4 mirror never received (a failed bridge sync only logs) is not invented back: the run writes exactly the v4 projection",
	"diverged transcripts are reported as conflicts and left untouched; reconciling them means deleting v3 rows, which is a user decision",
	"goal/plan/todo states are not materialized into sidecars (the fork re-derives them on continuation, as with fresh sessions)",
}

// RunV4Import executes a planned backfill end to end.
func RunV4Import(ctx context.Context, opts V4ImportRunOptions) (*V4ImportReport, error) {
	started := time.Now().UTC()
	say := func(format string, args ...any) {
		if opts.Progress != nil {
			opts.Progress(format, args...)
		}
	}
	plan, err := PlanV4Import(ctx, opts.V4ImportOptions)
	if err != nil {
		return nil, err
	}
	report := &V4ImportReport{
		SchemaVersion: 1,
		Command:       "storage-panel backfill v4-to-v3",
		Mode:          plan.Mode,
		StartedAt:     started,
		V4Root:        plan.V4Root,
		Entries:       plan.Entries,
		AppendCount:   plan.AppendCount,
		RebuildCount:  plan.RebuildCount,
		SkipCount:     plan.SkipCount,
		ConflictCount: plan.ConflictCount,
		FailCount:     plan.FailCount,
		Losses:        v4ImportLossNotes,
	}

	before, _, _, err := SourceTreeFingerprint(ctx, plan.V4Root)
	if err != nil {
		return nil, fmt.Errorf("fingerprint v4 source before run: %w", err)
	}
	report.SourceBefore = before

	// Boundary 1: back up every legacy file this run will touch.
	backup, err := backupV4ImportTargets(ctx, plan, opts)
	if err != nil {
		return nil, fmt.Errorf("pre-run backup failed; nothing was written: %w", err)
	}
	report.Backup = backup
	if backup.Dir != "" {
		say("backup: %d files -> %s", backup.Files, backup.Dir)
	}

	// Boundary 3: journal the touched legacy files so a failure restores them.
	journal := newV4ImportJournal()
	touched := []string{}
	rollback := func(runErr error) (*V4ImportReport, error) {
		report.TouchedFiles = touched
		restoreErr := journal.restore()
		if runErr == nil {
			return report, restoreErr
		}
		if restoreErr != nil {
			return nil, errors.Join(runErr, fmt.Errorf("rollback also failed (the backup copy at %s is authoritative): %w", backup.Dir, restoreErr))
		}
		return nil, fmt.Errorf("%w (rolled back; backup at %s)", runErr, backup.Dir)
	}

	performed := 0
	for i := range plan.Entries {
		entry := &plan.Entries[i]
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		switch entry.Status {
		case V4ImportAppend:
			say("append %s: +%d message(s) -> %s", entry.SessionID, entry.TailCount, filepath.Base(entry.TargetPath))
			if err := runV4ImportAppend(ctx, plan.V4Root, *entry, journal); err != nil {
				entry.Status = V4ImportFail
				entry.Reason = "append: " + err.Error()
				report.FailCount++
				report.AppendCount--
				return rollback(err)
			}
			touched = append(touched, entry.TargetPath)
			performed++
		case V4ImportRebuild:
			say("rebuild %s: %d message(s) -> %s", entry.SessionID, entry.V4Count, filepath.Base(entry.TargetPath))
			if err := runV4ImportRebuild(ctx, plan.V4Root, *entry, journal); err != nil {
				entry.Status = V4ImportFail
				entry.Reason = "rebuild: " + err.Error()
				report.FailCount++
				report.RebuildCount--
				return rollback(err)
			}
			touched = append(touched, entry.TargetPath)
			performed++
		default:
			say("skip %s: %s", entry.SessionID, entry.Reason)
		}
	}

	// Boundary 4: verify. The v4 source must be byte-identical, every written
	// transcript must decode to exactly the v4 projection, and the accounting
	// must close.
	after, _, _, err := SourceTreeFingerprint(ctx, plan.V4Root)
	if err != nil {
		return rollback(fmt.Errorf("fingerprint v4 source after run: %w", err))
	}
	report.SourceAfter = after
	report.SourceUnmoved = after == before
	if !report.SourceUnmoved {
		return rollback(fmt.Errorf("v4 store changed during the run (%s -> %s)", before, after))
	}
	verifyErrs := []error{}
	for _, entry := range plan.Entries {
		if entry.Status != V4ImportAppend && entry.Status != V4ImportRebuild {
			continue
		}
		if err := verifyV4ImportTarget(ctx, plan.V4Root, entry); err != nil {
			verifyErrs = append(verifyErrs, err)
		}
	}
	if len(verifyErrs) > 0 {
		return rollback(errors.Join(verifyErrs...))
	}
	if performed+plan.SkipCount+plan.ConflictCount+plan.FailCount != len(plan.Entries) {
		return rollback(fmt.Errorf("backfill accounting does not close: %d+%d+%d+%d vs %d enumerated",
			performed, plan.SkipCount, plan.ConflictCount, plan.FailCount, len(plan.Entries)))
	}
	report.TouchedFiles = touched
	report.Verified = true
	report.Accounting = fmt.Sprintf("append=%d rebuild=%d skipped=%d conflict=%d failed=%d enumerated=%d touchedFiles=%d",
		report.AppendCount, report.RebuildCount, plan.SkipCount, plan.ConflictCount, plan.FailCount, len(plan.Entries), len(touched))
	report.FinishedAt = time.Now().UTC()
	return report, nil
}

// runV4ImportAppend extends an existing legacy transcript with the v4 tail.
func runV4ImportAppend(ctx context.Context, v4Root string, entry V4PlanEntry, journal *v4ImportJournal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	messages, err := readV4Projection(ctx, NewFilesystemPersistence(v4Root), entry.SessionID)
	if err != nil {
		return err
	}
	existing, err := loadLegacyMessages(entry.TargetPath)
	if err != nil {
		return err
	}
	tail, verdict := diffLegacyAgainstV4(existing, messages)
	if verdict != diffLegacyPrefix || len(tail) == 0 {
		return fmt.Errorf("v3 changed under the plan: diff verdict %d, tail %d", verdict, len(tail))
	}
	for _, path := range legacySaveArtifacts(entry.TargetPath) {
		if err := journal.recordBefore(path); err != nil {
			return err
		}
	}
	loaded, err := agent.LoadSession(entry.TargetPath)
	if err != nil {
		return err
	}
	loaded.AddBatch(tail...)
	return loaded.Save(entry.TargetPath)
}

// runV4ImportRebuild writes the whole v4 projection back as a legacy session.
// A transcript that still exists is removed first (sidecars included) — every
// removal is journaled and the backup copy is the recovery path.
func runV4ImportRebuild(ctx context.Context, v4Root string, entry V4PlanEntry, journal *v4ImportJournal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	messages, err := readV4Projection(ctx, NewFilesystemPersistence(v4Root), entry.SessionID)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return fmt.Errorf("v4 projection became empty between plan and run")
	}
	for _, path := range legacySaveArtifacts(entry.TargetPath) {
		if _, err := os.Stat(path); err == nil {
			if err := journal.recordBefore(path); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(entry.TargetPath), 0o700); err != nil {
		return err
	}
	sess := agent.NewSession("")
	sess.AddBatch(messages...)
	if err := sess.Save(entry.TargetPath); err != nil {
		return err
	}
	return writeBackfillMetaSeed(entry)
}

// legacySaveArtifacts lists the transcript plus every sidecar the fork's own
// save machinery may create for it (the journal and backup walk this list).
func legacySaveArtifacts(targetPath string) []string {
	return append([]string{targetPath}, store.SessionSidecarFiles(targetPath)...)
}

// v4ImportMeta is the minimal branch sidecar for a rebuilt session (same seed
// shape the 773 importer uses; the fork's own load path heals the rest).
type v4ImportMeta struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Model         string    `json:"model,omitempty"`
	ModelIdentity string    `json:"model_identity,omitempty"`
	SourceNote    string    `json:"source_note,omitempty"`
}

// writeBackfillMetaSeed writes the minimal branch sidecar when the save did
// not create one (the same seed shape the 773 importer uses; the fork's own
// load path heals the rest on first open).
func writeBackfillMetaSeed(entry V4PlanEntry) error {
	metaPath := store.SessionMeta(entry.TargetPath)
	if _, err := os.Stat(metaPath); err == nil {
		return nil
	}
	seed := v4ImportMeta{
		ID:         strings.TrimSuffix(filepath.Base(entry.TargetPath), ".jsonl"),
		Name:       entry.Title,
		CreatedAt:  entry.CreatedAt,
		UpdatedAt:  entry.UpdatedAt,
		Model:      entry.ModelRef,
		SourceNote: "backfilled from sessions-v4 by the storage migration (task 805)",
	}
	if seed.UpdatedAt.IsZero() {
		seed.UpdatedAt = time.Now().UTC()
	}
	payload, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, append(payload, '\n'), 0o600)
}

// verifyV4ImportTarget re-reads a written transcript and compares it with the
// v4 projection row by row (boundary 4: post-run verification).
func verifyV4ImportTarget(ctx context.Context, v4Root string, entry V4PlanEntry) error {
	messages, err := readV4Projection(ctx, NewFilesystemPersistence(v4Root), entry.SessionID)
	if err != nil {
		return fmt.Errorf("verify %s: %w", entry.SessionID, err)
	}
	written, err := loadLegacyMessages(entry.TargetPath)
	if err != nil {
		return fmt.Errorf("verify %s: %w", entry.SessionID, err)
	}
	if len(written) != len(messages) {
		return fmt.Errorf("verify %s: %d messages on disk, %d in the v4 projection", entry.TargetPath, len(written), len(messages))
	}
	for i := range messages {
		if messageDigest(written[i]) != messageDigest(messages[i]) {
			return fmt.Errorf("verify %s: row %d differs from the v4 projection", entry.TargetPath, i+1)
		}
	}
	return nil
}

// backupV4ImportTargets copies every legacy file the plan will modify into a
// timestamped backup directory. Nothing in the v3 store is touched.
func backupV4ImportTargets(ctx context.Context, plan *V4ImportPlan, opts V4ImportRunOptions) (V4ImportBackup, error) {
	targets := []string{}
	for _, entry := range plan.Entries {
		if entry.Status == V4ImportAppend || entry.Status == V4ImportRebuild {
			targets = append(targets, entry.TargetPath)
		}
	}
	if len(targets) == 0 {
		return V4ImportBackup{Skipped: true, Reason: "nothing to modify"}, nil
	}
	if opts.NoBackup {
		return V4ImportBackup{Skipped: true, Reason: "--no-backup"}, nil
	}
	backupRoot := strings.TrimSpace(opts.BackupRoot)
	if backupRoot == "" {
		backupRoot = filepath.Dir(firstNonEmpty(opts.V3Dirs...))
		if backupRoot == "" || backupRoot == "." {
			backupRoot = filepath.Dir(opts.V4Root)
		}
	}
	backupDir := filepath.Join(backupRoot, "import-v4-backup-"+time.Now().UTC().Format("20060102-150405"))
	// Same-second re-runs must not collide: probe for a free suffix.
	probe := backupDir
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(probe); os.IsNotExist(err) {
			break
		}
		probe = backupDir + "-" + strconv.Itoa(suffix)
	}
	backupDir = probe
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return V4ImportBackup{}, err
	}
	seen := map[string]bool{}
	files, bytes := 0, int64(0)
	for _, target := range targets {
		for _, path := range legacySaveArtifacts(target) {
			if seen[path] {
				continue
			}
			seen[path] = true
			info, err := os.Stat(path)
			if err != nil {
				continue // nothing to protect yet
			}
			if !info.Mode().IsRegular() {
				continue
			}
			copied, copiedBytes, err := copyFileForBackup(ctx, path, filepath.Join(backupDir, filepath.Base(path)))
			if err != nil {
				return V4ImportBackup{}, err
			}
			files += copied
			bytes += copiedBytes
		}
	}
	return V4ImportBackup{Dir: backupDir, Files: files, Bytes: bytes}, nil
}

func copyFileForBackup(ctx context.Context, source, target string) (int, int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	in, err := os.Open(source)
	if err != nil {
		return 0, 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, 0, err
	}
	written, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return 0, written, copyErr
	}
	if syncErr != nil {
		return 0, written, syncErr
	}
	return 1, written, closeErr
}

// v4ImportJournal remembers the pre-run state of every legacy file the run
// touches, so a failure can restore exactly that state (boundary 3).
type v4ImportJournal struct {
	entries []v4ImportJournalEntry
}

type v4ImportJournalEntry struct {
	path    string
	existed bool
	size    int64
}

func newV4ImportJournal() *v4ImportJournal { return &v4ImportJournal{} }

// recordBefore journals a file's current state. Recording twice keeps the
// FIRST (pre-run) state.
func (j *v4ImportJournal) recordBefore(path string) error {
	for _, entry := range j.entries {
		if entry.path == path {
			return nil
		}
	}
	info, err := os.Stat(path)
	state := v4ImportJournalEntry{path: path}
	if err == nil {
		state.existed, state.size = true, info.Size()
	} else if !os.IsNotExist(err) {
		return err
	}
	j.entries = append(j.entries, state)
	return nil
}

// restore rolls every journaled file back to its pre-run state.
func (j *v4ImportJournal) restore() error {
	errs := []error{}
	for i := len(j.entries) - 1; i >= 0; i-- {
		entry := j.entries[i]
		info, err := os.Stat(entry.path)
		switch {
		case os.IsNotExist(err):
			if entry.existed {
				errs = append(errs, fmt.Errorf("restore %s: file is missing", entry.path))
			}
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("restore %s: %w", entry.path, err))
			continue
		}
		if !entry.existed {
			if err := os.Remove(entry.path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("restore %s: %w", entry.path, err))
			}
			continue
		}
		if info.Size() > entry.size {
			if err := os.Truncate(entry.path, entry.size); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", entry.path, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
