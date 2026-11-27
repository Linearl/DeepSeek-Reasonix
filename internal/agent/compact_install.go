// The automatic fold's install tail: checkpoint body, CAS commit, success
// outcome, done telemetry.
package agent

import (
	"log/slog"
	"time"

	"reasonix/internal/provider"
)

// foldProjectionInstall carries one prepared summary through installation.
type foldProjectionInstall struct {
	trigger                                 string
	msgs, canonical, fold, kept, bodySuffix []provider.Message
	retention                               userTurnRetention
	summary                                 string
	head, covered, sourceTokens             int
	transcriptVersion                       uint64
	startProjectionVersion, startGeneration uint64
	activeTurn                              int64
	viewInputHash                           string
	res                                     foldSummary
	tele                                    CompactionTelemetry
}

func (a *Agent) installFoldProjection(in foldProjectionInstall) (CompactionOutcome, error) {
	tele := in.tele
	// The projection body freezes only prefix + digest + kept messages; the
	// verbatim tail splices live from canonical[start:] so tail-side rewrites
	// (rewind truncation, snips) stay visible without rebuilding the fold.
	projMsgs := checkpointProjectionMessages(in.msgs, in.head, in.kept, in.summary)
	if len(in.bodySuffix) > 0 {
		projMsgs = append(projMsgs, projectionMessagesPreservingPinnedContext(in.bodySuffix)...)
	}
	// 任务 1025 ③: a carrier chain of stale full snapshot + superseding deltas
	// collapses to one fresh full snapshot at the merged current state — the
	// 217KB memory block was riding twice after a mid-session memory save.
	projMsgs = collapseSessionContextChain(projMsgs, in.msgs)
	tele.UserTurnsKept, tele.UserTurnsDropped = in.retention.Kept, in.retention.Dropped
	projMsgs, spliced, projTokens, err := a.preparePinnedCheckpointCandidate(in.trigger, projMsgs, in.canonical, in.covered, in.sourceTokens, &tele)
	if err != nil {
		a.emitCompactionAborted(in.trigger)
		return CompactionNoop, err
	}
	viewOutputHash := providerVisibleFingerprint(modelInputMessages(spliced))
	_, err = a.commitSummaryProjection(summaryProjectionCommit{
		canonical: in.canonical, fold: in.fold, projected: projMsgs, result: in.res,
		transcriptVersion: in.transcriptVersion, projectionVersion: in.startProjectionVersion,
		generation: in.startGeneration, activeTurn: in.activeTurn, trigger: in.trigger,
		summary: in.summary, inputHash: in.viewInputHash, outputHash: viewOutputHash,
		sourceTokens: in.sourceTokens, projectionTokens: projTokens, covered: in.covered,
	})
	if err != nil {
		// 任务 867: the automatic path never recorded a stale CAS rejection —
		// now the spent request lands in both telemetry and the failed outcome.
		tele.Error = err.Error()
		a.emitCompactionTelemetry(tele)
		a.emitCompactionFailed(in.trigger, err)
		// tl1837：已花费摘要请求的失败也落侧车（日志轮转不丢失败现场）。
		a.recordCompactMetrics(foldFailedMetricsRecord(in.trigger, err.Error()))
		return CompactionNoop, err
	}
	// tl1695: read the interval BEFORE the success stamp overwrites it — the
	// done line answers "how long since the PREVIOUS compaction" (追着回合跑
	// 的读数半边; -1 = first successful compaction of the session).
	compactionIntervalMin := -1.0
	if last := a.sess.lastCompactionSuccessAt.Load(); last > 0 {
		compactionIntervalMin = time.Since(time.UnixMilli(last)).Minutes()
	}
	// tl1689 补强: the success receipt now carries the compaction telemetry
	// (duration / tokens / cache hit-miss) so the done-line and the hover
	// card read from one source.
	a.emitCompactionSucceeded(in.trigger, len(in.fold), in.summary, "", tele)
	// Done-log cache readout (task 303); 任务 867 adds fold_tokens so the
	// view estimate, summarizer input, and provider count stay separable.
	// tl1640 O2: last_turn_gap_min makes "the conversation went quiet N
	// minutes ago" visible on the same line as the hit=0 readout — no more
	// reconstructing the gap from timestamps (unknown session reads -1).
	// tl1695: split_active names the active-turn split and
	// compaction_interval_min names the cadence — the「压后仍过半」repair
	// surface and the「追着回合跑」judgment read from one line.
	slog.Info("agent: compaction done", "trigger", in.trigger, "stage", "done",
		"folded_messages", len(in.fold), "source_tokens", in.sourceTokens,
		"fold_tokens", tele.FoldTokens,
		"projection_tokens", projTokens,
		"input_tokens", tele.InputTokens, "output_tokens", tele.OutputTokens,
		"cache_hit_tokens", tele.CacheHitTokens, "cache_miss_tokens", tele.CacheMissTokens,
		"request_count", tele.RequestCount,
		"last_turn_gap_min", a.lastTurnGapMinutes(),
		"split_active", tele.SplitActive,
		"compaction_interval_min", compactionIntervalMin)
	// tl1770 日志增强（用户令 20261123 15:19，与 cache 读数同批）: the view's
	// composition enters the log before AND after the fold — the 641K 现场's
	// 『消息』桶 and the gauge's remainder『其他』桶 were un-attributable without
	// it (the events-replace census proved the projection carries ~85%
	// session-context carrier weight, but only per-view logs can show it live).
	// Bucket sums equal the view's own total (LocalOnly / pinned revisions are
	// skipped on both sides); transient_blocks and images are sub-readouts
	// (already inside their host buckets, never additive).
	sb := a.compactionViewBuckets(in.msgs)
	slog.Info("agent: compaction source buckets", append([]any{
		"trigger", in.trigger, "stage", "buckets_pre",
		"session", a.sessionLogRef(), "messages", len(in.msgs), "total_tokens", in.sourceTokens,
	}, sb.fields()...)...)
	pb := a.compactionViewBuckets(spliced)
	slog.Info("agent: compaction projection buckets", append([]any{
		"trigger", in.trigger, "stage", "buckets_post",
		"session", a.sessionLogRef(), "messages", len(spliced), "total_tokens", projTokens,
	}, pb.fields()...)...)
	// tl1837：执行半边（done 读数 + 压前/压后两份 buckets 分项）一次成行落
	// 侧车——案B 的「计划 1384 实际 329」与 672.8K 分项现场从 desktop.log
	// 轮转魔咒下解放，事后可复核、可跨期对照。
	a.recordCompactMetrics(foldDoneMetricsRecord(in.trigger, tele, in.sourceTokens,
		len(in.fold), projTokens, compactionIntervalMin, sb, pb))
	// Task 317 (cache 保全 面 b): the automatic fold attributes its miss the
	// same way the explicit tool does (shared helper, both entry points).
	a.logCompactionMissAttribution(in.trigger, tele, in.sourceTokens)
	return CompactionInstalled, nil
}

// compactionViewBuckets prices one compaction view's composition by message
// class (tl1770 日志增强, 用户令 20261123 15:19). One ruler — messageTokens,
// the calibrated per-message estimate the fold planner and the done line
// already read — so the buckets reconcile against source_tokens /
// projection_tokens on the same line. Classification is first-match:
// system → compaction summary → session-context carrier → role. LocalOnly
// and pinned-context revisions never reach the wire and stay out of every
// bucket. transient_blocks prices the host-injected per-turn blocks riding a
// user turn's head (the 92 条 <context-state> occupancy snapshots the 641K
// 现场 exposed) as a SUBSET readout of bucket_real_user, not an additive
// class; images likewise price inside their owning message's bucket and are
// reported once more as a cross-cut line (Q1 构成表口径: 已含在上面各类内).
type compactionViewBuckets struct {
	system, realUser, carrier, assistant, tool, summary, other int
	transient                                                  int
	images, imageCount                                         int
	messages                                                   int
}

func (a *Agent) compactionViewBuckets(msgs []provider.Message) compactionViewBuckets {
	var b compactionViewBuckets
	for _, m := range msgs {
		if m.LocalOnly || IsPinnedContextRevision(m) {
			continue
		}
		b.messages++
		tok := a.messageTokens(m)
		for _, img := range m.Images {
			b.images += imageEstimateTokens(img)
		}
		b.imageCount += len(m.Images)
		switch {
		case m.Role == provider.RoleSystem:
			b.system += tok
		case isCompactionSummary(m):
			b.summary += tok
		case isSessionContextMessage(m):
			b.carrier += tok
		case m.Role == provider.RoleUser:
			b.realUser += tok
			b.transient += a.transientBlockTokens(m)
		case m.Role == provider.RoleAssistant:
			b.assistant += tok
		case m.Role == provider.RoleTool:
			b.tool += tok
		default:
			// The remainder bucket the gauge's『其他』rounded everything into —
			// with the classes above it should read ~0, and a nonzero value
			// names an unclassified role for the next diagnosis.
			b.other += tok
		}
	}
	return b
}

// transientBlockTokens prices the host-injected transient blocks riding one
// user turn's head (context-state 快照 etc.) on the same calibrated ruler:
// the message against its own stripTransientUserBlocks copy — everything but
// the content differs never, so the difference is exactly the blocks' share.
func (a *Agent) transientBlockTokens(m provider.Message) int {
	if m.Role != provider.RoleUser {
		return 0
	}
	stripped := m
	stripped.Content = StripTransientUserBlocks(m.Content)
	if d := a.messageTokens(m) - a.messageTokens(stripped); d > 0 {
		return d
	}
	return 0
}

// total sums the seven additive buckets: the view's own ruler total, so the
// log line reconciles against source_tokens / projection_tokens.
func (b compactionViewBuckets) total() int {
	return b.system + b.realUser + b.carrier + b.assistant + b.tool + b.summary + b.other
}

// fields renders the bucket readout for one compaction view log line. Bucket
// sums equal total_tokens; transient_blocks ⊆ bucket_real_user_tokens and
// image_tokens ⊆ the host buckets (cross-cut annotations, per the Q1 构成表
// 口径).
func (b compactionViewBuckets) fields() []any {
	return []any{
		"bucket_system_tokens", b.system,
		"bucket_real_user_tokens", b.realUser,
		"bucket_session_context_tokens", b.carrier,
		"bucket_assistant_tokens", b.assistant,
		"bucket_tool_tokens", b.tool,
		"bucket_summary_tokens", b.summary,
		"bucket_other_tokens", b.other,
		"transient_blocks_tokens", b.transient,
		"image_tokens", b.images,
		"image_count", b.imageCount,
	}
}
