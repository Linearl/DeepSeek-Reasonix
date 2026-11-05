// Run: tsx src/__tests__/tl1426-subagent-split-layout.test.ts
//
// tl1426 需求②③：子代理面板 split 形态的列表列固定宽 + 列表/详情比例可调。
// ②——dock 进 1:1 宽态（~824px）后，旧 minmax(180px, 34%) 把列表列拉到
// ~280px 挤占详情；固定 200px 让列表列不随面板宽度漂移。
// ③——列表↔详情加拖拽分隔条（ResizableDrawer 同款：拖动经 CSS 变量实时
// 生效，松手/键盘持久化，双击回默认）。钉五层：
// ① 纯函数层——clampSubagentsSplitListWidth 的上下界族（默认界 / 面板宽
//    感知界 / 取整），面板与本文件同式调用零漂移；
// ② CSS 契约层——.subagents-panel--split 的列表列是固定 px（var 兜底值），
//    不再有百分比拉伸；兜底值与 SUBAGENTS_SPLIT_LIST_DEFAULT_WIDTH 同源；
// ③ 持久化层——subagentsSplitListWidth sizes 键注册 + 真实 localStorage
//    往返；
// ④ 接线层——SubagentsDockPanel 分隔条（拖拽/键盘/双击复位）调用点静态
//    断言（side-dock-user-width 先例：渲染编排行为无法组件级挂载，静态
//    断言防回归漂移）；
// ⑤ 主题/i18n 层——分隔条视觉全走主题 token（--accent 等）+ 三语键齐。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const testDir = dirname(fileURLToPath(import.meta.url));
const cssSource = readFileSync(resolve(testDir, "../styles.css"), "utf8");
const prefsSource = readFileSync(resolve(testDir, "../lib/layoutPreferences.ts"), "utf8");
const panelSource = readFileSync(resolve(testDir, "../components/SubagentsDockPanel.tsx"), "utf8");
const enSource = readFileSync(resolve(testDir, "../locales/en.ts"), "utf8");
const zhSource = readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8");
const zhTwSource = readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8");

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
  ok(Object.is(actual, expected), `${label} (actual=${String(actual)}, expected=${String(expected)})`);
}

console.log("\nsubagent split list width (tl1426 需求②: 面板内列表列固定宽)");

// ① 纯函数层（先装 window 再动态 import，持久化段共用）。
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;

const layout = await import("../store/layout");
const prefs = await import("../lib/layoutPreferences");

eq(layout.SUBAGENTS_SPLIT_LIST_DEFAULT_WIDTH, 200, "固定宽默认 = 200（旧 34% 在 824 宽 dock 拉到 ~280，固定后不漂移）");
eq(layout.SUBAGENTS_SPLIT_LIST_MIN_WIDTH, 180, "列表列下界 180（旧 minmax 地板保留，行可读性不回退）");
eq(layout.SUBAGENTS_SPLIT_LIST_MAX_WIDTH, 400, "列表列上界 400（1:1 dock 下列表最多吃一半略少）");

eq(layout.clampSubagentsSplitListWidth(300), 300, "界内值原样");
eq(layout.clampSubagentsSplitListWidth(50), 180, "低于下界抬回 180");
eq(layout.clampSubagentsSplitListWidth(9999), 400, "高于上界压回 400");
eq(layout.clampSubagentsSplitListWidth(220.6), 221, "四舍五入取整");
eq(
  layout.clampSubagentsSplitListWidth(380, 500),
  320,
  "面板感知界: pane 500 时列表最多 320（详情保底 180 不被挤穿）",
);
eq(
  layout.clampSubagentsSplitListWidth(200, 300),
  180,
  "面板感知界: pane 300 时列表保住下界 180（max 先于下界再抬）",
);
eq(layout.clampSubagentsSplitListWidth(300, 0), 300, "paneWidth 缺省/0 视为无面板感知，走 400 硬上界");

// ② CSS 契约层：.subagents-panel--split 列表列固定 px，无百分比。
{
  const idx = cssSource.indexOf(".subagents-panel--split {");
  ok(idx >= 0, "styles.css 仍持有 .subagents-panel--split 规则");
  const block = cssSource.slice(idx, cssSource.indexOf("}", idx));
  const columnsLine = block
    .split("\n")
    .map((line) => line.trim())
    .find((line) => line.startsWith("grid-template-columns:"));
  ok(Boolean(columnsLine), "split 规则仍声明 grid-template-columns");
  eq(columnsLine, "grid-template-columns: var(--subagents-list-width, 200px) minmax(0, 1fr);", "列表列 = var 固定 px 兜底 + 详情列 1fr");
  ok(!columnsLine.includes("%"), "列表列无百分比拉伸（34% 旧契约移除）");
  ok(
    columnsLine.includes(`var(--subagents-list-width, ${layout.SUBAGENTS_SPLIT_LIST_DEFAULT_WIDTH}px)`),
    "CSS 兜底值与 SUBAGENTS_SPLIT_LIST_DEFAULT_WIDTH 同源（200px）",
  );
  ok(!block.includes("minmax(180px, 34%)"), "旧 minmax(180px, 34%) 已移除（需求②的反向钉）");
}

// ③ 持久化层：sizes 键注册 + 真实 localStorage 往返。
ok(
  prefsSource.includes('"subagentsSplitListWidth"'),
  "layoutPreferences 注册 subagentsSplitListWidth sizes 键",
);
ok(
  prefsSource.includes("subagentsSplitListWidth: []"),
  "LEGACY_SIZE_KEYS 含 subagentsSplitListWidth（无遗留旧键）",
);

layout.saveSubagentsSplitListWidth(260);
eq(prefs.loadOptionalLayoutSize("subagentsSplitListWidth", layout.clampSubagentsSplitListWidth), 260, "宽度键往返: 保存 260 → 读回 260");
layout.saveSubagentsSplitListWidth(100);
eq(prefs.loadOptionalLayoutSize("subagentsSplitListWidth", layout.clampSubagentsSplitListWidth), 180, "保存越界值被 clamp 家族拦回 180 下界");
layout.saveSubagentsSplitListWidth(380, 480);
eq(prefs.loadOptionalLayoutSize("subagentsSplitListWidth", layout.clampSubagentsSplitListWidth), 300, "面板感知界随保存生效（pane 480 → 列表上限 300）");

// ④ 接线层：SubagentsDockPanel 分隔条接线静态断言。
console.log("\ntl1426③ 接线: 拖拽分隔条 + 持久化");
ok(
  panelSource.includes('role="separator"') && panelSource.includes('aria-orientation="vertical"'),
  "分隔条 role=separator + vertical 朝向",
);
ok(
  panelSource.includes('aria-label={t("subagentPanel.splitResize")}'),
  "分隔条挂三语 aria-label",
);
ok(
  panelSource.includes("aria-valuemin={SUBAGENTS_SPLIT_LIST_MIN_WIDTH}") && panelSource.includes("aria-valuemax={SUBAGENTS_SPLIT_LIST_MAX_WIDTH}") && panelSource.includes("aria-valuenow={splitListWidth}"),
  "分隔条 aria 值三元组挂 clamp 族常量与当前宽",
);
ok(
  panelSource.includes("onPointerDown={startSplitResize}") && panelSource.includes("onKeyDown={onSplitResizeKeyDown}") && panelSource.includes("onDoubleClick={resetSplitListWidth}"),
  "分隔条三入口: 拖拽/键盘/双击复位",
);
{
  const dragBody = panelSource.slice(
    panelSource.indexOf("const startSplitResize = useCallback"),
    panelSource.indexOf("const onSplitResizeKeyDown = useCallback"),
  );
  ok(dragBody.includes('cssVar: "--subagents-list-width"'), "拖拽经 CSS 变量实时生效（createRafResizeUpdater rAF 批处理）");
  ok(dragBody.includes("clampSubagentsSplitListWidth(moveEvent.clientX - rect.left, rect.width)"), "拖拽实时值按面板实际宽截断（详情保底 180 不被挤穿）");
  ok(dragBody.includes("saveSubagentsSplitListWidth(nextWidth"), "拖拽松手持久化（onDone 落 sizes 键）");
  ok(dragBody.includes('document.body.style.cursor = "col-resize"'), "拖拽中全局 col-resize 光标（ResizableDrawer 同款）");
}
{
  const keyBody = panelSource.slice(
    panelSource.indexOf("const onSplitResizeKeyDown = useCallback"),
    panelSource.indexOf("const resetSplitListWidth = useCallback"),
  );
  ok(keyBody.includes('event.key === "ArrowLeft"') && keyBody.includes("16"), "键盘左右 ±16 步进");
  ok(keyBody.includes("SUBAGENTS_SPLIT_LIST_MIN_WIDTH") && keyBody.includes("SUBAGENTS_SPLIT_LIST_MAX_WIDTH"), "键盘 Home/End 跳下上界（同走持久化路径）");
}
ok(
  panelSource.includes("const resetSplitListWidth = useCallback(() => applySplitListWidth(SUBAGENTS_SPLIT_LIST_DEFAULT_WIDTH)"),
  "双击复位回固定宽默认 200",
);
ok(
  panelSource.includes('loadOptionalLayoutSize("subagentsSplitListWidth", clampSubagentsSplitListWidth) ?? SUBAGENTS_SPLIT_LIST_DEFAULT_WIDTH'),
  "初始宽从持久化读回（无记录走默认 200）——跨重启保持用户比例",
);
ok(
  panelSource.includes('"--subagents-list-width"') && panelSource.includes("subagents-panel--split-resizing"),
  "容器挂 CSS 变量 + 拖拽中 resizing 类（发丝线/光标联动）",
);

// ⑤ 主题/i18n 层：分隔条视觉全 token + 三语键齐。
{
  const idx = cssSource.indexOf(".subagents-panel__splitter {");
  ok(idx >= 0, "styles.css 持有 .subagents-panel__splitter 规则");
  // 切片带到 resizing 段为止（含 ::after 发丝线规则）。
  const block = cssSource.slice(idx, cssSource.indexOf(".subagents-panel--split-resizing,", idx));
  ok(block.includes("position: absolute") && block.includes("left: calc(var(--subagents-list-width, 200px) - 4px)"), "分隔条绝对定位钉在列表列边界（随 var 联动）");
  ok(block.includes("--wails-draggable: no-drag"), "分隔条 no-drag（窗口拖拽区豁免，drawer-resizer 同款）");
  ok(block.includes("cursor: col-resize"), "分隔条 col-resize 光标");
  ok(block.includes("var(--accent)"), "发丝线用主题 token --accent（七主题兼容，无硬编码色）");
  ok(!/#[0-9a-fA-F]{3,8}\b/.test(block), "分隔条规则无硬编码色值");
}
ok(enSource.includes('"subagentPanel.splitResize"'), "en 词典含 splitResize 键");
ok(zhSource.includes('"subagentPanel.splitResize"'), "zh 词典含 splitResize 键");
ok(zhTwSource.includes('"subagentPanel.splitResize"'), "zh-TW 词典含 splitResize 键");

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
