package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// 本文件是 desktop 主链（普通删除 / cleanup-pending 补收 / 终端归档）共用的
// 「侧车搬桶 + stem 前缀兜底清扫 + 计数」单点（tl1442-ext，2026-11-07 用户
// 裁决 A）。清单本体在 store.SessionTrashSidecars（单一来源）；搬移、清扫与
// 计数格式与 recovery trash 链（tl1442）共用 agent 导出的同一套机制——同
// 一文件无论从哪条链删掉，日志按键名字汇可以对账。

// moveSessionTrashArtifactsToBucket moves the session's full sidecar set
// (store inventory, bucket-named after the session key) plus its subagent
// artifacts into the trash bucket, then runs the stem-prefix catch-all sweep
// for shapes the inventory does not list (external repair ".bak-<stamp>"
// copies and any future shape). Returns the shared counting report; a sweep
// that catches anything is an inventory gap and Warns with the store list
// named as the single place to extend.
func moveSessionTrashArtifactsToBucket(dir, sessionPath, key, itemDir string) (agent.TrashMoveReport, error) {
	var report agent.TrashMoveReport
	keyStem := strings.TrimSuffix(key, store.SessionTranscriptSuffix)
	seen := map[string]bool{key: true}
	for _, artifact := range sessionTrashArtifacts(sessionPath, key) {
		moved, err := agent.MoveTrashArtifactTo(artifact.src, filepath.Join(itemDir, artifact.name), keyStem, &report)
		if err != nil {
			return report, err
		}
		if moved {
			seen[filepath.Base(artifact.src)] = true
		}
	}
	if err := trashSubagentArtifacts(dir, sessionPath, itemDir, &report); err != nil {
		return report, err
	}
	stem := strings.TrimSuffix(filepath.Base(sessionPath), store.SessionTranscriptSuffix)
	swept, err := agent.SweepTrashStragglers(dir, stem, agent.TrashRuntimeHandleNames(sessionPath), itemDir, seen, &report)
	if err != nil {
		return report, err
	}
	if swept > 0 {
		// 同 recovery trash 链的纪律：清扫保证「删除=零残留」，这行 Warn
		// 保证清单诚实——点名漏掉的形态，扩清单只有 store 一处。
		slog.Warn("desktop: session trash swept stem-prefixed stragglers not in the sidecar inventory — extend store.SessionTrashSidecars",
			"key", key, "dir", dir, "names", strings.Join(report.Stragglers, ","))
	}
	return report, nil
}

// logSessionTrashMove is the desktop main chain's counting line — the same
// key vocabulary as the recovery trash chain's (tl1442 format), so a deletion
// reconciles identically no matter which chain performed it.
func logSessionTrashMove(dir, key string, report *agent.TrashMoveReport) {
	agent.LogTrashMoveCounts("desktop: session artifacts trashed", dir, key, report)
}

// sweepSessionTrashResidueHard is the /clear teardown's form of the catch-all:
// the hard-delete chain has no bucket, so unlisted stem-prefixed entries are
// removed outright instead of moved. Runtime handles stay for their owners
// (the removal guard deletes the lock files at release; ClearCleanupPending
// owns the marker).
func sweepSessionTrashResidueHard(sessionPath string, report *agent.TrashMoveReport) (int, error) {
	dir := filepath.Dir(sessionPath)
	stem := strings.TrimSuffix(filepath.Base(sessionPath), store.SessionTranscriptSuffix)
	excluded := agent.TrashRuntimeHandleNames(sessionPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if excluded[name] || !strings.HasPrefix(name, stem+".") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		report.Stragglers = append(report.Stragglers, name)
		removed++
	}
	return removed, nil
}
