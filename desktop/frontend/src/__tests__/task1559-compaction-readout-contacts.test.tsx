// Run: tsx src/__tests__/task1559-compaction-readout-contacts.test.tsx
//
// tl1559 验收（tl556/973 复发续作：压缩实时读数 UI 触点补全）：
//  ① ContextPanel（右上上下文窗口卡）接 compactionLiveReadout——压缩进行中
//     显示 tokens+t/s（刀2）；② 底部折叠头条 readout 空窗（summarizer TTFT，
//     首 token 估计未到）降级为「估算中…」占位腿，不再裸秒数半截读数（刀1）；
//  ③ 首 chunk 延迟机制核实：后端 gate 不卡首帧（compact_live.go 首个 emission
//     绕过 1s 节流间隔），空窗=TTFT，前端 startedAt+估算占位兜底（本文件钉）；
//  ④ 既有 556/973 钉零回退：降级是 opt-in（estimatingWhenEmpty），压缩卡与
//     无 opts 调用面的输出逐字节不变（Part A 钉死兼容形态）。
//
// 刀1（本文件初始版）：helper 降级参数化 + 折叠头条接线钉。
// 刀2 追加：compactionWindowOverride 读数源字段 + ContextPanel 容量卡渲染钉。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { ProcessFoldHeader } from "../components/ProcessFoldHeader";
import { ContextPanel } from "../components/ContextPanel";
import { compactionLiveReadout } from "../lib/compactionLive";
import { compactionWindowOverride } from "../lib/contextPanelUtils";
import type { Item } from "../lib/useController";
import type { ContextInfo, ContextPanelInfo } from "../lib/types";
import { en } from "../locales/en";
import type { Translator } from "../lib/i18n";

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
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// en 字典直灌的最小 Translator（973 同款拼装契约）。
const t: Translator = ((key: keyof typeof en, vars?: Record<string, string | number>) => {
  let out: string = en[key] ?? String(key);
  if (vars) {
    for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, String(v));
  }
  return out;
}) as Translator;

// ── Part A: compactionLiveReadout 降级参数化（opt-in，无 opts 逐字节兼容）────

{
  process.stdout.write("helper: estimatingWhenEmpty opt-in degradation\n");
  const now = 1_000_000;

  // tl1559 ②: 有戳无 tokens（TTFT 空窗）→ 裸秒数升格为「秒数 · 估算中…」。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 7_000 }, now, t, { estimatingWhenEmpty: true }),
    " · 7s · estimating…",
    "TTFT window: bare elapsed upgrades to elapsed + estimating leg",
  );

  // tokens 已到 → 实数腿照常，绝不出现估算占位。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 40_000, tokens: 12300, tokensPerSec: 45 }, now, t, { estimatingWhenEmpty: true }),
    " · 40s · 12.3K tokens · 45 tokens/s",
    "tokens present: full readout, no estimating leg",
  );
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 5_000, tokens: 800, tokensPerSec: 0 }, now, t, { estimatingWhenEmpty: true }),
    " · 5s · 800 tokens",
    "tokens below tps noise floor: tokens leg without tps, no estimating leg",
  );

  // 水合 pending 卡（无内存戳）→ 空串零残值，降级不复活静态卡。
  eq(compactionLiveReadout({ pending: true }, now, t, { estimatingWhenEmpty: true }), "", "hydrated pending card: no stamp → still empty");

  // 非 pending → 空串（降级不越界到 resolved/failed 卡）。
  eq(
    compactionLiveReadout({ pending: false, startedAt: now - 40_000 }, now, t, { estimatingWhenEmpty: true }),
    "",
    "resolved card: empty even with the opt-in",
  );

  // ④ 兼容钉：无 opts 的调用面（973 压缩卡）保持既有精确串——TTFT 空窗仍是裸秒数。
  eq(compactionLiveReadout({ pending: true, startedAt: now - 1_000 }, now, t), " · 1s", "legacy (no opts): bare elapsed byte-for-byte");
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 90_000 }, now, t),
    " · 1m 30s",
    "legacy (no opts): minute-formatted elapsed byte-for-byte",
  );
}

// ── Part B: 折叠头条（底部条）接线渲染 ───────────────────────────────────────

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

const dom = installDom();
const container = dom.window.document.getElementById("root") as HTMLElement;
let root: Root | null = null;

function renderHeader(compactionItem: Item) {
  const segment = {
    key: "seg-1559",
    processItems: [compactionItem],
    outsideItems: [],
    displayItems: [compactionItem],
    hasOutsideContent: false,
    foldActive: false,
    hasRunningWork: compactionItem.kind === "compaction" ? compactionItem.pending : false,
    durationMs: 0,
    labelStyle: "full",
    turnItems: [],
  } as unknown as Parameters<typeof ProcessFoldHeader>[0]["segment"];
  const header = React.createElement(ProcessFoldHeader, {
    segment,
    open: false,
    onToggle: () => {},
  });
  root!.render(React.createElement(LocaleProvider, null, header));
}

function headerLabel(): string {
  const el = container.querySelector(".turn-collapse__label, .readonly-batch__label");
  return el ? el.textContent ?? "" : "";
}

// LocaleProvider 在 jsdom 里可能落到任一语言——占位腿两形态都收。
const estimatingShapes = ["estimating…", "估算中…", "估算中…"];

try {
  process.stdout.write("component: ProcessFoldHeader TTFT-window degradation\n");
  root = createRoot(container);

  // 有戳无 tokens（TTFT 空窗）→ 头条带秒数 + 估算占位，不再是半截读数。
  const ttftWindow: Item = { kind: "compaction", id: "c1559a", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", startedAt: Date.now() - 7_000 };
  act(() => renderHeader(ttftWindow));
  let label = headerLabel();
  ok(elapsedShapePresent(label, 7), `TTFT window: strip keeps the elapsed leg: ${JSON.stringify(label)}`);
  ok(estimatingShapes.some((shape) => label.includes(shape)), `TTFT window: strip shows the estimating placeholder: ${JSON.stringify(label)}`);

  // tokens 已到 → 实数腿照常（占位消失）。
  const withTokens: Item = { kind: "compaction", id: "c1559b", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", startedAt: Date.now() - 40_000, tokens: 12300, tokensPerSec: 45 };
  act(() => renderHeader(withTokens));
  label = headerLabel();
  ok(label.includes("12.3K tokens") && label.includes("45 tokens/s"), `tokens landed: strip shows the real readout: ${JSON.stringify(label)}`);
  ok(!estimatingShapes.some((shape) => label.includes(shape)), `tokens landed: estimating placeholder gone: ${JSON.stringify(label)}`);

  // 水合态（无戳无 tokens）→ 无读数（与 556 基线一致，降级不越界）。
  const hydrated: Item = { kind: "compaction", id: "c1559c", pending: true, trigger: "auto", messages: 0, summary: "", archive: "" };
  act(() => renderHeader(hydrated));
  label = headerLabel();
  ok(!estimatingShapes.some((shape) => label.includes(shape)) && !/\d+s\b/.test(label) && !label.includes("tokens"), `hydrated: no readout, static label only: ${JSON.stringify(label)}`);

  await act(async () => {
    root?.unmount();
    root = null;
  });
} catch (err) {
  ok(false, `component test threw: ${err}`);
}

// 秒数腿两语言形态（"Ns" / "N秒"）。
function elapsedShapePresent(label: string, seconds: number): boolean {
  return new RegExp(`\\b${seconds}s\\b`).test(label) || label.includes(`${seconds}秒`) || label.includes(`${seconds} 秒`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);

// ── Part C: compactionWindowOverride 读数源字段（刀2 数据面）─────────────────

{
  process.stdout.write("override: running phase carries the live readout source\n");
  const running: Item[] = [
    { kind: "compaction", id: "c1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", done: 6, total: 22, tokens: 12300, tokensPerSec: 45, startedAt: 1_000 },
  ];
  const override = compactionWindowOverride(running);
  ok(override?.phase === "running" && override.pending === true, "running: phase + pending source leg");
  eq(override?.tokens, 12300, "running: tokens ride through");
  eq(override?.tokensPerSec, 45, "running: tps rides through");
  eq(override?.startedAt, 1_000, "running: startedAt rides through");
  eq(override?.done, 6, "running: done/total unchanged (1147 钉不回退)");
  eq(override?.total, 22, "running: total unchanged");

  // failed 相不带读数源（失败卡无 live 数字）。
  const failedOnly: Item[] = [{ kind: "compaction", id: "c0", pending: false, failed: true, attempt: 2, reason: "x", trigger: "", messages: 0, summary: "", archive: "" }];
  const failedOverride = compactionWindowOverride(failedOnly);
  ok(failedOverride?.phase === "failed" && failedOverride.tokens === undefined && failedOverride.startedAt === undefined, "failed: no live source fields");

  // 直接喂共享 helper：结构兼容 CompactionLiveSource，非 pending 自卫返回空。
  eq(
    override ? compactionLiveReadout(override, 41_000, t, { estimatingWhenEmpty: true }) : "no-override",
    " · 40s · 12.3K tokens · 45 tokens/s",
    "override feeds the shared helper directly (same seam)",
  );
}

// ── Part D: ContextPanel 容量卡渲染（修法①：右上上下文窗口卡接线）────────────

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function emptyPanelInfo(): ContextPanelInfo {
  return {
    usedTokens: 0,
    windowTokens: 0,
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    reasoningTokens: 0,
    cacheHitTokens: 0,
    cacheMissTokens: 0,
    sessionCacheHitTokens: 0,
    sessionCacheMissTokens: 0,
    sessionCompletionTokens: 0,
    requestCount: 0,
    elapsedMs: 0,
    sessionCost: 0,
    readFiles: [],
    changedFiles: [],
  };
}

const panelDom = installDom();
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = TestResizeObserver;
const goStub = { main: { App: { ContextPanel: async () => emptyPanelInfo() } } };
(window as unknown as { go: unknown }).go = goStub;
const panelRootEl = panelDom.window.document.getElementById("root") as HTMLElement;

async function renderPanel(context: ContextInfo, items?: Item[]): Promise<Root> {
  const scenarioRoot = createRoot(panelRootEl);
  await act(async () => {
    scenarioRoot.render(
      React.createElement(LocaleProvider, null, React.createElement(ContextPanel, { tabId: "tab-1559", context, items })),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  return scenarioRoot;
}

async function unmountPanel(scenarioRoot: Root) {
  await act(async () => {
    scenarioRoot.unmount();
  });
}

function capacityLiveText(): string | null {
  return panelRootEl.querySelector(".context-panel__capacity-live")?.textContent ?? null;
}

function capacityStatusText(): string {
  return panelRootEl.querySelector(".context-panel__capacity-status")?.textContent ?? "";
}

const overLimitContext: ContextInfo = { used: 1_300_000, window: 1_000_000, sessionTokens: 1_300_000, compactRatio: 0.8 };

try {
  process.stdout.write("component: ContextPanel capacity card live readout\n");
  let panelRoot = await renderPanel(overLimitContext, [
    { kind: "compaction", id: "cp1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", done: 6, total: 22, tokens: 12300, tokensPerSec: 45, startedAt: Date.now() - 40_000 },
  ]);
  let live = capacityLiveText();
  ok(live !== null, "running: the live readout span renders");
  ok(live !== null && live.includes("12.3K tokens") && live.includes("45 tokens/s"), `running: tokens + tps visible (acceptance ①): ${JSON.stringify(live)}`);
  ok(live !== null && elapsedShapePresent(live!, 40), `running: elapsed leg leads: ${JSON.stringify(live)}`);
  // 1147 钉保持：状态行本身是纯标签（读数走独立 span，不污染精确串钉）。
  ok(/(正在压缩…6\/22|正在壓縮…6\/22|Compacting… 6\/22)/.test(capacityStatusText()), `running: status pill stays the pure 1147 label: ${JSON.stringify(capacityStatusText())}`);
  await unmountPanel(panelRoot);

  // TTFT 空窗（有戳无 tokens）→ 降级形态「估算中…」。
  panelRoot = await renderPanel(overLimitContext, [
    { kind: "compaction", id: "cp2", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", startedAt: Date.now() - 7_000 },
  ]);
  live = capacityLiveText();
  ok(live !== null && estimatingShapes.some((shape) => live!.includes(shape)), `TTFT window: capacity card degrades to the estimating shape: ${JSON.stringify(live)}`);
  await unmountPanel(panelRoot);

  // 完成卡 → 无读数 span（压缩后读数即时照常，零残值）。
  panelRoot = await renderPanel(overLimitContext, [
    { kind: "compaction", id: "cp3", pending: false, trigger: "auto", messages: 40, summary: "done", archive: "" },
  ]);
  ok(capacityLiveText() === null, `done: no live span (post-compaction readout untouched): ${JSON.stringify(capacityLiveText())}`);
  await unmountPanel(panelRoot);

  // 无压缩卡 → 基线 DOM 逐字节（无 live span）。
  panelRoot = await renderPanel(overLimitContext);
  ok(capacityLiveText() === null, `no compaction: baseline DOM without the live span: ${JSON.stringify(capacityLiveText())}`);
  await unmountPanel(panelRoot);
} catch (err) {
  ok(false, `ContextPanel test threw: ${err}`);
}

panelDom.window.close();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
