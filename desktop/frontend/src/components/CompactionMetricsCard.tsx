import { useI18n } from "../lib/i18n";
import { formatTokens } from "../lib/format";
import type { CompactionItem } from "../lib/transcriptRows";

// tl1689: 压缩卡 hover 指标卡（对标 tl1671 思考指标卡 ReasoningMetricsCard：
// 同一条 .tooltip 载体 ReactNode 直渲，不新造弹层，样式复用
// .tooltip__thoughtcard 行族）。数据取自压缩卡既有字段：
//   耗时——durationMs（tl1689 补强 Done 直带，水合卡也可用）优先，
//        回退 startedAt→endedAt 内存戳；
//   平均吞吐——tokensPerSec（seal 帧落卡）；
//   折叠消息——messages；
//   摘要输出——tokens（chars÷4 估算）；
//   释放量——sourceTokens→projectionTokens（tl1689 补强 Done 直带）；
//   摘要输入——summaryHitTokens/summaryMissTokens（tl1689 补强 Done 直带）。
// 缺数据的行整行隐藏（水合历史卡/legacy 后端只显有值行），不造 0 值噪音。
// 挂点：CompactionCard 终局卡头部按钮（终局卡才挂——pending 卡已有内联
// 实时读数，failed 卡的失败事实另有其面）。

type MetricsRow = { label: string; value: string };

/** 行可用性判定（挂点复用）：有任一可显数据行才挂 Tooltip。 */
export function compactionHasMetrics(item: CompactionItem): boolean {
  const timed = (item.durationMs ?? 0) > 0
    || Boolean(item.startedAt && item.endedAt && item.endedAt > item.startedAt);
  const released = (item.sourceTokens ?? 0) > 0 && (item.projectionTokens ?? 0) > 0;
  const input = ((item.summaryHitTokens ?? 0) + (item.summaryMissTokens ?? 0)) > 0;
  return timed
    || (item.tokensPerSec ?? 0) > 0
    || (item.tokens ?? 0) > 0
    || released
    || input;
}

export function CompactionMetricsCard({ item }: { item: CompactionItem }) {
  const { t } = useI18n();
  const rows: MetricsRow[] = [];
  const durationMs = item.durationMs ?? 0;
  if (durationMs > 0) {
    rows.push({
      label: t("compaction.metricsDurationLabel"),
      value: t("compaction.metricsDurationValue", { s: (durationMs / 1000).toFixed(1) }),
    });
  } else if (item.startedAt && item.endedAt && item.endedAt > item.startedAt) {
    rows.push({
      label: t("compaction.metricsDurationLabel"),
      value: t("compaction.metricsDurationValue", { s: ((item.endedAt - item.startedAt) / 1000).toFixed(1) }),
    });
  }
  const tps = item.tokensPerSec ?? 0;
  if (tps > 0) {
    rows.push({ label: t("compaction.metricsTpsLabel"), value: t("compaction.metricsTpsValue", { n: tps }) });
  }
  if (item.messages > 0) {
    rows.push({ label: t("compaction.metricsMessagesLabel"), value: t("compaction.metricsMessagesValue", { n: item.messages }) });
  }
  const sourceTokens = item.sourceTokens ?? 0;
  const projectionTokens = item.projectionTokens ?? 0;
  if (sourceTokens > 0 && projectionTokens > 0) {
    rows.push({
      label: t("compaction.metricsReleasedLabel"),
      value: t("compaction.metricsReleasedValue", { from: formatTokens(sourceTokens), to: formatTokens(projectionTokens) }),
    });
  }
  const hit = item.summaryHitTokens ?? 0;
  const miss = item.summaryMissTokens ?? 0;
  if (hit + miss > 0) {
    rows.push({
      label: t("compaction.metricsInputLabel"),
      value: t("compaction.metricsInputValue", { hit: formatTokens(hit), miss: formatTokens(miss) }),
    });
  }
  const tokens = item.tokens ?? 0;
  if (tokens > 0) {
    rows.push({ label: t("compaction.metricsTokensLabel"), value: t("compaction.metricsTokensValue", { n: formatTokens(tokens) }) });
  }
  if (rows.length === 0) return null;
  return (
    <span className="tooltip__thoughtcard">
      {rows.map((row) => (
        <span className="tooltip__thoughtcard-row" key={row.label}>
          <span className="tooltip__thoughtcard-label">{row.label}</span>
          <span className="tooltip__thoughtcard-value">{row.value}</span>
        </span>
      ))}
    </span>
  );
}
