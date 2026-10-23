package sessioninbox

// 任务1094缺陷C（观测补强）：sessioninbox 此前全模块零 slog——「谁把信箱标成
// Paused」这类问题在调查时只能停在「无法定案」，事后磁盘只剩最终状态（RunID
// 已被覆盖、items 已清空），过程无痕。本文件补上两个观测面：
//
//  1. 每次跨进程恢复打一条 slog.Info（oldRunId → newRunId / recovered / items
//     计数 / 触发点 / session path）——这是日志侧的可 grep 锚点；
//  2. 一份 <reasonix home>/logs/inbox-recovery.jsonl 快照（全局单文件，含条目
//     完整 id、状态、时间戳、RunID 前后值、触发点、session path——**绝不存
//     payload body**，body 已在 items 的 blob 里，这是体积可控的关键）。轮转
//     沿用既有参数化（desktop.log 同款 rename-once 滚动，5MB×2 代）。
//
// 触发点三类分开：cross_process_recovery（RunID 变更）、manifest_corrupt（manifest
// 损坏后 salvage）、schema_upgrade（schema 版本升级）——同一现场的三种成因必须
// 可以分别检索。

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/config"
)

const (
	// recoverySnapshotFileName is the global single-file snapshot ledger under
	// <reasonix home>/logs/. Every cross-process recovery appends one JSON line.
	recoverySnapshotFileName = "inbox-recovery.jsonl"
	// recoverySnapshotMaxBytes caps one generation — 5 MiB, matching the
	// parameterized rotation the task pins (5MB×2). Rotation: rename the
	// oversized file to <path>.1 at open, one generation deep — same pattern
	// as desktop.log's rollingLogWriter (reuse the pattern, do not write a
	// new rotation algorithm).
	recoverySnapshotMaxBytes = 5 << 20
	// recoverySnapshotMaxBackups is how many rolled generations stay on disk
	// beside the live file (5MB × 2 files on disk total = live + 1 backup).
	recoverySnapshotMaxBackups = 2
)

// recoveryTrigger names why a recovery pass ran. Wire-stable: never rename or
// reuse one once shipped.
const (
	RecoveryTriggerCrossProcess = "cross_process_recovery"
	RecoveryTriggerManifestCorrupt = "manifest_corrupt"
	RecoveryTriggerSchemaUpgrade  = "schema_upgrade"
)

// recoveryEntry is ONE JSON line in the snapshot ledger. Content-free with
// respect to message bodies — only ids, states, timestamps, counts, and
// RunID readings ever leave the store through this face.
type recoveryEntry struct {
	TS         string               `json:"ts"`
	Session    string               `json:"session"`
	OldRunID   string               `json:"oldRunId,omitempty"`
	NewRunID   string               `json:"newRunId"`
	Trigger    string               `json:"trigger"`
	Recovered  int                  `json:"recovered"`
	Items      int                  `json:"items"`
	Paused     bool                 `json:"paused"`
	Entries    []recoveryEntryItem  `json:"entries,omitempty"`
}

// recoveryEntryItem is one queue row's content-free fingerprint inside a
// recoveryEntry. Id, state and timestamp only — no body, no preview.
type recoveryEntryItem struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Source    string `json:"source,omitempty"`
	At        string `json:"at"`
}

// recoverySnapshotWriter is the process-wide (package-level) append-only
// writer for the recovery ledger. One global file, guarded by its own mutex
// (never the Store's — this runs on the store-open path, which already holds
// s.mu; the snapshot must not recurse back into the Store).
var recoverySnapshot = struct {
	mu   sync.Mutex
	f    *os.File
	size int64
}{
	f: nil,
}

// recoverySnapshotPath resolves <reasonix home>/logs/inbox-recovery.jsonl.
// Empty string when the reasonix home cannot be resolved — the caller then
// skips the snapshot (best effort, never fails the recovery itself).
var recoverySnapshotPath = func() string {
	home := config.ReasonixHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "logs", recoverySnapshotFileName)
}

// appendRecoverySnapshot appends one recoveryEntry as a single JSON line,
// rotating an oversized live file to <path>.1 first (one generation deep —
// the same rename-once pattern desktop.log uses, reusing the existing
// parameterized rotation semantics rather than writing a new algorithm).
// Best effort: a failed snapshot must never fail the recovery that is
// recording it.
func appendRecoverySnapshot(entry recoveryEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := recoverySnapshotPath()
	if path == "" {
		return
	}
	recoverySnapshot.mu.Lock()
	defer recoverySnapshot.mu.Unlock()
	if recoverySnapshot.f == nil {
		if err := openRecoverySnapshotLocked(path); err != nil {
			return
		}
	}
	if recoverySnapshot.size+int64(len(data))+1 > recoverySnapshotMaxBytes {
		rotateRecoverySnapshotLocked(path)
	}
	n, err := recoverySnapshot.f.Write(append(data, '\n'))
	if err != nil {
		return
	}
	recoverySnapshot.size += int64(n)
}

// openRecoverySnapshotLocked creates the log dir if needed, rotates an
// oversized previous generation to <path>.1 first, then opens the live file
// for append. Caller holds recoverySnapshot.mu.
func openRecoverySnapshotLocked(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil && st.Size() >= recoverySnapshotMaxBytes {
		_ = os.Remove(path + ".1")
		if err := os.Rename(path, path+".1"); err != nil {
			// Keep appending to the current file instead.
			_ = err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	recoverySnapshot.f = f
	recoverySnapshot.size = info.Size()
	return nil
}

// rotateRecoverySnapshotLocked rolls live → .1 (one generation deep), drops
// the previous .1 first. Caller holds recoverySnapshot.mu.
func rotateRecoverySnapshotLocked(path string) {
	if recoverySnapshot.f != nil {
		_ = recoverySnapshot.f.Close()
		recoverySnapshot.f = nil
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	recoverySnapshot.f = f
	recoverySnapshot.size = 0
}

// buildRecoveryEntryItem fingerprints one queue row for the recovery ledger:
// id, state, source and updated-at only. Never the body.
func buildRecoveryEntryItem(meta InboxItemMeta) recoveryEntryItem {
	return recoveryEntryItem{
		ID:     meta.ID,
		State:  string(meta.State),
		Source: meta.Source,
		At:     meta.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// logRecoveryEmit is the single emit point for a recovery pass: one slog.Info
// line (grep anchor in desktop.log) + one JSONL line in the recovery ledger.
// Never fails — observability must not gate the recovery it records.
func (s *Store) logRecoveryEmit(trigger, oldRunID string, recovered int, items []InboxItemMeta) {
	now := time.Now().UTC()
	slog.Info("sessioninbox: recovery pass",
		"trigger", trigger,
		"oldRunId", oldRunID,
		"newRunId", s.runID,
		"recovered", recovered,
		"items", len(items),
		"session", s.session,
	)
	entryItems := make([]recoveryEntryItem, 0, len(items))
	for _, meta := range items {
		entryItems = append(entryItems, buildRecoveryEntryItem(meta))
	}
	entry := recoveryEntry{
		TS:        now.Format(time.RFC3339),
		Session:   s.session,
		OldRunID:  oldRunID,
		NewRunID:  s.runID,
		Trigger:   trigger,
		Recovered: recovered,
		Items:     len(items),
		Paused:    s.man != nil && s.man.Paused,
		Entries:   entryItems,
	}
	appendRecoverySnapshot(entry)
}

// CloseRecoverySnapshot closes the package-level recovery ledger file so a
// process exit flushes cleanly. Safe to call multiple times. Exported so
// hosts that own process lifecycle can close it at shutdown.
func CloseRecoverySnapshot() {
	recoverySnapshot.mu.Lock()
	defer recoverySnapshot.mu.Unlock()
	if recoverySnapshot.f != nil {
		_ = recoverySnapshot.f.Close()
		recoverySnapshot.f = nil
	}
}
