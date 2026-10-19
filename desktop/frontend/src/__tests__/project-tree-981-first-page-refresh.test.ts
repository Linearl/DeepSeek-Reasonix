// Run: tsx src/__tests__/project-tree-981-first-page-refresh.test.ts
//
// 981 二轮（残余放大面 A，候选1 分场景）：962 的沿游标取全只留给「用户主动
// 展开 / 首次挂载 / 筛选变化」；catalog 事件驱动的背景刷新（400ms flush 后
// 每个受影响的展开文件夹各跑一轮 loadProjectTopics）只取首页。验收判据
// （1125+1145 块，硬条目 1）：空闲态一次 flush 的 ListProjectTopics 调用数
// ≤ 展开文件夹数（修前为 ⌈会话数/limit⌉ × 展开数，本机 83/50 → 2×）。
// 钉住：① full=false 走纯函数恒停（不续拉）；② 事件路径接线 full=false、
// 用户主动路径仍默认取全；③ 首页合并保住已取全文件夹的尾部行（防事件刷新
// 把 83 行收缩到 50 行的可见跳变）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { mergeProjectTopicFirstPage, mergeProjectTopicPage, projectTreeBrowsePageContinues } from "../lib/projectTreeTopic";
import type { ProjectNode } from "../lib/types";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

console.log("\nproject tree 981 round-2 event-driven first-page refresh");

// ── 纯函数：full=false 恒停在首页（事件驱动的背景刷新不进续拉循环） ────────
ok(projectTreeBrowsePageContinues({ query: "", full: false, requestCursor: "", nextCursor: "c1", itemCount: 50 }) === false,
  "full=false 首页仍有下一页也不续拉（背景刷新每文件夹恰好 1 次 ListProjectTopics）");
ok(projectTreeBrowsePageContinues({ query: "", full: false, requestCursor: "c1", nextCursor: "c2", itemCount: 50 }) === false,
  "full=false 续拉页上下文同样恒停");
ok(projectTreeBrowsePageContinues({ query: "", requestCursor: "", nextCursor: "c1", itemCount: 50 }) === true,
  "full 缺省（用户主动路径）仍取全——962 行为零回归");
ok(projectTreeBrowsePageContinues({ query: "foo", full: true, requestCursor: "", nextCursor: "c1", itemCount: 50 }) === false,
  "full=true 搜索态仍停（962 三停机条件保留）");

// 模拟循环决策：83 会话 / limit 50 → 2 页文件夹，事件 flush 走 full=false。
{
  const sessionDirs = 83;
  const limit = 50;
  const pagesPerFolderBefore = Math.ceil(sessionDirs / limit); // 962 全量续拉：循环拉到取全
  const callsPerFlushBefore = 2 * pagesPerFolderBefore; // 2 个展开文件夹 × 2 页
  let callsPerFlushAfter = 0;
  for (const _folder of [0, 1]) {
    // 与 loadProjectTopics 循环同构：唯一续拉判定是 projectTreeBrowsePageContinues。
    let currentCursor = "";
    for (;;) {
      callsPerFlushAfter += 1; // app.ListProjectTopics
      const nextCursor = currentCursor === "" ? "c1" : undefined; // 后端 2 页
      const continues = projectTreeBrowsePageContinues({
        query: "", full: false, requestCursor: currentCursor, nextCursor, itemCount: limit,
      });
      if (!continues) break;
      currentCursor = nextCursor ?? "";
    }
  }
  ok(callsPerFlushAfter <= 2, "一次 flush 的 ListProjectTopics 调用数 ≤ 展开文件夹数（硬条目 1）");
  ok(callsPerFlushAfter < callsPerFlushBefore, "相比 962 全量续拉严格下降");
  process.stdout.write(
    `  READ  2 展开文件夹 × 83 会话/limit50：事件 flush 调用数 ${callsPerFlushBefore} → ${callsPerFlushAfter}（判据 ≤ 展开文件夹数 2）\n`,
  );
}

// ── 纯函数：首页合并保尾部 ─────────────────────────────────────────────────
const row = (key: string, extra: Partial<ProjectNode> = {}): ProjectNode =>
  ({ key, kind: "topic", label: key, children: [], ...extra });
const fullRows = Array.from({ length: 83 }, (_, index) => row(`t${String(index).padStart(2, "0")}`));
const firstPage = fullRows.slice(0, 50).map((node) => ({ ...node, label: `${node.label}-fresh` })); // 新首页（带新数据）

{
  // 首页即全量（无 nextCursor）：整体替换，删除照常生效。
  const resident = [row("a"), row("gone"), row("b")];
  const incoming = [row("a"), row("b")];
  const merged = mergeProjectTopicFirstPage(resident, incoming, {});
  ok(merged.every((node, index) => node === mergeProjectTopicPage(resident, incoming, false)[index]),
    "首页无 nextCursor → 整体替换（一页内文件夹的删除/重排行为与 962 前一致）");
  ok(!merged.some((node) => node.key === "gone"), "首页即全量时删除行被移除");
}
{
  // 首页仍是窗口（nextCursor 存在）：新首页在前，驻留尾部原序保留。
  const merged = mergeProjectTopicFirstPage(fullRows, firstPage, { nextCursor: "c1" });
  ok(merged.length === 83, "83 行驻留文件夹经事件刷新后行数不收缩（防可见跳变）");
  ok(merged.slice(0, 50).every((node, index) => node === firstPage[index]), "首页 50 行以新数据为准");
  ok(merged.slice(50).every((node, index) => node === fullRows[50 + index]), "第 51 行起的驻留尾部原序保留");
}
{
  // 尾部保留下，新会话插入首页照常生效：被挤出首页的原第 50 行（仍是有值的
  // 第 51 名）作为尾部保留，不因窗口刷新消失。
  const inserted = [row("new"), ...firstPage.slice(0, 49)];
  const merged = mergeProjectTopicFirstPage(fullRows, inserted, { nextCursor: "c1" });
  ok(merged[0].key === "new", "新会话出现在首页首位（背景刷新的核心目的）");
  ok(merged.length === 84 && merged.some((node) => node.key === "t49"),
    "插入场景不收缩：被挤出首页的 t49 作为有效尾部保留（83→84 行）");
}
{
  // 离页置顶行（pinned 不在首页）继续保留——与 mergeProjectTopicPage 的 off-page pin 口径一致。
  const pinnedOffPage = row("t70", { pinned: true });
  const resident = fullRows.map((node) => (node.key === "t70" ? pinnedOffPage : node));
  const merged = mergeProjectTopicFirstPage(resident, firstPage, { nextCursor: "c1" });
  ok(merged.filter((node) => node.pinned).length === 1 && merged.some((node) => node === pinnedOffPage),
    "不在首页的置顶行保留（pin 覆盖口径不回退）");
}

// ── ProjectTree 接线契约 ───────────────────────────────────────────────────
const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");

ok(/projectTreeEventAffectsFolder\(project, affected\)\) void loadProjectTopics\(project, false, false\)/.test(source),
  "事件驱动刷新路径传 full=false（只取首页）");
ok(/const continues = projectTreeBrowsePageContinues\(\{[\s\S]*?full,[\s\S]*?requestCursor: currentCursor,/.test(source),
  "续拉判定把 full 传入纯函数（分场景决策集中可测）");
ok(/: full\n\s+\? mergeProjectTopicPage\(asArray\(node\.children\), items, pageAppend\)\n\s+: mergeProjectTopicFirstPage\(asArray\(node\.children\), items, \{ nextCursor: page\.nextCursor \}\)/.test(source),
  "full=false 首页合并走保尾部的 mergeProjectTopicFirstPage");
ok((source.match(/void loadProjectTopics\(project\);/g) ?? []).length === 3,
  "用户主动路径（排序切换 / 防抖重载 / 展开文件夹）仍默认取全（3 处裸调用）");
ok(/void loadProjectTopics\(node, true\)/.test(source),
  "搜索态「加载更多」按钮仍走 append 单页（962 零回归）");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
