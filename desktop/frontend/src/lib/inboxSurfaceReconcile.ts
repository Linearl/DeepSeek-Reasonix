import { STEER_NOTICE_PREFIX } from "./useController";
import type { InboxSnapshotLike } from "./composerInboxQueue";

// P17: a degraded submit renders only volatile frontend state (the optimistic
// user row plus the ↪ guidance bubble). The durable copy lives in the session
// inbox until the turn consumes it, so any surface replace that draws from a
// history page or transcript-store snapshot taken before consumption — reset on
// tab switch, the Task-232 local-snapshot switch-back, a session-changed
// reload — erases both, and nothing re-renders them afterwards: turn events
// never carry queued inbox items, and a followup-drain merges them into the
// next turn's prompt without per-item events. This reconcile heals the
// transcript surface after every hydrate: queued durable rows come back as
// ↪ bubbles (the reducer dedupes by inboxItemId, and the Steer consume event
// carries the same id, so a later live consumption cannot double-render).

export interface QueuedGuidanceBubble {
  inboxItemId: string;
  text: string;
  /** 任务998：inbox CreatedAt（RFC3339 UTC）→ epoch ms，随 bubble 带给
   *  guidance_bubble action；无值/解析失败保持 undefined。 */
  createdAt?: number;
}

/**
 * States whose item is durable but has not reached the transcript yet.
 * `steer_consumed` and `running` are excluded: the item is being injected (or
 * already sits in a transcript page), so re-rendering it here would duplicate
 * the merged entry once history reloads.
 */
const RECONCILED_INBOX_STATES: ReadonlySet<string> = new Set(["queued", "steer_accepted"]);

export function missingQueuedGuidanceBubbles(
  snapshot: InboxSnapshotLike | null | undefined,
  surfaceItems: ReadonlyArray<{ kind: string; inboxItemId?: string }>,
): QueuedGuidanceBubble[] {
  const covered = new Set<string>();
  for (const item of surfaceItems) {
    if (item.inboxItemId) covered.add(item.inboxItemId);
  }
  const bubbles: QueuedGuidanceBubble[] = [];
  for (const row of snapshot?.items ?? []) {
    if (!row.id || covered.has(row.id)) continue;
    if (!RECONCILED_INBOX_STATES.has(row.state ?? "")) continue;
    const preview = (row.preview ?? "").trim();
    if (!preview) continue;
    const at = row.createdAt ? Date.parse(row.createdAt) : NaN;
    bubbles.push({ inboxItemId: row.id, text: `${STEER_NOTICE_PREFIX}${preview}`, ...(Number.isFinite(at) ? { createdAt: at } : {}) });
  }
  return bubbles;
}

/**
 * 任务811: 把 preview 气泡升级成全文气泡。
 *
 * P17 的 reconcile 气泡只带 `row.preview`（PreviewText 空白折叠 + 120 rune
 * 截断）。对跨会话消息这是致命的：header（含「，发送于 <时间>」）本身就占掉
 * 近百 rune，卡片化后正文只剩时间戳残片，且没有任何入口能看全文（811 症状
 * 2）。queued 项的正文在盘上，ReadInboxItem 换回 displayText；读失败（项已被
 * 消费/删除，或宿主未接线）保留 preview 气泡——降级口径与 shelf 的
 * hydrateEmptyGuidancePreviews 一致。
 */
export async function hydrateQueuedGuidanceBubbles(
  bubbles: readonly QueuedGuidanceBubble[],
  readItem: (id: string) => Promise<{ displayText?: string; rawText?: string; submitText?: string } | undefined>,
): Promise<QueuedGuidanceBubble[]> {
  if (bubbles.length === 0) return [];
  return Promise.all(bubbles.map(async (bubble) => {
    if (!bubble.inboxItemId) return bubble;
    try {
      const env = await readItem(bubble.inboxItemId);
      const text = (env?.displayText || env?.rawText || env?.submitText || "").trim();
      // 保持与 preview 气泡同形状：guidance_bubble action 的 text 是裸文本，
      // 但 reconcile 气泡已带 ↪ 前缀（reducer 会再包一层）——升级只换正文，
      // 不改前缀契约。
      if (!text) return bubble;
      return { ...bubble, text: bubble.text.startsWith(STEER_NOTICE_PREFIX) ? `${STEER_NOTICE_PREFIX}${text}` : text };
    } catch {
      // preview-only 降级：读不到全文就维持原气泡，不吞消息。
      return bubble;
    }
  }));
}
