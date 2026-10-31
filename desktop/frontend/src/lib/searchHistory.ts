// tl1282: 检索历史——设置面板搜索框与侧栏「搜索项目或会话」共用一份最近检索词
// （LRU：重复词置顶去重，上限 10 条，最旧的先淘汰）。
//
// 持久化通道选型：localStorage 而非 desktop config（settings 偏好）通道。惯例
// 与 dockTabs / projectTreeFilterPanel 同源——纯前端偏好 live 生效、无重启需求，
// 项目里已有 38 个 lib 模块以 localStorage 跨重启记忆状态（webview profile 持久
// 化，composerDraftPersistence 亦以此为跨重启通道）；config 通道每次写都要过 Go
// applyConfigOnly 落全量配置，为一个检索词不值得。
// 裸 localStorage + try/catch（不可用时静默退化为内存外无持久化），读取端做形状
// 校验（脏数据不炸 UI，非字符串/空串/超限条目一律丢弃）。
// 去重键 = trim 后 toLocaleLowerCase（两处检索框的过滤语义都是大小写不敏感），
// 命中已有条目时保留最新一次的原样写法并移到队首。

export const SEARCH_HISTORY_STORAGE_KEY = "search:history";
export const SEARCH_HISTORY_LIMIT = 10;

/** 归一化去重键：trim + 小写（与两处检索框的过滤匹配语义一致）。 */
function dedupeKey(term: string): string {
  return term.toLocaleLowerCase();
}

/** 读取端形状校验：非数组/脏条目丢弃，trim 空串过滤，按归一化键去重，截到上限。 */
export function normalizeSearchHistory(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of value) {
    if (typeof raw !== "string") continue;
    const term = raw.trim();
    if (!term) continue;
    const key = dedupeKey(term);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(term);
    if (out.length >= SEARCH_HISTORY_LIMIT) break;
  }
  return out;
}

export function readSearchHistory(): string[] {
  try {
    const raw = localStorage.getItem(SEARCH_HISTORY_STORAGE_KEY);
    if (!raw) return [];
    return normalizeSearchHistory(JSON.parse(raw));
  } catch {
    // localStorage unavailable / corrupt payload — degrade to empty history.
    return [];
  }
}

export function writeSearchHistory(entries: string[]): void {
  try {
    localStorage.setItem(SEARCH_HISTORY_STORAGE_KEY, JSON.stringify(normalizeSearchHistory(entries)));
  } catch { /* ignore: persistence is best-effort */ }
}

/**
 * 纯核：把一个词并入既有历史——重复词（大小写不敏感）原样置顶，其余顺序不变，
 * 超出上限从队尾淘汰。空白词原样返回既有历史。
 */
export function withRecordedTerm(existing: string[], term: string): string[] {
  const normalized = typeof term === "string" ? term.trim() : "";
  if (!normalized) return normalizeSearchHistory(existing);
  const key = dedupeKey(normalized);
  const rest = normalizeSearchHistory(existing).filter((entry) => dedupeKey(entry) !== key);
  return [normalized, ...rest].slice(0, SEARCH_HISTORY_LIMIT);
}

/** 记录一次检索：读 → 并入 → 写穿 → 返回最新列表（调用方可直接 setState）。 */
export function recordSearchTerm(term: string): string[] {
  const next = withRecordedTerm(readSearchHistory(), term);
  writeSearchHistory(next);
  return next;
}

/** 删除单条（按条目原文精确匹配）：读 → 删 → 写穿 → 返回最新列表。 */
export function removeSearchTerm(term: string): string[] {
  const next = readSearchHistory().filter((entry) => entry !== term);
  writeSearchHistory(next);
  return next;
}

/** 一键清空：写穿空列表，返回空数组。 */
export function clearSearchHistory(): string[] {
  writeSearchHistory([]);
  return [];
}
