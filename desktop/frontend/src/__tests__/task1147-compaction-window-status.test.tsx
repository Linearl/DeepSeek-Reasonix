// Run: tsx src/__tests__/task1147-compaction-window-status.test.tsx
//
// 任务1147（压缩域 UI 可见性）：分块压缩进行中容量卡不得钉着「已超过窗口
// 上限」红条读作失败——2026-10-24 用户实测 1.3M→250K 压缩全程被误读。
// 四态断言：
//   ① 压缩中（有进度）→「正在压缩…N/22」中性态替代超限红条；
//  ② 压缩中（进度未到）→「正在压缩对话…」；
//  ③ 完成后 → 状态行恢复真实窗口读数（超限时明确显示超限，非误导）；
//  ④ 失败卡 → 独立失败文案（与进行中区分）。
// 另加 notice 映射：压缩运行态的静默告警（turn_stalled_compacting）渲染
// 中性文案，不再出现「请点击停止」。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextPanel } from "../components/ContextPanel";
import { compactionWindowOverride } from "../lib/contextPanelUtils";
import { localizedNoticeText } from "../lib/controllerNotices";
import { LocaleProvider } from "../lib/i18n";
import type { ContextInfo, ContextPanelInfo } from "../lib/types";
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
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function wait(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.ResizeObserver = TestResizeObserver;
  return dom;
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

// ── helper 单元钉：最新压缩卡决定阶段 ──
{
  const done: Item[] = [{ kind: "compaction", id: "c0", pending: false, trigger: "", messages: 3, summary: "s", archive: "" }];
  eq(compactionWindowOverride(undefined), null, "no items → no override");
  eq(compactionWindowOverride([]), null, "empty items → no override");
  eq(compactionWindowOverride(done), null, "finished compaction card → no override (post-readout shows)");
  const running: Item[] = [...done, { kind: "compaction", id: "c1", pending: true, trigger: "", messages: 0, summary: "", archive: "", done: 6, total: 22 }];
  const runningOverride = compactionWindowOverride(running);
  ok(runningOverride?.phase === "running" && runningOverride.done === 6 && runningOverride.total === 22, "pending card wins as the latest phase");
  const failedThenRunning: Item[] = [
    { kind: "compaction", id: "c0", pending: false, failed: true, attempt: 1, reason: "x", trigger: "", messages: 0, summary: "", archive: "" },
    { kind: "compaction", id: "c1", pending: true, trigger: "", messages: 0, summary: "", archive: "", done: 2, total: 22 },
  ];
  eq(compactionWindowOverride(failedThenRunning)?.phase, "running", "a retried pass supersedes an older failed card");
  const onlyFailed: Item[] = [{ kind: "compaction", id: "c0", pending: false, failed: true, attempt: 2, reason: "x", trigger: "", messages: 0, summary: "", archive: "" }];
  ok(compactionWindowOverride(onlyFailed)?.phase === "failed", "failed card maps to the failed phase");
}

const overLimitContext: ContextInfo = { used: 1_300_000, window: 1_000_000, sessionTokens: 1_300_000, compactRatio: 0.8 };

const dom = installDom();
(window as unknown as { go: { main: { App: { ContextPanel: () => Promise<ContextPanelInfo> } } } }).go = {
  main: { App: { App: { ContextPanel: async () => emptyPanelInfo() } as never } as never },
};
// capacity harness shape (also accept the flat App call used elsewhere)
(window as unknown as { go: { main: { App: { ContextPanel: () => Promise<ContextPanelInfo> } } } }).go = {
  main: { App: { ContextPanel: async () => emptyPanelInfo() } },
};
const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");

async function renderScenario(context: ContextInfo, items?: Item[]): Promise<ReturnType<typeof createRoot>> {
  const scenarioRoot = createRoot(rootEl);
  await act(async () => {
    scenarioRoot.render(
      <LocaleProvider>
        <ContextPanel tabId="tab-1147" context={context} items={items} />
      </LocaleProvider>,
    );
    await wait();
  });
  return scenarioRoot;
}

async function unmountScenario(scenarioRoot: ReturnType<typeof createRoot>) {
  await act(async () => {
    scenarioRoot.unmount();
  });
}

function capacityStatus(): { text: string; toneClass: string } {
  const card = document.querySelector(".context-panel__capacity-card");
  return {
    text: card?.querySelector(".context-panel__capacity-status")?.textContent ?? "",
    toneClass: card?.className ?? "",
  };
}

// 现场复刻：1.3M/1M（129%）+ 分块压缩 6/22 进行中。
const runningRoot = await renderScenario(overLimitContext, [
  { kind: "compaction", id: "c1", pending: true, trigger: "", messages: 0, summary: "", archive: "", done: 6, total: 22 },
]);
{
  const { text, toneClass } = capacityStatus();
  const compactingShapes = ["正在压缩…6/22", "正在壓縮…6/22", "Compacting… 6/22"];
  ok(compactingShapes.includes(text), `compacting status shows progress 6/22 (got ${JSON.stringify(text)})`);
  ok(!text.includes("上限") && !text.includes("limit") && !text.includes("上限"), "over-limit label is replaced while compacting");
  ok(toneClass.includes("capacity-card--notice"), `capacity card takes the neutral tone (got ${toneClass.match(/capacity-card--\w+/)?.[0]})`);
  ok(!toneClass.includes("capacity-card--warn"), "compacting never renders as the warn card");
}
await unmountScenario(runningRoot);

// 进度事件未到（done/total 缺省）→「正在压缩对话…」兜底文案。
const runningNoProgressRoot = await renderScenario(overLimitContext, [
  { kind: "compaction", id: "c1", pending: true, trigger: "", messages: 0, summary: "", archive: "" },
]);
{
  const { text } = capacityStatus();
  const fallbackShapes = ["正在压缩对话…", "正在壓縮對話…", "Compacting conversation…"];
  ok(fallbackShapes.includes(text), `no-progress compacting falls back to the working copy (got ${JSON.stringify(text)})`);
}
await unmountScenario(runningNoProgressRoot);

// 压缩完成（卡不再 pending）→ 状态行恢复真实读数；129% 明确显示超限（这是
// 压缩完成后的真实状态提示，不是误导）。
const doneRoot = await renderScenario(overLimitContext, [
  { kind: "compaction", id: "c1", pending: false, trigger: "", messages: 40, summary: "done", archive: "" },
]);
{
  const { text, toneClass } = capacityStatus();
  const overLimitShapes = ["已超过窗口上限", "已超過視窗上限", "Over context limit"];
  ok(overLimitShapes.includes(text), `post-compaction status returns to the real window readout (got ${JSON.stringify(text)})`);
  ok(toneClass.includes("capacity-card--warn"), "post-compaction over-limit keeps its own tone");
}
await unmountScenario(doneRoot);

// 失败卡 → 独立失败文案，与「进行中」可辨识。
const failedRoot = await renderScenario(overLimitContext, [
  { kind: "compaction", id: "c1", pending: false, failed: true, attempt: 2, reason: "boom", trigger: "", messages: 0, summary: "", archive: "" },
]);
{
  const { text, toneClass } = capacityStatus();
  const failedShapes = ["上下文压缩失败", "上下文壓縮失敗", "Context compaction failed"];
  ok(failedShapes.includes(text), `failed compaction shows its own copy (got ${JSON.stringify(text)})`);
  ok(!text.includes("正在") && !text.includes("Compacting"), "failed copy is distinct from the running copy");
  ok(toneClass.includes("capacity-card--warn"), "failed compaction renders as the warn card");
}
await unmountScenario(failedRoot);

// 无压缩卡 → 与改动前一致（基线行为不回归）。
const plainRoot = await renderScenario(overLimitContext);
{
  const { text } = capacityStatus();
  const overLimitShapes = ["已超过窗口上限", "已超過視窗上限", "Over context limit"];
  ok(overLimitShapes.includes(text), `no compaction card keeps the plain over-limit status (got ${JSON.stringify(text)})`);
}
await unmountScenario(plainRoot);

// notice 映射：压缩中的静默告警走中性文案，不含「停止/Stop」指令。
{
  const neutral = localizedNoticeText("No progress for 11m0s because a context compaction is running.", "turn_stalled_compacting");
  ok(/压缩|壓縮|compaction/i.test(neutral), `compacting stall notice maps to the neutral copy (got ${JSON.stringify(neutral)})`);
  ok(!/请点击停止|請點擊停止|press Stop/i.test(neutral), "compacting stall notice never urges pressing Stop (请勿停止 is the reverse and is fine)");
  const plain = localizedNoticeText("No progress for 11m0s.", "turn_stalled");
  ok(/进展|進展|progress/i.test(plain), `plain stall notice keeps its existing copy (got ${JSON.stringify(plain)})`);
}

dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
