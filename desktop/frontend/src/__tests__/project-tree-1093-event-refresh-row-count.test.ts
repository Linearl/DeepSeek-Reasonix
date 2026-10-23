// Run: tsx src/__tests__/project-tree-1093-event-refresh-row-count.test.ts
//
// 任务 1093：侧栏分组计数与渲染行数不一致——reasonix-for-ai 分组计数显示
// 17、只渲染 5 行；折叠再展开（full=true 重载）才全量。数据层（分组
// topicIds）正确，问题在事件刷新（full=false，只取首页）合并路径：目录扫描
// 分批提交期间首页可以返回 0 行且 complete=false（不是「列表为空」的权威
// 信号），旧合并按「空页即全量」整体替换，把组内已渲染行整组抹掉——分组
// 计数不变（roster 渲染），行数却塌到 0/个位数。本测试钉住两条：
// ① 空的不完全首页保留驻留行（组内行数=计数不塌）；
// ② 空的完全首页仍整体替换（用户清空文件夹/删除全部话题照常生效）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { mergeProjectTopicFirstPage } from "../lib/projectTreeTopic";
import type { ProjectNode } from "../lib/types";

const here = dirname(fileURLToPath(import.meta.url));

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

console.log("\nproject tree 1093 event-refresh row count");

const topicRow = (topicId: string, label = topicId): ProjectNode =>
  ({ key: "global_topic_" + topicId, kind: "global_topic", label, topicId, children: [] });

// The reasonix-for-ai group's 17 members, all resident after a full reload.
const resident = Array.from({ length: 17 }, (_, index) => topicRow(`t${String(index).padStart(2, "0")}`));

// ── 空首页分支 ────────────────────────────────────────────────────────────
{
  // 不完全空首页（目录分批提交中，complete=false）：整组驻留行保留，行数不塌。
  const merged = mergeProjectTopicFirstPage(resident, [], { nextCursor: "", complete: false });
  ok(merged.length === 17 && merged.every((node, index) => node === resident[index]),
    "空的不完全首页保留全部驻留行（分组计数 17 / 行数仍 17，不整组消失）");
}
{
  // 完全空首页（用户真的清空了文件夹，complete=true）：整体替换语义不变，行清空。
  const merged = mergeProjectTopicFirstPage(resident, [], { nextCursor: "", complete: true });
  ok(merged.length === 0,
    "空的完全首页仍整体替换（用户清空文件夹的行清空行为不回退）");
}
{
  // 调用方接线：ProjectTree 把 page.complete !== false 作为 complete 传入。
  const source = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");
  ok(source.includes("complete: page.complete !== false"),
    "ProjectTree 把后端 complete 标志透传给首页合并（空的不完全页不整体替换）");
}
{
  // 非空首页照常整体替换（无 nextCursor 时）：删除行仍被移除。
  const merged = mergeProjectTopicFirstPage(resident, [topicRow("t00"), topicRow("t01")], { nextCursor: "" });
  ok(merged.length === 2 && merged.every((node, index) => node.topicId === ["t00", "t01"][index]),
    "非空完全首页仍整体替换（删除/重排行为不回退）");
}

// ── 有游标的窗口首页分支（981 合同零回归）────────────────────────────────
{
  const firstPage = resident.slice(0, 5).map((node) => ({ ...node, label: node.label + "-fresh" }));
  const merged = mergeProjectTopicFirstPage(resident, firstPage, { nextCursor: "c1" });
  ok(merged.length === 17, "窗口首页刷新后行数不收缩（981 保尾部合同零回归）");
  ok(merged.slice(0, 5).every((node, index) => node === firstPage[index]), "首页 5 行以新数据为准");
  ok(merged.slice(5).every((node, index) => node === resident[5 + index]), "第 6 行起的驻留尾部原序保留");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
