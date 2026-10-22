// Run: tsx src/__tests__/project-tree-1074-shadow-entries.test.ts
//
// 任务 1074：Global 项目树「影子条目」——同一个会话在树里显示 3 条，数据层
// 实测唯一（catalog_topics 一行 / globalTopics 无重复 id）。根因：话题页合并
// 以 node.key 去重，而同一 topic 的多个物理 session 文件（collab 联系文件、
// global 副本、recovery 副本）在 catalog 里是不同的 key、同一 topic id ⇒ 逐页
// 累积成 3 行。修法：三个合并函数改按 projectTreeTopicRowIdentity（topicId
// 为一逻辑会话的身份）去重，topic-less 行退回 node.key。

import { mergeIncompleteProjectTopicPage, mergeProjectTopicPage, mergeProjectTopicFirstPage, projectTreeTopicRowIdentity } from "../lib/projectTreeTopic";
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

console.log("\nproject tree 1074 shadow entries");

// 现场形状（20261022-074433 ai-passport-转线2026…）：同一 topic id，三个不同
// backing session（collab 联系文件 / global 副本 / recovery 副本），node.key
// 各不相同 —— 修前按 key 合并 ⇒ 3 行。
const topicId = "topic_20261022-074433_1b651ae8c4b39703";
const collabRow: ProjectNode = { key: "global_topic_" + topicId, kind: "global_topic", label: "ai-passport-转线2026…", topicId, children: [] };
const globalCopy: ProjectNode = { key: "global_topic_" + topicId + "__copy", kind: "global_topic", label: "ai-passport-转线2026…", topicId, children: [] };
const recoveryCopy: ProjectNode = { key: "global_topic_" + topicId + "__recovery", kind: "global_topic", label: "ai-passport-转线2026…", topicId, children: [] };

// 身份函数本身：同 topicId（global 侧）同身份；topic-less 行退回 key。
ok(projectTreeTopicRowIdentity(collabRow) === projectTreeTopicRowIdentity(globalCopy)
  && projectTreeTopicRowIdentity(collabRow) === projectTreeTopicRowIdentity(recoveryCopy),
  "同一 topicId 的三条物理行身份相同");
ok(projectTreeTopicRowIdentity({ key: "shell-1", kind: "topic", label: "", children: [] }) === "shell-1",
  "无 topicId 的壳行退回 node.key");
ok(projectTreeTopicRowIdentity({ key: "folder", kind: "project", label: "P", children: [] }) === null,
  "文件夹行不参与话题去重");

// append 路径：incoming 自身按身份折叠为 1 行，与驻留行（不同 key、同
// topicId）并列；后续刷新（下一轮 append/complete page）再把它收敛为 1 行。
// topic-less 壳行（无 topicId）不受影响，仍按 key 并列。
{
  const shell: ProjectNode = { key: "shell-x", kind: "topic", label: "", children: [] };
  const merged = mergeProjectTopicPage([shell], [globalCopy, recoveryCopy], true);
  ok(merged.filter((node) => node.topicId === topicId).length === 1
    && merged.some((node) => node === globalCopy) && !merged.some((node) => node === recoveryCopy)
    && merged.some((node) => node === shell),
    "append 合并把同 topicId 的第二个/第三个副本折叠为 1 行（incoming 自身去重），无 topicId 壳行不受影响");
  // 第二轮刷新（complete page 同 topicId 的 canonical 行）把驻留影子收敛掉。
  const settled = mergeProjectTopicPage([collabRow, ...merged], [collabRow], false);
  ok(settled.filter((node) => node.topicId === topicId).length === 1,
    "下一轮 complete 刷新把驻留影子行收敛为 1 行（会话恰好出现 1 次）");
}

// 首页非全量（nextCursor 存在）：不在首页的驻留副本行不再作为 shadow 幸存。
{
  const firstPage = [globalCopy];
  const merged = mergeProjectTopicFirstPage([collabRow, recoveryCopy], firstPage, { nextCursor: "c1" });
  const sameTopic = merged.filter((node) => node.topicId === topicId);
  ok(sameTopic.length === 1 && sameTopic[0] === globalCopy,
    "首页窗口合并后同 topicId 只剩首页那 1 行（驻留副本被身份折叠）");
}

// 首页即全量（无 nextCursor）：整体替换语义不变，同 topicId 依然 1 行。
{
  const merged = mergeProjectTopicFirstPage([collabRow, globalCopy, recoveryCopy], [globalCopy], {});
  ok(merged.filter((node) => node.topicId === topicId).length === 1,
    "首页即全量时整体替换，同 topicId 恰 1 行");
}

// 不完全页（目录分批提交）：新发现的副本行不再以新 key 追加成影子。
{
  const resident = [collabRow];
  const merged = mergeIncompleteProjectTopicPage(resident, [globalCopy]);
  ok(merged.length === 1 && merged[0] === collabRow,
    "不完全页把同 topicId 的发现行折叠进驻留行，不新增影子行");
}

// 不同 topicId 不互相折叠（真两条会话不受影响）。
{
  const other: ProjectNode = { key: "global_topic_topic_other", kind: "global_topic", label: "另一个会话", topicId: "topic_other", children: [] };
  const merged = mergeProjectTopicPage([collabRow], [other], true);
  ok(merged.length === 2, "不同 topicId 各自成行（真两条不被误折叠）");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
