// Run: tsx src/__tests__/project-tree-981-flicker.test.ts
//
// 981 — 左侧项目栏间歇闪烁（调查报告
// docs/report/调研报告【非诊断】/项目栏闪烁根因调查-tl981-20261018.md）。
//
// 根因三事件流 × 前端零防抖消费：每个 project-tree:changed-v2 事件都全量跑
// GetSessionCatalogStatus + refreshProjectGroups + 每个展开文件夹一轮话题分页
// 续拉；活跃会话每落盘一次就 bump last_activity_at，按活跃排序的行随之持续
// 上跳。本测试钉住两处修复：① v2 事件消费端固定窗口合批（源码契约）；
// ② running 行排序键分钟桶化（纯函数行为）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { arrangeClassicProjectTree } from "../lib/projectTreePresentation";
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

console.log("\nproject tree 981 flicker suppression");

function topic(topicId: string, lastActivityAt: number, running = false): ProjectNode {
  return { key: `t-${topicId}`, kind: "topic", label: topicId, topicId, lastActivityAt, running };
}

function order(topics: ProjectNode[]): string[] {
  return arrangeClassicProjectTree(
    [{ key: "p", kind: "project", label: "P", root: "/repo", children: topics }],
    "updated",
  )[0].children?.map((child) => child.topicId ?? "") ?? [];
}

// ── ② running 行排序键分钟桶化 ─────────────────────────────────────────────
const base = 1_700_000_000_000; // 远古定值，跨分钟边界可控
const minute = 60_000;

// 桶化语义：running 行 b 的排序键 = floor((base+30s)/分钟)*分钟。选一个让
// 桶化与精确排序结果相反的构造：b 精确时间晚于 a 但桶值早于 a。
const crossA = Math.floor(base / minute) * minute; // 桶起点
const aTime = crossA + 10_000;                     // 桶内 10s
const bTime = crossA + 50_000;                     // 桶内 50s（精确更晚）
ok(
  order([topic("a", aTime), topic("b", bTime, true)]).join(",") === "a,b",
  "running 行桶化后同一分钟内不再上跳：b 精确更晚但排序键同桶，按 topicId 稳定序",
);

ok(
  order([topic("a", aTime), topic("b", bTime)]).join(",") === "b,a",
  "非 running 行不受桶化影响：精确时间更晚仍排前（零回归对照）",
);

ok(
  order([topic("a", aTime), topic("b", bTime + minute, true)]).join(",") === "b,a",
  "running 行跨桶后仍正常上浮：桶值更高排前",
);

ok(
  order([topic("x", base), topic("y", base)]).join(",") === "x,y",
  "同时间戳 tie 走 topicId 稳定序（桶化不改变既有 tie-break）",
);

// ── ① v2 事件消费端合批接线契约 ───────────────────────────────────────────
const here = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");

ok(/PROJECT_TREE_EVENT_BATCH_MS = 400/.test(panel), "合批窗口常量 400ms（报告建议 300–500ms 带内）");
ok(/pendingTreeEventsRef\.current\.push\(event\)/.test(panel), "事件先入批再统一处理");
ok(/window\.setTimeout\(\(\) => \{\s*treeEventWindowRef\.current = null;\s*flushTreeEventsRef\.current\(\);/.test(panel),
  "固定窗口到期统一 flush（固定窗非滑动窗：事件流下不饿死）");
ok(/const event = events\.reduce\(\(current, item\) => item\.revision >= current\.revision \? item : current\)/.test(panel),
  "合批取最新 revision 的事件作为新鲜度代表");
ok(/projectTreeRevisionIsFresh\(latestRevisionRef\.current, event\.revision\)/.test(panel),
  "新鲜度判定沿用共享契约（stale 批仍走全量 refresh）");
ok(/affected = \[\]; break; \} \/\/ empty roots = every folder/.test(panel),
  "合批 roots 并集：任一事件空 roots 即退化为全部文件夹");
ok(!/useEffect\(\(\) => onProjectTreeChangedV2/.test(panel),
  "订阅 effect 不再依赖 expanded/loadProjectTopics/refresh——每次状态变化重订阅会丢批");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
