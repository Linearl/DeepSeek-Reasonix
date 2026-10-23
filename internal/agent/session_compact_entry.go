package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"reasonix/internal/baseproc/pidalive"
	"reasonix/internal/store"
)

// CompactSessionFile is the manual "repair / slim this session" entry (task
// 275). It performs the same maintenance rewrite the automatic oversized gate
// would have done — DAG rotation plus the schema-1 compact — but as an
// explicit command, and reports the byte sizes it saw so callers can show the
// before/after to the user.
//
// Idleness is the lease: acquiring it fails when another runtime currently
// holds the session (desktop window, serve process), which is exactly the
// "single writer proof" the rotation path needs — and holding it for the
// duration satisfies sessionDAGSingleWriterProof's own lease check. The
// rewrite itself goes through Session.SaveRewriteCompact, so it reuses the
// production save locks, CAS and AtomicWriteFile rollback: a failed compact
// leaves the previous log in place rather than a half-rewritten one.
func CompactSessionFile(sessionPath string) (before, after int64, err error) {
	if sessionPath == "" {
		return 0, 0, fmt.Errorf("empty session path")
	}
	lease, leaseErr := TryAcquireSessionLease(sessionPath)
	if leaseErr != nil {
		return 0, 0, fmt.Errorf("session is not idle (lease busy): %w", leaseErr)
	}
	defer lease.Release()
	before = sessionCompactFileSize(sessionPath)
	// tl 1086 现象 b 根治: schema-2 (append-only DAG) 日志的修复必须退休陈旧
	// 并发头再物理旋转。
	//
	// 修复前的 SaveRewriteCompact 在 schema-2 上只追加一个 rewind 标记 +
	// 一个 replace 重编码(不追加节点)——不删任何字节。maintainDAGLog 的旋转门
	// (sessionDAGLogOversized) 只在「超限」时旋转, 而 ×16.9/×14.1 型会话的
	// 冗余形态恰恰不是超限: 它是「多并发头的可达并集」(每个头贡献自己的整条
	// 链), rotatedContentBytes() 的 union 去重把「所有 live 头的可达链」去重后
	// 只算一次, 所以「所有头链之和 >> live 并集」时不超限, 旋转门不触发。
	// 实测: 20 个陈旧分支, events=5.6MB live=250KB ratio=22.4, 修复前 freed=−9339 B。
	//
	// 手动修复是显式用户动作, 现在在 schema-2 上: ①退休陈旧并发头(零年龄门,
	// 因为 lease 已验证无其他 runtime 持有); ②强制物理旋转——reachable() 只保留
	// 活跃头链, 退休头的整条链物理丢弃。单写者证明由本函数持有的 lease 满足,
	// CAS+staged write 保证失败不损原日志。
	//
	// ②仅在有头被退休时执行: SaveRewriteCompact 本身的 maintainDAGLog 已经会
	// 在需要时旋转一次(超限/有洞/rewind), 无条件再旋一次会让 921 死会话形态的
	// generation 多进一位(预存红 TestCompactSessionFileClearsUnreachableRowsUnderCap
	// 期望 generation=2 = 一次旋转)。
	if st := sessionCompactRepairDAG(sessionPath); st != nil && st.repairRotated {
		if rotErr := sessionCompactRotateDAG(sessionPath, st); rotErr != nil {
			slog.Warn("session: forced DAG rotation during repair failed (falling back to SaveRewriteCompact)",
				"path", sessionPath, "err", rotErr)
		}
	}
	s, err := LoadSession(sessionPath)
	if err != nil {
		return before, 0, err
	}
	if err := s.SaveRewriteCompact(sessionPath); err != nil {
		return before, 0, err
	}
	after = sessionCompactFileSize(sessionPath)
	return before, after, nil
}

// sessionCompactFileSize is the size of the DAG event log — the file the
// oversized gate watches and the one the user-facing before/after numbers
// refer to (task 275: 300MB+ .events.jsonl files). The compat jsonl is
// deliberately excluded: it tracks transcript content, not log bloat.
func sessionCompactFileSize(sessionPath string) int64 {
	if info, err := os.Stat(store.SessionEventLog(sessionPath)); err == nil {
		return info.Size()
	}
	return 0
}

// sessionCompactRepairDAG prepares a schema-2 log for the forced rotation:
// retire stale concurrent heads (so reachable() drops their chains), then
// return the fresh replayed state. nil when the log is not schema-2 or
// unreadable — the caller falls through to SaveRewriteCompact, which still
// reports honestly (the log stays as-is).
//
// tl 1086 现象 b 根治: pre-fix CompactSessionFile only ran SaveRewriteCompact
// which appends a rewind marker + replace re-encode — no bytes drop. A ×16.9
// bloat is "many live heads each carrying a full chain" (union de-dup never
// trips the oversized gate), so maintainDAGLog's sessionDAGLogOversized gate
// never fires. Retire + forced rotate is the schema-2 equivalent of schema-1's
// fold-to-one-replace-record: buildRotatedSessionDAG keeps only reachable()
// nodes, so retired heads' chains physically leave the log.
func sessionCompactRepairDAG(sessionPath string) *sessionDAGState {
	probe, err := probeSessionEventLog(sessionPath)
	if err != nil {
		slog.Warn("session: compact DAG probe failed", "path", sessionPath, "err", err)
		return nil
	}
	if !probe.dag {
		return nil
	}
	// Retire stale concurrent heads first: the retire judgment (dead writer /
	// conservative chain-prefix) is conservative and cannot lose data; it only
	// marks heads retired so the rotation's reachable() drops their chains. The
	// sweep already does this on its path (945, with a 72h age gate); the manual
	// repair passes a zero age gate because the user explicitly asked to slim
	// THIS session and the lease (idleness check) already ran.
	host, _ := os.Hostname()
	var retired []string
	if r, err := retireStaleConcurrentHeads(sessionPath, time.Now(), pidalive.Alive, host, 0); err != nil {
		slog.Warn("session: retire stale concurrent heads before repair rotation failed (rotation continues)",
			"path", sessionPath, "err", err)
	} else {
		retired = r
		if len(retired) > 0 {
			slog.Info("session: retired stale concurrent heads before repair rotation",
				"path", sessionPath, "count", len(retired))
		}
	}
	// Fresh replay so the retire markers are reflected before rotation.
	st, err := replaySessionDAG(context.Background(), store.SessionEventLog(sessionPath), defaultSessionReplayLimits)
	if err != nil {
		slog.Warn("session: compact DAG replay failed", "path", sessionPath, "err", err)
		return nil
	}
	if st.damaged {
		slog.Warn("session: compact DAG replay damaged", "path", sessionPath)
		return nil
	}
	st.repairRotated = len(retired) > 0
	return st
}

// sessionCompactRotateDAG forces one maintenance rotation on a schema-2 log.
// Rotation is the only schema-2 action that physically drops unreachable
// bytes (buildRotatedSessionDAG keeps only reachable() nodes); the normal
// save path gates it behind sessionDAGLogOversized, which a multi-head
// bloat (×16.9) does not trip because the union of live chains still reads
// "not oversized". This function is only called from CompactSessionFile —
// the user-facing explicit repair — so it bypasses that gate deliberately.
// Failures are returned so the caller can report them; the log is never
// half-written (staged write + CAS publish).
func sessionCompactRotateDAG(sessionPath string, st *sessionDAGState) error {
	if err := sessionDAGSingleWriterProof(sessionPath, st, time.Now()); err != nil {
		return err
	}
	st.mu.RLock()
	reachable := len(st.reachable())
	nodes := len(st.nodes)
	live := len(st.liveHeads())
	st.mu.RUnlock()
	slog.Info("session: forced DAG rotation during repair", "path", sessionPath, "reachable", reachable, "nodes", nodes, "liveHeads", live)
	if err := rotateSessionDAG(sessionPath, st, time.Now()); err != nil {
		return err
	}
	st.mu.RLock()
	liveAfter := len(st.liveHeads())
	reachableAfter := len(st.reachable())
	st.mu.RUnlock()
	slog.Info("session: forced DAG rotation done", "path", sessionPath, "liveHeadsAfter", liveAfter, "reachableAfter", reachableAfter)
	return nil
}
