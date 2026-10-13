// Task 51 UI-2: short-window inbox enqueue dedup. A double-click or an
// impatient second submit of the exact same guidance (same tab, same display,
// same structured fingerprint) within the window is collapsed into one durable
// enqueue instead of three identical inbox items.

const DEFAULT_WINDOW_MS = 5_000;
const MAX_TRACKED = 32;

type DedupEntry = { at: number; itemId?: string };

const recent = new Map<string, DedupEntry>();

function keyFor(tabId: string, display: string, structuredKey: string): string {
  return [tabId, display.trim(), structuredKey].join("||");
}

function prune(now: number, windowMs: number): void {
  for (const [k, v] of recent) {
    if (now - v.at > windowMs) recent.delete(k);
  }
  while (recent.size > MAX_TRACKED) {
    const oldest = recent.keys().next().value;
    if (oldest === undefined) break;
    recent.delete(oldest);
  }
}

export type DedupHit = { itemId?: string; at: number };

/**
 * Returns the prior receipt when the same enqueue happened inside the window,
 * otherwise records this attempt and returns null so the caller proceeds.
 */
export function noteInboxEnqueue(
  tabId: string,
  display: string,
  structuredKey: string,
  opts?: { windowMs?: number; itemId?: string; now?: number },
): DedupHit | null {
  const windowMs = opts?.windowMs ?? DEFAULT_WINDOW_MS;
  const now = opts?.now ?? Date.now();
  prune(now, windowMs);
  const key = keyFor(tabId, display, structuredKey);
  const prior = recent.get(key);
  if (prior && now - prior.at <= windowMs) {
    return { itemId: prior.itemId, at: prior.at };
  }
  recent.set(key, { at: now, itemId: opts?.itemId });
  return null;
}

/** After a successful enqueue, attach the durable item id to the tracked attempt. */
export function attachInboxDedupItemId(tabId: string, display: string, structuredKey: string, itemId: string): void {
  const key = keyFor(tabId, display, structuredKey);
  const entry = recent.get(key);
  if (entry) entry.itemId = itemId;
}

/**
 * 任务 828：明确的未执行拒绝（后端 definite pre-execution rejection，或未拿到
 * 持久 itemId 的失败）撤销本次窗口记录。防双击去重只该覆盖「已成功入队」的
 * 尝试——失败尝试占据窗口会让用户在 5 秒内的有意重试被短路成 duplicate，且该
 * duplicate 缺少 itemId，会被提交层当成功清掉草稿（消息既未入队也已丢失）。
 */
export function forgetInboxEnqueue(tabId: string, display: string, structuredKey: string): void {
  recent.delete(keyFor(tabId, display, structuredKey));
}

/** Test helper: clears the short-window ledger. */
export function resetInboxDedup(): void {
  recent.clear();
}

export type { DedupEntry };
