// Package store is the single authority for reasonix's on-disk persistence
// layout. Nothing else should construct a persistence path by hand.
//
// This first slice owns the session-artifact sidecars — the files and
// directories that live beside a session's .jsonl (branch metadata, goal state,
// checkpoints, background-job artifacts, the cleanup-pending marker). They were
// previously derived independently in internal/agent, internal/jobs,
// internal/control and internal/acp, each re-spelling the suffix convention; a
// layout change meant hunting across packages. Centralizing them here makes
// store the one place that knows where a session's artifacts go.
//
// store is a leaf: it imports only the standard library, so any package may
// depend on it without risking an import cycle. Root/directory resolution (the
// ~/.reasonix tree) and the desktop root unification land in later slices.
package store

import "strings"

// Suffix spellings of the session persistence layout (X6 pattern J, task 475).
// store is the only place allowed to spell these literals; every constructor
// below is written in terms of them so a constant and the layout it names
// cannot drift apart, and classification outside store must reference the
// constants instead of re-spelling the suffix (source-asserted by
// TestSessionPathSpellingsSingleSource in this package).
const (
	SessionTranscriptSuffix      = ".jsonl"
	SessionEventLogSuffix        = ".events.jsonl"
	SessionEventLogDamagedSuffix = SessionEventLogSuffix + ".damaged"
	// SessionEventLogZstSuffix 是事件日志的整文件 zstd 归档形态（tl 1172 L2，
	// 照 codex rollout 的 .jsonl.zst 同款机制）：<id>.events.jsonl.zst。它不改变
	// append-only 语义：归档只发生在空闲维护窗口（lease/文件锁保护），任何读取
	// 与追加都会先物化回明文（agent.ensureSessionEventLogPlain），稳态下明文与
	// .zst 只存在其一；两者并存（压缩中途崩溃）时明文是事实源。
	SessionEventLogZstSuffix = SessionEventLogSuffix + ".zst"
	// SessionTranscriptZstSuffix 是主转录的整文件 zstd 归档形态（tl 1174，
	// 与 SessionEventLogZstSuffix 同款机制）：<id>.jsonl.zst。冷分层语义：
	// 热会话（冷阈值内活跃）保持明文；冷会话整文件压缩；读取透明解压（首次
	// 打开物理物化）；恢复活跃追加/重写时回到明文。稳态下明文与 .zst 只存在
	// 其一；两者并存（压缩中途崩溃）时明文是事实源。开关
	// experimental_jsonl_zstd（默认关）守压缩的「写」面——物化解压不随开关，
	// 关态下已存在的归档必须照常可开。
	SessionTranscriptZstSuffix = SessionTranscriptSuffix + ".zst"
	SessionTurnEventLogSuffix  = ".turns.jsonl"
	SessionConflictLogSuffix   = ".conflicts.jsonl"
	// SessionRepairLogSuffix 是会话自愈修复记录（任务 1011：<stem>.repairlog.jsonl）。
	// 它属于会话侧车：分类侧不得自行拼写该后缀，删除会话时必须一并清掉。
	SessionRepairLogSuffix = ".repairlog.jsonl"
	// SessionRewriteAttributionLogSuffix 是改写归因日志（tl1652：
	// <stem>.rewritelog.jsonl）。Session.Rewrite/NoteContentRewrite 等把
	// provider-visible 改写原因排队给下一次 provider 请求的 drain 归因
	// （pendingContentReasons，纯内存），该队列在关机/崩溃时丢失——tl1651
	// 现场的指纹过期归因就是这样丢的。本侧车在改写落地时同步追加一行
	// JSON，让归因跨进程可查；与 repairlog 同纪律：追加式审计侧车，不进
	// 会话列表，删除会话时一并清掉。
	SessionRewriteAttributionLogSuffix = ".rewritelog.jsonl"
	// SessionConflictPreserveLogSuffix 是分叉接管保全日志（tl1748：
	// <stem>.conflict-preserved.jsonl）。rev-lag takeover 用快照 rewrite 覆盖
	// 磁盘前，被弃侧（disk 侧）相对活跃线独有的消息先落一份完整副本到这里，
	// 用户消息随后以 origin=host 中继进活跃 turn——覆盖不再是销毁。与
	// repairlog 同纪律：追加式审计侧车，每会话一个文件、一次接管追加一行，
	// 不进会话列表（IsSessionTranscriptName 显式排除），删除会话时一并清掉。
	SessionConflictPreserveLogSuffix = ".conflict-preserved.jsonl"
	// SessionSpliceLedgerSuffix 是 pre-repair 分叉拼接档案袋（tl1787 续作，
	// tl1674 阶段二定案：<stem>.splices.json）。合并是纯元信息操作——拼接点
	// 登记/段边界/校验值都记在这份 sidecar 上，投影内容一个字节不动。与
	// repairlog 同纪律：元信息审计侧车，不进会话列表，删除会话时一并清掉。
	SessionSpliceLedgerSuffix = ".splices.json"
	// SessionCompactMetricsLogSuffix 是压缩/折叠关键读数日志（tl1837：
	// <stem>.compactmetrics.jsonl）。折叠计划/执行读数（buckets 分项、
	// covered_tokens、source→projection 压缩比、fold completed/refused 及
	// 其触发源）此前只写 desktop.log——日志轮转/进程重启后即不可得（20261127
	// 案B 调查被轮转卡住：fold 计划 1384 实际 329 的现场无历史可查）。本侧车
	// 每次压缩事件同步追加一行 JSON，尾部截断保上界；与 repairlog 同纪律：
	// 追加式审计侧车，不进会话列表（IsSessionTranscriptName 显式排除），
	// 删除会话时一并清掉。
	SessionCompactMetricsLogSuffix = ".compactmetrics.jsonl"
	SessionEventIndexSuffix        = ".event-index.json"
	SessionDisplayIndexSuffix      = ".display-index.json"
	// SessionGuardianTranscriptSuffix 是 guardian 伴随转录的尾缀
	// （<stem>.guardian.jsonl）。它是会话形态的伴随文件：经同一会话层存取、带
	// 自己的 meta/events/index 侧车，但不进会话列表（IsSessionTranscriptName
	// 显式排除）。单一来源：构造器 SessionGuardianTranscript 与分类函数
	// IsGuardianTranscriptName 都从这里拼写。
	SessionGuardianTranscriptSuffix = ".guardian.jsonl"
	// SessionMetaFileSuffix is the full classification tail of SessionMeta
	// (which appends ".meta" to the whole transcript path, historical layout).
	SessionMetaFileSuffix  = SessionTranscriptSuffix + ".meta"
	SessionLockFileSuffix  = SessionTranscriptSuffix + ".lock"
	SessionLeaseLockSuffix = SessionTranscriptSuffix + ".lease.lock"
	SessionLeaseInfoSuffix = SessionTranscriptSuffix + ".lease.json"
)

// IsSessionTranscriptName reports whether name is a primary session transcript
// file. Append-only event logs and guardian sidecars also end in .jsonl, so
// callers that discover sessions by directory scan must use this helper instead
// of filepath.Ext.
func IsSessionTranscriptName(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(name, SessionTranscriptSuffix) &&
		!strings.HasSuffix(name, SessionEventLogSuffix) &&
		!strings.HasSuffix(name, SessionTurnEventLogSuffix) &&
		!strings.HasSuffix(name, SessionConflictLogSuffix) &&
		!strings.HasSuffix(name, SessionRepairLogSuffix) &&
		!strings.HasSuffix(name, SessionRewriteAttributionLogSuffix) &&
		!strings.HasSuffix(name, SessionConflictPreserveLogSuffix) &&
		!strings.HasSuffix(name, SessionCompactMetricsLogSuffix) &&
		!strings.HasSuffix(name, SessionGuardianTranscriptSuffix)
}

// IsGuardianTranscriptName reports whether name is a guardian companion
// transcript (<stem>.guardian.jsonl). The companion is session-shaped — it
// saves and loads through the same session layer as a primary transcript —
// but it is deliberately NOT IsSessionTranscriptName: session-list discovery
// must keep treating it as a sidecar so it never surfaces as a session row
// (TestIsSessionTranscriptName pins that).
func IsGuardianTranscriptName(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(name, SessionGuardianTranscriptSuffix)
}

// IsLoadableSessionTranscriptName reports whether name may be opened as a
// session by agent.LoadSession: primary transcripts plus the guardian
// companion transcript. tl1424: the task-1071 load gate used to key directly
// on IsSessionTranscriptName, which also rejected the guardian companion —
// guardian restore then failed silently on every restart (conversation lost,
// delta cursor never restored). Audit/derived sidecars (event / turn /
// conflict / repair logs) stay excluded either way, so 1071's "a sidecar is
// never load-rewritten" intent is intact.
func IsLoadableSessionTranscriptName(name string) bool {
	return IsSessionTranscriptName(name) || IsGuardianTranscriptName(name)
}

// SessionRecoveryState is the persisted Auto-mode recovery checkpoint state
// (<id>.recovery.json). It is a regular session-owned sidecar, not a transcript.
func SessionRecoveryState(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".recovery.json"
}

// SessionContext is the context-projection / compaction-state sidecar
// (<id>.context.json). It holds the model-visible projection and cache
// telemetry; transcript authority remains with the native event log once one
// exists, with the primary .jsonl retained as its compatibility checkpoint.
func SessionContext(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".context.json"
}

// SessionPinnedContext is the optional desktop pinned-workspace-context
// sidecar (<id>.pinned-context.json). Older versions ignore it while keeping
// the primary transcript fully readable.
func SessionPinnedContext(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".pinned-context.json"
}

// SessionTelemetry is the desktop tab telemetry snapshot sidecar
// (<id>.jsonl.telemetry.json). Like the lock files it spells against the full
// transcript path, not the stem. Before tl1442 the deletion chains hand-spelled
// this suffix (desktop sessionTelemetryPath / a bare literal in the recovery
// trash move); the constructor is the single source now.
func SessionTelemetry(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".telemetry.json"
}

// SessionToolStats is the tool-error statistics sidecar
// (<id>.jsonl.toolstats.json), written by the agent runtime for every session
// it runs. Also spelled against the full transcript path. tl1442: every
// deletion chain missed it — it is the most common single-file residue in the
// 2026-11-05 field measurement.
func SessionToolStats(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".toolstats.json"
}

// SessionGuardianTranscript is the guardian companion session file
// (<stem>.guardian.jsonl). The guardian transcript is session-shaped — it saves
// through the same session layer as a main transcript and carries its own
// sidecars (meta / event log / indexes) derived from this path.
func SessionGuardianTranscript(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionGuardianTranscriptSuffix
}

// SessionGuardianCursor is the guardian replay cursor sidecar
// (<stem>.guardian.cursor.json).
func SessionGuardianCursor(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".guardian.cursor.json"
}

// SessionImagePackDir is the legacy image-extraction directory
// (<stem>.jsonl.imgpack) that pre-P19 builds wrote beside sessions. No current
// writer produces it, but field directories still carry it, and a deleted
// session must not leave it behind (tl1442).
func SessionImagePackDir(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".imgpack"
}

// sessionStem strips the .jsonl suffix so a sidecar sits beside the session as
// <id>.<kind> rather than <id>.jsonl.<kind>.
func sessionStem(sessionPath string) string {
	return strings.TrimSuffix(sessionPath, ".jsonl")
}

// SessionMeta is the branch-metadata sidecar. Unlike the other sidecars it
// appends to the full session path (historical layout), so session.jsonl yields
// session.jsonl.meta.
func SessionMeta(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".meta"
}

// SessionGoalState is the persisted active-goal sidecar (<id>.goal-state.json).
func SessionGoalState(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".goal-state.json"
}

// SessionEventLog is the append-only transcript event log (<id>.events.jsonl).
func SessionEventLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventLogSuffix
}

// SessionEventLogZst is the whole-file zstd archive of the event log
// (<id>.events.jsonl.zst, tl 1172 L2). It is a storage form, not a separate
// log: readers and writers materialize it back to SessionEventLog before use
// (the agent layer owns that discipline), so nothing here beyond the path.
func SessionEventLogZst(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventLogZstSuffix
}

// SessionTranscriptZst is the whole-file zstd archive of the primary
// transcript (<id>.jsonl.zst, tl 1174). Same storage-form discipline as
// SessionEventLogZst: the agent layer materializes it back to the plain
// transcript before use; this only spells the path.
func SessionTranscriptZst(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionTranscriptZstSuffix
}

// IsSessionTranscriptZstName reports whether name is an archived primary
// transcript (<id>.jsonl.zst). The events-archive sibling
// (<id>.events.jsonl.zst) is excluded: it classifies with its own constants
// and must never be discovered as a session.
func IsSessionTranscriptZstName(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(name, SessionTranscriptZstSuffix) &&
		!strings.HasSuffix(name, SessionEventLogZstSuffix)
}

// zstArchiveTail is the generic zstd archive tail both archive forms share
// (<id>.events.jsonl.zst, tl 1172; <id>.jsonl.zst, tl 1174).
const zstArchiveTail = ".zst"

// SessionTranscriptPlainName strips the archive tail from a discovered
// transcript-archive name ("<id>.jsonl.zst" -> "<id>.jsonl") so every consumer
// (load, meta/lease sidecars, deletion) works on the canonical plain spelling.
// Only the ".zst" tail comes off — the ".jsonl" part of the full archive
// suffix is the transcript identity and must survive the strip. Anything that
// is not an archive name reports unchanged.
func SessionTranscriptPlainName(zstName string) string {
	return strings.TrimSuffix(zstName, zstArchiveTail)
}

// SessionEventLogDamaged is the salvage sidecar for event-log bytes that tail
// repair would otherwise discard (<id>.events.jsonl.damaged). It must NOT end
// in .jsonl: older binaries scanning a shared session directory classify any
// non-excluded .jsonl file as a primary transcript and would resurrect the
// damaged bytes as a phantom session.
func SessionEventLogDamaged(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventLogDamagedSuffix
}

// IsSessionEventLogName reports whether name — a base filename or a full path —
// is a session's primary event log (<id>.events.jsonl). The .damaged sibling
// ends in another tail and is classified with its own constant.
// Scan-side classification belongs here so the family shape lives in one place
// (X6 pattern J, task 475).
func IsSessionEventLogName(name string) bool {
	return strings.HasSuffix(name, SessionEventLogSuffix)
}

// ResolveSessionEventLog is the read-side half of the write/read path
// convention: a reader may be handed either accepted shape — the transcript
// path (<id>.jsonl) or an already-resolved event-log path (<id>.events.jsonl,
// e.g. the path a sessionDAGState carries) — and normalizes it here instead of
// re-spelling the suffix branch. Applying SessionEventLog to a resolved path
// would yield "x.events.events.jsonl", which stats as missing and silently
// reported size zero (task 104: the adaptive budget became a no-op for every
// caller holding a resolved path).
func ResolveSessionEventLog(sessionPathOrLog string) string {
	p := strings.TrimSpace(sessionPathOrLog)
	if p == "" || strings.HasSuffix(p, SessionEventLogSuffix) {
		return p
	}
	return SessionEventLog(p)
}

// SessionTranscriptFromEventLog is the inverse of SessionEventLog for a
// resolved log path: it recovers the owning transcript path (<id>.jsonl).
// Anything that is not an event log — a transcript itself, a .damaged salvage,
// an empty string — reports "".
func SessionTranscriptFromEventLog(logPath string) string {
	p := strings.TrimSpace(logPath)
	if !strings.HasSuffix(p, SessionEventLogSuffix) {
		return ""
	}
	return strings.TrimSuffix(p, SessionEventLogSuffix) + SessionTranscriptSuffix
}

// SessionTurnEventLog is the append-only local runtime lifecycle ledger
// (<id>.turns.jsonl). It is independent from the provider transcript so old
// readers can continue to consume the primary session unchanged.
func SessionTurnEventLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionTurnEventLogSuffix
}

// SessionTurnEventLogDamaged preserves a corrupt/torn ledger tail before the
// valid prefix is truncated back into service.
func SessionTurnEventLogDamaged(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return SessionTurnEventLog(sessionPath) + ".damaged"
}

// SessionEventIndex is the listing/checkpoint index for the event log
// (<id>.event-index.json). It contains derived offsets and digests, not the
// transcript body.
func SessionEventIndex(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionEventIndexSuffix
}

// SessionDisplayIndex is the paging sidecar for the transcript
// (<id>.display-index.json). It contains per-message byte offsets, roles, and
// turn boundaries derived from the transcript, never message bodies, so a
// reader can page a huge history without parsing whole session files.
func SessionDisplayIndex(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionDisplayIndexSuffix
}

// SessionConflictLog is the append-only diagnostic log for snapshot conflict
// recoveries (<id>.conflicts.jsonl). It contains revision counters and branch
// ids, not transcript content.
func SessionConflictLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionConflictLogSuffix
}

// SessionRepairLog is the self-heal repair log of a session (task 1011:
// <stem>.repairlog.jsonl). It stays beside the transcript like every other
// sidecar so deleting the session removes the repair history too.
func SessionRepairLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionRepairLogSuffix
}

// SessionRewriteAttributionLog is the rewrite-attribution journal of a session
// (tl1652: <stem>.rewritelog.jsonl). It stays beside the transcript like every
// other sidecar so deleting the session removes the attribution trail too.
func SessionRewriteAttributionLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionRewriteAttributionLogSuffix
}

// SessionCompactMetricsLog is the compaction/fold key-metrics journal of a
// session (tl1837: <stem>.compactmetrics.jsonl). It stays beside the
// transcript like every other sidecar so deleting the session removes the
// metrics trail too.
func SessionCompactMetricsLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionCompactMetricsLogSuffix
}

// SessionSpliceLedger is the pre-repair fork splice dossier of a session
// (tl1787 续作, tl1674 阶段二定案: <stem>.splices.json). The merge is a pure
// metadata operation — splice-point registration, segment boundaries and
// checksums live on this sidecar while the projection content stays
// byte-for-byte untouched. It stays beside the transcript like every other
// sidecar so deleting the session removes the splice records too.
func SessionSpliceLedger(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionSpliceLedgerSuffix
}

// SessionConflictPreserveLog is the fork-takeover preservation log of a
// session (tl1748: <stem>.conflict-preserved.jsonl). Before a rev-lag takeover
// rewrites the transcript, the abandoned disk side's messages that the live
// line does not already contain land here in full, so the overwrite is never a
// silent destruction. It stays beside the transcript like every other sidecar
// so deleting the session removes the preserved copies too.
func SessionConflictPreserveLog(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + SessionConflictPreserveLogSuffix
}

// SessionLockFile is the advisory save lock (<id>.jsonl.lock).
func SessionLockFile(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".lock"
}

// SessionLeaseLock is the runtime ownership lock (<id>.jsonl.lease.lock).
func SessionLeaseLock(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".lease.lock"
}

// SessionLeaseInfo is the runtime ownership metadata
// (<id>.jsonl.lease.json).
func SessionLeaseInfo(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".lease.json"
}

// SessionCheckpointDir is the snapshot-checkpoint directory (<id>.ckpt).
func SessionCheckpointDir(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".ckpt"
}

// SessionJobsDir is the background-job artifact directory (<id>.jobs).
func SessionJobsDir(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".jobs"
}

// SessionInboxDir is the durable session-level instruction inbox
// (<id>.inbox/). Manifest metadata and frozen prompt blobs live here.
func SessionInboxDir(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".inbox"
}

// SessionRemindersDir is the durable in-session delayed-reminder queue
// (<id>.reminders/, task 1001). One JSON file holds the pending entries.
func SessionRemindersDir(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".reminders"
}

// SessionCleanupPending is the delayed-cleanup marker (<id>.cleanup-pending.json).
func SessionCleanupPending(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	return sessionStem(sessionPath) + ".cleanup-pending.json"
}

// SessionSidecarFiles returns every regular-file sidecar owned by a session
// transcript: branch meta, goal state, event/index logs, pinned context, and
// diagnostic logs.
// Every surface that deletes a session (desktop trash, /clear, serve, ACP)
// must remove all of these — the event log is the authoritative transcript, so
// leaving it behind both leaks the "deleted" conversation and lets LoadSession
// resurrect it. Directory artifacts (checkpoints, jobs) and ephemeral
// lock/lease files have their own lifecycles and are intentionally not listed.
func SessionSidecarFiles(sessionPath string) []string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return nil
	}
	return []string{
		SessionMeta(sessionPath),
		SessionGoalState(sessionPath),
		SessionEventLog(sessionPath),
		SessionEventLogZst(sessionPath),
		SessionEventLogDamaged(sessionPath),
		// tl-1174：归档态主转录也是会话拥有的字节——删除会话漏掉它等于
		// 泄露「已删除」的对话（与 SessionEventLogZst 同理）。
		SessionTranscriptZst(sessionPath),
		SessionTurnEventLog(sessionPath),
		SessionTurnEventLogDamaged(sessionPath),
		SessionEventIndex(sessionPath),
		SessionDisplayIndex(sessionPath),
		SessionConflictLog(sessionPath),
		SessionRepairLog(sessionPath),
		// tl1787 续作：pre-repair 分叉拼接档案袋（纯元信息 sidecar）随家族
		// 走删除/还原/身份换位全链——清单单一来源纪律（加一行=全删除链同
		// commit 生效）。
		SessionSpliceLedger(sessionPath),
		SessionRewriteAttributionLog(sessionPath),
		// tl1837：压缩/折叠关键读数（buckets/fold 指标）随家族走删除/还原/
		// 身份换位全链——清单单一来源纪律（加一行=全删除链同 commit 生效）。
		SessionCompactMetricsLog(sessionPath),
		// tl1841（d1 扫描 G1+G6）：takeover 保全日志同纪律随行。此前它只在
		// 注释里承诺「像其他 sidecar 一样随行」，登记表却没收——删除链靠
		// stem 兜底清扫每次 WARN 捡走、转正换位链被副本同名件无归档覆盖、
		// native 迁移链留旧 stem 孤儿（三条链一行同 commit 收口）。转正链的
		// 「先归档后换位」另需归档白名单同 commit 补键（session_selfheal.go，
		// 差集门禁 TestSessionBackupAllowlistGateCoversStaticInventory 钉两表
		// 不再漂移）。
		SessionConflictPreserveLog(sessionPath),
		SessionRecoveryState(sessionPath),
		SessionContext(sessionPath),
		SessionPinnedContext(sessionPath),
	}
}
