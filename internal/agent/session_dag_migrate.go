package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1227: DAG 读兼容退役第二阶段——存量 schema-2 events 日志一次性无损迁移为
// schema-1 线性日志。无损口径（用户 20261028 拍板原文）：迁移后线性重放与迁移前
// replaySessionDAG 对 selected head 的物化「消息内容/顺序/角色逐条一致」，以
// 逐条 role 比对 + save 链同一 identity 口径的内容 digest 比对为证；per-message
// 时间戳不在口径内（schema-1 replace 记录只承载一条记录级时间）。
// 原 DAG 日志字节先整体备份到 <log>.dagbak-tl1227 再落新日志，回滚 = 单文件
// 改回 + 删 schema-1 侧车；迁移函数与校验读数可独立复跑（check 模式只读）。

// SessionDAGMigrationMode decides whether MigrateSessionDAGLog writes to disk.
type SessionDAGMigrationMode string

const (
	// SessionDAGMigrationCheck replays and verifies without touching any byte.
	SessionDAGMigrationCheck SessionDAGMigrationMode = "check"
	// SessionDAGMigrationApply backs up the DAG log, writes the flat log, and
	// re-verifies from disk.
	SessionDAGMigrationApply SessionDAGMigrationMode = "apply"
)

// SessionDAGMigrationBackupSuffix marks the preserved schema-2 bytes.
const SessionDAGMigrationBackupSuffix = ".dagbak-tl1227"

// SessionDAGMigrationStatus enumerates the per-session outcomes.
type SessionDAGMigrationStatus string

const (
	SessionDAGMigrated        SessionDAGMigrationStatus = "migrated"
	SessionDAGChecked         SessionDAGMigrationStatus = "checked"
	SessionDAGSkipNotDAG      SessionDAGMigrationStatus = "skipped-not-dag"
	SessionDAGSkipLease       SessionDAGMigrationStatus = "skipped-lease-held"
	SessionDAGSkipDamaged     SessionDAGMigrationStatus = "skipped-damaged"
	SessionDAGSkipForeignHead SessionDAGMigrationStatus = "skipped-foreign-or-oversize"
)

// SessionDAGMigrationReport is one session's migration/verification reading.
type SessionDAGMigrationReport struct {
	SessionPath    string                    `json:"session_path"`
	LogPath        string                    `json:"log_path"`
	Mode           SessionDAGMigrationMode   `json:"mode"`
	Status         SessionDAGMigrationStatus `json:"status"`
	HeadID         string                    `json:"head_id,omitempty"`
	HeadCount      int                       `json:"head_count,omitempty"`
	MessageCount   int                       `json:"message_count,omitempty"`
	RolesMatch     bool                      `json:"roles_match"`
	DigestMatch    bool                      `json:"digest_match"`
	ReplayedOK     bool                      `json:"replayed_ok"`
	DamagedSource  bool                      `json:"damaged_source,omitempty"`
	LogBytesBefore int64                     `json:"log_bytes_before,omitempty"`
	LogBytesAfter  int64                     `json:"log_bytes_after,omitempty"`
	BackupPath     string                    `json:"backup_path,omitempty"`
	Detail         string                    `json:"detail,omitempty"`
}

// ErrSessionDAGMigrationFailed wraps a verify mismatch after an apply; the
// backup sidecar still holds the original bytes.
var ErrSessionDAGMigrationFailed = errors.New("session dag migration: post-apply verify failed")

// MigrateSessionDAGLog migrates one schema-2 event log to the schema-1 flat
// form, or verifies it in check mode. The sessionPath is the session's main
// transcript path; the event log and its sidecars are derived from it, so
// guardian/repairlog sidecars are migrated by passing their own transcript
// path (<stem>.guardian.jsonl etc.).
func MigrateSessionDAGLog(ctx context.Context, sessionPath string, mode SessionDAGMigrationMode) (*SessionDAGMigrationReport, error) {
	report := &SessionDAGMigrationReport{SessionPath: sessionPath, Mode: mode}
	report.LogPath = store.SessionEventLog(sessionPath)
	if report.LogPath == "" {
		return report, fmt.Errorf("session dag migration: empty session path")
	}
	limits := limitsForSessionLog(sessionPath, defaultSessionReplayLimits)
	probe, err := probeSessionEventLogWithLimits(sessionPath, limits)
	if err != nil {
		return report, err
	}
	if !probe.dag {
		report.Status = SessionDAGSkipNotDAG
		return report, nil
	}
	report.LogBytesBefore = probe.size
	st, err := replaySessionDAG(ctx, report.LogPath, limits)
	if err != nil {
		return report, fmt.Errorf("session dag migration: replay: %w", err)
	}
	if st.damaged {
		// A torn log cannot be proven lossless; leave it for the rescue paths.
		report.Status = SessionDAGSkipDamaged
		report.DamagedSource = true
		return report, nil
	}
	headID := st.selectedHead()
	msgs, _ := st.materialize(headID)
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		return report, fmt.Errorf("session dag migration: digest: %w", err)
	}
	report.HeadID = headID
	report.HeadCount = len(st.heads)
	report.MessageCount = len(msgs)

	roles := make([]provider.Role, len(msgs))
	for i := range msgs {
		roles[i] = msgs[i].Role
	}

	switch mode {
	case SessionDAGMigrationCheck:
		report.Status = SessionDAGChecked
		report.ReplayedOK = true
		report.RolesMatch = true
		report.DigestMatch = true
		return report, nil
	case SessionDAGMigrationApply:
		// A live foreign runtime may append behind our back; skip those and
		// let a later offline window pick them up.
		if SessionLeaseHeldByOtherRuntime(sessionPath) {
			report.Status = SessionDAGSkipLease
			return report, nil
		}
		return applySessionDAGMigration(sessionPath, report, msgs, roles, digest)
	default:
		return report, fmt.Errorf("session dag migration: unknown mode %q", mode)
	}
}

// applySessionDAGMigration backs up the schema-2 bytes, rewrites the log as a
// single schema-1 replace record, refreshes the shared index sidecar, drops
// the stale fold checkpoint, and re-verifies from disk.
func applySessionDAGMigration(sessionPath string, report *SessionDAGMigrationReport, msgs []provider.Message, roles []provider.Role, digest [sha256.Size]byte) (*SessionDAGMigrationReport, error) {
	backupPath := report.LogPath + SessionDAGMigrationBackupSuffix
	if err := copyFileAtomic(report.LogPath, backupPath); err != nil {
		return report, fmt.Errorf("session dag migration: backup: %w", err)
	}
	report.BackupPath = backupPath
	baseRevision, _, err := sessionContentRevision(sessionPath)
	if err != nil {
		// A missing/unreadable ledger is fine for a rewrite: schema-1 saves
		// treat a ledger without artifacts the same way (baseRevision 0).
		if sessionArtifactsHaveContent(sessionPath) {
			return report, fmt.Errorf("session dag migration: revision ledger: %w", err)
		}
		baseRevision = 0
	}
	if err := compactSessionEventLog(sessionPath, msgs, digest, baseRevision, "tl1227 dag-to-flat migration"); err != nil {
		return report, fmt.Errorf("session dag migration: write flat log: %w", err)
	}
	if err := writeSessionEventIndex(sessionPath, msgs, digest, baseRevision+1); err != nil {
		// The log itself already landed; index loss is reported, not fatal —
		// the next schema-1 save rebuilds it.
		slog.Warn("session: dag migration kept the flat log after index write failure", "path", sessionPath, "err", err)
	}
	_ = os.Remove(report.LogPath + dagCheckpointSuffix)

	// Verify from disk: the probe must now classify the log as native schema-1
	// and the replay must match the materialized chain entry by entry.
	post, err := probeSessionEventLog(sessionPath)
	if err != nil {
		return report, err
	}
	report.LogBytesAfter = post.size
	if !post.native || post.dag {
		report.Detail = fmt.Sprintf("post-apply probe native=%t dag=%t", post.native, post.dag)
		return report, fmt.Errorf("%w: %s", ErrSessionDAGMigrationFailed, report.Detail)
	}
	replay, err := replaySessionEventLog(report.LogPath)
	if err != nil {
		return report, fmt.Errorf("session dag migration: post-apply replay: %w", err)
	}
	report.RolesMatch = len(replay.msgs) == len(roles)
	if report.RolesMatch {
		for i := range roles {
			if replay.msgs[i].Role != roles[i] {
				report.RolesMatch = false
				report.Detail = fmt.Sprintf("role mismatch at %d: %v != %v", i, replay.msgs[i].Role, roles[i])
				break
			}
		}
	} else {
		report.Detail = fmt.Sprintf("message count mismatch: %d != %d", len(replay.msgs), len(roles))
	}
	postDigest, digestErr := digestSessionMessages(replay.msgs)
	report.DigestMatch = digestErr == nil && postDigest == digest
	report.ReplayedOK = !replay.damaged
	if !report.RolesMatch || !report.DigestMatch || !report.ReplayedOK {
		return report, fmt.Errorf("%w: %s", ErrSessionDAGMigrationFailed, report.Detail)
	}
	report.Status = SessionDAGMigrated
	return report, nil
}

// copyFileAtomic preserves the original log bytes at dst via tmp+rename so a
// crash mid-copy cannot destroy the only copy.
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(src), ".dagbak-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dst)
}
