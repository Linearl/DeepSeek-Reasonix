package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"reasonix/internal/store"
)

// tl1383 追加面：branch-meta 侧车就是 CAS 账本（sessionContentRevision 从
// meta.Revision 读账位），所以一个「指名别会话」或「带着别代系谱 revision」
// 的 meta 会让之后每一次保存都撞冲突——base 永远对不上账本，冲突恢复梯子
// 无限重燃，会话不可写（2026-11-04 9318b1f6 转正事故：转正走的是侧车整体
// rename 链，meta.id 残留 fork1 身份、revision/digest 停在 fork1 时代，而
// 转录字节已是 fork2 内容，三方互不相认）。
//
// ReconcileBranchMetaIdentity 把侧车对齐到会话文件的磁盘实况：
//   - meta.ID 强制改写为文件名 stem 的 branch id——与 migrateSessionSidecars
//     和 consolidation 转正已执行的同一规则；
//   - 且仅当身份错代（id 与文件名不符）时，才把 Revision/DiskRevision/
//     ContentDigest 对齐到事件日志末条干净记录的 revision 与重放内容摘要
//     ——日志是下一次保存要对账的磁盘真相。身份一致（id 相符）的 meta 是
//     本会话自己的账本，其 revision 即使与日志有 ±1 级偏差也属于崩溃窗口
//     的合法形态，绝不回拨（保留 recordSessionContentRevision 的
//     已记账未落盘语义）。
//
// meta 缺席是无事可办（no-op）；日志不可读或受损时跳过账本对齐（日志修复
// 归 damaged-marker 机制管），身份仍照改。返回是否发生了改写。
//
// 写入不走 saveBranchMeta：其内部的 preserveBranchMetaPersistence 账位不回
// 拨保序（防并发货账回退的全局不变量）会把外来世代的账位原样保回去，错代
// 对齐恰恰是必须回拨的唯一场景。这里在 meta 锁内做同形态的原子写（tmp+
// rename、0600），锁保证没有并发账本写者可被回拨伤害。
func ReconcileBranchMetaIdentity(sessionPath string) (bool, error) {
	unlock, err := LockSessionMetaPath(sessionPath)
	if err != nil {
		return false, err
	}
	defer unlock()

	meta, ok, err := LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		return false, err
	}
	wantID := BranchID(sessionPath)
	if meta.ID == wantID {
		return false, nil
	}
	changed := false
	if meta.ID != wantID {
		meta.ID = wantID
		changed = true
	}
	logPath := store.SessionEventLog(sessionPath)
	if info, statErr := os.Stat(logPath); statErr == nil && !info.IsDir() && info.Size() > 0 {
		replay, replayErr := replaySessionEventLog(logPath)
		if replayErr == nil && replay.records > 0 && replay.lastRevision > 0 {
			if meta.Revision != replay.lastRevision {
				meta.Revision = replay.lastRevision
				changed = true
			}
			if meta.DiskRevision != replay.lastRevision {
				meta.DiskRevision = replay.lastRevision
				changed = true
			}
			digest, _, digestErr := digestAndSizeSessionMessages(replay.msgs)
			if digestErr == nil {
				reconciledDigest := digestString(digest)
				if meta.ContentDigest != reconciledDigest {
					meta.ContentDigest = reconciledDigest
					changed = true
				}
			}
		} else if replayErr != nil {
			slog.Warn("agent: wrong-generation branch meta left its ledger unaligned; the event log is unreadable",
				"err", replayErr)
		}
	}
	if !changed {
		return false, nil
	}
	slog.Warn("agent: rewrote a wrong-generation branch meta sidecar",
		"id", meta.ID,
		"revision", meta.Revision,
	)
	metaPath := BranchMetaPath(sessionPath)
	if metaPath == "" {
		return false, fmt.Errorf("empty session path")
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return false, err
	}
	b = append(b, '\n')
	if err := atomicWriteFileContext(context.Background(), metaPath, ".branch.*.tmp", "branch-meta-reconcile", b, 0o600, false); err != nil {
		return false, err
	}
	return true, nil
}
