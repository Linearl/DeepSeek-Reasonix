// Run: tsx src/__tests__/tl1426-subagent-split-layout.test.ts
//
// tl1426 需求②：子代理面板 split 形态的列表列固定宽——dock 进 1:1 宽态
// （~824px）后，旧 minmax(180px, 34%) 把列表列拉到 ~280px 挤占详情；固定
// 200px 让列表列不随面板宽度漂移。钉三层：
// ① 纯函数层——clampSubagentsSplitListWidth 的上下界族（默认界 / 面板宽
//    感知界 / 取整），App(面板) 与本文件同式调用零漂移；
// ② CSS 契约层——.subagents-panel--split 的列表列是固定 px（var 兜底值），
//    不再有百分比拉伸；兜底值与 SUBAGENTS_SPLIT_LIST_DEFAULT_WIDTH 同源；
// ③ 持久化层——subagentsSplitListWidth sizes 键注册（① commit 已进
//    LayoutSizeKey + LEGACY_SIZE_KEYS），真实 localStorage 往返。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const testDir = dirname(fileURLToPath(import.meta.url));
const cssSource = readFileSync(resolve(testDir, "../styles.css"), "utf8");
const prefsSource = readFileSync(resolve(testDir, "../lib/layoutPreferences.ts"), "utf8");

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

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
