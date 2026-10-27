package agent

import (
	"context"
	"os"
	"time"

	"reasonix/internal/baseproc/pidalive"
	"reasonix/internal/store"
)

// tl 1186 —— 折叠收益估计的口径收口（fold-landing 口径）。
//
// 失真机理：desktop 空闲 sweep 的收益判据 EventsFoldProfitable(logSize, live)
// 的第二个入参曾用主转录文件（main .jsonl）当 live 代理——那是「选中链」口径
// （落盘视图），而折叠的真实产物是「所有 live 头链的去重 union」（884 修复后
// rotatedContentBytes 的语义，1073 A/B 报告成因②）。视图 11MiB / 链 ~100MB 的
// 深分叉会话因此被判「折叠能省 ~89%」，sweep 过门后或卡 busy、或发起一次
// freed ∈ [−217, 0] 的空转重写——估计口径与产物口径脱钩后，估计值越大，
// 空转越勤。
//
// 本文件把估计口径钉在「折叠落地尺寸」上：CompactSessionFile 现在会做什么
// （repair 退休 + 强制旋转 + 维护重写，tl 1086/921 域），预测就算什么——
// 退休判定与 sessionCompactRepairDAG 完全同参（零年龄门、同 pidAlive/主机名），
// 幸存头链的 union 就是落地产物的忠实预测（单头形态实测 0.99×，A/B 报告）。
// 门槛数字（10%）不动（1172 红线），改的只是第二个入参的口径。
//
// 与「视图口径」的关系（对 1172 回板建议的正面回答）：视图口径只在产物跟得上
// 的形态生效——
//   - 不可达链历史 / 可退休并发头：落地 ≈ 视图，本函数给出的就是视图量级，
//     这类会话从此被放行并真实折回视图尺寸（123MB→~11MiB 形态）；
//   - 不可退休的 live 兄弟链（用户 fork 头是显式版本资产，945 判定永不 retire；
//     独占链的未知/跨主机写者头保守保留）：落地 ≈ 链 union，若按视图估计会说
//     谎——折完并不缩小，下个 tick 又超限（1172 担心的频繁折叠即此），本函数
//     给出链口径的数，估计如实拒绝，空转归零。
//
// 这是「估计口径必须与产物口径一致」的一致性落点：产物侧机制（repair 退休 +
// 强制旋转）已存在（1086），本估计直接复用其判定，两者不可能漂移。

// SessionFoldLandingBytes predicts, in bytes, where CompactSessionFile lands
// this session's event log if run now: the encoded union of the chains that
// survive the repair retire (zero age gate — the same judgment, facts and
// inputs as the repair itself runs). 0 means "no honest prediction": schema-1
// logs fold to their transcript (the caller's existing proxy already measures
// exactly that), archived logs are not probed (materializing an archive as a
// side effect of estimating would be a behavior change, not a measurement),
// and damaged or unreadable logs have no predictable landing. Callers keep
// their previous proxy on 0.
func SessionFoldLandingBytes(sessionPath string) int64 {
	if sessionEventLogIsArchived(sessionPath) {
		return 0
	}
	probe, err := probeSessionEventLog(sessionPath)
	if err != nil || !probe.dag {
		return 0
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	st, err := replaySessionDAG(context.Background(), store.SessionEventLog(sessionPath), defaultSessionReplayLimits)
	if err != nil || st.damaged {
		return 0
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.foldLandingBytesLocked(pidalive.Alive, host, time.Now(), 0)
}

// sessionEventLogIsArchived reports the at-rest archive form (plaintext
// missing, archive present) with two stats and no reads: exactly the shape
// estimation must not touch, since any probe would materialize the plaintext.
func sessionEventLogIsArchived(sessionPath string) bool {
	plain := sessionEventLogPlainPath(sessionPath)
	if plain == "" {
		return false
	}
	if _, err := os.Stat(plain); err == nil {
		return false
	}
	zst := sessionEventLogZstPathFor(sessionPath)
	info, err := os.Stat(zst)
	return err == nil && !info.IsDir()
}

// foldLandingBytesLocked is the survivor-union measurement behind
// SessionFoldLandingBytes: every live head the repair retire would keep
// (selected head included — the judgment never retires it) contributes its
// chain once, deduped by node id, and the union is measured with the same
// encoder rotatedContentBytesLocked uses. minAge follows the caller: the
// manual repair passes 0, and a sweep that wants the 72h-gated prediction
// passes staleConcurrentHeadMinAge. Caller holds st.mu for reading (same
// discipline as rotatedContentBytesLocked; nothing in the call tree locks).
func (st *sessionDAGState) foldLandingBytesLocked(pidAlive func(int) bool, selfHost string, now time.Time, minAge time.Duration) int64 {
	selected := st.selectedHead()
	selectedActivity := time.Time{}
	if h := st.heads[selected]; h != nil {
		selectedActivity = h.lastActivity
	}
	selectedChainLen := len(st.chainIDs(selected))
	chainLens := make(map[string]int, len(st.headOrder))
	for _, id := range st.headOrder {
		if h := st.heads[id]; h != nil {
			chainLens[id] = len(st.chainIDs(id))
		}
	}
	survivors := make([]string, 0, len(st.headOrder))
	for _, id := range st.liveHeads() {
		if id == selected {
			survivors = append(survivors, id)
			continue
		}
		h := st.heads[id]
		if h == nil {
			continue
		}
		var w *staleHeadWriterFact
		if wr := st.writers[h.writer]; wr != nil {
			w = &staleHeadWriterFact{pid: wr.pid, hostname: wr.hostname}
		}
		keep, _ := staleConcurrentHeadDecision(
			staleHeadCandidate{id: h.id, kind: h.kind, writer: h.writer, lastActivity: h.lastActivity},
			selected, selectedActivity, w, pidAlive, selfHost, now, minAge,
			func(string) int { return selectedChainLen },
			func(headID string) int { return chainLens[headID] })
		if !keep {
			survivors = append(survivors, id)
		}
	}
	return st.unionContentBytesLocked(survivors)
}
