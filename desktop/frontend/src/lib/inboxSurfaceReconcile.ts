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
    bubbles.push({ inboxItemId: row.id, text: `${STEER_NOTICE_PREFIX}${preview}` });
  }
  return bubbles;
}
