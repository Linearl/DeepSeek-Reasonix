// Run: tsx src/__tests__/project-tree-startup-loading.test.tsx
//
// Task 403（上游 #10957→#10963 对照）— 启动期侧栏三态：
//   项目树首读返回前显示「正在读取项目…」加载行，首读返回后按数据渲染行；
//   只有「已确认读到空」才允许出现「还没有项目」空态（它与"数据丢了"视觉一致，
//   正是上游 #10957 抱怨的点）。覆盖：纯函数三态判定 + ProjectTree 接线契约
//   （两处渲染点、refresh 全路径 settle）+ 三语文案 + CSS 钩子。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { projectTreeBodyState } from "../lib/projectTreePresentation";

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

console.log("\nproject tree startup loading (task 403)");

// ── 纯函数：loading / rows / 确认空 三态 ────────────────────────────────────
ok(projectTreeBodyState({ initialReadSettled: false, hasRows: false }) === "loading",
  "首读未返回且暂无数据 → loading（不允许空态提前出现）");
ok(projectTreeBodyState({ initialReadSettled: false, hasRows: true }) === "loading",
  "首读未返回（即使有数据写入）→ loading");
ok(projectTreeBodyState({ initialReadSettled: true, hasRows: true }) === "rows",
  "首读已返回且有行 → rows");
ok(projectTreeBodyState({ initialReadSettled: true, hasRows: false }) === "empty",
  "首读已返回且确认为空 → empty（空态只在确认后出现）");

// ── ProjectTree 接线契约 ────────────────────────────────────────────────────
const here = dirname(fileURLToPath(import.meta.url));
const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");

ok(/const \[initialTreeReadSettled, setInitialTreeReadSettled\] = useState\(false\);/.test(treeSource),
  "ProjectTree 持有首读 settle 标记，初始为未返回");
ok(/projectTreeBodyState\(\{ initialReadSettled: initialTreeReadSettled, hasRows: hasTreeRows \}\)/.test(treeSource),
  "树体状态由 projectTreeBodyState 三态判定驱动（不再裸用 hasTreeRows 二分）");
ok(
  (treeSource.match(/treeBody === "loading" \? \(/g) ?? []).length === 2 &&
    (treeSource.match(/\) : treeBody === "empty" \? \(/g) ?? []).length === 2,
  "workbench 与 classic 两处渲染点都走 loading→empty→rows 三态分支",
);
ok(
  /const renderTreeLoadingState = \(\) => \(\s*<div className="project-tree__empty project-tree__loading" role="status">\{t\("projectTree\.readingFolders"\)\}<\/div>\s*\);/.test(treeSource),
  "加载行带 role=\"status\" 与 projectTree.readingFolders 文案（可被辅助技术播报）",
);
ok(
  /await reloadRequestedProjects\(treeRef\.current\);\s*\} finally \{\s*[^}]*setInitialTreeReadSettled\(true\);/s.test(treeSource),
  "refresh 在 finally 中翻转 settle 标记（快照失败也不得永久卡在加载态）",
);
ok(
  /projectTree\.emptyNoProjects/.test(treeSource) &&
    /onClick=\{\(\) => void handleAddProject\(\)\}/.test(treeSource),
  "空态本体未被改动（还没有项目 + 添加按钮原样保留在确认空之后）",
);

// ── 三语文案 ────────────────────────────────────────────────────────────────
const locale = (name: string) => readFileSync(resolve(here, `../locales/${name}.ts`), "utf8");
ok(locale("zh").includes('"projectTree.readingFolders": "正在读取项目…"'),
  "zh 文案：正在读取项目…");
ok(locale("zh-TW").includes('"projectTree.readingFolders": "正在讀取專案…"'),
  "zh-TW 文案：正在讀取專案…");
ok(locale("en").includes('"projectTree.readingFolders": "Reading folders…"'),
  "en 文案与上游 #10963 的 Reading folders… 对齐");

// ── CSS 钩子 ────────────────────────────────────────────────────────────────
const css = readFileSync(resolve(here, "../styles.css"), "utf8");
ok(/\.project-tree__loading \{/.test(css), "styles.css 定义 .project-tree__loading 样式钩子");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
