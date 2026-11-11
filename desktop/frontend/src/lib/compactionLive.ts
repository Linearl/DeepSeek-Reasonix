// 任务 973: 压缩实时读数的统一接入点。任务 556 把后端 CompactionProgress 接到
// 了折叠头（ProcessFoldHeader），但用户视线落点的 pending 压缩卡
// （CompactionCard——截图里那块静态「正在压缩对话…」）没有读数，折叠头之外
// 也没有任何实时反馈。这里把读数格式化收口成一个纯函数：折叠头与压缩卡共用
// 同一份「已 Xs · XX tokens · X t/s」拼装，后续改口径只动一处。
//
// 数据来源（事件链，556 建成）：agent 侧 meter → CompactionProgress →
// reducer compaction_progress → item.tokens / item.tokensPerSec；elapsed 用
// compaction_started 时 reducer 打的 startedAt 内存戳（与工具卡 startedAt 同
// 一约定：仅内存，水合卡无戳也就没有残值）。

import { formatTokens } from "./format";
import { formatDurationLabel } from "./durationFormat";
import type { Translator } from "./i18n";

export type CompactionLiveSource = {
  pending?: boolean;
  startedAt?: number;
  tokens?: number;
  tokensPerSec?: number;
};

// formatElapsedShort mirrors the composer run-strip's fmtElapsed (tl1299 起
// 两者同走 lib/durationFormat："40s" / "1m 30s" / "1h 2m 3s" / "2d 3h"），
// 压缩中的会话与运行中的会话读数同款。
function formatElapsedShort(ms: number, t: Translator): string {
  return formatDurationLabel(Math.floor(Math.max(0, ms) / 1000), t);
}

// tl1559: 首个 token 估计到达前的降级形态。后端 gate 不卡首帧（compact_live.go
// 的首个 emission 绕过节流间隔），数据空窗 = summarizer 请求的 TTFT（排队+首
// token 延迟，可达数秒）——这期间 pending 卡有 startedAt 却没有 tokens，读数
// 只剩裸秒数，用户读作「半截读数」。estimatingWhenEmpty 让调用面（折叠头条、
// 容量卡）在此窗口显式补「估算中…」占位腿；压缩卡（973）不启用，其输出保持
// 逐字节不变（973 的精确串钉不受影响）。水合 pending 卡无戳（内存戳约定），
// 两态都维持空串零残值。
export interface CompactionLiveReadoutOptions {
  estimatingWhenEmpty?: boolean;
}

// compactionLiveReadout renders the " · 40s · 12.3K tokens · 45 tokens/s"
// suffix for a PENDING compaction item, or "" when there is nothing live to
// show (resolved card / no data yet). Elapsed leads because it appears the
// moment compaction_started lands — acceptance ① wants a live number within
// 3s, and the first token estimate can lag behind the provider's queue time.
// tps stays hidden until the backend's 500ms noise floor passes (it sends 0).
export function compactionLiveReadout(
  item: CompactionLiveSource,
  now: number,
  t: Translator,
  opts?: CompactionLiveReadoutOptions,
): string {
  if (!item.pending) return "";
  const parts: string[] = [];
  const hasStamp = Boolean(item.startedAt && item.startedAt > 0);
  if (hasStamp) {
    parts.push(formatElapsedShort(now - item.startedAt!, t));
  }
  const tokens = item.tokens ?? 0;
  const tps = item.tokensPerSec ?? 0;
  if (tokens > 0) {
    parts.push(
      tps > 0
        ? t("compaction.liveReadout", { tokens: formatTokens(tokens), tps })
        : `${formatTokens(tokens)} ${t("status.tokens")}`,
    );
  } else if (opts?.estimatingWhenEmpty && hasStamp) {
    parts.push(t("compaction.liveEstimating"));
  }
  return parts.length > 0 ? ` · ${parts.join(" · ")}` : "";
}
