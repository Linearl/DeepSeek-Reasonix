// tl1397 (迭代一,实验档): ranges 小段增量压缩。
//
// 现状(全量压缩,compactToProjectionLocked):顶线后把整段折叠区重写成一条
// 滚动摘要——下一请求前缀全断(1396 态①豁免的理论必然),且摘要输入随历史
// 线性涨(「越压越大」的结构性根因)。本文件实现相反的执行形态:
//
//	低于全量触发线的主动小折通道——把「上一摘要边界 → 活跃轮」的未折叠
//	增量折成一条追加式小摘要。保头前缀逐字节不动(1396 态③红线在装前以
//	真实读数把关,破线拒装);摘要输入只含增量段,不随历史长度涨;摘要
//	之间不合并不重写(任何中段重写都会破 95% 线),陈旧载体的回收由前缀
//	膨胀阀让位给既有全量路径(并存,验收③)。
//
// 与用户三约束(20261105,迭代三适用、迭代一架构预留)的对应:
//  1. 频次:本通道由「未折叠增量 ≥ delta 触发下限」驱动,频次天然高于全量
//     压缩;不挂 fold 冷却(冷却对高频小折不适用,约束①)。
//  2. 洪水闸种子:单次收益限的雏形 = delta 下限 + foldEconomics 同型判据 +
//     装门 acceptCheckpointCandidate(投影必须真缩小);滑动窗口留迭代三,
//     观测面已备(约束②)。
//  3. 观测面:每次折叠发「频次(index/间隔)×缓存(hit)」联合 slog + 遥测
//     ranges_* 字段,迭代三定闸直接读数(约束③)。
//
// 触发源解耦:折叠事务本身不绑定触发源(前置评估的「叠加」定案),迭代三
// 换模型驱动触发只改调用方,不动本文件的红线门与几何门。
package agent

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// rangesDeltaTriggerTokens 是 ranges 小折的触发下限:自上次折叠(或基线摘要)
// 以来的未折叠增量计费 token 达到窗口的 2%(下限 512)才发起一次。它同时是
// 频次限制器(每增长这么多才折一次)和单次收益下限(折叠的收益 ≥ 增量的
// 90%,远高于全量路径的 400 地板)。
func rangesDeltaTriggerTokens(window int) int {
	if window <= 0 {
		return 512
	}
	return max(512, window/50)
}

// rangesSegmentMaxTokens 封顶单次小折的输入(验收①的执行面:摘要输入
// ≤ 窗口 1/4,与历史长度无关)。超限说明出现了非常规增长(大跳变/基线
// 刚重建),让位给全量路径处理。
func rangesSegmentMaxTokens(window int) int {
	if window <= 0 {
		return 2048
	}
	return max(2048, window/4)
}

// rangesNoteOutputBudget 封顶小段笔记的输出 token(窗口 0.5%,夹在
// [256, 1024])。笔记尺寸有了硬上界,几何门的占位估计才是可靠上界;
// 运算见 rangesGeometryOK。
func rangesNoteOutputBudget(window int) int {
	if window <= 0 {
		return 256
	}
	return min(max(window/200, 256), 1024)
}

// rangesPrefixBloatShare 是保头前缀的膨胀阀:前缀(基线摘要+累计笔记)
// 超过窗口一半时本通道不再追加,让全量路径回收(验收③的并存循环:
// ranges 相位 → 前缀膨胀 → 全量收缩 → ranges 相位)。
const rangesPrefixBloatShare = 2

// rangesNoteInstructionLead 是小段笔记请求的指令首行。与全量压缩指令
// (compactionInstruction)分开:笔记只描述本段,不复述、不合并已有摘要。
const rangesNoteInstructionLead = "Summarize ONLY the preceding small work segment into a terse running note"

func rangesNoteInstruction(focus string) string {
	instr := rangesNoteInstructionLead + `.
Write 3-8 short bullet lines: what was attempted, key files/paths/IDs touched, commands and their outcomes, errors and their fixes, and the single most concrete next step. Preserve identifiers, paths, and numbers exactly. Do NOT restate or merge any earlier <compaction-summary>; do not invent facts; leave unknowns out. Output only the note. Do not call tools. Do not output reasoning.`
	if strings.TrimSpace(focus) != "" {
		instr += "\n\nAdditional focus for this note (prioritize keeping this):\n" + strings.TrimSpace(focus)
	}
	return instr
}

// rangesSwitchCacheTTL 是 ranges 开关活读缓存的存活期。armed 检查挂在每轮
// 低于触发线的 Prepare 上(采样热路径),配置读取不能每轮走;TTL 换来
// 「改动 ≤30s 生效,重建会话立即可见」的准活读语义。
const rangesSwitchCacheTTL = 30 * time.Second

// rangesCompactEnabled 是开关的缓存活读:缓存过期才重新读一次只读用户配置
// (RangesCompactLive),并发读可能重复刷新但结果幂等。
func (a *Agent) rangesCompactEnabled() bool {
	now := time.Now().UnixNano()
	if now < a.rangesSwitchUntil.Load() {
		return a.rangesSwitchCached.Load()
	}
	on := config.RangesCompactLive()
	a.rangesSwitchCached.Store(on)
	a.rangesSwitchUntil.Store(now + int64(rangesSwitchCacheTTL))
	return on
}

// rangesCompactArmed 报告本次低于触发线的维护检查是否先走 ranges 通道。
// 全部软门槛:任何不满足都安静回落到现状(铁律 2 的关态等价)。
func rangesCompactArmed(a *Agent, policy ContextPreparePolicy, est, fold, hard int) bool {
	if a == nil || a.sess.conversation == nil {
		return false
	}
	// 主动小折只属于自动压力维护:manual/overflow/mustFree 的回收责任仍归
	// 全量路径,触发线之上(est ≥ fold)同样归全量路径管(回收量保证)。
	if policy.Trigger != CompactionTriggerPressure || policy.Force || est >= fold || est >= hard {
		return false
	}
	// 实验开关缓存活读(零 IO 的结构门槛先行,读开关只在门槛全过后发生)。
	return a.rangesCompactEnabled()
}

// rangesFoldBeforeTrigger 在 prepareOnce 的「低于触发线」分支运行一次 ranges
// 小折。返回 ran=true 表示本次检查已被 ranges 通道接管(含折叠后的刷新视图);
// 出错不向采样轮传播——主动通道的失败不配让轮次失败,下轮检查自然重试。
func (m ContextManager) rangesFoldBeforeTrigger(ctx context.Context, policy ContextPreparePolicy, est, fold, hard int) (PreparedContext, error, bool) {
	a := m.agent
	if !rangesCompactArmed(a, policy, est, fold, hard) {
		return PreparedContext{}, nil, false
	}
	// 与压力路径同型的单飞门:以下要做真实的分钟级摘要工作。
	a.maintaining.Add(1)
	defer a.endMaintenance()
	outcome, err := a.compactRangesFoldLocked(ctx, policy.Trigger, policy.Instructions)
	if ctx.Err() != nil {
		return PreparedContext{}, ctx.Err(), true
	}
	if err != nil {
		// 主动通道失败:留痕、不传播、不失败轮次(与压力路径的关键差异)。
		slog.Warn("agent: ranges fold failed (below-trigger lane parks this attempt)",
			"trigger", policy.Trigger, "err", err)
	}
	if outcome == CompactionInstalled {
		slog.Info("agent: ranges fold installed below the fold trigger",
			"trigger", policy.Trigger, "est_before", est, "fold_trigger", fold)
	}
	return m.currentPrepared(), nil, true
}

// rangesSegmentStart 返回未折叠增量的起点:视图里最新一条压缩摘要之后。
// 返回 -1 表示视图没有摘要基线(raw 会话)——基线由全量路径建立。
func rangesSegmentStart(msgs []provider.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if isCompactionSummary(msgs[i]) {
			return i + 1
		}
	}
	return -1
}

// rangesGeometryOK 以 tl1396 态③的口径预检几何:按笔记输出预算的上界占位,
// 组装折后候选视图,实测命中率必须站上绝对线。真实摘要装好后还会用实际
// 读数复测一次(CompactRangesFoldLocked 内)——预估只是省一次摘要请求的
// 门槛,运行时门才是权威。
func (a *Agent) rangesGeometryOK(msgs []provider.Message, segStart, end int, onProjection bool, canonical []provider.Message, stateSnapshot CompactionState) bool {
	window := a.effectiveContextWindow()
	noteRunes := rangesNoteOutputBudget(window)*4 + 256 // token→字节上界 + 包装开销
	candidate := make([]provider.Message, 0, segStart+1+len(msgs)-end)
	candidate = append(candidate, msgs[:segStart]...)
	candidate = append(candidate, formatSummaryMessage(strings.Repeat("s", noteRunes)))
	covEnd, bodySuffix := projectionCoverageForFold(stateSnapshot, msgs, end, onProjection)
	if len(bodySuffix) > 0 {
		candidate = append(candidate, bodySuffix...)
	} else if covEnd >= 0 && covEnd < len(canonical) {
		candidate = append(candidate, canonical[covEnd:]...)
	}
	hit := PrefixCacheHitRate(msgs, candidate)
	return hit >= PartialCompressCacheFloorRatio
}

// compactRangesFoldLocked 执行一次 ranges 小段增量折叠(compactionRunMu 已
// 持有)。任何门槛不过 → (CompactionNoop, nil, 无事件):调用方安静回到
// 现状。事件配对:CompactionStarted 发出后的每个出口都以 Done/Aborted 收口。
//
// 与全量路径(commitSummaryProjection 同一 CAS 安装面)的关键差异:
//   - 投影体 = 保头前缀 + 段内保留消息 + 一条追加笔记;不跑
//     collapseSessionContextChain(任何中段重写都会破 95% 线);
//   - 笔记请求单次封顶输出预算,不做截断翻倍重试(笔记膨胀就该被拒);
//   - 装门 = tl1396 态③红线(实测 ≥95%)+ acceptCheckpointCandidate。
func (a *Agent) compactRangesFoldLocked(ctx context.Context, trigger, instructions string) (CompactionOutcome, error) {
	decline := func(reason string, fields ...any) (CompactionOutcome, error) {
		fields = append([]any{"reason", reason}, fields...)
		slog.Info("agent: ranges fold declined", fields...)
		return CompactionNoop, nil
	}
	canonical, transcriptVersion := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	stateSnapshot := a.sess.compactionState
	startProjectionVersion := a.sess.compactionState.Projection.ProjectionVersion
	startGeneration := a.sess.compactionState.Generation
	a.sess.compactionMu.Unlock()
	msgs, onProjection := a.visibleInputForFold(stateSnapshot, canonical, transcriptVersion)

	segStart := rangesSegmentStart(msgs)
	if segStart < 0 {
		return decline("no-summary-base")
	}
	end := len(msgs)
	if active := a.activeTurnStart(msgs); active >= 0 {
		end = active // 活跃轮保持逐字,折叠到它之前的最后一个已完成交换
	}
	if end <= segStart {
		return decline("empty-segment", "seg_start", segStart, "end", end)
	}
	latestContext := sessionContextCarrierSet(msgs)
	kept, fold, retention := a.partitionFoldForProjectionAt(msgs[segStart:end], segStart, latestContext)
	if len(fold) == 0 {
		return decline("nothing-foldable")
	}
	window := a.effectiveContextWindow()
	segTokens := a.estimatedPromptTokens(fold)
	if segTokens < rangesDeltaTriggerTokens(window) {
		return decline("below-delta-trigger", "seg_tokens", segTokens)
	}
	if segTokens > rangesSegmentMaxTokens(window) {
		return decline("segment-over-cap", "seg_tokens", segTokens, "cap", rangesSegmentMaxTokens(window))
	}
	preservedTokens := a.estimatedPromptTokens(msgs[:segStart])
	if window > 0 && preservedTokens > window/rangesPrefixBloatShare {
		return decline("prefix-bloat-valve", "preserved_tokens", preservedTokens)
	}
	covEnd, bodySuffix := projectionCoverageForFold(stateSnapshot, msgs, end, onProjection)
	if !a.rangesGeometryOK(msgs, segStart, end, onProjection, canonical, stateSnapshot) {
		return decline("geometry-gate", "preserved_tokens", preservedTokens, "seg_tokens", segTokens)
	}

	// 门槛全过:开一段自己的压缩 pass(事件配对从这里开始)。
	pass := a.sess.compactionSessionPass.Add(1)
	a.svc.sink.Emit(event.Event{Kind: event.CompactionStarted, Compaction: event.Compaction{Trigger: trigger, Pass: int(pass)}})
	a.compactionLiveBegin(trigger)
	defer a.compactionLiveStop()
	abort := func(tele *CompactionTelemetry) {
		if tele != nil {
			a.emitCompactionTelemetry(*tele)
		}
		a.emitCompactionAborted(trigger)
	}

	if a.svc.hooks != nil {
		if hookInstr := a.svc.hooks.PreCompact(ctx, trigger); hookInstr != "" {
			if instructions != "" {
				instructions += "\n"
			}
			instructions += hookInstr
		}
	}
	fold, instructions, err := a.interceptCompactionPrepare(ctx, fold, instructions)
	if err != nil {
		slog.Info("agent: ranges fold declined by compaction interceptor", "err", err)
		abort(nil)
		return CompactionNoop, nil
	}
	if len(fold) == 0 {
		abort(nil)
		return CompactionNoop, nil
	}
	segTokens = a.estimatedPromptTokens(fold)

	summary, usage, err := a.rangesNoteSummary(ctx, fold, instructions)
	if err != nil {
		a.alertCredentialExpired(err)
		slog.Warn("agent: ranges note request failed", "trigger", trigger, "seg_tokens", segTokens, "err", err)
		abort(&CompactionTelemetry{
			Trigger: trigger, CacheState: a.CacheState(), Mode: CompactionModeSummarized,
			SourceTokens: a.estimatedVisibleRequestTokens(msgs), FoldTokens: segTokens,
			Spans: 1, SummaryInputMode: SummaryInputNonPrefix, RequestCount: 1,
			Error: err.Error(),
		})
		return CompactionNoop, nil
	}
	summary, err = a.interceptCompactionComplete(ctx, summary)
	if err != nil {
		slog.Warn("agent: ranges note rejected by compaction interceptor", "err", err)
		abort(&CompactionTelemetry{
			Trigger: trigger, CacheState: a.CacheState(), Mode: CompactionModeSummarized,
			SourceTokens: a.estimatedVisibleRequestTokens(msgs), FoldTokens: segTokens,
			Spans: 1, SummaryInputMode: SummaryInputNonPrefix, RequestCount: 1,
			Error: err.Error(),
		})
		return CompactionNoop, nil
	}

	sourceTokens := a.estimatedVisibleRequestTokens(msgs)
	tele := CompactionTelemetry{
		Trigger: trigger, CacheState: a.CacheState(), Mode: CompactionModeSummarized,
		SourceTokens: sourceTokens, FoldTokens: segTokens, Spans: 1,
		SummaryInputMode: SummaryInputNonPrefix, UserTurnsKept: retention.Kept, UserTurnsDropped: retention.Dropped,
		RequestCount: 1,
	}
	if usage != nil {
		tele.InputTokens = usage.PromptTokens
		tele.OutputTokens = usage.CompletionTokens
		tele.CacheHitTokens = usage.CacheHitTokens
		tele.CacheMissTokens = usage.CacheMissTokens
		tele.CacheWriteTokens = usage.CacheWriteTokens
		tele.RequestCount = max(1, usage.RequestCount)
	}

	// 投影体:保头前缀逐字节 + 段内保留消息 + 追加笔记(+退化尾缀)。
	body := make([]provider.Message, 0, segStart+len(kept)+1+len(bodySuffix))
	body = append(body, msgs[:segStart]...)
	body = append(body, kept...)
	body = append(body, formatSummaryMessage(summary))
	if len(bodySuffix) > 0 {
		body = append(body, bodySuffix...)
	}
	projection, pinnedCheckpoint, err := rebasePinnedContextProjection(projectionMessagesPreservingPinnedContext(body), canonical, covEnd)
	if err != nil {
		tele.Error = err.Error()
		abort(&tele)
		return CompactionNoop, nil
	}
	spliced := append(append([]provider.Message(nil), projection...), canonical[covEnd:]...)
	projTokens := a.estimatedVisibleRequestTokens(spliced)
	tele.ProjectionTokens = projTokens
	tele.RangesFold = true
	tele.RangesFoldIndex = int(a.rangesFoldIndex.Load()) + 1
	tele.PreservedTokens = preservedTokens
	rangesHit := PrefixCacheHitRate(msgs, spliced)
	tele.RangesCacheHit = rangesHit

	// tl1396 态③运行时门(验收②的执行面):真实读数不过 95% 绝对线 → 拒装。
	// 拒装后本通道安静收场,会话留在原视图,下一轮检查重估。
	if err := CheckCompressCacheRedline(CompressScenarioPartial, 1.0, rangesHit); err != nil {
		tele.Error = err.Error()
		slog.Warn("agent: ranges fold rejected by the tl1396 state-3 red line",
			"ranges_cache_hit", rangesHit, "preserved_tokens", preservedTokens, "note_bytes", len(summary))
		abort(&tele)
		return CompactionNoop, nil
	}
	if err := a.acceptCheckpointCandidate(trigger, sourceTokens, projTokens); err != nil {
		tele.Error = err.Error()
		abort(&tele)
		return CompactionNoop, nil
	}
	if pinnedCheckpoint {
		slog.Warn("agent: ranges fold kept a pinned-context checkpoint that prevents shrinking", "trigger", trigger)
	}

	inputHash := providerVisibleFingerprint(modelInputMessages(msgs))
	outputHash := providerVisibleFingerprint(modelInputMessages(spliced))
	state, err := a.commitSummaryProjection(summaryProjectionCommit{
		canonical: canonical, fold: fold, projected: projection,
		transcriptVersion: transcriptVersion, projectionVersion: startProjectionVersion,
		generation: startGeneration, activeTurn: a.activeTurnCreatedAt.Load(), trigger: trigger,
		summary: summary, inputHash: inputHash, outputHash: outputHash,
		sourceTokens: sourceTokens, projectionTokens: projTokens, covered: covEnd,
	})
	if err != nil {
		tele.Error = err.Error()
		slog.Info("agent: ranges fold lost the checkpoint race", "err", err)
		abort(&tele)
		return CompactionNoop, nil
	}

	idx := a.rangesFoldIndex.Add(1)
	now := time.Now().UnixNano()
	prev := a.lastRangesFoldAt.Swap(now)
	a.emitCompactionSucceeded(trigger, len(fold), summary, state.LastReceipt.Archive)
	a.emitCompactionTelemetry(tele)
	// 「压缩频次 × 缓存抖动」联合观测(三约束③的观测面):index/间隔是
	// 频次半边,hit 是缓存半边,一行出全。迭代三洪水闸直接在此定参。
	slog.Info("agent: ranges fold observation",
		"ranges_fold_index", idx,
		"since_prev_ranges_fold_ms", max(0, (now-prev)/int64(time.Millisecond)),
		"preserved_tokens", preservedTokens,
		"segment_tokens", segTokens,
		"note_tokens", estimateTextTokens(summary),
		"ranges_cache_hit", rangesHit,
		"proj_tokens", projTokens,
		"source_tokens", sourceTokens,
		"trigger", trigger)
	a.logCompactionMissAttribution(trigger, tele, sourceTokens)
	slog.Info("agent: compaction done", "trigger", trigger, "stage", "done", "ranges", true,
		"folded_messages", len(fold), "source_tokens", sourceTokens, "fold_tokens", tele.FoldTokens,
		"projection_tokens", projTokens, "input_tokens", tele.InputTokens, "output_tokens", tele.OutputTokens,
		"cache_hit_tokens", tele.CacheHitTokens, "cache_miss_tokens", tele.CacheMissTokens)
	return CompactionInstalled, nil
}

// rangesNoteSummary 发一次封顶输出预算的小段笔记请求。输入只有增量段
// (summaryRequest 会补 system 头),不重读基线摘要或更早历史——这是验收①
// 「压缩耗时不随历史线性涨」的执行面。失败路径(含输出截断)一律单次:
// 主动通道的笔记不值得重试预算,膨胀的笔记本来就该被几何门/红线拒收。
func (a *Agent) rangesNoteSummary(ctx context.Context, fold []provider.Message, instructions string) (string, *provider.Usage, error) {
	req := a.summaryRequest(fold, rangesNoteInstruction(instructions))
	budget := rangesNoteOutputBudget(a.effectiveContextWindow())
	req.MaxTokens = budget
	summary, usage, err := a.runSummaryRequestWithBudget(ctx, req, budget)
	a.observeSummaryOutcome(req, usage, err)
	return summary, usage, err
}
