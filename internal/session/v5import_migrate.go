package session

// Task 773: v5 import execution with the four safety boundaries from the 766
// design (report docs/report/调研报告【非诊断】/766-*.md §7.3):
//
//	1. pre-import full backup   — the whole by-id tree + registry is copied to
//	                               a timestamped backup directory before any
//	                               target write (opt-out: --no-backup);
//	2. dry-run preview          — PlanV5Import (no --apply stops there);
//	3. failure rollback         — everything is built in a staging directory
//	                               outside any session root; any failure
//	                               deletes the staging tree, and a partially
//	                               published run removes exactly the files it
//	                               renamed in (tracked in a journal);
//	4. post-import verification — per-session message-count and JSONL decode
//	                               checks, a source fingerprint before/after
//	                               proving the v5 store untouched, and total
//	                               accounting (import+skip+fail == enumerated).
//
// The source store is opened read-only throughout; imported artifacts are NEW
// files in the legacy store (never replacements), so re-running is idempotent
// and a failed run cannot damage existing sessions.

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

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// V5ImportBackup is the record of the pre-import backup.
type V5ImportBackup struct {
	Dir        string `json:"dir"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skipReason,omitempty"`
}

// V5ImportRunOptions carries the executed-run switches on top of the plan
// options.
type V5ImportRunOptions struct {
	V5ImportOptions
	// NoBackup skips the pre-import full copy (not recommended).
	NoBackup bool
	// BackupRoot overrides where the timestamped backup directory is created.
	// Empty defaults to a sibling of the v5 tree root (never inside by-id).
	BackupRoot string
	// Progress receives one line per completed step when non-nil.
	Progress func(format string, args ...any)
}

// V5ImportReport is the receipt written after every apply run and printed by
// the CLI (--json). It doubles as the migration ledger entry.
type V5ImportReport struct {
	SchemaVersion  int               `json:"schemaVersion"`
	Command        string            `json:"command"`
	StartedAt      time.Time         `json:"startedAt"`
	FinishedAt     time.Time         `json:"finishedAt"`
	SourceRoot     string            `json:"sourceRoot"`
	Backup         V5ImportBackup    `json:"backup"`
	Entries        []V5PlanEntry     `json:"entries"`
	PublishedFiles []string          `json:"publishedFiles"`
	ImportCount    int               `json:"importCount"`
	SkipCount      int               `json:"skipCount"`
	FailCount      int               `json:"failCount"`
	Verified       bool              `json:"verified"`
	SourceBefore   string            `json:"sourceFingerprintBefore"`
	SourceAfter    string            `json:"sourceFingerprintAfter"`
	SourceUnmoved  bool              `json:"sourceUnchanged"`
	Accounting     string            `json:"accounting"`
	Losses         []string          `json:"losses"`
	PlanFailures   []V5ImportFailure `json:"planFailures,omitempty"`
}

// v5ImportLossNotes lists the reverse-degradation losses the fork cannot
// express in the legacy store (766 ⑦ table). They are always attached to the
// report so "有损可控、损失面预先告知" is on the receipt, not in folklore.
var v5ImportLossNotes = []string{
	"registry display state (order/pin/archive visibility) is not carried over",
	"provenance chain and writer generations are not carried over; source recorded in report only",
	"goal/plan/todo/interaction event states are folded into the report counts but not materialized into sidecars yet",
	"turn boundaries are re-derived from the message stream instead of carried over",
}

// RunV5Import executes a planned import end to end.
func RunV5Import(ctx context.Context, opts V5ImportRunOptions) (*V5ImportReport, error) {
	started := time.Now().UTC()
	say := func(format string, args ...any) {
		if opts.Progress != nil {
			opts.Progress(format, args...)
		}
	}
	plan, err := PlanV5Import(ctx, opts.V5ImportOptions)
	if err != nil {
		return nil, err
	}
	report := &V5ImportReport{
		SchemaVersion: 1,
		Command:       "sessions import-v5",
		StartedAt:     started,
		SourceRoot:    plan.SourceRoot,
		Entries:       plan.Entries,
		ImportCount:   plan.ImportCount,
		SkipCount:     plan.SkipCount,
		FailCount:     plan.FailCount,
		PlanFailures:  plan.Failures,
		Losses:        v5ImportLossNotes,
	}
	before, _, _, err := SourceTreeFingerprint(ctx, plan.SourceRoot)
	if err != nil {
		return nil, fmt.Errorf("fingerprint source before import: %w", err)
	}
	report.SourceBefore = before

	// Boundary 1: full backup of the source tree + registry before writing.
	if opts.NoBackup {
		report.Backup = V5ImportBackup{Skipped: true, SkipReason: "--no-backup"}
		say("backup: skipped (--no-backup)")
	} else {
		backupDir, files, bytes, backupErr := backupV5Source(ctx, plan, opts.BackupRoot)
		if backupErr != nil {
			return nil, fmt.Errorf("pre-import backup failed; nothing was written: %w", backupErr)
		}
		report.Backup = V5ImportBackup{Dir: backupDir, Files: files, Bytes: bytes}
		say("backup: %d files, %.1f MiB -> %s", files, float64(bytes)/(1024*1024), backupDir)
	}

	// Boundary 3: build in staging; publish only what verified.
	staging, err := os.MkdirTemp("", "reasonix-v5import-stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	published := []string{}
	publish := func(err error) (*V5ImportReport, error) {
		// Roll back whatever landed; the staging tree dies with the defer.
		for _, file := range published {
			_ = os.Remove(file)
		}
		if err != nil {
			return nil, err
		}
		return report, nil
	}

	built := 0
	for i := range plan.Entries {
		entry := &plan.Entries[i]
		if err := ctx.Err(); err != nil {
			return publish(err)
		}
		if entry.Status != "import" {
			say("skip %s: %s", entry.SessionID, entry.Reason)
			continue
		}
		say("import %s (rev%d, %d messages)...", entry.SessionID, entry.StorageRevision, entry.MessageCount)
		stagedDir := filepath.Join(staging, entry.SessionID)
		if err := buildV5ImportStaged(ctx, plan, *entry, stagedDir); err != nil {
			entry.Status = "fail"
			entry.Reason = "build staged artifacts: " + err.Error()
			report.FailCount++
			report.ImportCount--
			return publish(err)
		}
		moved, err := publishV5ImportStaged(*entry, stagedDir)
		if err != nil {
			return publish(err)
		}
		published = append(published, moved...)
		built++
		say("import %s: published %d files", entry.SessionID, len(moved))
	}
	report.PublishedFiles = published

	// Boundary 4: verification. Source fingerprint must be unchanged, every
	// published transcript must decode to the planned message count, and the
	// accounting must close.
	after, _, _, err := SourceTreeFingerprint(ctx, plan.SourceRoot)
	if err != nil {
		return publish(fmt.Errorf("fingerprint source after import: %w", err))
	}
	report.SourceAfter = after
	report.SourceUnmoved = after == before
	if !report.SourceUnmoved {
		return publish(fmt.Errorf("source store changed during import (%s -> %s); imported files were rolled back", before, after))
	}
	for _, entry := range plan.Entries {
		if entry.Status != "import" {
			continue
		}
		if err := verifyV5ImportLegacyFile(entry); err != nil {
			return publish(err)
		}
	}
	// tl1287 回归修复：守恒只对「会话条目」闭合。plan.FailCount 还含非条目
	// 失败（registry 不可读 / 源文件打不开，它们在 Failures 里、没有 entry），
	// 直接拿它对账会让每一次带这类失败的 run 都在这里误判翻车并整体回滚——
	// 与 plan 侧「报错但继续、让操作者决断」的设计自相矛盾。非条目失败照旧
	// 进 report.FailCount（展示口径）与 PlanFailures（明细）。
	entryFails := 0
	for _, entry := range plan.Entries {
		if entry.Status == "fail" {
			entryFails++
		}
	}
	if built+plan.SkipCount+entryFails != len(plan.Entries) {
		return publish(fmt.Errorf("import accounting does not close: %d+%d+%d vs %d enumerated",
			built, plan.SkipCount, entryFails, len(plan.Entries)))
	}
	report.Verified = true
	report.Accounting = fmt.Sprintf("imported=%d skipped=%d failed=%d enumerated=%d publishedFiles=%d nonEntryFailures=%d",
		built, plan.SkipCount, entryFails, len(plan.Entries), len(published), plan.FailCount-entryFails)
	report.FinishedAt = time.Now().UTC()
	return report, nil
}

// backupV5Source copies the by-id tree and the registry file into a
// timestamped backup directory. Nothing in the source tree is touched.
func backupV5Source(ctx context.Context, plan *V5Plan, backupRoot string) (string, int, int64, error) {
	if strings.TrimSpace(backupRoot) == "" {
		backupRoot = filepath.Dir(filepath.Dir(plan.SourceRoot))
	}
	backupDir := filepath.Join(backupRoot, "import-v5-backup-"+time.Now().UTC().Format("20060102-150405"))
	// Same-second re-runs (or a restored tree) must not collide: probe for a
	// free suffix instead of failing the run before anything was written.
	probe := backupDir
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(probe); os.IsNotExist(err) {
			break
		}
		probe = backupDir + "-" + strconv.Itoa(suffix)
	}
	backupDir = probe
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", 0, 0, err
	}
	byIDDir := filepath.Join(backupDir, "desktop-sessions-v5", "by-id")
	files, bytes, err := copyTree(ctx, plan.SourceRoot, byIDDir)
	if err != nil {
		_ = os.RemoveAll(backupDir)
		return "", 0, 0, err
	}
	if plan.RegistryFound {
		registryTarget := filepath.Join(backupDir, "desktop", "workspace-state-v1.json")
		copied, copiedBytes, err := copyFile(ctx, plan.RegistryFile, registryTarget)
		if err != nil {
			_ = os.RemoveAll(backupDir)
			return "", 0, 0, err
		}
		files += copied
		bytes += copiedBytes
	}
	return backupDir, files, bytes, nil
}

func copyTree(ctx context.Context, source, target string) (int, int64, error) {
	var files int
	var total int64
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		copied, bytes, err := copyFile(ctx, path, filepath.Join(target, rel))
		files += copied
		total += bytes
		return err
	})
	return files, total, err
}

func copyFile(ctx context.Context, source, target string) (int, int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	in, err := os.Open(source)
	if err != nil {
		return 0, 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return 0, 0, err
	}
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
	if closeErr != nil {
		return 0, written, closeErr
	}
	return 1, written, nil
}

// v5ImportMeta is the minimal branch sidecar the legacy store needs: identity,
// display name, timestamps, scope. Everything else (revision, digests, heads)
// is healed by the fork's own load path on first open (BranchMeta{ID: ...} is
// the same minimal seed the fork's own save path uses for fresh sessions).
type v5ImportMeta struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Scope         string    `json:"scope,omitempty"`
	WorkspaceRoot string    `json:"workspace_root,omitempty"`
	Model         string    `json:"model,omitempty"`
	ModelIdentity string    `json:"model_identity,omitempty"`
	SourceNote    string    `json:"source_note,omitempty"`
}

// buildV5ImportStaged writes one session's legacy artifacts into a private
// staging directory: the JSONL transcript (one provider.Message per line —
// the same bytes the v4 event carried, now the compatibility checkpoint the
// fork's readers own), the branch sidecar with title/model/timestamps, and a
// receipt with the folded kind counts.
func buildV5ImportStaged(ctx context.Context, plan *V5Plan, entry V5PlanEntry, stagedDir string) error {
	session, err := OpenV5Session(filepath.Join(plan.SourceRoot, entry.SessionID))
	if err != nil {
		return err
	}
	transcript, err := session.Transcript(ctx)
	if err != nil {
		return err
	}
	if len(transcript.Messages) != entry.MessageCount {
		return fmt.Errorf("transcript changed between plan and run: %d messages now, %d planned", len(transcript.Messages), entry.MessageCount)
	}
	if err := os.MkdirAll(stagedDir, 0o700); err != nil {
		return err
	}
	transcriptPath := filepath.Join(stagedDir, entry.SessionID+".jsonl")
	transcriptFile, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writeErr := func() error {
		for _, message := range transcript.Messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Decode-verify every line: a message the fork's own provider
			// struct cannot read must fail the import here, not at open time.
			var decoded provider.Message
			if err := json.Unmarshal(message.Message, &decoded); err != nil {
				return fmt.Errorf("message %s does not decode as a provider message: %w", message.ID, err)
			}
			if _, err := transcriptFile.Write(message.Message); err != nil {
				return err
			}
			if _, err := transcriptFile.Write([]byte("\n")); err != nil {
				return err
			}
		}
		return transcriptFile.Sync()
	}()
	closeErr := transcriptFile.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	scope := "project"
	workspaceRoot := entry.WorkspaceRoot
	if strings.TrimSpace(workspaceRoot) == "" {
		scope = "global"
	}
	meta := v5ImportMeta{
		ID:            entry.SessionID,
		Name:          firstNonEmpty(transcript.Title, entry.Title),
		CreatedAt:     session.Manifest.CreatedAt,
		UpdatedAt:     session.UpdatedAt(),
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		Model:         transcript.ModelRef,
		ModelIdentity: transcript.ModelIdentity,
		SourceNote:    "imported from desktop-sessions-v5 (upstream v1.38.9+ desktop store) by sessions import-v5",
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stagedDir, entry.SessionID+store.SessionMetaFileSuffix), append(metaBytes, '\n'), 0o600); err != nil {
		return err
	}
	receipt, err := json.MarshalIndent(map[string]any{
		"sessionId":       entry.SessionID,
		"storageRevision": entry.StorageRevision,
		"messageCount":    len(transcript.Messages),
		"kindCounts":      transcript.KindCounts,
		"origin":          session.Header.Origin,
		"cwd":             session.Header.CWD,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stagedDir, "import-receipt.json"), append(receipt, '\n'), 0o600)
}

// publishV5ImportStaged moves the staged artifacts into the legacy target
// directory. Targets are new files (the plan skipped existing ones); a rename
// across devices falls back to a verified copy. The receipt file stays behind
// in staging.
func publishV5ImportStaged(entry V5PlanEntry, stagedDir string) ([]string, error) {
	targetDir := filepath.Dir(entry.TargetPath)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, err
	}
	var published []string
	for _, name := range []string{entry.SessionID + store.SessionTranscriptSuffix, entry.SessionID + store.SessionMetaFileSuffix} {
		source := filepath.Join(stagedDir, name)
		target := filepath.Join(targetDir, name)
		if err := os.Rename(source, target); err != nil {
			// Cross-device fallback: copy then remove, keeping EXCL semantics.
			if _, _, copyErr := copyFile(context.Background(), source, target); copyErr != nil {
				return published, fmt.Errorf("publish %s: %w", name, copyErr)
			}
			_ = os.Remove(source)
		}
		published = append(published, target)
	}
	return published, nil
}

// verifyV5ImportLegacyFile re-reads a published transcript and checks the
// message count against the plan (the fork's own decoder, one provider.Message
// per line, no partial trailing line).
func verifyV5ImportLegacyFile(entry V5PlanEntry) error {
	file, err := os.Open(entry.TargetPath)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	count := 0
	for {
		var message provider.Message
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("verify %s: line %d does not decode: %w", entry.TargetPath, count+1, err)
		}
		count++
	}
	if count != entry.MessageCount {
		return fmt.Errorf("verify %s: %d messages on disk, %d planned", entry.TargetPath, count, entry.MessageCount)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
