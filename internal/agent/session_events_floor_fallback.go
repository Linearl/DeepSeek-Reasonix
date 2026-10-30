package agent

import (
	"log/slog"
	"os"

	"reasonix/internal/secrets"
)

// tl 1285 —— 折叠地板的落盘兜底（任务书方向①：「预估不达标时不得直接跳过」）。
//
// 前链：sessionEventLogOversized 的 probe ladder（session_events_rotation.go）
// 已用实测证明折叠地板（连续 foldProbeMaxStrikes 次真实折叠省不到 10%），此
// 时日志仍超 cap、且不再折叠——旧行为到此死在 "left in place"（最大的日志被
// 永久豁免，只涨不缩）。本文件在每次成功保存的尾部（文件锁仍持有）把这样的
// 日志无损归档：tl1172 L2 zstd 原语（compressSessionEventLogZstLocked），JSONL
// 实测 5~10×，自描述、可逆、不删历史（红线），落盘体积真实下降可量化
// （tl1285 验收②）。
//
// 纪律：
//   - 每 regime 至多一次：archivedAt 闩与 probe ladder 同一增长常数（1.10），
//     忙碌 tab 会话付出的代价是「每 ~10% 增长一次压缩 + 下次保存一次物化」，
//     不是每保存一次；
//   - 有损的 L3 裁剪不在此处：仍归 desktop sweep 的实验开关（tl1172）管，
//     save 路径默认动作只有无损形态；
//   - 归档态安全性依赖已装机契约：save 首步 probeLogForSave → probeSession
//     EventLog → limitsForSessionLog → ensureSessionEventLogPlain 必然先把
//     明文物化回来，任何追加都不会落在明文缺失的窗口里；
//   - 失败不闩：压缩失败（含低于 4MiB 压缩地板的防御性拒绝——本入口另行
//     size 前置）只告警，下个保存重试，原日志不动（staged write + 发布校验）。

// maybeFloorArchiveEventLog runs at the tail of a successful save while the
// session file lock is still held. Cheap when inactive: two atomic loads, one
// stat, one map read for the overwhelming majority of saves.
func (s *Session) maybeFloorArchiveEventLog(path string) {
	plain := sessionEventLogPlainPath(path)
	if plain == "" {
		return
	}
	info, err := os.Stat(plain)
	if err != nil || info.IsDir() {
		// 归档态（明文缺失）：sweep 的 L2 已接管，或本就无事可做。
		return
	}
	cfg := currentEventsRotation()
	if cfg.mode != eventsRotationAuto || cfg.capMB <= 0 {
		return
	}
	logSize := info.Size()
	if logSize <= cfg.capMB<<20 || logSize < sessionEventZstCompressFloor {
		return
	}
	if !foldProbeFloorReached(path, logSize) || !foldProbeFloorArchiveDue(path, logSize) {
		return
	}
	before, after, err := compressSessionEventLogZstLocked(plain, sessionEventLogZstPathFor(path), "tl1285-fold-floor-fallback")
	if err != nil {
		slog.Warn("session: fold-floor archive fallback failed (will retry next save)",
			"path", plain, "bytes", logSize, "err", secrets.RedactError(err))
		return
	}
	foldProbeNoteFloorArchived(path, before)
	slog.Info("session: fold-floor fallback archived the oversized event log (lossless; materializes on next read/append)",
		"path", plain, "bytesBefore", before, "bytesAfter", after, "freedBytes", before-after)
}
