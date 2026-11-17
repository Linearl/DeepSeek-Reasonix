package agent

// tl1669 — 压缩执行模型改同步 await（用户 20261117 21:33 拍板终稿）。
//
// 现状根因（20261117 18:30/20:34 实锤）：压缩挂在请求构建链
// （sampling_request.go buildSamplingRequest → ContextManager.Prepare），
// 但压缩 pass 的【执行】与回合的【写入】是两条并行的路：后台 rebuild episode
// （preflight.go armRebuildEpisode）起独立 goroutine 跑 Prepare，压缩在飞时
// 回合继续写 canonical transcript（活跃轮 assistant/工具结果、channel F 引导、
// steer 注入、resume 提交）→ conversation 版本变化 → 压缩 commit 的 CAS
// （compact_commit.go:31-38）必然失配 → 整条 pass 被丢
// （errCompressStaleContext："conversation changed while compression"，
// fold_installed=false）。
//
// 竞品六家（codex turn.rs:1160-1238 / zcode turn-loop.ts:134 等）全部在
// turn loop 内【同步 await】压缩：压缩期间整个回合就地等待，写入者天然等在
// 压缩外面，竞态结构上撞不到。本件把同一语义搬过来：
//
//  1. 执行模型同步化：回合的 canonical 写入在压缩在飞时排队等待，压缩完成
//     后自动继续（US1/US3）；压缩期间不存在并发写入者 ⇒ conversation changed
//     的对账结构性消失（对账保留作防线）。
//  2. 在途写入排队：host 引导注入 / steer / resume 走同一条等待语义
//     （tl1648 排队门语义保留，见下方对账）。
//  3. 失败兜底：压缩失败不杀当前回合（用现有上下文继续，US2；实现见
//     sampling_request.go 的 prepareSamplingRequest 兜底腿）。
//  4. 进度可见：等待期间压缩进度卡照常（tl1657 已交付 tps/fold 读数）。
//
// 与 tl1648 / tl1648x 排队门的对账（不虚留两套）：
//
//   - tl1648 的 consumeSteer 门：语义【保留】。它管的是【出队】（压缩在飞时
//     条目留在队首，不弹队不注入），本件管的是【落盘】（写入前等压缩 settle）。
//     两层缺一不可：若把出队门改成「出队后等待」，等待期间会长期持有
//     steerMu，Steer() 的入队被堵死。
//   - tl1648x 的 host 引导门：等待实现【收拢】到本件的 awaitCompactionSettle
//     （单一原语 + 单一有界上界），行为不变（有界等待 → 超时照写 → CAS 兜底）。
//   - 本件新增：回合自身的 canonical 写入（assistant / 工具结果 / 回合记录）
//     走同一个写入漏斗 appendTurnMessage —— 这是「压缩期间无并发写入者」的
//     结构性保证，tl1648/1648x 两轮的排队门都只盖了注入面，没盖活跃轮消息。

import (
	"context"
	"log/slog"
	"time"

	"reasonix/internal/provider"
)

var (
	// compactionAwaitBound bounds ONE wait for an in-flight compaction.
	// Production compressions run minutes-long (the 20261117 09:40 window was
	// ~100s; the 包68 boot compaction 2m50s), so five minutes covers them with
	// headroom; past the bound the write goes through and the commit CAS stays
	// the backstop (a burned pass is reported, never silent). Vars so tests can
	// shrink them.
	compactionAwaitBound = 5 * time.Minute
	// compactionAwaitPoll is the re-check cadence while waiting. The wake
	// channel normally wakes a waiter the moment the pass settles; the poll
	// bounds the case where a slot was released without a wake.
	compactionAwaitPoll = 100 * time.Millisecond
)

// awaitCompactionSettle blocks the caller until no real maintenance
// transaction is in flight (任务920's maintaining counter) or the caller's
// context is done. Reports whether the caller actually waited.
//
// The idle face pays one atomic load and returns — ordinary sampling rounds
// carry zero cost (验收②：普通采样轮零门成本).
//
// This is the ONE await primitive of tl1669: the host-guidance funnel
// (tl1648x) and the turn write funnel (this file) both call it, so the
// "queue behind the compaction" semantics exists exactly once.
func (a *Agent) awaitCompactionSettle(ctx context.Context) bool {
	if a == nil || a.maintaining.Load() == 0 {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waited := false
	deadline := time.Now().Add(compactionAwaitBound)
	slog.Info("agent: write queued behind in-flight compaction", "writer", SessionWriterID())
	for a.maintaining.Load() > 0 {
		ch := a.compactionWakeChannel()
		// Re-check AFTER taking the channel: a release that lands between the
		// load above and the grab must not leave the waiter parked on a fresh
		// channel that nobody will close.
		if a.maintaining.Load() == 0 {
			break
		}
		waited = true
		remaining := time.Until(deadline)
		if remaining <= 0 {
			// Bounded wait exhausted: write through and let the commit CAS be
			// the backstop (tl1648x precedent: guidance is never silently
			// dropped; a burned pass is reported, never silent).
			slog.Warn("agent: write through past an unsettled compaction",
				"writer", SessionWriterID(), "bound", compactionAwaitBound.String())
			return waited
		}
		wait := compactionAwaitPoll
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			slog.Info("agent: compaction wait cancelled", "writer", SessionWriterID(), "err", ctx.Err())
			return waited
		case <-ch:
			timer.Stop()
		case <-timer.C:
		}
	}
	if waited {
		slog.Info("agent: compaction settled — queued write resumes", "writer", SessionWriterID())
	}
	return waited
}

// compactionWakeChannel returns the channel closed on the next maintenance
// release. Waiters park on it instead of polling, so the turn resumes the
// moment the pass settles.
func (a *Agent) compactionWakeChannel() chan struct{} {
	a.compactionWakeMu.Lock()
	defer a.compactionWakeMu.Unlock()
	if a.compactionWake == nil {
		a.compactionWake = make(chan struct{})
	}
	return a.compactionWake
}

// releaseCompactionWaiters wakes every writer waiting for a compaction to
// settle. Called on EVERY maintenance release (not only the last one): a
// session can hold several maintenance slots, and a waiter must not sleep
// through the release that freed its slot.
func (a *Agent) releaseCompactionWaiters() {
	a.compactionWakeMu.Lock()
	old := a.compactionWake
	a.compactionWake = make(chan struct{})
	a.compactionWakeMu.Unlock()
	if old != nil {
		close(old)
	}
}

// appendTurnMessage is the single write funnel for turn-owned canonical
// transcript writes (tl1669). Every append that can race a compaction pass
// goes through it, so a compaction in flight parks the writer instead of
// invalidating the pass's commit CAS.
//
// ctx may be nil (no cancellation); the wait is bounded by
// compactionAwaitBound either way.
func (a *Agent) appendTurnMessage(ctx context.Context, msg provider.Message) {
	if a == nil {
		return
	}
	a.awaitCompactionSettle(ctx)
	a.sess.conversation.Add(msg)
}

// appendTurnMessageBatch is the batch form of appendTurnMessage (the pinned
// revision + user turn landing at turn start).
func (a *Agent) appendTurnMessageBatch(ctx context.Context, msgs ...provider.Message) {
	if a == nil || len(msgs) == 0 {
		return
	}
	a.awaitCompactionSettle(ctx)
	a.sess.conversation.AddBatch(msgs...)
}

// AppendTurnMessage is the exported face of the canonical write funnel. The
// control package owns two canonical writes of its own — the host-initiated
// subagent answer (turn_orchestrator.go) and the MCP app result (mcp_apps.go)
// — and both must queue behind an in-flight compaction exactly like the
// agent's own messages (tl1669: 压缩期间无并发写入者).
func (a *Agent) AppendTurnMessage(ctx context.Context, msg provider.Message) {
	a.appendTurnMessage(ctx, msg)
}
