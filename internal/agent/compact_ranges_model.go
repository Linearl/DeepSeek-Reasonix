// tl1419 (迭代三,实验档): model-driven ranges folds — the flood gate and
// the model-facing entry.
//
// 迭代三 = 叠加,不替代(前置评估定案,用户拍板):模型驱动是新增的触发
// 「发起源」,阈值规则(压力/hard ceiling/overflow 安全网)降级为兜底,经济
// 判据与 tl1396 红线对所有触发源保持硬闸。折叠事务本体在 compact_ranges.go,
// 本文件承载模型触发源专属的两件东西:
//
//  1. 洪水闸(替代冷却,约束①②):全量 compress 工具的 10min 冷却对小段
//     高频通道不适用——一刀切的时间间隔会拦掉正当的连折、又拦不住小收益的
//     折。替代的是两道正交闸:
//     ① 单次收益限:实测节省(计费尺,与 foldEconomics 同尺)低于地板拒装
//     (事务内,below-savings-floor);
//     ② 滑动窗口:窗口内安装数达到上限拒发起(参数化预留,配置活读
//     ModelRangesFoldWindowLive,默认 5min/2 次,调参等 tl1397 观测面出数)。
//  2. 模型入口 RangesFoldCompact:compress_ranges 工具的 host 执行面,开关
//     门控 + 事务回执(rangesFoldReport)译成模型可读结果。
package agent

import (
	"context"
	"fmt"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// rangesModelSavingsFloor is the flood gate's first cut (约束②单次收益限):
// a model-initiated fold whose MEASURED savings (source − projection, the
// same calibrated billing ruler as foldEconomics) lands below this floor is
// abandoned pre-install. Anchored to minFoldTokens so every lane reads one
// economics floor (tl1395 统一计费尺纪律). Var, not const: tests pin it.
var rangesModelSavingsFloor = minFoldTokens

// rangesModelWindowBlock reports the model-readable reject reason when the
// sliding-window half of the flood gate is exhausted, pruning expired
// install timestamps along the way. Caller must hold compactionRunMu (the
// transaction holds it throughout; the tool entry locks before checking).
// Returns "" when another model-initiated fold may proceed.
func (a *Agent) rangesModelWindowBlock() string {
	minutes, maxFolds := config.ModelRangesFoldWindowLive()
	window := time.Duration(minutes) * time.Minute
	now := time.Now().UnixNano()
	kept := make([]int64, 0, len(a.modelRangesFoldTimes))
	for _, ts := range a.modelRangesFoldTimes {
		if now-ts < int64(window) {
			kept = append(kept, ts)
		}
	}
	a.modelRangesFoldTimes = kept
	if len(kept) < maxFolds {
		return ""
	}
	return fmt.Sprintf("frequency-window: %d model-initiated folds already ran within the last %d minutes (limit %d per %d minutes); every fold rewrites the prompt prefix, so let the window slide before folding again — the newest summary already covers this work",
		len(kept), minutes, maxFolds, minutes)
}

// RangesFoldCompact is the host-execution face of the compress_ranges tool
// (tl1419): the model initiates one ranges small-segment fold. The gates:
//
//   - two live switches (model switch + the ranges lane switch — the fold
//     rides the tl1397 transaction, so its own consent gate applies too);
//   - the transaction's own doors, unchanged for every trigger source
//     (segment doors, geometry gate, tl1396 state-③ red line,
//     acceptCheckpointCandidate) plus the model-source-only flood gate
//     (foldEconomics re-check + below-savings-floor + sliding window);
//   - NO cooldown: the whole-range compress tool's interval gate is replaced
//     by the savings floor + window (约束①,冷却不适用).
//
// Rejections return (result with Status "rejected", nil error) carrying the
// model-readable reason and the billing-ruler readings — 失败可见性: the
// model must be able to decide retry-vs-wait from the result alone. Errors
// return only for a canceled context.
func (a *Agent) RangesFoldCompact(ctx context.Context, req tool.RangesFoldRequest) (tool.RangesFoldResult, error) {
	result := tool.RangesFoldResult{Status: "rejected"}
	if len(req.Focus) > maxCompressFocusBytes {
		return result, fmt.Errorf("compress_ranges: focus exceeds %d bytes", maxCompressFocusBytes)
	}
	// 开关双门(活读):模型开关管工具注入与发起权,ranges 通道开关管折叠
	// 事务本身(迭代一的知情同意面)。任一关闭 → 可读拒因,零动作。
	if !config.ModelRangesCompactLive() {
		result.Reason = "model-driven ranges folding is disabled by the experiment switch (experimental_model_ranges_compact)"
		return result, nil
	}
	if !a.rangesCompactEnabled() {
		result.Reason = "the ranges small-fold lane is disabled by its experiment switch (experimental_ranges_compact); model-driven folds ride that lane"
		return result, nil
	}
	// 与压力通道同型的单飞门+串行锁:以下要做真实的分钟级摘要工作。
	a.maintaining.Add(1)
	defer a.endMaintenance()
	a.sess.compactionRunMu.Lock()
	defer a.sess.compactionRunMu.Unlock()
	report, err := a.compactRangesFoldLocked(ctx, CompactionTriggerModelRanges, req.Focus)
	if ctx.Err() != nil {
		return tool.RangesFoldResult{}, ctx.Err()
	}
	if err != nil {
		return tool.RangesFoldResult{}, err
	}
	result.SegmentTokens = report.segmentTokens
	result.SourceTokens = report.sourceTokens
	result.ProjectionTokens = report.projectionTokens
	if report.outcome != CompactionInstalled {
		result.Reason = report.reason // decline/abandon 的模型可读拒因(失败可见性)
		return result, nil
	}
	result.Status = "ok"
	result.FoldIndex = report.foldIndex
	return result, nil
}
