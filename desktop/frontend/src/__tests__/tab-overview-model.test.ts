// Run: tsx src/__tests__/tab-overview-model.test.ts
// 任务 552:标签页概览——搜索算法(AND 过滤+五档加权+稳定排序)与
// 「最近关闭」栈纯函数(上限 8/同身份去重置顶/重开即剪除)。
// 权重约定对齐 zcode sidePaneTabSearch.ts:title 前缀 120 / title 词前缀 90
// (按 [\s/_.:-]+ 分词)/ title 包含 70 / hint 40 / typeLabel 20 / 兜底 1。

import {
  buildTabSearchFields,
  filterAndRankTabSearchItems,
  normalizeTabSearchQuery,
  normalizeTabSearchText,
} from "../lib/tabOverviewSearch";
import {
  RECENT_CLOSED_TAB_LIMIT,
  pruneRecentClosedTabs,
  pushRecentClosedTab,
  tabOverviewSearchHint,
  tabOverviewTitle,
  tabOverviewTypeLabel,
  tabReopenIdentity,
  type RecentClosedTab,
} from "../lib/tabOverviewModel";
import type { TabMeta } from "../lib/types";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function section(title: string) {
  process.stdout.write(`\n${title}\n`);
}

const tabMeta = (overrides: Partial<TabMeta> = {}): TabMeta => ({
  id: "tab-1",
  scope: "project",
  workspaceRoot: "/repo",
  workspaceName: "repo",
  topicId: "topic-1",
  topicTitle: "Topic",
  label: "model",
  ready: true,
  running: false,
  cancellable: false,
  mode: "normal",
  active: true,
  cwd: "/repo",
  ...overrides,
});

section("查询归一化");
eq(normalizeTabSearchText("  Hello World  "), "hello world", "trim + 小写归一");
eq(normalizeTabSearchQuery("  Foo   bar ").join("|"), "foo|bar", "按空白切词并去掉空词");
eq(normalizeTabSearchQuery("   ").length, 0, "空白查询 = 零词");

section("AND 过滤:所有词都必须命中");
{
  const fields = buildTabSearchFields("deploy script", "run book", "project");
  eq(filterAndRankTabSearchItems([{ searchFields: fields }], ["deploy"]).length, 1, "单词命中保留");
  eq(filterAndRankTabSearchItems([{ searchFields: fields }], ["deploy", "book"]).length, 1, "两词分别在 title/hint 命中 → AND 通过");
  eq(filterAndRankTabSearchItems([{ searchFields: fields }], ["deploy", "missing"]).length, 0, "任一词未命中 → 整条淘汰");
}

section("五档加权:命中档位决定排序");
{
  const mk = (title: string, hint: string, typeLabel: string) => ({ searchFields: buildTabSearchFields(title, hint, typeLabel) });
  const ranked = filterAndRankTabSearchItems(
    [
      mk("foo", "foo", "x"), // title 前缀? "foo".startsWith("foo") ✓ → 120
      mk("bar foo", "x", "x"), // 词前缀(foo 是第二词)→ 90
      mk("xfoo", "x", "x"), // 仅包含 → 70
      mk("bar", "see foo here", "x"), // hint → 40
      mk("bar", "x", "the foo type"), // typeLabel → 20
      mk("bar", "x", "x"), // 兜底 1(AND 已命中? "foo" 不在 all → 0,应被过滤)
    ],
    ["foo"],
  );
  eq(ranked.length, 5, "兜底档只在 AND 已过的情况下给 1 分(all 不含 → 淘汰)");
  eq(ranked[0]?.searchFields.title, "foo", "title 前缀(120)排第一");
  eq(ranked[1]?.searchFields.title, "bar foo", "title 词前缀(90)第二");
  eq(ranked[2]?.searchFields.title, "xfoo", "title 包含(70)第三");
  eq(ranked[3]?.searchFields.hint, "see foo here", "hint(40)第四");
  eq(ranked[4]?.searchFields.typeLabel, "the foo type", "typeLabel(20)第五");
}

section("稳定排序:同分保持原列表顺序");
{
  const mk = (title: string) => ({ searchFields: buildTabSearchFields(title, "", "") });
  const ranked = filterAndRankTabSearchItems([mk("alpha one"), mk("alpha two"), mk("alpha three")], ["alpha"]);
  eq(ranked.map((r) => r.searchFields.title).join("|"), "alpha one|alpha two|alpha three", "同分不打乱原顺序");
}

section("最近关闭栈:入栈去重置顶 + 上限");
{
  const base = tabMeta({ id: "t1", topicId: "topic-1" });
  const second = tabMeta({ id: "t2", topicId: "topic-2" });
  let list: RecentClosedTab[] = [];
  list = pushRecentClosedTab(list, base, 1000);
  list = pushRecentClosedTab(list, second, 2000);
  eq(list.map((e) => e.tab.id).join("|"), "t2|t1", "新关闭的排前面");
  // 同一 tab(身份四元组相同)再关闭 → 去重,只保留最新一条,置顶
  list = pushRecentClosedTab(list, { ...base, id: "t1-reopened-again" }, 3000);
  eq(list.length, 2, "同身份不产生重复条目");
  eq(list[0]?.closedAt, 3000, "重复关闭 = 置顶并刷新关闭时刻");
  for (let i = 0; i < RECENT_CLOSED_TAB_LIMIT + 3; i += 1) {
    list = pushRecentClosedTab(list, tabMeta({ id: `x${i}`, topicId: `topic-x${i}` }), 4000 + i);
  }
  eq(list.length, RECENT_CLOSED_TAB_LIMIT, `栈深不超过上限 ${RECENT_CLOSED_TAB_LIMIT}`);
}

section("最近关闭栈:重开即剪除(按重开四元组判定,不看 tabId)");
{
  const closed: RecentClosedTab[] = [
    { tab: tabMeta({ id: "old-id", topicId: "topic-1", sessionPath: "/s/abc.json" }), closedAt: 1 },
    { tab: tabMeta({ id: "keep", topicId: "topic-9", sessionPath: "/s/zzz.json" }), closedAt: 2 },
  ];
  // 重开后的 tab id 往往已变,剪除按 scope+root+topic+sessionPath 身份
  const reopened = tabMeta({ id: "new-id", topicId: "topic-1", sessionPath: "/s/abc.json" });
  const pruned = pruneRecentClosedTabs(closed, [reopened]);
  eq(pruned.length, 1, "已重开的条目被剪除");
  eq(pruned[0]?.tab.id, "keep", "未重开的条目保留");
}

section("身份四元组与搜索面");
{
  eq(
    tabReopenIdentity(tabMeta({ scope: "project", workspaceRoot: "/r", topicId: "t", sessionPath: "/s.json" })),
    tabReopenIdentity(tabMeta({ id: "other", scope: "project", workspaceRoot: "/r", topicId: "t", sessionPath: "/s.json" })),
    "身份只由 scope+root+topic+sessionPath 决定",
  );
  eq(tabOverviewTitle(tabMeta({ topicTitle: "  " })), "Untitled", "空标题回退 Untitled(project)");
  eq(tabOverviewTitle(tabMeta({ scope: "global", topicTitle: "" })), "Global", "空标题回退 Global(global)");
  eq(tabOverviewTitle(tabMeta({ topicTitle: "部署脚本" })), "部署脚本", "正常标题原样");
  eq(tabOverviewTypeLabel(tabMeta({ scope: "global" }), "项目", "全局"), "全局", "global → 全局");
  eq(tabOverviewTypeLabel(tabMeta({ scope: "project" }), "项目", "全局"), "项目", "project → 项目");
  const hint = tabOverviewSearchHint(tabMeta({
    workspaceName: "repo",
    workspaceRoot: "/home/u/repo",
    sessionPath: "/home/u/repo/.reasonix/sessions/sess_20261007_abc.json",
    topicId: "topic-77",
  }));
  ok(hint.includes("sess_20261007_abc.json"), "hint 含 sessionPath(排查时手里往往只有会话 id)");
  ok(hint.includes("topic-77"), "hint 含 topicId");
  ok(hint.includes("repo"), "hint 含工作区标识");
}

section("源码锚:组件接线防回归");
{
  const testDir = dirname(fileURLToPath(import.meta.url));
  const panelSource = readFileSync(resolve(testDir, "../components/TabOverviewPanel.tsx"), "utf8");
  const chromeSource = readFileSync(resolve(testDir, "../components/AppChrome.tsx"), "utf8");
  const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
  // 行内 × 防误触(对齐 zcode):pointerdown 与 click 各断一次冒泡,
  // 行级「切换」只由行自身 onClick 触发。
  ok(/onPointerDown=\{\(event\) => \{\s*event\.preventDefault\(\);\s*event\.stopPropagation\(\);/.test(panelSource),
    "行内 × 的 pointerdown 先行吞掉(防误触切换)");
  ok(/onClick=\{\(event\) => \{\s*event\.preventDefault\(\);\s*event\.stopPropagation\(\);\s*row\.onClose\?\.\(\);/.test(panelSource),
    "行内 × 的 click 断冒泡后只做关闭");
  ok(panelSource.includes("60_000"), "相对时间刷新间隔 60s");
  // 刷新 interval 只在面板打开时挂载:effect 以 open 为门
  ok(/if \(!open\) return;\s*setNow\(Date\.now\(\)\)/.test(panelSource), "interval 仅面板打开时运行");
  // 三套 chrome 各一处 {tabOverview} 插槽,且每一处都在同分支的放大镜按钮之前
  eq((chromeSource.match(/\{tabOverview\}/g) ?? []).length, 3, "经典/darwin/workbench 三处入口");
  ok(
    chromeSource.indexOf("{tabOverview}") < chromeSource.indexOf("app-chrome__workbench-search"),
    "workbench:概览入口在放大镜(workbench-search)之前",
  );
  ok(
    chromeSource.indexOf("{tabOverview}") < chromeSource.indexOf("app-chrome__command"),
    "tools:概览入口在放大镜(app-chrome__command)之前",
  );
  ok(appSource.includes("pushRecentClosedTab(current, closedTabSnapshot, Date.now())"), "关闭成功后入栈(finishTabClose 单一漏斗)");
  ok(appSource.includes('kind: "topic", scope, workspaceRoot, topicId, sessionPath'), "重开走既有 topic 导航");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
