// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1137-turn-tail-info.test.tsx
//
// 任务 1137 验收：转录每个回合底部带三条信息（对齐 DSH 轮次信息带）——
//  ① 本轮修改文件卡片：明细来自回合 items（turnFilePaths，与 1155 的
//     tally.files 同一 toolFilePaths 口径），>5 折叠为「+N 个文件」可展开；
//  ② 轮次 token 消耗：与状态栏同源（turn_done 时快照自 State.turnTokens /
//     turnInputTokens，outputTokens 即状态栏回合读数同字段），estimated 加
//     ≈ 前缀（与状态栏 markEstimated 同语义）；
//  ③ assistant 完成时间戳：运行时回合 = turn_done 到达时刻（turnDoneAt），
//     历史回合降级为后端入库的 createdAt（CardTime 1122 三档格式化）。
// 降级：零工具纯对话轮无文件卡片；历史回合无 usage 快照 → 只显示时刻。
// 1155 回归：同域先例的 turn-actions 行 tally 行为零改动（另跑
// tl1155-turn-tail-tally.test.tsx 定向回归）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { buildTranscriptRowBlocks, buildTurnModels, EMPTY_FOLDS } from "../lib/transcriptRows";
import type { BuildRowsOptions } from "../lib/transcriptRows";
import { turnFilePaths, turnTailInfo } from "../lib/turnToolTally";
import type { Item } from "../lib/useController";

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

let toolSeq = 0;
function tool(name: string, args: Record<string, unknown> = {}): Item {
  toolSeq += 1;
  return {
    kind: "tool", id: `t${toolSeq}`, name, args: JSON.stringify(args),
    readOnly: false, status: "done",
  };
}

// 回合尾部 assistant 条目构造器：可选带 1137 快照（运行时回合）与 createdAt（历史回合）。
let assistantSeq = 0;
function assistant(opts: { text: string; doneAt?: number; createdAt?: number; usage?: { inputTokens: number; outputTokens: number; estimated?: boolean } }): Item {
  assistantSeq += 1;
  return {
    kind: "assistant", id: `a${assistantSeq}`, text: opts.text, reasoning: "", streaming: false,
    ...(opts.createdAt !== undefined ? { createdAt: opts.createdAt } : {}),
    ...(opts.doneAt !== undefined ? { turnDoneAt: opts.doneAt } : {}),
    ...(opts.usage !== undefined ? { turnUsage: opts.usage } : {}),
  };
}

type ActionsRow = Extract<ReturnType<typeof buildTranscriptRowBlocks>[number]["rows"][number], { kind: "turn-actions" }>;

function actionRowsOf(items: Item[]): ActionsRow[] {
  const models = buildTurnModels(items);
  const options = {
    folds: EMPTY_FOLDS,
    hasOlderHistory: false,
    creationMode: false,
    turnForUser: (item: Extract<Item, { kind: "user" }>) => {
      const index = items.findIndex((it) => it.kind === "user" && it.id === item.id);
      return index >= 0 ? index + 1 : undefined;
    },
  } satisfies BuildRowsOptions;
  const rows = buildTranscriptRowBlocks(models, options).flatMap((block) => block.rows);
  return rows.filter((row): row is ActionsRow => row.kind === "turn-actions");
}

console.log("\ntl1137 turn-tail info band");

// ── ① 纯函数：turnFilePaths 明细与 tally.files 同口径（去重保序、检索范围不计）──
{
  const items: Item[] = [
    tool("read_file", { path: "a.ts" }),
    tool("read_file", { path: "a.ts" }), // 重复读取只计一次
    tool("write_file", { path: "b.ts" }),
    tool("grep", { pattern: "TODO", path: "src" }), // 检索范围不是被改文件
    tool("move_file", { source_path: "c.ts", destination_path: "d.ts" }), // move 两端
    tool("bash", { command: "npm test" }), // 无路径工具
  ];
  const files = turnFilePaths(items);
  eq(files.length, 4, "① 明细 4 条（重复去重、grep 范围不计、bash 无路径）");
  eq(files.join("|"), "a.ts|b.ts|c.ts|d.ts", "① 去重保序：a|b|c|d");
  const tail = turnTailInfo(items);
  eq(tail.files.length, files.length, "① turnTailInfo.files 与 turnFilePaths 同源");
  ok(tail.usage === undefined && tail.doneAt === undefined, "① 无 assistant 条目时 usage/doneAt 皆无");
}

// ── ①③ 行模型：settle 回合的 tail 三要素（明细 + 快照 + 完成时刻）────────────
{
  const doneAt1 = 1761360000000; // 固定时刻（epoch ms）
  const items: Item[] = [
    { kind: "user", id: "u1", text: "第一回合" },
    tool("edit_file", { path: "src/x.ts" }),
    tool("edit_file", { path: "src/y.ts" }),
    assistant({
      text: "答一", doneAt: doneAt1,
      usage: { inputTokens: 1234, outputTokens: 5678, estimated: true },
    }),
    { kind: "user", id: "u2", text: "第二回合" },
    tool("read_file", { path: "src/x.ts" }), // 跨回合重复路径——各回合各自收集
    assistant({ text: "答二" }),
  ];
  const rows = actionRowsOf(items);
  eq(rows.length, 2, "①③ 两个 settle 回合各一条 turn-actions 行");

  eq(rows[0].tail.files.join("|"), "src/x.ts|src/y.ts", "① 回合一明细 2 条且保序");
  eq(rows[0].tail.usage?.inputTokens, 1234, "② 回合一输入 token 来自快照");
  eq(rows[0].tail.usage?.outputTokens, 5678, "② 回合一输出 token 来自快照（状态栏同字段）");
  eq(rows[0].tail.usage?.estimated, true, "② 回合一 estimated 标记在快照上");
  eq(rows[0].tail.doneAt, doneAt1, "③ 回合一完成时刻 = turn_done 快照");

  eq(rows[1].tail.files.join("|"), "src/x.ts", "① 回合二明细 1 条（非两回合累计）");
  eq(rows[1].tail.usage, undefined, "② 回合二无快照（模拟历史回合）→ usage 无值");
  eq(rows[1].tail.doneAt, undefined, "③ 回合二无 turnDoneAt 且无 createdAt → 时刻无值");
}

// ── ③ 降级链：历史回合（后端入库 createdAt）在无 turnDoneAt 时兜底 ─────────────
{
  const createdAt = 1761360060000;
  const items: Item[] = [
    { kind: "user", id: "u-h", text: "历史回合" },
    tool("write_file", { path: "h.ts" }),
    assistant({ text: "历史答", createdAt }),
  ];
  const rows = actionRowsOf(items);
  eq(rows.length, 1, "③ 历史回合有 turn-actions 行");
  eq(rows[0].tail.doneAt, createdAt, "③ 时刻降级为最后 assistant 的 createdAt");
  eq(rows[0].tail.usage, undefined, "③ 历史回合无 usage（后端不存 token）");
}

// ── ③ 优先级：turnDoneAt 与 createdAt 同时存在时 turnDoneAt 优先 ─────────────
{
  const tail = turnTailInfo([
    assistant({ text: "双戳", createdAt: 111, doneAt: 222 }),
  ]);
  eq(tail.doneAt, 222, "③ turnDoneAt 优先于 createdAt");
}

// ── ⑥ memo：settle 回合重复重建命中缓存（tail 引用稳定）──────────────────────
{
  const settled: Item[] = [
    { kind: "user", id: "u-m", text: "已结束" },
    tool("read_file", { path: "m.ts" }),
    assistant({ text: "答" }),
  ];
  const active: Item[] = [
    ...settled,
    { kind: "user", id: "u-run2", text: "跑着" },
    tool("bash", { command: "sleep 1" }),
  ];
  const firstRows = actionRowsOf(active);
  const againRows = actionRowsOf(active);
  const first = firstRows[0]?.tail;
  const again = againRows[0]?.tail;
  ok(first !== undefined && again !== undefined && again === first, "⑥ settle 回合 tail memo 命中（引用稳定）");
}

// ── ⑤ DOM 级：三要素渲染 + 折叠展开 + 多回合各自显示 + 零工具轮降级 ─────────────
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

  const doneAt = 1761360000000;

  // ⑤a 三要素齐备：文件卡片 + token span + CardTime 时刻。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions
        text="答" turn={1}
        tail={{
          files: ["src/x.ts", "src/y.ts"],
          usage: { inputTokens: 1234, outputTokens: 5678 },
          doneAt,
        }}
      />,
    );
    const files = host.querySelector(".turn-actions__files");
    ok(files !== null, "⑤a 文件卡片渲染");
    eq(host.querySelectorAll(".turn-actions__file-chip").length, 2, "⑤a 两个文件 chip");
    eq(host.querySelector(".turn-actions__file-chip")?.textContent, "x.ts", "⑤a chip 只显文件名（basename）");
    eq(host.querySelector(".turn-actions__file-chip")?.getAttribute("title"), "src/x.ts", "⑤a chip title 带全路径");
    eq(host.querySelector(".turn-actions__files-label")?.textContent, "本轮文件", "⑤a 卡片标签文案");
    const tokens = host.querySelector(".turn-actions__tokens");
    eq(tokens?.textContent, "↑1,234 ↓5,678", "⑤a token 读数「↑输入 ↓输出」（toLocaleString 与状态栏同格式）");
    const time = host.querySelector(".turn-actions__time");
    ok(time !== null, "⑤a 完成时刻渲染（CardTime）");
    eq(time?.getAttribute("datetime"), new Date(doneAt).toISOString(), "⑤a 时刻 dateTime 为 ISO（1122 CardTime 契约）");
    await cleanup();
  }
  // ⑤b 折叠展开：6 文件默认 5 chip +「+1 个文件」，点击展开全部，再点收起。
  {
    const six = ["f1.ts", "f2.ts", "f3.ts", "f4.ts", "f5.ts", "f6.ts"];
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={1} tail={{ files: six, doneAt }} />,
    );
    eq(host.querySelectorAll(".turn-actions__file-chip").length, 5, "⑤b 默认只显示前 5 个 chip");
    const toggle = host.querySelector(".turn-actions__files-toggle");
    ok(toggle !== null, "⑤b 折叠按钮存在");
    eq(toggle?.textContent, "+1 个文件", "⑤b 按钮文案「+1 个文件」");
    eq(toggle?.getAttribute("aria-expanded"), "false", "⑤b aria-expanded=false（折叠态）");
    await act(async () => {
      toggle!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    eq(host.querySelectorAll(".turn-actions__file-chip").length, 6, "⑤b 展开后 6 个 chip 全显");
    eq(host.querySelector(".turn-actions__files-toggle")?.textContent, "收起", "⑤b 展开后按钮变「收起」");
    eq(host.querySelector(".turn-actions__files-toggle")?.getAttribute("aria-expanded"), "true", "⑤b aria-expanded=true（展开态）");
    await act(async () => {
      host.querySelector(".turn-actions__files-toggle")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    eq(host.querySelectorAll(".turn-actions__file-chip").length, 5, "⑤b 再点收起回到 5 chip");
    await cleanup();
  }
  // ⑤c ≤5 文件不出现折叠按钮。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={1} tail={{ files: ["a.ts", "b.ts"], doneAt }} />,
    );
    eq(host.querySelector(".turn-actions__files-toggle"), null, "⑤c 不足阈值无折叠按钮");
    await cleanup();
  }
  // ⑤d 多回合各自显示：两行各见各的三要素。
  {
    const { host, cleanup } = await renderIn(
      <>
        <TurnActions text="一" turn={1} tail={{ files: ["one.ts"], usage: { inputTokens: 10, outputTokens: 20 }, doneAt }} />
        <TurnActions text="二" turn={2} tail={{ files: ["two-a.ts", "two-b.ts"], usage: { inputTokens: 30, outputTokens: 40 }, doneAt: doneAt + 60000 }} />
      </>,
    );
    const metas = host.querySelectorAll(".turn-actions__meta");
    eq(metas.length, 2, "⑤d 两个回合各渲染一条 meta 带");
    const allTokens = host.querySelectorAll(".turn-actions__tokens");
    eq(allTokens[0]?.textContent, "↑10 ↓20", "⑤d 回合一自身 token");
    eq(allTokens[1]?.textContent, "↑30 ↓40", "⑤d 回合二自身 token（非累计）");
    const fileCards = host.querySelectorAll(".turn-actions__files");
    eq(fileCards.length, 2, "⑤d 两个回合各渲染一张文件卡片");
    eq(fileCards[0]?.querySelectorAll(".turn-actions__file-chip").length, 1, "⑤d 回合一 1 个文件");
    eq(fileCards[1]?.querySelectorAll(".turn-actions__file-chip").length, 2, "⑤d 回合二 2 个文件");
    await cleanup();
  }
  // ⑤e 零工具纯对话轮：无文件卡片，token/时刻仍在（运行时快照）。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={1} tail={{ files: [], usage: { inputTokens: 5, outputTokens: 6 }, doneAt }} />,
    );
    eq(host.querySelector(".turn-actions__files"), null, "⑤e 纯对话轮无文件卡片");
    ok(host.querySelector(".turn-actions__tokens") !== null, "⑤e 纯对话轮 token 仍在");
    ok(host.querySelector(".turn-actions__time") !== null, "⑤e 纯对话轮完成时刻仍在");
    await cleanup();
  }
  // ⑤f 历史回合降级：无 usage 快照 → 只显示时刻不显示 token。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={1} tail={{ files: ["h.ts"], doneAt }} />,
    );
    eq(host.querySelector(".turn-actions__tokens"), null, "⑤f 历史回合无 token 读数");
    ok(host.querySelector(".turn-actions__time") !== null, "⑤f 历史回合时刻仍在（createdAt 降级）");
    ok(host.querySelector(".turn-actions__files") !== null, "⑤f 历史回合文件卡片仍在（items 可得）");
    await cleanup();
  }
  // ⑤g estimated 快照：token 读数带 ≈ 前缀（与状态栏 markEstimated 同语义）。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={1} tail={{ files: [], usage: { inputTokens: 7, outputTokens: 8, estimated: true } }} />,
    );
    eq(host.querySelector(".turn-actions__tokens")?.textContent, "≈↑7 ↓8", "⑤g estimated 读数带 ≈ 前缀");
    await cleanup();
  }
  // ⑤h 三要素皆无（极端降级）：meta 与卡片整段不渲染，工具栏行本体仍在。
  {
    const { host, cleanup } = await renderIn(
      <TurnActions text="答" turn={1} tail={{ files: [] }} />,
    );
    eq(host.querySelector(".turn-actions__meta"), null, "⑤h 无 usage 无时刻不渲染 meta");
    eq(host.querySelector(".turn-actions__files"), null, "⑤h 无文件不渲染卡片");
    ok(host.querySelector(".turn-actions") !== null, "⑤h 工具栏行本体仍在");
    await cleanup();
  }
}

// ── ⑦ CSS 源码钉住：信息带四条规则存在 ────────────────────────────────────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  const css = readFileSync(resolve(here, "../styles.css"), "utf8");
  ok(/\.turn-actions__meta \{[^}]*flex:\s*0 0 auto;/s.test(css), "⑦ .turn-actions__meta 规则存在（行内空白区）");
  ok(/\.turn-actions__files \{[^}]*flex:\s*0 0 100%;/s.test(css), "⑦ .turn-actions__files 占满换行（flex-basis 100%）");
  ok(/\.turn-actions__file-chip \{[^}]*text-overflow:\s*ellipsis;/s.test(css), "⑦ 文件 chip 溢出省略");
  ok(/\.turn-actions__files-toggle \{[^}]*cursor:\s*pointer;/s.test(css), "⑦ 折叠按钮可点（pointer）");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
