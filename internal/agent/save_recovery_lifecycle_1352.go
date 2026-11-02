package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/store"
)

// tl1352 A/B：recovery 副本生命周期的两个收口。
//
// 现场（20261031，tl1352）：主会话文件 mtime 停更两天，活跃写入落在
// recovery-*.jsonl 副本上，一晚 fork 出 6 个副本（副本风暴）。
//   A（副本转正）：fork 落地后，若两条路径的保存锁都空闲，把副本的转录与
//     自有 sidecar 整体 rename 回主文件名——主文件先经 BackupSessionFiles
//     白名单归档（被逐下的盘上字节零丢失），副本随之消失，活跃写入回到
//     「主文件即权威」的不变量上。
//   B（fork 总量闸）：每个 logical topic（剥到非副本根 stem）生命周期内
//     现存副本数 ≥ SnapshotForkQuotaPerTopic 时拒绝再 fork，由调用方强制
//     adopt 盘上转录并 fail-loud——副本风暴的硬上限。

// SnapshotForkQuotaPerTopic bounds how many recovery copies one logical topic
// may hold at once (tl1352 B). The third conflict for one topic adopts the
// disk transcript instead of forking a fourth copy.
const SnapshotForkQuotaPerTopic = 2

// RecoveryTopicRootID returns the stable logical-topic root of a session path:
// a plain session is its own root; a recovery copy peels every nested
// -recovery-<16 hex> suffix back to the non-recovery stem.
func RecoveryTopicRootID(path string) string {
	if root, ok := RecoveryFilenameRootID(path); ok {
		return root
	}
	return BranchID(path)
}

// RecoveryForkCountForTopic counts the recovery copies on disk that share the
// path's logical topic (excluding the path itself). Disk truth, stable across
// restarts — the 20261031 storm accumulated six copies across writer lanes,
// which an in-process counter would never have seen.
func RecoveryForkCountForTopic(path string) int {
	root := RecoveryTopicRootID(path)
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !store.IsSessionTranscriptName(e.Name()) {
			continue
		}
		sibling := filepath.Join(dir, e.Name())
		if sibling == path || !LooksLikeRecoveryFilename(sibling) {
			continue
		}
		if RecoveryTopicRootID(sibling) != root {
			continue
		}
		count++
	}
	return count
}

// PromoteRecoveryBranchToMain moves a freshly forked recovery branch back onto
// the main session path (tl1352 A). The main file's current bytes are archived
// first through the BackupSessionFiles allowlist, so the displaced on-disk
// transcript survives byte-for-byte; then the copy's transcript and every
// sidecar it owns rename onto the main names. The transcript renames last: any
// failure before it leaves the copy intact and the caller still bound to it.
//
// Both save-path mutexes (main first, then recovery — a fixed order, so two
// concurrent promoters cannot deadlock) and both compatibility file locks are
// held across the whole swap: in-process writers are excluded by the path
// mutexes, cross-process writers by the file locks. The locks are taken with
// try-lock on the path mutexes — promotion never queues behind a foreground
// save; when either lock is busy the caller stays on the copy.
//
// Main-side sidecars the copy does not own are left in place (never deleted):
// only allowlisted bytes are archived, so deleting outside the allowlist could
// destroy unarchived data. Stale leftovers are inert — the event log, which is
// the authoritative transcript, always renames over.
func PromoteRecoveryBranchToMain(recoveryPath, mainPath string) (archived string, err error) {
	recoveryPath = filepath.Clean(strings.TrimSpace(recoveryPath))
	mainPath = filepath.Clean(strings.TrimSpace(mainPath))
	if recoveryPath == "" || mainPath == "" {
		return "", fmt.Errorf("empty session path")
	}
	if recoveryPath == mainPath {
		return "", fmt.Errorf("recovery and main paths are identical")
	}
	if !LooksLikeRecoveryFilename(recoveryPath) {
		return "", fmt.Errorf("recovery path %s does not carry the recovery naming convention", filepath.Base(recoveryPath))
	}
	unlockMain, ok := tryLockSessionSavePath(mainPath)
	if !ok {
		return "", fmt.Errorf("main save path busy: %w", ErrSessionFileLockHeld)
	}
	defer unlockMain()
	unlockRecovery, ok := tryLockSessionSavePath(recoveryPath)
	if !ok {
		return "", fmt.Errorf("recovery save path busy: %w", ErrSessionFileLockHeld)
	}
	defer unlockRecovery()
	unlockMainFile, err := lockSessionFile(mainPath)
	if err != nil {
		return "", fmt.Errorf("lock main session file: %w", err)
	}
	defer unlockMainFile()
	unlockRecoveryFile, err := lockSessionFile(recoveryPath)
	if err != nil {
		return "", fmt.Errorf("lock recovery session file: %w", err)
	}
	defer unlockRecoveryFile()

	// 白名单归档先于任何落笔（1116「先备份再动手」同一纪律）。主文件缺席
	// （外部移除现场）时无可归档，直接转正。
	if _, statErr := os.Lstat(mainPath); statErr == nil {
		backupDir, backupErr := BackupSessionFiles(mainPath)
		if backupErr != nil {
			return "", fmt.Errorf("archive main file before promotion: %w", backupErr)
		}
		archived = backupDir
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("stat main file: %w", statErr)
	}

	recoveryArts := store.SessionSidecarFiles(recoveryPath)
	mainArts := store.SessionSidecarFiles(mainPath)
	for i := range recoveryArts {
		if _, statErr := os.Lstat(recoveryArts[i]); statErr != nil {
			continue // 副本没有这个侧车
		}
		if err := os.Rename(recoveryArts[i], mainArts[i]); err != nil {
			return "", fmt.Errorf("promote sidecar %s: %w", filepath.Base(recoveryArts[i]), err)
		}
	}
	if err := os.Rename(recoveryPath, mainPath); err != nil {
		return "", fmt.Errorf("promote recovery transcript: %w", err)
	}
	return archived, nil
}
