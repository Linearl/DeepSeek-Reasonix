// Run: tsx src/__tests__/project-tree-741-load-indicator.test.ts
//
// 741 — 项目栏底部「加载更多/正在整理历史」持续跳动不收敛。
//
// 根因：project-tree:changed-v2 事件搭着每一次 catalog revision 自增走（只要有
// 会话活跃它就持续到达），每次事件触发的后台首页重拉都把页尾指示行翻成
// 「正在整理历史」再翻回来——指示器永不收敛。修复：只有用户可见的加载允许翻转
// 指示器（显式「加载更多」点击 / 无驻留页的首次加载驱动骨架），驻留页的后台
// 刷新静默替换内容。本测试钉住纯函数判定 + ProjectTree 接线契约 + 标签三态。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { projectTreePageLoadFlipsIndicator } from "../lib/projectTreeTopic";

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

console.log("\nproject tree 741 load indicator convergence");

// ── 纯函数：哪类加载允许翻转指示器 ──────────────────────────────────────────
ok(projectTreePageLoadFlipsIndicator(undefined, false) === true,
  "无驻留页（首次加载）→ 允许翻转（驱动骨架）");
ok(projectTreePageLoadFlipsIndicator({ loading: false }, true) === true,
  "显式「加载更多」点击 → 允许翻转（用户自己触发的请求，天然收敛）");
ok(projectTreePageLoadFlipsIndicator({ loading: false }, false) === false,
  "驻留页上的后台首页重拉 → 不允许翻转（741 跳动源头）");
ok(projectTreePageLoadFlipsIndicator(undefined, true) === true,
  "显式点击且无驻留页 → 允许翻转");

// ── ProjectTree 接线契约 ────────────────────────────────────────────────────
const here = dirname(fileURLToPath(import.meta.url));
const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");

ok(/if \(projectTreePageLoadFlipsIndicator\(pageState, append\)\) \{\s*updateTopicPageState\(key, \{ \.\.\.pageState, loading: true \}\);/.test(treeSource),
  "loadProjectTopics 的 loading 写入被翻转门包裹（741 修复本体）");
ok(!/topicLoadSeqRef\.current\[key\] = seq;\s*updateTopicPageState\(key, \{ \.\.\.pageState, loading: true \}\);/.test(treeSource),
  "门控之外不得再有无条件的 loading:true 写入（防回退成每次重拉都翻转）");
ok(/backendPage\.loading \? t\("projectTree\.indexing"\) : t\("projectTree\.loadMore"\)/.test(treeSource),
  "页尾标签仍只反映真实 loading 状态（翻转收敛后标签不再抖动）");

// ── 三语文案未被改动（加载更多 / 正在整理历史） ────────────────────────────
const locale = (name: string) => readFileSync(resolve(here, `../locales/${name}.ts`), "utf8");
ok(locale("zh").includes('"projectTree.loadMore": "加载更多"') && locale("zh").includes('"projectTree.indexing": "正在整理历史"'),
  "zh 文案保留");
ok(locale("zh-TW").includes('"projectTree.loadMore"') && locale("zh-TW").includes('"projectTree.indexing"'),
  "zh-TW 文案保留");
ok(locale("en").includes('"projectTree.loadMore"') && locale("en").includes('"projectTree.indexing"'),
  "en 文案保留");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
