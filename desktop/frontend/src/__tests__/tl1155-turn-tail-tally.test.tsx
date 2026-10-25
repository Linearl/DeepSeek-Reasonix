// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1155-turn-tail-tally.test.tsx
//
// 任务 1155 验收：回合尾部常驻显示本轮统计（工具/文件/检索）。
//  ① 回合 settle 后 turn-actions 行携带该回合自己的 tally（行模型级）；
//  ② 历史回合各见其统计（多回合各有独立数值，非会话累计）；
//  ③ 与输入框状态行同源一致（同库 turnToolTally：行内 tally 与
//     turnTallyLabel(turnItems) 逐字段相等）；
//  ④ 零工具纯对话轮不渲染统计（TurnActions 不输出 .turn-actions__tally）；
//  ⑤ DOM 级：有工具轮显示统计 span 且数字正确、零工具轮无 span、
//     两个回合并排各显示各自的数字；
//  ⑥ memo 约束：settle 回合重复重建行模型命中缓存（tally 引用稳定），
//     活跃回合不构建 turn-actions 行（1141 禁全量扫描约束下的实现钉住）；
//  ⑦ CSS 源码钉住：.turn-actions__tally 规则存在（JSDOM 无布局引擎，
//     与 1012/1136 手法一致）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { buildTranscriptRowBlocks, buildTurnModels, EMPTY_FOLDS } from "../lib/transcriptRows";
import type { BuildRowsOptions } from "../lib/transcriptRows";
import { turnTally, turnTallyLabel, turnTallyText, type TurnTally } from "../lib/turnToolTally";
import type { Item } from "../lib/useController";
import { zh } from "../locales/zh";

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

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

// 纯函数级断言用的中文翻译器（组件渲染走 LocaleProvider；这里直接按 zh 表拼）。
const t: Parameters<typeof turnTallyText>[1] = (key, vars) => {
  const template = zh[key as keyof typeof zh] ?? key;
  return template.replace(/\{(\w+)\}/g, (_, name) => (vars?.[name] !== undefined ? String(vars[name]) : `{${name}}`));
};

let toolSeq = 0;
function tool(name: string, args: Record<string, unknown> = {}): Item {
  toolSeq += 1;
  return {
    kind: "tool", id: `t${toolSeq}`, name, args: JSON.stringify(args),
    readOnly: false, status: "done",
  };
}

console.log("\ntl1155 turn-tail tally");

// ── ①③ 行模型：settle 回合的 turn-actions 行携带该回合自己的 tally ──────────
{
  const items: Item[] = [
    { kind: "user", id: "u1", text: "第一回合" },
    tool("read_file", { path: "a.ts" }),
    tool("read_file", { path: "b.ts" }),
    tool("grep", { pattern: "TODO", path: "src" }),
    { kind: "assistant", id: "a1", text: "答一", reasoning: "", streaming: false },
    { kind: "user", id: "u2", text: "第二回合" },
    tool("bash", { command: "npm test" }),
    tool("edit_file", { path: "a.ts", old_string: "1", new_string: "2" }),
    { kind: "assistant", id: "a2", text: "答二", reasoning: "", streaming: false },
  ];
  const models = buildTurnModels(items);
  const options = {
    folds: EMPTY_FOLDS,
    hasOlderHistory: false,
    creationMode: false,
    turnForUser: (item: Extract<Item, { kind: "user" }>) => (item.id === "u1" ? 1 : item.id === "u2" ? 2 : undefined),
  } satisfies BuildRowsOptions;
  const rows = buildTranscriptRowBlocks(models, options).flatMap((block) => block.rows);
  const actionRows = rows.filter((row) => row.kind === "turn-actions");
  eq(actionRows.length, 2, "① 两个 settle 回合各有一条 turn-actions 行");

  const row1 = actionRows[0] as Extract<(typeof actionRows)[number], { kind: "turn-actions" }>;
  const row2 = actionRows[1] as Extract<(typeof actionRows)[number], { kind: "turn-actions" }>;
  // ③ 同库同口径：行内 tally 与直接对「该回合自己的 items」调库的结果逐字段一致。
  const turn1Items = models[0].turnItems;
  const turn2Items = models[1].turnItems;
  const expect1 = turnTally(turn1Items);
  const expect2 = turnTally(turn2Items);
  eq(row1.tally.tools, expect1.tools, "③ 回合一工具数与同库对 turnItems 直算一致");
  eq(row1.tally.files, expect1.files, "③ 回合一文件数与同库直算一致");
  eq(row1.tally.searches, expect1.searches, "③ 回合一检索数与同库直算一致");
  eq(row1.tally.tools, 3, "① 回合一计 3 工具（2 读 + 1 grep）");
  eq(row1.tally.files, 2, "① 回合一计 2 文件（grep 的范围参数不计文件）");
  eq(row1.tally.searches, 1, "① 回合一计 1 检索");
  eq(row2.tally.tools, 2, "① 回合二计 2 工具");
  eq(row2.tally.files, 1, "① 回合二计 1 文件（a.ts）");
  eq(row2.tally.searches, 0, "① 回合二计 0 检索");
  // ② 历史回合各见其统计：回合二是独立数值，不是两回合累计（累计会是 5 工具）。
  ok(row2.tally.tools !== row1.tally.tools + 1 || row1.tally.tools === 3, "② 回合二为自身统计而非会话累计");
  // 与输入框状态行文案同格式化（同库 turnTallyText / turnTallyLabel）。
  eq(turnTallyText(row1.tally, t), turnTallyLabel(turn1Items, t), "③ 行内 tally 与 turnTallyLabel 同格式化输出一致");
  eq(turnTallyText(row2.tally, t), "2 工具 · 1 文件 · 0 检索", "③ 统计文案为「2 工具 · 1 文件 · 0 检索」");
}

// ── ④ 行模型：零工具纯对话轮行存在但 tally.tools=0（渲染层据此不显示）────────
{
  const items: Item[] = [
    { kind: "user", id: "u-chat", text: "纯对话" },
    { kind: "assistant", id: "a-chat", text: "好的", reasoning: "", streaming: false },
  ];
  const models = buildTurnModels(items);
  const options = {
    folds: EMPTY_FOLDS,
    hasOlderHistory: false,
    creationMode: false,
    turnForUser: () => 1,
  } satisfies BuildRowsOptions;
  const rows = buildTranscriptRowBlocks(models, options).flatMap((block) => block.rows);
  const actionRow = rows.find((row) => row.kind === "turn-actions") as Extract<(typeof rows)[number], { kind: "turn-actions" }> | undefined;
  ok(actionRow !== undefined, "④ 纯对话轮仍有 turn-actions 行（复制入口不丢）");
  eq(actionRow?.tally.tools, 0, "④ 纯对话轮 tally.tools=0（渲染层整段不渲染统计）");
  eq(turnTallyText(actionRow!.tally, t), null, "④ 零工具轮 turnTallyText 返回 null");
}

// ── ⑥ 活跃回合不构建 turn-actions 行 + settle 回合 memo 命中（引用稳定）──────
{
  const settled: Item[] = [
    { kind: "user", id: "u-s", text: "已结束" },
    tool("read_file", { path: "s.ts" }),
    { kind: "assistant", id: "a-s", text: "答", reasoning: "", streaming: false },
  ];
  const active: Item[] = [
    ...settled,
    { kind: "user", id: "u-run", text: "跑着" },
    tool("bash", { command: "sleep 1" }),
  ];
  const options = {
    folds: EMPTY_FOLDS,
    hasOlderHistory: false,
    creationMode: false,
    turnForUser: (item: Extract<Item, { kind: "user" }>) => (item.id === "u-s" ? 1 : item.id === "u-run" ? 2 : undefined),
  } satisfies BuildRowsOptions;
  const models = buildTurnModels(active, { hasAnswerText: false, hasReasoning: false }, true);
  const rows = buildTranscriptRowBlocks(models, options).flatMap((block) => block.rows);
  const actionRows = rows.filter((row) => row.kind === "turn-actions");
  eq(actionRows.length, 1, "⑥ 活跃回合不构建 turn-actions 行（只结算回合有）");
  eq(actionRows[0]?.turn, 1, "⑥ 仅已 settle 的回合一带行");

  const firstTally = actionRows[0] ? (actionRows[0] as Extract<(typeof actionRows)[number], { kind: "turn-actions" }>).tally : undefined;
  const modelsAgain = buildTurnModels(active, { hasAnswerText: false, hasReasoning: false }, true);
  const rowsAgain = buildTranscriptRowBlocks(modelsAgain, options).flatMap((block) => block.rows);
  const again = rowsAgain.filter((row) => row.kind === "turn-actions")[0] as Extract<(typeof rowsAgain)[number], { kind: "turn-actions" }>;
  ok(firstTally !== undefined && again.tally === firstTally, "⑥ settle 回合重复构建命中 memo（tally 引用稳定，不重复解析 args）");
}

// ── ⑤ DOM 级：TurnActions 有工具轮显示统计、零工具轮不渲染、多回合各自显示 ──
{
  class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  (window as unknown as { go: unknown }).go = {
    main: { App: { ClipboardSetText: async () => true } },
  };

  const flushTimers = (): Promise<void> => new Promise((r) => setTimeout(r, 0));
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { TurnActions } = await import("../components/Message");
  const rootEl = document.getElementById("root")!;

  const renderIn = async (node: React.ReactNode): Promise<{ host: HTMLDivElement; cleanup: () => Promise<void> }> => {
    const host = document.createElement("div");
    rootEl.appendChild(host);
    const root = createRoot(host);
    await act(async () => {
      root.render(React.createElement(LocaleProvider, null, node));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    return {
      host,
      cleanup: async () => {
        await act(async () => {
          root.unmount();
        });
        host.remove();
      },
    };
  };

  const toolsOf = (tally: TurnTally) => tally;

  // ⑤a 有工具轮：统计 span 渲染且数字正确。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={1} tally={toolsOf({ tools: 12, files: 3, searches: 1 })} />,
    );
    const span = host.querySelector(".turn-actions__tally");
    ok(span !== null, "⑤a 有工具轮渲染 .turn-actions__tally");
    eq(span?.textContent, "12 工具 · 3 文件 · 1 检索", "⑤a 统计文案为「12 工具 · 3 文件 · 1 检索」");
    eq(span?.getAttribute("aria-hidden"), "true", "⑤a 统计为装饰性读数（aria-hidden，与输入框同契约）");
    await cleanup();
  }
  // ⑤b 零工具轮：不渲染统计 span（纯对话不制造噪音）。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={2} tally={toolsOf({ tools: 0, files: 0, searches: 0 })} />,
    );
    eq(host.querySelector(".turn-actions__tally"), null, "⑤b 零工具轮不渲染统计");
    ok(host.querySelector(".turn-actions") !== null, "⑤b 工具栏行本身仍在（复制入口不丢）");
    await cleanup();
  }
  // ⑤c 多回合各自显示：两个回合并排各见其值。
  {
    const { host, cleanup } = await renderIn(
      <>
        <TurnActions text="一" turn={1} tally={toolsOf({ tools: 3, files: 2, searches: 1 })} />
        <TurnActions text="二" turn={2} tally={toolsOf({ tools: 7, files: 1, searches: 0 })} />
      </>,
    );
    const spans = host.querySelectorAll(".turn-actions__tally");
    eq(spans.length, 2, "⑤c 两个回合各渲染一条统计");
    eq(spans[0]?.textContent, "3 工具 · 2 文件 · 1 检索", "⑤c 回合一显示自身统计");
    eq(spans[1]?.textContent, "7 工具 · 1 文件 · 0 检索", "⑤c 回合二显示自身统计（非累计）");
    await cleanup();
  }
}

// ── ⑦ CSS 源码钉住：.turn-actions__tally 规则存在 ────────────────────────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  const css = readFileSync(resolve(here, "../styles.css"), "utf8");
  ok(/\.turn-actions__tally \{[^}]*color:\s*var\(--fg-faint\);/s.test(css), "⑦ .turn-actions__tally 规则存在（弱化字色，与工具栏按钮同色）");
  ok(/\.turn-actions__tally \{[^}]*white-space:\s*nowrap;/s.test(css), "⑦ 统计不折行（与输入框状态行读数同契约）");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
