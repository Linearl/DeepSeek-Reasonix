// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/search-history-lru.test.ts
//
// tl1282 验收逐条钉（检索历史：设置面板搜索 + 侧栏全局搜索共用一份，至多 10 条）:
//   ① 检索词记录 + 重复词置顶去重（大小写不敏感去重，保留最新写法）；
//   ② 上限 10 条 LRU——超出从队尾淘汰，新词插队首；
//   ③ 重启后仍在——写穿 localStorage 后「重新读取」（模拟新进程挂载）读回同列表；
//   ④ 持久化健壮性——无存储/脏 JSON/形状错误全部静默回退，不炸 UI。

import {
  SEARCH_HISTORY_LIMIT,
  SEARCH_HISTORY_STORAGE_KEY,
  clearSearchHistory,
  normalizeSearchHistory,
  readSearchHistory,
  recordSearchTerm,
  removeSearchTerm,
  withRecordedTerm,
  writeSearchHistory,
} from "../lib/searchHistory";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

type StorageStub = { getItem: (k: string) => string | null; setItem: (k: string, v: string) => void; removeItem?: (k: string) => void };
function withStorage<T>(store: Record<string, string>, fn: () => T): T {
  const stub: StorageStub = {
    getItem: (k) => (k in store ? store[k] : null),
    setItem: (k, v) => { store[k] = v; },
  };
  (globalThis as typeof globalThis & { localStorage?: StorageStub }).localStorage = stub;
  try { return fn(); } finally { delete (globalThis as typeof globalThis & { localStorage?: StorageStub }).localStorage; }
}

// ── 纯核 withRecordedTerm ───────────────────────────────────────────────────
console.log("\ntl1282 ① record + dedupe-to-top");

eq(withRecordedTerm([], "mcp"), ["mcp"], "empty history → term recorded");
eq(withRecordedTerm(["mcp", "memory"], "mcp"), ["mcp", "memory"], "repeat term moves to front (no duplicate)");
eq(withRecordedTerm(["mcp", "memory"], "MCP"), ["MCP", "memory"], "repeat is case-insensitive; latest spelling wins and leads");
eq(withRecordedTerm(["mcp"], "  mcp  "), ["mcp"], "whitespace-only padding is trimmed before matching");
eq(withRecordedTerm(["mcp"], "   "), ["mcp"], "blank term is a no-op (nothing recorded)");
eq(withRecordedTerm(["a", "b", "a"], "c"), ["c", "a", "b"], "older duplicate inside history collapses to one entry (first occurrence wins)");

// ── ② 上限 10 条 LRU ────────────────────────────────────────────────────────
console.log("\ntl1282 ② cap at 10, LRU eviction from the tail");

eq(SEARCH_HISTORY_LIMIT, 10, "the cap is exactly 10");
const eleven = ["t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"];
eq(withRecordedTerm(eleven, "t11").length, 10, "recording an 11th term keeps the list at 10");
eq(withRecordedTerm(eleven, "t11"), ["t11", "t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9"], "the OLDEST tail entry (t10) is evicted, newest leads");
eq(withRecordedTerm(eleven, "t1"), ["t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"], "re-recording an existing term evicts nothing (still 10, no dup)");
// LRU 晋升不改列表长度：先滤除旧位再置顶，长度守恒（淘汰只发生在插入新词时）。
eq(withRecordedTerm(["t11", "t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9"], "t2"),
  ["t2", "t11", "t1", "t3", "t4", "t5", "t6", "t7", "t8", "t9"], "LRU: re-recording t2 promotes it to front, list length unchanged");

// ── ③④ 持久化通道（localStorage 桩）────────────────────────────────────────
console.log("\ntl1282 ③④ persistence: survives restart, corrupt payloads fall back");

eq(readSearchHistory(), [], "no localStorage at all → empty history, no throw");
withStorage({}, () => eq(readSearchHistory(), [], "empty store → empty history"));
withStorage({ [SEARCH_HISTORY_STORAGE_KEY]: "not-json" }, () => eq(readSearchHistory(), [], "corrupt JSON → empty history, no throw"));
withStorage({ [SEARCH_HISTORY_STORAGE_KEY]: JSON.stringify({ x: 1 }) }, () => eq(readSearchHistory(), [], "non-array payload → empty history"));
withStorage({ [SEARCH_HISTORY_STORAGE_KEY]: JSON.stringify(["mcp", 42, "", "  ", "memory", "mcp"]) },
  () => eq(readSearchHistory(), ["mcp", "memory"], "dirty entries (non-string/blank/dup) are dropped on read"));
withStorage({ [SEARCH_HISTORY_STORAGE_KEY]: JSON.stringify(["a", "b", ...eleven]) },
  () => eq(readSearchHistory().length, 10, "over-cap stored payload is truncated to 10 on read"));

// 重启存活断言：写穿 → 同一存储里出现序列化列表 → 「新进程」读回一致。
{
  const store: Record<string, string> = {};
  withStorage(store, () => {
    recordSearchTerm("project tree");
    recordSearchTerm("mcp");
    recordSearchTerm("project tree"); // 置顶去重
    const stored = JSON.parse(store[SEARCH_HISTORY_STORAGE_KEY] ?? "[]") as unknown;
    eq(stored, ["project tree", "mcp"], "write-through serialises the list to the storage key");
    eq(readSearchHistory(), ["project tree", "mcp"], "fresh read (restart path) returns the same list");
  });
}
withStorage({}, () => {
  writeSearchHistory(["keep", "me"]);
  eq(readSearchHistory(), ["keep", "me"], "writeSearchHistory round-trips through storage (restart keeps it)");
});
// localStorage 抛错（配额/隐私模式）：记录不炸，重启自然回退为无历史。
{
  const throwing: StorageStub = { getItem: () => null, setItem: () => { throw new Error("quota"); } };
  (globalThis as typeof globalThis & { localStorage?: StorageStub }).localStorage = throwing;
  let threw = false;
  try { recordSearchTerm("mcp"); } catch { threw = true; }
  ok(!threw, "recordSearchTerm survives a throwing storage (best-effort persistence)");
  (globalThis as typeof globalThis & { localStorage?: StorageStub }).localStorage = undefined;
}

// 单条删除 / 一键清空。
withStorage({}, () => {
  recordSearchTerm("mcp");
  recordSearchTerm("memory");
  eq(removeSearchTerm("memory"), ["mcp"], "single-entry removal leaves the rest");
  eq(removeSearchTerm("not-there"), ["mcp"], "removing an absent term is a no-op");
  eq(clearSearchHistory(), [], "clear-all empties the list");
  eq(readSearchHistory(), [], "clear-all persists (restart reads empty)");
});

// 存储键命名空间惯例（与 projectTree:* / rightDockTabs:* 同风格）。
ok(SEARCH_HISTORY_STORAGE_KEY.includes(":"), "the storage key follows the area-namespace convention");

process.stdout.write(`\ntl1282 search-history-lru: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
