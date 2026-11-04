// Run: tsx src/__tests__/context-window-ring.test.tsx

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextWindowRing } from "../components/ContextWindowRing";
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
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function contextPanelInfo(requestCount: number): ContextPanelInfo {
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
    requestCount,
    elapsedMs: 0,
    sessionCost: 0,
    sessionCurrency: "",
    readFiles: [],
    changedFiles: [],
  };
}

function installContextPanelMock(fn: (tabId: string) => Promise<ContextPanelInfo>) {
  (window as unknown as { go: { main: { App: { ContextPanel: typeof fn } } } }).go = {
    main: {
      App: {
        ContextPanel: fn,
      },
    },
  };
}

async function checkSeparateDurations() {
  const dom = installDom();
  installContextPanelMock(async () => ({ ...contextPanelInfo(12), elapsedMs: 120_000 }));
  const { root } = await renderRing({ turnMetrics: { elapsed: "20s", tokens: "104 tokens", tps: "12 tokens/s" } });
  await act(async () => {
    (document.querySelector(".context-ring") as HTMLButtonElement).click();
    await wait();
  });
  const rows = [...document.querySelectorAll(".context-ring-popover__row")];
  const value = (label: string) => rows.find(row => row.querySelector(".context-ring-popover__label")?.textContent === label)
    ?.querySelector(".context-ring-popover__value")?.textContent;
  eq(value("Turn time"), "20s", "turn duration has its own stable label");
  ok(Boolean(value("Session time")) && value("Session time") !== "20s", "session duration stays separate from turn duration");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

async function renderRing(props: Partial<Parameters<typeof ContextWindowRing>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: Parameters<typeof ContextWindowRing>[0] = {
    enabled: true,
    tabId: "tab-a",
    context: { used: 10, window: 100, compactRatio: 0.8 },
    ...props,
  };
  const paint = async (nextProps: Partial<Parameters<typeof ContextWindowRing>[0]> = {}) => {
    currentProps = { ...currentProps, ...nextProps };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ContextWindowRing {...currentProps} />
        </LocaleProvider>,
      );
      await wait();
    });
  };
  await paint();
  return { root, rerender: paint };
}

console.log("\ncontext window ring");

{
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo(2));
  const { root } = await renderRing();
  const trigger = document.querySelector<HTMLButtonElement>(".context-ring")!;
  eq(trigger.textContent, "10%", "ring exposes its percentage outside Creation layout");
  await act(async () => { trigger.focus(); trigger.click(); await wait(); });
  eq(trigger.getAttribute("aria-expanded"), "true", "keyboard activation opens usage details");
  await act(async () => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await wait(230);
  });
  eq(trigger.getAttribute("aria-expanded"), "false", "Escape cancels pending hover timers without reopening");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

{
  const dom = installDom();
  const calls: string[] = [];
  installContextPanelMock(async (tabId) => {
    calls.push(tabId);
    return contextPanelInfo(1);
  });

  const { root } = await renderRing({ enabled: false });

  eq(document.querySelector(".context-ring"), null, "disabled ring renders nothing");
  eq(calls.length, 0, "disabled ring does not request context panel data");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo(0));

  const { root } = await renderRing({ turnCost: 0.125, currency: "$" });
  const button = document.querySelector(".context-ring") as HTMLButtonElement | null;
  if (!button) throw new Error("missing context ring button");
  await act(async () => {
    button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await wait(220);
  });
  const turnCostRow = [...document.querySelectorAll(".context-ring-popover__row")]
    .find((row) => row.querySelector(".context-ring-popover__label")?.textContent === "turn cost");
  eq(
    turnCostRow?.querySelector(".context-ring-popover__value")?.textContent,
    "$0.1250",
    "turn cost uses the session currency before panel info is available",
  );

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo(0));

  const { root } = await renderRing({ context: { used: 1_001, window: 1_000, compactRatio: 0.8 } });
  const button = document.querySelector(".context-ring") as HTMLButtonElement | null;
  if (!button) throw new Error("missing over-limit context ring button");
  await act(async () => {
    button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await wait(220);
  });

  const popover = document.querySelector(".context-ring-popover");
  const fill = popover?.querySelector(".context-ring-popover__fill") as HTMLElement | null;
  eq(popover?.querySelector(".context-ring-popover__pct")?.textContent, "101%", "ring popover keeps a just-over-limit ratio visibly above 100 percent");
  eq(fill?.style.width, "100%", "ring popover fill is capped at the physical track width");
  eq(popover?.querySelectorAll(".context-ring-popover__seg").length, 0, "ring popover does not mix token composition into its capacity fill");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  const dom = installDom();
  const calls: string[] = [];
  const resolvers = new Map<string, (value: ContextPanelInfo) => void>();
  installContextPanelMock((tabId) => {
    calls.push(tabId);
    return new Promise<ContextPanelInfo>((resolve) => {
      resolvers.set(tabId, resolve);
    });
  });

  const { root, rerender } = await renderRing({ tabId: "old-tab" });
  const button = document.querySelector(".context-ring") as HTMLButtonElement | null;
  if (!button) throw new Error("missing context ring button");
  await act(async () => {
    button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await wait();
  });

  await rerender({ tabId: "new-tab" });
  const nextButton = document.querySelector(".context-ring") as HTMLButtonElement | null;
  if (!nextButton) throw new Error("missing context ring button after tab switch");
  await act(async () => {
    nextButton.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await wait();
  });

  await act(async () => {
    resolvers.get("new-tab")?.(contextPanelInfo(2));
    await wait();
  });
  await act(async () => {
    resolvers.get("old-tab")?.(contextPanelInfo(1));
    await wait();
  });
  await act(async () => {
    nextButton.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await wait(220);
  });

  eq(calls[0], "old-tab", "old tab request starts first");
  eq(calls[1], "new-tab", "new tab request starts after tab switch");
  const requestRow = [...document.querySelectorAll(".context-ring-popover__row")]
    .find((row) => row.querySelector(".context-ring-popover__label")?.textContent === "Requests");
  eq(
    requestRow?.querySelector(".context-ring-popover__value")?.textContent,
    "2",
    "stale old-tab response cannot overwrite the new tab info",
  );

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  // tl1379 功能层：重置会话费用入口——确认框把门、取消不触发。开轮前先等
  // 250ms 让前序场景留下的 queryCoalesce 窗口（200ms）过期，否则模块级
  // inflight 缓存会吞掉本场景的 ContextPanel 调用（跨场景共享导致）。
  const dom = installDom();
  const resetCalls: string[] = [];
  const panelFetches: string[] = [];
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        ContextPanel: async (tabId: string) => {
          panelFetches.push(tabId);
          return contextPanelInfo(panelFetches.length);
        },
        ClearSessionCostForTab: async (tabId?: string) => {
          resetCalls.push(tabId ?? "");
        },
      },
    },
  };
  const { root } = await renderRing();
  await act(async () => { await wait(250); });
  await act(async () => {
    (document.querySelector(".context-ring") as HTMLButtonElement).click();
    await wait();
  });
  const resetButton = document.querySelector<HTMLButtonElement>(".context-ring-popover__reset");
  ok(Boolean(resetButton), "reset cost entry renders in the popover");

  await act(async () => { resetButton!.click(); await wait(); });
  const dialog = document.querySelector(".reasonix-confirm-dialog");
  ok(Boolean(dialog), "reset asks for confirmation before clearing");
  // 取消键按结构（非 danger 主键）定位，文案随语言变。
  const cancelButton = dialog?.querySelector("button.btn:not(.btn--danger)");
  await act(async () => { cancelButton?.dispatchEvent(new MouseEvent("click", { bubbles: true })); await wait(); });
  eq(resetCalls.length, 0, "cancelling the dialog keeps the accrued cost");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  // tl1379 功能层：确认后调用桌面命令一次，并重拉 ContextPanel 让读数归零
  // 上屏。ClearSessionCostForTab 命中 queryCoalesce 的 mutation 前缀表，
  // 刷新不会被 200ms 合并窗口吞掉；先等 250ms 隔离前序场景缓存。
  const dom = installDom();
  const resetCalls: string[] = [];
  const panelFetches: string[] = [];
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        ContextPanel: async (tabId: string) => {
          panelFetches.push(tabId);
          return contextPanelInfo(panelFetches.length);
        },
        ClearSessionCostForTab: async (tabId?: string) => {
          resetCalls.push(tabId ?? "");
        },
      },
    },
  };
  const { root } = await renderRing();
  await act(async () => { await wait(250); });
  await act(async () => {
    (document.querySelector(".context-ring") as HTMLButtonElement).click();
    await wait();
  });
  const resetButton = document.querySelector<HTMLButtonElement>(".context-ring-popover__reset");
  await act(async () => { resetButton!.click(); await wait(); });
  const confirmButton = document.querySelector(".reasonix-confirm-dialog")?.querySelector("button.btn--danger");
  ok(Boolean(confirmButton), "confirm dialog exposes the danger confirm button");
  await act(async () => { confirmButton!.dispatchEvent(new MouseEvent("click", { bubbles: true })); await wait(); });
  eq(resetCalls.length, 1, "confirmed reset calls the desktop command once");
  eq(resetCalls[0], "tab-a", "reset targets the popover's tab");
  ok(panelFetches.length >= 2, "panel data refreshes after a confirmed reset");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

{
  // tl1386: partial 态显示简化——0 元权威总额（全无定价按 0）在弹层里渲染
  // ¥0.00 而非「≈-」，且不再出现「incl. N unpriced」行标注；tl1379 数据层
  // 口径（partialPricedCount 等）不动。
  const dom = installDom();
  installContextPanelMock(async () => ({
    ...contextPanelInfo(3),
    sessionCost: 0,
    sessionCurrency: "CNY",
    sessionCostComplete: true,
    sessionCostQuote: {
      original: { amount: "0", currency: "CNY" },
      selected: { amount: "0", currency: "CNY" },
      estimated: true,
      costComplete: true,
      displayComplete: true,
      complete: true,
      displayStatus: "partial_priced",
      partialPricedCount: 8,
    },
  }));
  // Wait out the bridge's 200ms query-coalesce window first — the previous
  // block's ContextPanel("tab-a") response is still cached and my fixture
  // (requestCount 3) would otherwise be shadowed by it (same trick as the
  // reset-cost block above).
  const { root } = await renderRing();
  await act(async () => { await wait(250); });
  const button = document.querySelector(".context-ring") as HTMLButtonElement | null;
  if (!button) throw new Error("missing context ring button");
  await act(async () => {
    button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await wait(220);
  });
  // Locale-independent assertions: the row label follows the active locale
  // (the en-label row lookups above are the known zh-machine reds), so pin
  // the VALUE and the annotation absence on the popover text itself.
  const popoverText = document.querySelector(".context-ring-popover")?.textContent ?? "";
  ok(popoverText.includes("≈¥0.00"), `partial-priced zero total renders ≈¥0.00 (popover: ${popoverText.slice(0, 160) || "EMPTY"})`);
  ok(!popoverText.includes("≈-") && !popoverText.includes("-（") && !popoverText.includes("- ("), "zero total never renders the dash face");
  ok(!popoverText.includes("unpriced") && !popoverText.includes("无定价"), "the incl-N-unpriced annotation is gone from the ring popover");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

await checkSeparateDurations();
console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
