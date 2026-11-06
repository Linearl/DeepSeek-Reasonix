package agent

import (
	"errors"
	"fmt"
	"log/slog"
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

// promoteStagingSuffix marks a main-side sidecar displaced mid-promotion
// (tl1390). The two-step swap — main sidecar to the staging name first, copy
// sidecar onto the main name second — keeps every original byte reachable
// under a known name until the whole transaction lands, so a failed or
// crashed attempt can roll back or resume without losing anything.
const promoteStagingSuffix = ".promote-staging"

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

	// tl1442-ext（2026-11-07 用户裁决 A）：换位清单从 SessionSidecarFiles 核心
	// 16 件扩到 store.SessionStaticTrashSidecars 全集（telemetry/toolstats/
	// guardian 全家/五个目录）——旧清单转正后把副本的这些侧车留在死 stem 下，
	// 正是本任务要清的残留。这里必须用 static 形态：两条清单按位配对
	// （tl1390 两步换位 + 幂等续跑按索引配对），glob 形态的时间戳目录两侧
	// 数量可以不同，会错位；副本的 pre-repair 目录群在换位落定后单独改嫁。
	recoveryArts := store.SessionStaticTrashSidecars(recoveryPath)
	mainArts := store.SessionStaticTrashSidecars(mainPath)

	// tl1390 ②幂等续跑：上一次转正在换位中途崩溃会留下 .promote-staging 残留。
	// 按三态还原——副本侧车还在 = 配对未完成，原始主字节归还主名（主名已被占
	// 则以现主名为准、丢弃 staging）；副本侧车已不在 = 配对已完成，原始主字节
	// 在本轮白名单归档里，staging 直接丢弃。还原后按全新一轮继续。
	for i := range mainArts {
		staging := mainArts[i] + promoteStagingSuffix
		if _, err := os.Lstat(staging); err != nil {
			continue
		}
		_, recoveryStillThere := os.Lstat(recoveryArts[i])
		if recoveryStillThere != nil {
			// 副本侧车已不在 = 配对在上次崩溃前已完成；原始主字节在本轮
			// 白名单归档里，staging 退出舞台。
			if rmErr := os.Remove(staging); rmErr != nil {
				slog.Warn("promote: stale staging sidecar could not be removed", "file", staging, "err", rmErr)
			}
			continue
		}
		// 配对未完成：原始主字节归还主名（主名已被占则按现主名为准、丢 staging）。
		if _, mainOccupied := os.Lstat(mainArts[i]); mainOccupied == nil {
			if rmErr := os.Remove(staging); rmErr != nil {
				slog.Warn("promote: stale staging sidecar could not be removed", "file", staging, "err", rmErr)
			}
			continue
		}
		if err := os.Rename(staging, mainArts[i]); err != nil {
			return "", fmt.Errorf("resume staged sidecar %s: %w", filepath.Base(mainArts[i]), err)
		}
	}

	// tl1390 ①②：Windows 上 rename/replace 要求源与目标都没有未关句柄
	// （Go 的 os.Open 共享模式不含 FILE_SHARE_DELETE，MoveFileEx 拿不到
	// DELETE 权限即 "being used by another process"——2026-11-04 事故的永久
	// declined 机制）。换位用两步：主侧车先挪 staging 名、副本侧车再落主名。
	// 任何一步失败都整笔回滚（副本侧车退回副本名、staging 归还主名），主侧车
	// 字节自始至终以原名或 staging 名可达——绝不留下 meta/events 已搬主名而
	// turns.jsonl 卡住的半搬现场（该现场正是「event log vanished」WARN 与每
	// 30 秒 declined 伴生的来源，tl1313 的 vanished 哨兵因此免加抑制开关）。
	type movedSidecar struct {
		recovery, main, staging string
	}
	var moved []movedSidecar
	rollback := func(cause error, what string) error {
		for j := len(moved) - 1; j >= 0; j-- {
			p := moved[j]
			if err := os.Rename(p.main, p.recovery); err != nil {
				slog.Error("promote: rollback of promoted sidecar failed; file stays on the main name",
					"file", p.main, "err", err)
				continue // 主名被占时 staging 也还不了；两处都大声留痕
			}
			if p.staging != "" {
				if err := os.Rename(p.staging, p.main); err != nil {
					slog.Error("promote: rollback of staged main sidecar failed; original stays staged",
						"file", p.staging, "err", err)
				}
			}
		}
		return fmt.Errorf("%s: %w", what, cause)
	}
	// tl1442-ext：换位计数与删除链同格式对账（LogTrashMoveCounts 单一出口）。
	var migration TrashMoveReport
	for i := range recoveryArts {
		if _, statErr := os.Lstat(recoveryArts[i]); statErr != nil {
			continue // 副本没有这个侧车
		}
		if info, statErr := os.Lstat(recoveryArts[i]); statErr == nil {
			if info.IsDir() {
				migration.Dirs++
			} else {
				migration.Files++
			}
			migration.Categories = append(migration.Categories,
				StemRelativeSuffix(filepath.Base(mainArts[i]), strings.TrimSuffix(filepath.Base(mainPath), store.SessionTranscriptSuffix)))
		}
		pair := movedSidecar{recovery: recoveryArts[i], main: mainArts[i]}
		if _, statErr := os.Lstat(mainArts[i]); statErr == nil {
			staging := mainArts[i] + promoteStagingSuffix
			if err := os.Rename(mainArts[i], staging); err != nil {
				return "", rollback(err, "promote sidecar (stage main "+filepath.Base(mainArts[i])+")")
			}
			pair.staging = staging
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", rollback(statErr, "promote sidecar (stat main "+filepath.Base(mainArts[i])+")")
		}
		if err := os.Rename(recoveryArts[i], mainArts[i]); err != nil {
			if pair.staging != "" {
				if rstErr := os.Rename(pair.staging, mainArts[i]); rstErr != nil {
					slog.Error("promote: staged main sidecar could not return to the main name",
						"file", pair.staging, "err", rstErr)
				}
			}
			return "", rollback(err, "promote sidecar "+filepath.Base(recoveryArts[i]))
		}
		moved = append(moved, pair)
	}
	// tl1371：副本的 pinned-context 归属写在副本 id 上（fork 时由 desktop
	// copyPinnedContextState 重建）。rename 只换文件名不换字节——转正后主
	// 会话每轮读 pinned context 都会被 belongs-to 校验拒绝（20261103 新11
	// 现场）。归属改写挪到事务落位后统一做（tl1390 ②）：改写失败回滚会把
	// 已改写的归属留在副本名上，污染回滚后的副本。改写失败按 consolidation
	// 同一先例丢弃该文件（钉住列表从空重建），绝不留下一个每轮被拒的坏归属。
	if pinnedPath := store.SessionPinnedContext(mainPath); pinnedPath != "" {
		if _, statErr := os.Lstat(pinnedPath); statErr == nil {
			if rewriteErr := RewritePinnedContextSessionID(pinnedPath, BranchID(mainPath)); rewriteErr != nil {
				slog.Warn("promote: pinned-context rewrite failed; dropping",
					"file", pinnedPath, "err", rewriteErr)
				os.Remove(pinnedPath)
			}
		}
	}
	if err := os.Rename(recoveryPath, mainPath); err != nil {
		return "", rollback(err, "promote recovery transcript")
	}
	// 事务落定：被换下的原始主字节已在白名单归档里，staging 退出舞台。
	for _, p := range moved {
		if p.staging == "" {
			continue
		}
		if rmErr := os.Remove(p.staging); rmErr != nil {
			slog.Warn("promote: staged original sidecar could not be removed after promotion",
				"file", p.staging, "err", rmErr)
		}
	}
	// tl1383 追加面：rename 只换文件名不换字节——副本的 branch-meta 侧车搬进
	// 主名后，meta.id 仍是副本身份（fork 嵌套时甚至停在更早的世代），CAS 账本
	// 与文件名互不相认会让之后每次保存都撞冲突、会话不可写（2026-11-04
	// 9318b1f6 转正事故）。转正落位即对齐：id 改写为主身份，账位对齐事件日志
	// 实况（与 consolidation 转正的 meta 改写、migrateSessionSidecars 的
	// ID 改写同一规则）。对齐失败不阻断转正（字节已就位，元数据下次保存仍可
	// 收敛），但必须具名留下痕迹。
	if _, reconcileErr := ReconcileBranchMetaIdentity(mainPath); reconcileErr != nil {
		slog.Warn("promote: branch meta identity reconciliation failed; the meta may stay on the copy's generation",
			"err", reconcileErr)
	}
	// tl1442-ext 收口：副本 stem 下不得残留任何字节。static 换位清单不含
	// glob 的 pre-repair 备份目录群，清单外的形态（外部修复 .bak 等）也不在
	// 换位配对里——两类统一在此改嫁主名（副本身份的字节归到主身份下，跟
	// consolidate 转正的同一纪律）。两把 save 锁此刻仍在手，锁三件与
	// cleanup-pending 由各自机制处置，排除；promote-staging 中转名不在此轮
	// 语义内（上面 moved[] 清理已收口）。
	copyStem := strings.TrimSuffix(filepath.Base(recoveryPath), store.SessionTranscriptSuffix)
	mainStem := strings.TrimSuffix(filepath.Base(mainPath), store.SessionTranscriptSuffix)
	excluded := runtimeHandleNames(recoveryPath)
	dir := filepath.Dir(recoveryPath)
	if entries, readErr := os.ReadDir(dir); readErr == nil {
		for _, entry := range entries {
			name := entry.Name()
			if excluded[name] || strings.HasSuffix(name, promoteStagingSuffix) ||
				!strings.HasPrefix(name, copyStem+".") {
				continue
			}
			dst := filepath.Join(dir, mainStem+strings.TrimPrefix(name, copyStem))
			if err := os.Rename(filepath.Join(dir, name), dst); err != nil && !os.IsNotExist(err) {
				slog.Warn("promote: copy-stem leftover could not follow onto the main identity",
					"file", name, "err", err)
				continue
			}
			migration.Stragglers = append(migration.Stragglers, name)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		slog.Warn("promote: copy-stem leftover sweep could not read the session directory", "err", readErr)
	}
	if len(migration.Stragglers) > 0 {
		slog.Warn("promote: recovery copy carried entries outside the sidecar inventory — extend store.SessionTrashSidecars",
			"copy", filepath.Base(recoveryPath), "names", strings.Join(migration.Stragglers, ","))
	}
	LogTrashMoveCounts("agent: promoted recovery copy sidecars migrated onto the main identity",
		dir, filepath.Base(mainPath), &migration)
	return archived, nil
}
