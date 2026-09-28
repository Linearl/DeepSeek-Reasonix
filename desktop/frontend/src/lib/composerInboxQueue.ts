import type { PendingGuidance } from "../components/ComposerGuidanceShelf";
import { asArray } from "./array";
import { STEER_NOTICE_PREFIX } from "./useController";

export type InboxSnapshotLike = {
  paused?: boolean;
  recovered?: boolean;
  recoveredCount?: number;
  revision?: number;
  sessionPath?: string;
  items?: Array<{
    id: string;
    preview?: string;
    state?: string;
    intent?: string;
    source?: string;
  }>;
};

export function inboxScopeKey(sessionPath?: string, workspaceScopeKey?: string): string {
  return (sessionPath || "").trim() || workspaceScopeKey || "";
}

export function inboxSnapshotBelongsToScope(snapshotPath: string | undefined, scopeKey: string): boolean {
  const path = (snapshotPath || "").trim();
  if (!path || !scopeKey) return true;
  if (scopeKey === path) return true;
  return scopeKey.split("\u0000").includes(path);
}

// Task 237 (次修①): the preview key arrives as the caller's item array, not a
// joined string — splitting on "\n" used to shred any message that itself
// contains newlines back into fake queue rows. Each entry stays whole.
export function localGuidanceFallback(previewItems: readonly string[]): PendingGuidance[] {
  return previewItems
    .filter((text) => text.trim() !== "")
    .map((text, i) => ({ id: `local-${i}`, text, submitText: text }));
}

export function guidanceFromInboxSnapshot(snap: InboxSnapshotLike | null | undefined): PendingGuidance[] {
  const visible = asArray(snap?.items).filter((it) => it.state !== "steer_consumed" && it.state !== "running");
  return visible.map((it) => ({
    id: it.id,
    text: it.preview || "",
    submitText: "",
    state: it.state,
    intent: it.intent,
    source: it.source,
    paused: Boolean(snap?.paused),
    recoveredCount: snap?.paused && snap?.recovered
      ? visible.length
      : undefined,
  }));
}

export async function hydrateEmptyGuidancePreviews(
  items: PendingGuidance[],
  readItem: (id: string) => Promise<{ displayText?: string; submitText?: string; rawText?: string }>,
): Promise<PendingGuidance[]> {
  const missing = items.filter((item) => !item.text.trim() && !item.id.startsWith("local-"));
  if (missing.length === 0) return items;
  await Promise.all(missing.map(async (item) => {
    try {
      const env = await readItem(item.id);
      const text = (env.displayText || env.submitText || env.rawText || "").trim();
      if (text) item.text = text;
    } catch {
      // Preview-only snapshot remains if the body cannot be read.
    }
  }));
  return items;
}

export function mergeGuidanceSnapshot(durable: PendingGuidance[], fallback: PendingGuidance[]): PendingGuidance[] {
  return durable.length > 0 ? durable : fallback;
}

// Task 258: rows whose receipt already landed (steer accepted, or a follow-up
// queued for later dispatch) stay retired across snapshot refreshes — the
// backend still reports them as queued/steer_accepted, and re-showing the row
// right after the click is the reported "flash, then the row is back" no-op.
export function retireSubmittedGuidance(items: PendingGuidance[], submitted: ReadonlySet<string>): PendingGuidance[] {
  return submitted.size === 0 ? items : items.filter((item) => !submitted.has(item.id));
}

// Task 336: ids of every guidance item whose injection receipt already landed
// in the transcript (the durable ↪ steer notice, prefix is the only marker —
// see STEER_NOTICE_PREFIX). The set is derived from the live items on every
// change, so a tab round-trip re-filters the inbox snapshot instead of
// trusting one keyed one-shot effect: host guidance and collab replies are
// injected without ever passing through the shelf's submitted set, and the
// backend still reports them until its own acknowledgement lands.
export function consumedGuidanceIdsFromItems(
  items: ReadonlyArray<{ kind: string; text?: string; inboxItemId?: string }>,
): Set<string> {
  const ids = new Set<string>();
  for (const item of items) {
    if (item.kind === "notice" && (item.text ?? "").startsWith(STEER_NOTICE_PREFIX) && item.inboxItemId) {
      ids.add(item.inboxItemId);
    }
  }
  return ids;
}

// Task 153: the manual "merge next" affordance. Joining is a plain double
// newline — no separator prose, no rephrasing — so both bodies stay verbatim
// and the merged row keeps the first entry's id (its durable row is the one
// that gets updated; the next entry's row is removed by the caller).
export function mergeGuidanceTexts(current: string, next: string): string {
  return `${current.trimEnd()}\n\n${next.trimStart()}`;
}

export function mergeGuidanceWithNext<T extends { id: string; text: string; submitText: string }>(
  items: T[],
  id: string,
): T[] {
  const index = items.findIndex((item) => item.id === id);
  if (index < 0 || index + 1 >= items.length) return items;
  const current = items[index];
  const next = items[index + 1];
  const merged: T = {
    ...current,
    text: mergeGuidanceTexts(current.text, next.text),
    submitText: mergeGuidanceTexts(current.submitText || current.text, next.submitText || next.text),
  };
  return [...items.slice(0, index), merged, ...items.slice(index + 2)];
}
