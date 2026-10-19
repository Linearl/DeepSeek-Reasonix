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
import type { Translator } from "./i18n";

export type CompactionLiveSource = {
  pending?: boolean;
  startedAt?: number;
  tokens?: number;
  tokensPerSec?: number;
};

// formatElapsedShort mirrors the composer run-strip's fmtElapsed ("40s" /
// "1m 30s") so a compacting session reads like a working one. Deliberately
// not localized — the run-strip's elapsed isn't either.
function formatElapsedShort(ms: number): string {
  const s = Math.floor(Math.max(0, ms) / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
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
): string {
  if (!item.pending) return "";
  const parts: string[] = [];
  if (item.startedAt && item.startedAt > 0) {
    parts.push(formatElapsedShort(now - item.startedAt));
  }
  const tokens = item.tokens ?? 0;
  const tps = item.tokensPerSec ?? 0;
  if (tokens > 0) {
    parts.push(
      tps > 0
        ? t("compaction.liveReadout", { tokens: formatTokens(tokens), tps })
        : `${formatTokens(tokens)} ${t("status.tokens")}`,
    );
  }
  return parts.length > 0 ? ` · ${parts.join(" · ")}` : "";
}
