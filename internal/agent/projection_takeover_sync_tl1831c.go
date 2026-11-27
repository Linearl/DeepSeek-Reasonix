package agent

import (
	"log/slog"
)

// tl1831c 阶段③：rev-lag takeover 整文件重写与投影层的协同信号。
//
// 20261127 08:58 事故的根：11-26 21:07:15 的 stale-disk takeover
// （fork_decision=rewrite，live 2417 覆写 disk 2414，3 条 disk-only preserved
// +relayed）落地后，投影层没有得到任何信号——磁盘 sidecar 仍是旧 writer 封存
// 的字节（v10，17:20 封存），与重写后的 transcript 必然失配。次晨 08:58 重启
// 时 LoadProjectionSidecar 校验撞 hash_mismatch，tl1785 stale keep 双重失效
// （阶段②已修）导致整体 drop → 2.9M token 全量回退。
//
// 本信号把「下次启动校验撞失配才发现」提前到「重写落盘的那一刻」：
//
//   - 活进程自己的 fold 仍与 live canonical 逐字节一致（takeover 写的就是
//     自己内存形态）→ 立即重发 sidecar，磁盘 (transcript, sidecar) 对保持
//     hash 一致——08:58 的失配从源头消失；
//   - 其余任何形态（内存无 fold / fold 已失效）→ 磁盘上描述旧字节的
//     sidecar（常常正是死 writer 的封存）确定过期，删除之并踢有序重建
//     （kickProjectionRebuild），下次启动见到的是「无 sidecar → 新建」而
//     永远不是「旧 sidecar × 新 transcript 的失配现场」。
//
// 时序契约（tl1651 修复方向 b 的原子性思路——水位序）：
// transcript 字节先落盘（SaveRewrite 的 owned-rewrite commit），sidecar 写入
// 只能 FOLLOW 在其后。信号在 SaveAdoptOverStaleDisk 返回（save 锁已释放）后
// 同步触发，body 侧自取 compactionMu——与 installPrefixRewriteProbe 相同的
// 「Session 侧在 s.mu 外回调、agent 侧取 compactionMu」锁序纪律。两写之间
// 的崩溃窗口由阶段②兜底：落下的旧 sidecar 撞新 transcript 属 repair 家族
// 失配，stale keep 保留折叠有序重建，不再整体作废。

// installTakeoverProjectionSync binds the takeover→projection sync hook to
// the agent's session. Called once from NewAgent, next to
// installPrefixRewriteProbe; a plain Session without an agent keeps the nil
// hook (no-op).
func (a *Agent) installTakeoverProjectionSync() {
	if a == nil || a.sess.conversation == nil {
		return
	}
	a.sess.conversation.takeoverProjectionSync = a.onStaleDiskTakeoverCommitted
}

// onStaleDiskTakeoverCommitted is the agent-side half of the hook: fired by
// SaveAdoptOverStaleDisk after the rewrite commits, outside the save locks.
func (a *Agent) onStaleDiskTakeoverCommitted() {
	if a == nil || a.sess.conversation == nil {
		return
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	if len(st.Projection.Messages) > 0 && projectionValid(st, msgs) {
		// The live fold describes exactly the transcript this process just
		// wrote to disk. Republish the sidecar NOW so the disk pair stays
		// hash-consistent — the next load verifies on the fast path.
		a.sess.compactionMu.Lock()
		err := a.persistCompactionStateLocked()
		a.sess.compactionMu.Unlock()
		if err != nil {
			slog.Warn("agent: projection sidecar republish after takeover rewrite failed",
				"writer", SessionWriterID(), "err", err,
				"covered_count", st.Projection.CoveredCount)
			return
		}
		slog.Info("agent: projection sidecar republished after takeover rewrite",
			"writer", SessionWriterID(),
			"covered_count", st.Projection.CoveredCount,
			"projection_version", st.Projection.ProjectionVersion,
			"canonical_messages", len(msgs),
			"covered_prefix_hash", shortHash8(st.Projection.CoveredPrefixHash))
		return
	}
	// No usable in-memory fold: any sidecar on disk describes PRE-takeover
	// bytes (frequently the dead writer's own seal — the 08:58 shape).
	// invalidateProjection removes it and arms the ordered rebuild, so the
	// next startup sees "no sidecar → fresh fold", never the mismatch scene.
	a.invalidateProjection("stale_disk_takeover_rewrite")
}
