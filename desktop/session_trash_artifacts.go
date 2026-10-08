package main

import (
	"path/filepath"

	"reasonix/internal/store"
)

// sessionTrashArtifacts lists every file and directory a session owns, with
// the name each keeps inside the trash entry, so moving a session aside and
// restoring it round-trip the whole set.
// 任务 475（X6 模式 J）：name 一列不再手拼后缀——对 key（桶内保留的原始
// 会话文件名）套用与 src 同一个 store 构造器再取 Base。桶内名与恢复目标路径
// 因此保持既有的解耦语义（key 是原名，sessionPath 可以是改名后的目标），
// 而 store 布局变更时桶内名自动跟随，不再出现第二份拼写。
func sessionTrashArtifacts(sessionPath, key string) []sessionTrashArtifact {
	trashName := func(sidecar func(string) string) string {
		return filepath.Base(sidecar(key))
	}
	return []sessionTrashArtifact{
		{src: sessionPath, name: key},
		{src: store.SessionMeta(sessionPath), name: trashName(store.SessionMeta)},
		{src: store.SessionGoalState(sessionPath), name: trashName(store.SessionGoalState)},
		{src: store.SessionEventLog(sessionPath), name: trashName(store.SessionEventLog)},
		{src: store.SessionEventLogDamaged(sessionPath), name: trashName(store.SessionEventLogDamaged)},
		{src: store.SessionEventLogRotating(sessionPath), name: trashName(store.SessionEventLogRotating)},
		{src: store.SessionEventIndex(sessionPath), name: trashName(store.SessionEventIndex)},
		{src: store.SessionDisplayIndex(sessionPath), name: trashName(store.SessionDisplayIndex)},
		{src: store.SessionConflictLog(sessionPath), name: trashName(store.SessionConflictLog)},
		{src: store.SessionRecoveryState(sessionPath), name: trashName(store.SessionRecoveryState)},
		{src: store.SessionPinnedContext(sessionPath), name: trashName(store.SessionPinnedContext)},
		{src: sessionTelemetryPath(sessionPath), name: key + ".telemetry.json"},
		{src: store.SessionCheckpointDir(sessionPath), name: trashName(store.SessionCheckpointDir)},
		{src: store.SessionJobsDir(sessionPath), name: trashName(store.SessionJobsDir)},
		{src: store.SessionInboxDir(sessionPath), name: trashName(store.SessionInboxDir)},
	}
}
