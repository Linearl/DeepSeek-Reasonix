// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/search-history-tree-wiring.test.ts
//
// tl1282 验收（侧栏「搜索项目或会话」挂检索历史）——结构以源码断言钉住
// （同 tl1170 / task1157 先例：ProjectTree 全树渲染在 JSDOM 里过重，且共享
// 下拉组件与 LRU 存储的行为已由 search-history-settings-wiring / lru 测试
// 真渲染/真桩覆盖，这里钉接线不重复行为）:
//   ④ 挂现有检索框不新增入口——下拉渲染在既有 project-tree__search label 内，
//     聚焦且无查询词才出现，默认视图零改动；
//   ①② 与设置面板共用同一存储模块（同一 LRU、同一 localStorage 键）；
//   ③ 持久化由 lib 层断言覆盖（lru 测试），此处钉「两处同源」事实。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

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

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "../..");
const treeSource = readFileSync(resolve(frontendRoot, "src/components/ProjectTree.tsx"), "utf8");
const settingsNavSource = readFileSync(resolve(frontendRoot, "src/components/SettingsNavigation.tsx"), "utf8");
const menuSource = readFileSync(resolve(frontendRoot, "src/components/SearchHistoryMenu.tsx"), "utf8");
const libSource = readFileSync(resolve(frontendRoot, "src/lib/searchHistory.ts"), "utf8");
const styles = readFileSync(resolve(frontendRoot, "src/styles.css"), "utf8");
const zh = readFileSync(resolve(frontendRoot, "src/locales/zh.ts"), "utf8");
const zhTW = readFileSync(resolve(frontendRoot, "src/locales/zh-TW.ts"), "utf8");
const en = readFileSync(resolve(frontendRoot, "src/locales/en.ts"), "utf8");

// 输入框切片（searchVisible 渲染块 → label 闭合）。
const labelStart = treeSource.indexOf('<label className="project-tree__search">');
const labelEnd = treeSource.indexOf("</label>", labelStart);
ok(labelStart >= 0 && labelEnd > labelStart, "the existing project-tree search label is locatable");
const label = treeSource.slice(labelStart, labelEnd);

console.log("\ntl1282 tree ④ hooks the existing search box, no new entry point");

ok(label.includes("<Search size={14} />") && label.includes('placeholder={t("projectTree.searchPlaceholder")}'),
  "the existing search icon/placeholder remain untouched inside the label");
ok(label.includes("onFocus={() => setSearchHistoryOpen(true)}") && label.includes("onBlur={() => setSearchHistoryOpen(false)}"),
  "the existing input gains focus/blur dropdown toggles");
ok(label.includes("<SearchHistoryMenu"), "the history dropdown renders INSIDE the existing search label (no new entry)");
ok(!treeSource.includes("search-history-trigger") && !treeSource.includes("searchHistoryOpen && <button"),
  "no new trigger button is added to the tree chrome");

console.log("\ntl1282 tree ①② same shared LRU store as the settings panel");

ok(treeSource.includes('from "../lib/searchHistory"') && settingsNavSource.includes('from "../lib/searchHistory"'),
  "both hosts import the SAME searchHistory lib module");
eq(libSource.match(/SEARCH_HISTORY_STORAGE_KEY = "([^"]+)"/)?.[1], "search:history",
  "the lib persists through one storage key (both hosts converge)");
ok(treeSource.includes("useState<string[]>(readSearchHistory)"),
  "the tree history state initialises from the persisted store (remount = restart path)");
ok(treeSource.includes("const searchHistoryVisible = searchHistoryOpen && query.trim() === \"\" && searchHistory.length > 0;"),
  "visibility is gated on focus AND empty query AND non-empty history (default view zero change)");
ok(treeSource.includes("setSearchHistory(recordSearchTerm(term))") && settingsNavSource.includes("setHistory(recordSearchTerm(term))"),
  "both hosts promote a selected term through the same record path");
ok(treeSource.includes("onRemove={(term) => setSearchHistory(removeSearchTerm(term))}") && treeSource.includes("setSearchHistory(clearSearchHistory())"),
  "single-entry delete and clear-all are wired in the tree");
ok(treeSource.includes('if (event.key === "Enter") commitSearchQuery();') && treeSource.includes('if (event.key === "Escape") setSearchHistoryOpen(false);'),
  "Enter commits the query, Escape closes the dropdown");

console.log("\ntl1282 tree css/locales contract");

ok(/\.settings-center__search,\s*\.project-tree__search\s*\{\s*position: relative;/.test(styles),
  "both host labels are absolute anchors (position: relative only, no layout shift)");
ok(styles.includes(".search-history {") && styles.includes(".search-history__term") && styles.includes(".search-history__remove") && styles.includes(".search-history__clear"),
  "the shared dropdown styles exist in styles.css");
ok(styles.includes("z-index: var(--z-menu);"),
  "the dropdown uses the --z-menu token (no raw z-index values)");
ok(zh.includes('"searchHistory.title": "最近搜索"') && zh.includes('"searchHistory.clear": "清空"') && zh.includes('"searchHistory.remove": "删除该条历史"'),
  "zh locale carries the searchHistory keys");
ok(en.includes('"searchHistory.title": "Recent searches"') && zhTW.includes('"searchHistory.title": "最近搜尋"'),
  "en/zh-TW locales carry the searchHistory keys");
ok(menuSource.includes("onMouseDown={(event) => event.preventDefault()}"),
  "the dropdown suppresses mousedown default so clicks do not blur the input first");

process.stdout.write(`\ntl1282 tree wiring: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
