// Run: tsx src/__tests__/project-tree-962-browse-load-more.test.ts
//
// 962 — 干掉项目树普通浏览的「加载更多」分页按钮（对齐上游：仅搜索分页保留）。
//
// 上游对照（upstream/main-v2，ProjectTree.tsx:1750）：上游分页按钮的渲染守卫是
// `query.trim() && nextCursor` —— 分页按钮只出现在搜索结果里；普通浏览靠窗口机制
// 取全。fork 的旧实现在浏览态也渲染「加载更多」（backendPage.nextCursor 存在即
// 显示），用户反馈「意义不明、点击只是重排分组」。处置：
//   ① 按钮守卫加 query.trim()（搜索分页零回归，普通浏览不再渲染）；
//   ② 浏览态列表完整性改由 loadProjectTopics 沿后端游标自动续拉保证（首屏返回
//      后只要还有 nextCursor 就继续拉，直到取全）。
// 测试钉住：续拉判定纯函数三停机条件 + ProjectTree 接线契约（按钮守卫 / 续拉循环 /
// seq 复查 / append 合并语义）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { projectTreeBrowsePageContinues } from "../lib/projectTreeTopic";

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

console.log("\nproject tree 962 browse load-more removal");

// ── 纯函数：浏览态续拉判定 ──────────────────────────────────────────────────
ok(projectTreeBrowsePageContinues({ query: "", requestCursor: "", nextCursor: "c1", itemCount: 50 }) === true,
  "浏览态首页有下一页 → 继续续拉（取全）");
ok(projectTreeBrowsePageContinues({ query: "", requestCursor: "c1", nextCursor: "c2", itemCount: 50 }) === true,
  "浏览态续拉页仍有下一页 → 继续");
ok(projectTreeBrowsePageContinues({ query: "", requestCursor: "c1", nextCursor: undefined, itemCount: 30 }) === false,
  "后端不再给出游标 → 停（已取全）");
ok(projectTreeBrowsePageContinues({ query: "foo", requestCursor: "", nextCursor: "c1", itemCount: 50 }) === false,
  "搜索态不自动续拉 → 分页仍由用户显式点击（零回归）");
ok(projectTreeBrowsePageContinues({ query: "  ", requestCursor: "", nextCursor: "c1", itemCount: 50 }) === true,
  "空白 query trim 后归浏览态 → 续拉（与按钮守卫 query.trim() 口径一致）");
ok(projectTreeBrowsePageContinues({ query: "", requestCursor: "c1", nextCursor: "c1", itemCount: 50 }) === false,
  "游标不前进 → 停（防后端游标异常时无限续拉）");
ok(projectTreeBrowsePageContinues({ query: "", requestCursor: "", nextCursor: "c1", itemCount: 0 }) === false,
  "空页 → 停（防空页循环）");

// ── ProjectTree 接线契约 ────────────────────────────────────────────────────
const here = dirname(fileURLToPath(import.meta.url));
const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");

ok(/query\.trim\(\) && backendPage\?\.nextCursor && \(/.test(treeSource),
  "分页按钮渲染带 query.trim() 守卫（对齐上游 main-v2:1750，普通浏览不再渲染）");
ok(!/\{backendPage\?\.nextCursor && \(/.test(treeSource),
  "不存在无守卫的 backendPage.nextCursor 渲染点（防回退）");
ok(/for \(;;\) \{[\s\S]*?app\.ListProjectTopics\(\{[\s\S]*?cursor: currentCursor,/.test(treeSource),
  "loadProjectTopics 以 currentCursor 循环取页（浏览态自动续拉本体）");
ok(/const continues = projectTreeBrowsePageContinues\(\{[\s\S]*?query,[\s\S]*?requestCursor: currentCursor,[\s\S]*?nextCursor: page\.nextCursor,[\s\S]*?itemCount: page\.items\.length,[\s\S]*?\}\);/.test(treeSource),
  "续拉判定走纯函数（三停机条件集中可测）");
ok(/if \(!continues\) \{[\s\S]*?break;/.test(treeSource),
  "判定为否时写终态并 break（终态含 nextCursor 供搜索分页使用）");
ok(/const pageAppend = append \|\| !firstPage;/.test(treeSource) &&
    /mergeProjectTopicPage\(asArray\(node\.children\), items, pageAppend\)/.test(treeSource),
  "续拉页按 append 合并（首页替换、后续页追加，不重排已绘行）");
ok((treeSource.match(/if \(topicLoadSeqRef\.current\[key\] !== seq\) return;/g) ?? []).length >= 2 &&
    /for \(;;\) \{[\s\S]*?if \(topicLoadSeqRef\.current\[key\] !== seq\) return;/.test(treeSource),
  "每页之间复查 seq（循环体内含一次 + catch 一次，刷新可打断续拉）");
ok(/currentCursor = page\.nextCursor \?\? "";/.test(treeSource) && /firstPage = false;/.test(treeSource),
  "续拉推进游标并翻转 firstPage 标记");

// ── 三语文案不动（loadMore / indexing 保留给搜索分页按钮与 741 指示器） ────
const locale = (name: string) => readFileSync(resolve(here, `../locales/${name}.ts`), "utf8");
ok(locale("zh").includes('"projectTree.loadMore": "加载更多"') && locale("zh").includes('"projectTree.indexing": "正在整理历史"'),
  "zh 文案保留");
ok(locale("zh-TW").includes('"projectTree.loadMore"') && locale("zh-TW").includes('"projectTree.indexing"'),
  "zh-TW 文案保留");
ok(locale("en").includes('"projectTree.loadMore"') && locale("en").includes('"projectTree.indexing"'),
  "en 文案保留");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
