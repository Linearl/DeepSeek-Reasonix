import type { FoldEntry, FoldMap, FoldSegmentState } from "./transcriptRows";

const MAX_SESSIONS = 12;
const MAX_SEGMENTS_PER_SESSION = 512;
const overrides = new Map<string, Map<string, FoldEntry>>();

// 任务 857：会话级「全部收起」意图。collapse-all 只能对当时已存在的 segment
// 写 userOverridden 条目，之后新出现的 segment（新回合、回答文本后的新过程段、
// 回合中途注入的 notice 段）在 FoldMap 无条目，reconcile 按 defaultFoldOpen
// 给默认值——standard 下运行中即展开，用户看到「收起后新思考又自动展开」。
// 这张表把「用户要全部收起」记成会话级状态：新块先按意图收起，直到用户
// 主动展开（点开任一块头 / 全部展开 / 切显示档）才解除。与 overrides 同表
// 生命周期（按 sessionKey LRU，tab 切换/重挂载存活）。
const collapseIntents = new Set<string>();

export function setTranscriptCollapseIntent(sessionKey: string, intent: boolean): void {
  if (intent) {
    collapseIntents.add(sessionKey);
    return;
  }
  collapseIntents.delete(sessionKey);
}

export function readTranscriptCollapseIntent(sessionKey: string): boolean {
  return collapseIntents.has(sessionKey);
}

function sessionMap(sessionKey: string): Map<string, FoldEntry> {
  const existing = overrides.get(sessionKey);
  if (existing) {
    overrides.delete(sessionKey);
    overrides.set(sessionKey, existing);
    return existing;
  }
  const created = new Map<string, FoldEntry>();
  overrides.set(sessionKey, created);
  while (overrides.size > MAX_SESSIONS) {
    const evicted = overrides.keys().next().value!;
    overrides.delete(evicted);
    collapseIntents.delete(evicted);
  }
  return created;
}

export function readTranscriptFoldOverrides(
  sessionKey: string,
  segments: readonly FoldSegmentState[],
): FoldMap {
  const stored = sessionMap(sessionKey);
  const visible = new Set(segments.map((segment) => segment.key));
  return new Map(Array.from(stored).filter(([key]) => visible.has(key)));
}

export function writeTranscriptFoldOverride(sessionKey: string, segmentKey: string, entry: FoldEntry): void {
  const stored = sessionMap(sessionKey);
  stored.delete(segmentKey);
  stored.set(segmentKey, entry);
  while (stored.size > MAX_SEGMENTS_PER_SESSION) stored.delete(stored.keys().next().value!);
}

export function replaceTranscriptFoldOverrides(sessionKey: string, folds: FoldMap): void {
  const stored = sessionMap(sessionKey);
  for (const [key, entry] of folds) {
    if (!entry.userOverridden) continue;
    stored.delete(key);
    stored.set(key, entry);
  }
  while (stored.size > MAX_SEGMENTS_PER_SESSION) stored.delete(stored.keys().next().value!);
}

export function clearTranscriptFoldOverrideSessionForTest(sessionKey: string): void {
  overrides.delete(sessionKey);
  collapseIntents.delete(sessionKey);
}
