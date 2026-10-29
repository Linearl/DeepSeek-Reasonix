// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1170-filter-panel-order.test.tsx
//
// tl 1170 验收逐条钉（项目树筛选面板重构，用户已拍板）:
//   ① hover 标题改「筛选与排序」（filterAndSort）：闲置态 tooltip/aria 与面板
//     aria-label 都换键；失去使用点的旧键 projectTree.filterMenu 三语删除（零死键）；
//   ② 面板区块顺序 = 清除筛选 ▸ 排序条件 ▸ 按时间筛选 ▸ 按颜色筛选；
//   ③ 时间/颜色两区默认折叠 + 展开态记忆（localStorage，刷新/重挂载保持）；
//   ④ 既有契约保持：workbench 变体零变化、清除按钮禁用语义、选项即时生效。
//
// 结构以源码断言钉住（同 task 1157 / tl 1200 先例）；持久化逻辑独立成 lib，
// 用 localStorage 桩做行为断言（默认折叠 / 脏数据回退 / 写后读回 = 刷新保持）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { readFilterPanelExpanded, writeFilterPanelExpanded, FILTER_PANEL_EXPANDED_STORAGE_KEY, type FilterPanelSections } from "../lib/projectTreeFilterPanel";

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
const styles = readFileSync(resolve(frontendRoot, "src/styles.css"), "utf8");
const zh = readFileSync(resolve(frontendRoot, "src/locales/zh.ts"), "utf8");
const zhTW = readFileSync(resolve(frontendRoot, "src/locales/zh-TW.ts"), "utf8");
const en = readFileSync(resolve(frontendRoot, "src/locales/en.ts"), "utf8");

// classic 筛选面板切片（同 task1157 的锚点：classicFilterActive → renderProjectHeader）。
const panelStart = treeSource.indexOf("const classicFilterActive = timeFilter !== \"all\"");
const panelEnd = treeSource.indexOf("const renderProjectHeader = (mode:");
ok(panelStart >= 0 && panelEnd > panelStart, "the classic filter panel block is locatable");
const panel = treeSource.slice(panelStart, panelEnd);

function pIdx(needle: string): number {
  const at = panel.indexOf(needle);
  if (at < 0) throw new Error(`anchor not found in filter panel slice: ${needle}`);
  return at;
}

// ── ① hover 标题 =「筛选与排序」 ─────────────────────────────────────────────
console.log("\ntl1170 ① hover title is 筛选与排序 (filterAndSort)");

ok(panel.includes('const classicFilterLabel = classicFilterActive ? classicFilterSummary : t("projectTree.filterAndSort");'),
  "the idle hover/aria title uses the filterAndSort key (active state keeps the summary)");
ok(panel.includes('aria-label={t("projectTree.filterAndSort")}'),
  "the panel menu aria-label uses the same filterAndSort key");
ok(!treeSource.includes('t("projectTree.filterMenu")'),
  "no source reference to the retired filterMenu key remains");
ok(zh.includes('"projectTree.filterAndSort": "筛选与排序"') && en.includes('"projectTree.filterAndSort": "Filter & sort"') && zhTW.includes('"projectTree.filterAndSort": "篩選與排序"'),
  "filterAndSort copy exists verbatim in all three locales");
ok(!zh.includes('"projectTree.filterMenu"') && !en.includes('"projectTree.filterMenu"') && !zhTW.includes('"projectTree.filterMenu"'),
  "the dead filterMenu key is deleted from all three locales (zero dead keys)");

// ── ② 面板顺序：清除 ▸ 排序 ▸ 时间 ▸ 颜色 ───────────────────────────────────
console.log("\ntl1170 ② panel order: clear ▸ sort ▸ time ▸ colour");

const clearAt = pIdx("project-tree__filter-clear");
const sortAt = pIdx('t("projectTree.sortCriteria")');
const timeAt = pIdx('toggleFilterPanelSection("time")');
const colorAt = pIdx('toggleFilterPanelSection("color")');
ok(clearAt < sortAt, "clear filters leads the panel (was the trailing footer action)");
ok(sortAt < timeAt, "sort criteria comes before the time section");
ok(timeAt < colorAt, "time section comes before the colour section");
eq(panel.split("project-tree__filter-clear").length - 1, 1, "exactly ONE clear action in the panel (no duplicate at the old tail position)");
eq(panel.split('toggleFilterPanelSection("time")').length - 1, 1, "exactly ONE time section toggle");
eq(panel.split('toggleFilterPanelSection("color")').length - 1, 1, "exactly ONE colour section toggle");

// ── ③ 时间/颜色两区默认折叠 + 展开态记忆 ────────────────────────────────────
console.log("\ntl1170 ③ time/colour sections: collapsed by default, expansion remembered");

ok(treeSource.includes("useState<FilterPanelSections>(readFilterPanelExpanded)"),
  "the panel expansion state initialises from the persisted store (remount = refresh path)");
ok(treeSource.includes("writeFilterPanelExpanded(next)") && treeSource.includes("toggleFilterPanelSection"),
  "toggling a section writes through to the persisted store");
ok(panel.includes('aria-expanded={filterPanelExpanded.time}') && panel.includes('aria-expanded={filterPanelExpanded.color}'),
  "both section toggles carry aria-expanded from the persisted state");
ok(panel.includes("project-tree__filter-section-toggle") && styles.includes(".project-tree__filter-section-toggle"),
  "the section toggle style exists in styles.css (theme-token contract gated separately)");
ok(styles.includes(".project-tree__filter-badge") && styles.includes(".project-tree__filter-panel") && styles.includes(".project-tree__filter-clear"),
  "the tl1157 panel/badge/clear styles remain (no class silently dropped)");
ok(/ChevronDown size=\{12\}[\s\S]{0,120}ChevronRight size=\{12\}/.test(panel),
  "the chevron reflects the collapsed/expanded state");

type StorageStub = { getItem: (k: string) => string | null; setItem: (k: string, v: string) => void };
function withStorage<T>(store: Record<string, string>, fn: () => T): T {
  const stub: StorageStub = {
    getItem: (k) => (k in store ? store[k] : null),
    setItem: (k, v) => { store[k] = v; },
  };
  (globalThis as typeof globalThis & { localStorage?: StorageStub }).localStorage = stub;
  try { return fn(); } finally { delete (globalThis as typeof globalThis & { localStorage?: StorageStub }).localStorage; }
}

// 默认折叠：无 localStorage / 空存储 / 脏数据都回到 {time:false,color:false}。
eq(readFilterPanelExpanded(), { time: false, color: false }, "no localStorage at all → both sections collapsed (default)");
withStorage({}, () => eq(readFilterPanelExpanded(), { time: false, color: false }, "empty storage → both sections collapsed (default)"));
withStorage({ [FILTER_PANEL_EXPANDED_STORAGE_KEY]: "not-json" }, () => eq(readFilterPanelExpanded(), { time: false, color: false }, "corrupt payload → collapsed, no throw"));
withStorage({ [FILTER_PANEL_EXPANDED_STORAGE_KEY]: JSON.stringify({ time: "yes", color: 1 }) }, () => eq(readFilterPanelExpanded(), { time: false, color: false }, "non-boolean shape → collapsed, no throw"));

// 展开态记忆：写穿后重读 = 重挂载/刷新路径读回同一状态。
withStorage({}, () => {
  writeFilterPanelExpanded({ time: true, color: false });
  eq(readFilterPanelExpanded(), { time: true, color: false }, "expand time → remembered across remount (refresh keeps it)");
});
withStorage({}, () => {
  writeFilterPanelExpanded({ time: false, color: true });
  eq(readFilterPanelExpanded(), { time: false, color: true }, "expand colour only → time stays collapsed after refresh");
});
withStorage({}, () => {
  writeFilterPanelExpanded({ time: true, color: true });
  eq(readFilterPanelExpanded(), { time: true, color: true }, "both expanded → both remembered");
});
withStorage({ [FILTER_PANEL_EXPANDED_STORAGE_KEY]: JSON.stringify({ color: true }) }, () => {
  eq(readFilterPanelExpanded(), { time: false, color: true }, "partial payload fills the missing side with the default (collapsed)");
});

// ── ④ 既有契约保持 ──────────────────────────────────────────────────────────
console.log("\ntl1170 ④ existing contracts intact");

ok(treeSource.includes('renderTimeFilterControl("workbench")') && treeSource.includes('renderColorFilterControl("workbench")'),
  "workbench keeps its separate time and colour filter buttons (zero change)");
ok(panel.includes("disabled={!classicFilterActive}"),
  "clear filters stays disabled while nothing is filtered");
ok(panel.includes("onTimeFilterChange(value); setFilterMenuOpen(false); }}") && panel.includes("setColorFilter((current) =>"),
  "time options close the panel, colour toggles stay open (apply-immediately contract)");
ok(FILTER_PANEL_EXPANDED_STORAGE_KEY.startsWith("projectTree:"),
  "the storage key follows the projectTree: namespace convention");

process.stdout.write(`\ntl1170: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
