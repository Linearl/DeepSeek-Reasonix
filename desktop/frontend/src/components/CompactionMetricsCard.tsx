import { useI18n } from "../lib/i18n";
import { formatTokens } from "../lib/format";
import type { CompactionItem } from "../lib/transcriptRows";

// tl1689: 压缩卡 hover 指标卡（对标 tl1671 思考指标卡 ReasoningMetricsCard：
// 同一条 .tooltip 载体 ReactNode 直渲，不新造弹层，样式复用
// .tooltip__thoughtcard 行族）。数据全部取自压缩卡既有字段：startedAt→
// endedAt（完成耗时，内存戳）、tokensPerSec（seal 帧落卡的平均吞吐）、
// tokens（摘要输出估算，chars÷4 口径）、messages（折叠消息数）。
// 缺数据的行整行隐藏（水合历史卡无内存戳/读数时只显有值行），不造 0 值
// 噪音。挂点：CompactionCard 终局卡头部按钮（终局卡才挂——pending 卡已有
// 内联实时读数，failed 卡的失败事实另有其面）。

export function CompactionMetricsCard({ item }: { item: CompactionItem }) {
  const { t } = useI18n();
  const timed = Boolean(item.startedAt && item.endedAt && item.endedAt > item.startedAt);
  const tps = item.tokensPerSec ?? 0;
  const tokens = item.tokens ?? 0;
  const rows: Array<{ label: string; value: string }> = [];
  if (timed) {
    rows.push({
      label: t("compaction.metricsDurationLabel"),
      value: t("compaction.metricsDurationValue", { s: ((item.endedAt! - item.startedAt!) / 1000).toFixed(1) }),
    });
  }
  if (tps > 0) {
    rows.push({ label: t("compaction.metricsTpsLabel"), value: t("compaction.metricsTpsValue", { n: tps }) });
  }
  if (item.messages > 0) {
    rows.push({ label: t("compaction.metricsMessagesLabel"), value: t("compaction.metricsMessagesValue", { n: item.messages }) });
  }
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
