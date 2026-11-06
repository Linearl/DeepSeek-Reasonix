package store

import (
	"path/filepath"
	"strings"
)

// 本文件是「删除/换位链侧车全集清单」的单一事实源（tl1442 + tl1442-ext，
// 2026-11-07 用户裁决 A：desktop 主链与 merge-promote 两处与 recovery trash
// 链共用同一份清单——单一来源，不许任何链路复制第二份清单）。
//
// 每一条真正把 <stem>.jsonl 主线文件移走的路径都必须让这份清单里的工件跟着
// 走（进回收站桶、或随身份换位改名）。清单列出的是**会话拥有的数据工件**：
// 运行时句柄（save/lease 锁、lease 身份、cleanup-pending 标记）由各自的
// 机制（removal guard / ClearCleanupPending）处置，不在此列；主线转录自身
// 由各链路的两段式协议（先搬转录、再搬清单）处置，也不在此列。

// SessionPreRepairBackupSuffix 是自愈修复备份目录的后缀形态：
// <stem>.pre-repair-<UTC 紧凑时间>。一次修复落一个目录，反复修复的会话会
// 带多个。后缀常量落 store（拼写单一来源）；清单侧用 glob 把时间戳挡在
// 清单业务之外。
const SessionPreRepairBackupSuffix = ".pre-repair-"

// SessionTrashSidecars returns every session-owned artifact that must follow
// the transcript out of the session directory whenever a deletion chain moves
// it aside (recoverable trash, /clear teardown, merge-promote archiving) or
// swaps another transcript onto its identity (merge-promote winner sidecars).
//
// tl1442 (2026-11-05 用户实测 5 删 4 例残留 1~83 文件): every deletion chain
// used to hand-maintain its own list, and each missed different shapes — the
// toolstats counter, the whole guardian family, the self-heal pre-repair
// backup directories, the reminders queue, and the legacy imgpack directory.
// This function is the one inventory every chain now derives from: extending
// it changes every deletion path in the same commit.
//
// The explicit inventory is backed by a stem-prefix catch-all sweep at each
// call site (shapes invented after this list still leave zero residue — the
// 2026-11-05 field-measured ".jsonl.bak-<stamp>-<tag>" external repair copies
// are exactly such a shape); a sweep that catches anything is a Warn naming
// the inventory as stale.
//
// Runtime handles (lock/lease files, cleanup-pending) and the transcript
// itself are intentionally not here — the removal guard and the stage protocol
// own them.
func SessionTrashSidecars(sessionPath string) []string {
	artifacts := SessionStaticTrashSidecars(sessionPath)
	if len(artifacts) == 0 {
		return nil
	}
	stem := strings.TrimSuffix(sessionPath, SessionTranscriptSuffix)
	// Self-heal backups: one ".pre-repair-<UTC compact stamp>" directory per
	// repair run, so a repeatedly repaired session carries several. The glob
	// keeps the stamp out of this inventory's business.
	if backups, err := filepath.Glob(stem + SessionPreRepairBackupSuffix + "*"); err == nil {
		artifacts = append(artifacts, backups...)
	}
	return artifacts
}

// SessionStaticTrashSidecars is the glob-free form of SessionTrashSidecars:
// pure constructor applications, so two spellings of one session yield two
// lists of identical length and order. The identity-swap chain
// (PromoteRecoveryBranchToMain) pairs the lists positionally and therefore
// consumes this form; the pre-repair backup directories it omits are swept by
// that chain separately, after the swap lands. Deletion chains use the full
// SessionTrashSidecars.
func SessionStaticTrashSidecars(sessionPath string) []string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return nil
	}
	artifacts := append([]string(nil), SessionSidecarFiles(sessionPath)...)
	// The guardian companion is a session-shaped transcript in its own right;
	// its sidecars derive from its own path, and the replay cursor rides with
	// the family.
	guardianPath := SessionGuardianTranscript(sessionPath)
	artifacts = append(artifacts, guardianPath, SessionGuardianCursor(sessionPath))
	artifacts = append(artifacts, SessionSidecarFiles(guardianPath)...)
	artifacts = append(artifacts,
		// Runtime counters and telemetry (the 2026-11-05 measured gaps).
		SessionTelemetry(sessionPath),
		SessionToolStats(sessionPath),
		// Directory artifacts.
		SessionCheckpointDir(sessionPath),
		SessionJobsDir(sessionPath),
		SessionInboxDir(sessionPath),
		SessionRemindersDir(sessionPath),
		SessionImagePackDir(sessionPath),
	)
	return artifacts
}
