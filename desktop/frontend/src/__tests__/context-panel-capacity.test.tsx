// Run: tsx src/__tests__/context-panel-capacity.test.tsx

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextPanel } from "../components/ContextPanel";
import { capacityPinLayout } from "../lib/contextWindow";
import { LocaleProvider } from "../lib/i18n";
import type { ContextPanelInfo } from "../lib/types";

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

console.log("\ncontext panel capacity");

// 任务834: pin 防重叠判定钉——clamp 边界 + 重叠带内阈值标签让位。
{
  const overlap = capacityPinLayout(79, 80);
  ok(overlap.compactLabelVisible === false, "pin layout hides the threshold label inside the overlap band");
  const clear = capacityPinLayout(50, 80);
  eq(clear.compactLabelVisible, true, "pin layout keeps the threshold label when clear of the usage pin");
  eq(clear.usagePct, 50, "pin layout keeps mid usage position");
  eq(clear.compactPct, 80, "pin layout keeps the threshold position");
  const overLimit = capacityPinLayout(101, 80);
  eq(overLimit.usagePct, 94, "usage pin clamps to the track-safe maximum");
  ok(overLimit.compactLabelVisible === false, "over-limit usage also suppresses the overlapping threshold label");
  const lowClamp = capacityPinLayout(0, 0);
  eq(lowClamp.usagePct, 6, "pins clamp off the left track edge");
  eq(lowClamp.compactPct, 6, "threshold clamp matches the usage clamp floor");
}

const dom = installDom();
(window as unknown as { go: { main: { App: { ContextPanel: () => Promise<ContextPanelInfo> } } } }).go = {
  main: { App: { ContextPanel: async () => emptyPanelInfo() } },
};
const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");

// React 19: an unmounted root is final, so each scenario mounts a fresh one.
async function renderScenario(context: ContextInfo): Promise<ReturnType<typeof createRoot>> {
  const scenarioRoot = createRoot(rootEl);
  await act(async () => {
    scenarioRoot.render(
      <LocaleProvider>
        <ContextPanel tabId="tab-capacity" context={context} />
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

const root = await renderScenario({ used: 1_001, window: 1_000, sessionTokens: 1_001, compactRatio: 0.8 });

const capacity = document.querySelector(".context-panel__capacity-card");
const meter = capacity?.querySelector(".context-panel__capacity-meter");
const fill = meter?.querySelector(".context-panel__progress-fill") as HTMLElement | null;
const usedPin = meter?.querySelector(".context-panel__capacity-pin--used");
const compactMarker = meter?.querySelector(".context-panel__compact-marker") as HTMLElement | null;

// 任务834: the dictionary loads async and locale follows the OS (same race the
// projection-stale assertion below already accommodates) — the two hard-coded
// English assertions above it became order-sensitive once this file grew
// synchronous preamble work. Accept any shipped translation; the point is that
// the explicit over-limit label and the aria summary render at all.
const overLimitStatusText = capacity?.querySelector(".context-panel__capacity-status")?.textContent ?? "";
ok(["Over context limit", "已超过窗口上限", "已超過視窗上限"].includes(overLimitStatusText),
  `over-limit status is explicit (got ${JSON.stringify(overLimitStatusText)})`);
eq(usedPin?.textContent, "101%", "just-over-limit percentage pin remains visibly over 100 percent");
eq(fill?.style.width, "100%", "capacity fill is capped at the physical track width");
eq(meter?.querySelectorAll(".context-panel__progress-segment").length, 0, "capacity meter does not mix token composition into its fill");
eq(compactMarker?.style.left, "80%", "compression threshold marker stays at the configured ratio");
const ariaSummary = meter?.getAttribute("aria-label") ?? "";
// zh and zh-TW share the "{pct}% 已用" shape.
ok(["101% used", "101% 已用"].some((phrase) => ariaSummary.includes(phrase)), "accessible summary reports the over-limit ratio");
// 任务834: over-limit 时已用 pin（clamp 至 94%）与阈值 pin（80%）落入重叠带，
// 阈值文字标签让位；竖线 marker 上一行断言已保证仍在。
eq(meter?.querySelector(".context-panel__capacity-pin--compact"), null, "over-limit usage pin is never covered by the threshold label");
ok((capacity?.querySelector(".context-panel__capacity-top strong") as HTMLElement | null)?.getAttribute("title")?.includes("101%") === true,
  "used/window figure carries a hover title with the full readout");

await unmountScenario(root);

// 任务549: while the projection is INVALID the panel must say so — the huge
// unprojected readout may not present as normal usage (acceptance ④).
const staleRoot = await renderScenario({
  used: 3_700_000, window: 1_000_000, sessionTokens: 3_700_000, compactRatio: 0.8,
  maintenance: { canonicalTokens: 3_700_000, projectedTokens: 3_700_000, projectionValid: false, triggerTokens: 800_000 },
});

const staleCapacity = document.querySelector(".context-panel__capacity-card");
const staleBadge = staleCapacity?.querySelector(".context-panel__projection-stale");
const staleBadgeText = staleBadge?.textContent ?? "";
// Locale detection follows the OS and dictionaries load async, so accept any
// shipped translation — the point is that the badge renders its label.
ok(["Projection invalid · full transcript", "投影失效 · 全量回退中"].includes(staleBadgeText),
  `projection-invalid state is labelled explicitly (got ${JSON.stringify(staleBadgeText)})`);
ok((staleBadge as HTMLElement | null)?.getAttribute("title")?.includes("3.7M") === true, "badge tooltip carries the canonical figure");
const staleStrong = staleCapacity?.querySelector(".context-panel__capacity-top strong")?.textContent ?? "";
ok(staleStrong.startsWith("3.7"), "fallback readout stays the actual next-send size while invalid");

await unmountScenario(staleRoot);

// 任务549: with a VALID projection the main readout is the projected (actual
// send) figure — 264k, not the 3.7M canonical bulk — and no badge shows.
const validRoot = await renderScenario({
  used: 264_000, window: 1_000_000, sessionTokens: 264_000, compactRatio: 0.8,
  maintenance: { canonicalTokens: 3_700_000, projectedTokens: 264_000, projectionValid: true, triggerTokens: 800_000 },
});

const validCapacity = document.querySelector(".context-panel__capacity-card");
eq(validCapacity?.querySelector(".context-panel__projection-stale"), null, "valid projection shows no invalid badge");
const validStrong = validCapacity?.querySelector(".context-panel__capacity-top strong")?.textContent ?? "";
ok(validStrong.startsWith("264"), "main readout prefers the projected (actual send) figure over canonical");
ok(!validStrong.includes("3.7"), "valid projection never surfaces the canonical bulk in the main readout");

await unmountScenario(validRoot);

// ── 任务1025: 读数来源标记——「实测」/「估算」+ 预算卡移除 + 压缩线 tooltip ──
// observedPrompt>0（上次响应已校准）→ 「实测」标记；=0（新会话首轮/切 provider
// 首轮）→ 「估算」标记。两场景都渲染在容量卡头部。
{
  const measuredRoot = await renderScenario({
    used: 546_500, window: 1_000_000, sessionTokens: 546_500, compactRatio: 0.8,
    maintenance: { projectedTokens: 546_500, projectionValid: true, triggerTokens: 800_000,
      contextBudget: { windowTokens: 1_000_000, promptTokens: 545_900, observedPrompt: 545_900 } },
  });
  const measuredCapacity = document.querySelector(".context-panel__capacity-card");
  const measuredBadge = measuredCapacity?.querySelector(".context-panel__readout-source");
  const measuredBadgeText = measuredBadge?.textContent ?? "";
  ok(["实测", "Measured", "實測"].includes(measuredBadgeText),
    `measured scenario renders the 实测 marker (got ${JSON.stringify(measuredBadgeText)})`);
  ok((measuredBadge as HTMLElement | null)?.getAttribute("title") !== null,
    "the readout-source marker carries a tooltip explaining 实测/估算");
  // 验收⑦: 预算卡已整卡移除——上下文面板不再有预算卡区段。
  eq(document.querySelector(".context-panel__budget"), null, "the context budget card is removed (验收⑦)");
  eq(document.querySelector(".context-panel__budget-grid"), null, "the budget grid is removed with the card");
  // 验收③: 压缩线来源 tooltip 可见。
  const compactMarker = measuredCapacity?.querySelector(".context-panel__compact-marker");
  ok((compactMarker as HTMLElement | null)?.getAttribute("title")?.length ?? 0 > 0,
    "the compact-line marker carries a tooltip naming its source (窗口×压缩比)");
  await unmountScenario(measuredRoot);

  const estimatedRoot = await renderScenario({
    used: 1_200, window: 1_000_000, sessionTokens: 1_200, compactRatio: 0.8,
    maintenance: { projectedTokens: 1_200, projectionValid: true, triggerTokens: 800_000,
      contextBudget: { windowTokens: 1_000_000, promptTokens: 0, observedPrompt: 0 } },
  });
  const estimatedCapacity = document.querySelector(".context-panel__capacity-card");
  const estimatedBadgeText = estimatedCapacity?.querySelector(".context-panel__readout-source")?.textContent ?? "";
  ok(["估算", "Estimated", "估算"].includes(estimatedBadgeText),
    `first-turn scenario renders the 估算 marker (got ${JSON.stringify(estimatedBadgeText)})`);
  await unmountScenario(estimatedRoot);
}

// 任务834: 渲染场景钉——已用读数贴近压缩阈值（79% vs 80%）时阈值 pin 的
// 文字标签让位，两个「NN%」不再互相遮挡；远离阈值时照常双 pin 显示。
const nearRoot = await renderScenario({ used: 790, window: 1_000, sessionTokens: 790, compactRatio: 0.8 });
const nearMeter = document.querySelector(".context-panel__capacity-meter");
eq(nearMeter?.querySelector(".context-panel__capacity-pin--used")?.textContent, "79%", "near-threshold usage pin still renders its percentage");
eq(nearMeter?.querySelector(".context-panel__capacity-pin--compact"), null, "near-threshold threshold pin label yields to the usage pin");
ok((nearMeter?.querySelector(".context-panel__compact-marker") as HTMLElement | null)?.style.left === "80%", "threshold line marker stays put while its label yields");
await unmountScenario(nearRoot);

const midRoot = await renderScenario({ used: 500, window: 1_000, sessionTokens: 500, compactRatio: 0.8 });
const midMeter = document.querySelector(".context-panel__capacity-meter");
eq(midMeter?.querySelector(".context-panel__capacity-pin--used")?.textContent, "50%", "mid usage pin renders");
eq(midMeter?.querySelector(".context-panel__capacity-pin--compact")?.textContent, "80%", "threshold pin label renders when clear of the usage pin");
await unmountScenario(midRoot);

dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
