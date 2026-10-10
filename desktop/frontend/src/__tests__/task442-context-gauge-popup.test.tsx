// Run: tsx src/__tests__/task442-context-gauge-popup.test.tsx
//
// Task 442 — the composer gauge popup: composition segmented bar, session
// average cache hit rate, provider-conditional quota cards, the 「更多」entry,
// and the popup's open/close interaction. Acceptance (tasklist 442):
//   1. every segment's share matches the actual composition and the shares
//      sum to 100.0 (rounding can never drift the bar away from its legend);
//   2. the opencode-go quota card renders for an opencode-go provider and
//      issues NO query for any other provider (conditional, not a lab flag);
//   3. the popup opens on click and closes on an outside click;
//   4. the right dock's overview stays reachable through 「更多 >」.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextWindowRing } from "../components/ContextWindowRing";
import { LocaleProvider, t } from "../lib/i18n";
import type { ContextPanelInfo } from "../lib/types";
import {
  OPEN_CONTEXT_OVERVIEW_EVENT,
  compositionSegments,
  contextOverviewTarget,
  isOpencodeGoProvider,
  normalizeShares,
  requestContextOverview,
} from "../lib/contextGaugePopup";

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

function show(value: unknown): string {
  try {
    const json = JSON.stringify(value);
    if (json !== undefined) return json;
  } catch {
    // fall through — DOM nodes are circular
  }
  if (value instanceof Node) return `<${value.nodeName.toLowerCase()}>`;
  return String(value);
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${show(expected)}, got ${show(actual)}`);
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
  globalThis.window = dom.window as unknown as Window;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.CustomEvent = dom.window.CustomEvent as unknown as typeof CustomEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function baseInfo(): ContextPanelInfo {
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
    requestCount: 1,
    elapsedMs: 0,
    sessionCost: 0,
    sessionCurrency: "",
    readFiles: [],
    changedFiles: [],
  };
}

function installContextPanelMock(fn: (tabId: string) => Promise<ContextPanelInfo>) {
  (window as unknown as { go: { main: { App: { ContextPanel: typeof fn } } } }).go = {
    main: { App: { ContextPanel: fn } },
  };
}

function installUsageMock(fn: (baseUrl: string) => Promise<unknown>) {
  const w = window as unknown as { go: { main: { App: Record<string, unknown> } } };
  w.go ??= { main: { App: {} } };
  w.go.main.App.GetOpenCodeGoUsage = fn;
}

async function renderRing(props: Partial<Parameters<typeof ContextWindowRing>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const currentProps: Parameters<typeof ContextWindowRing>[0] = {
    enabled: true,
    tabId: "tab-a",
    context: { used: 10, window: 100, compactRatio: 0.8 },
    ...props,
  };
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ContextWindowRing {...currentProps} />
      </LocaleProvider>,
    );
    await wait();
  });
  return { root };
}

async function openPopover(selector = ".context-ring") {
  const trigger = document.querySelector(selector) as HTMLButtonElement | null;
  if (!trigger) throw new Error(`missing trigger ${selector}`);
  await act(async () => {
    trigger.click();
    await wait();
    await wait();
  });
  return trigger;
}

console.log("\ntask 442 — context gauge popup");

// ── pure model: shares, provider gate, overview destination ────────────────
{
  eq(compositionSegments(null).length, 0, "no composition means no segments (bar hides)");
  eq(
    compositionSegments({
      systemPromptTokens: 0, builtinToolTokens: 0, skillTokens: 0,
      mcpToolTokens: 0, messageTokens: 0, totalTokens: 0,
    }).length,
    0,
    "a zero total hides the bar instead of drawing an empty legend",
  );

  const shares = normalizeShares([1, 1, 1]);
  eq(shares.length, 3, "three buckets keep three shares");
  eq(Math.round(shares.reduce((a, b) => a + b, 0) * 10) / 10, 100, "an even third splits to exactly 100.0");
  eq(
    shares.every((value, index) => Math.abs(value - [33.4, 33.3, 33.3][index]) < 1e-9),
    true,
    "the leftover tenth lands on the largest remainder (first by index)",
  );

  const awkward = normalizeShares([7, 3, 11, 1, 0, 97]);
  eq(Math.round(awkward.reduce((a, b) => a + b, 0) * 10) / 10, 100, "awkward ratios still sum to exactly 100.0");
  eq(awkward.every((value) => Number.isFinite(value) && value >= 0), true, "every share is a finite non-negative percent");

  eq(isOpencodeGoProvider("opencode-go"), true, "the primary opencode-go provider qualifies");
  eq(isOpencodeGoProvider("opencode-go-anthropic"), true, "the Anthropic route qualifies (same subscription)");
  eq(isOpencodeGoProvider("opencode-go-responses"), true, "the Responses route qualifies");
  eq(isOpencodeGoProvider("opencode"), false, "OpenCode Zen (pay-as-you-go) does not");
  eq(isOpencodeGoProvider("zhipu/bigmodel"), false, "zhipu does not qualify");
  eq(isOpencodeGoProvider(undefined), false, "an unknown provider does not qualify");

  eq(contextOverviewTarget("classic"), "right-dock", "classic opens the right dock overview");
  eq(contextOverviewTarget("workbench"), "right-dock", "workbench opens the right dock overview");
  eq(contextOverviewTarget("creation"), "settings-usage", "creation has no 概览 tab — settings usage page instead");

  const dom = installDom();
  let events = 0;
  window.addEventListener(OPEN_CONTEXT_OVERVIEW_EVENT, () => { events += 1; });
  requestContextOverview();
  eq(events, 1, "requestContextOverview dispatches the App-level navigation event");
  window.removeEventListener(OPEN_CONTEXT_OVERVIEW_EVENT, () => {});
  dom.window.close();
}

// ── acceptance 1: segmented bar shares match the composition ───────────────
{
  const dom = installDom();
  installContextPanelMock(async () => ({
    ...baseInfo(),
    // 4300 / 1500 / 500 / 3000 / 0 / 700 of 10 000 → clean percents.
    composition: {
      systemPromptTokens: 3000,
      builtinToolTokens: 1500,
      skillTokens: 500,
      mcpToolTokens: 700,
      messageTokens: 4300,
      totalTokens: 10000,
    },
    sessionCacheHitTokens: 99_950,
    sessionCacheMissTokens: 50,
  }));
  const { root } = await renderRing({ tabId: "tab-442-bar" });
  await openPopover();

  const legend = [...document.querySelectorAll(".context-composition__legend-item")];
  eq(legend.length, 6, "six composition segments render (incl. the reserved 其他 row)");
  const labels = legend.map((row) => row.querySelector(".context-composition__label")?.textContent);
  eq(
    labels.join("|"),
    // Locale-agnostic: this machine's navigator speaks zh-CN, so expectations
    // come from the same dictionary the popup renders with.
    [
      t("context.segment.message"),
      t("context.segment.builtinTools"),
      t("context.segment.skills"),
      t("context.segment.systemPrompt"),
      t("context.segment.other"),
      t("context.segment.mcpTools"),
    ].join("|"),
    "segments follow the dispatched category order",
  );
  const shares = legend.map((row) => Number((row.querySelector(".context-composition__share")?.textContent ?? "0").replace("%", "")));
  eq(shares.reduce((a, b) => a + b, 0), 100, "segment shares sum to exactly 100.0%");
  const expected = [43, 15, 5, 30, 0, 7];
  eq(
    shares.every((value, index) => Math.abs(value - expected[index]) < 0.05),
    true,
    "each share equals its segment's share of the composition total",
  );
  const tokens = legend.map((row) => row.querySelector(".context-composition__tokens")?.textContent);
  eq(tokens[0], "4.3k", "the message segment prints its token count");
  eq(tokens[4], "0", "the reserved 其他 segment prints zero today");

  const bar = [...document.querySelectorAll(".context-composition__seg")];
  eq(bar.length, 6, "the bar draws one span per segment");
  const widthSum = bar.reduce((sum, el) => sum + Number((el as HTMLElement).style.width.replace("%", "")), 0);
  eq(Math.round(widthSum * 10) / 10, 100, "bar spans add up to the full track width");
  eq(
    document.querySelectorAll(".context-ring-popover__seg").length,
    0,
    "composition segments never mix into the capacity fill (separate block)",
  );

  const rows = [...document.querySelectorAll(".context-ring-popover__row")];
  const rowValue = (label: string) =>
    rows.find((row) => row.querySelector(".context-ring-popover__label")?.textContent === label)
      ?.querySelector(".context-ring-popover__value")?.textContent;
  eq(rowValue(t("context.avgCacheHitRate")), "99.95%", "the session-wide average cache hit rate renders beside the per-turn one");
  eq(rowValue(t("status.cacheLabel")), "-", "the per-turn cache row stays, reading its own request (none yet)");

  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── acceptance 1b: a host without the accessor hides the whole block ───────
{
  const dom = installDom();
  installContextPanelMock(async () => baseInfo());
  const { root } = await renderRing({ tabId: "tab-442-no-bar" });
  await openPopover();
  eq(document.querySelector(".context-composition"), null, "no composition data means no segmented bar");
  eq(
    document.querySelector(".context-ring-popover__more") !== null,
    true,
    "the 「更多」entry survives an older host (it does not depend on composition)",
  );
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── acceptance 2: quota cards render only for an opencode-go provider ──────
{
  const dom = installDom();
  const usageCalls: string[] = [];
  const resetsAt = new Date(Date.now() + (3 * 60 + 12) * 60_000).toISOString();
  installContextPanelMock(async () => ({
    ...baseInfo(),
    providerName: "opencode-go",
    composition: {
      systemPromptTokens: 100, builtinToolTokens: 50, skillTokens: 0,
      mcpToolTokens: 0, messageTokens: 850, totalTokens: 1000,
    },
  }));
  installUsageMock(async (baseUrl) => {
    usageCalls.push(baseUrl);
    return {
      tiers: [
        { window: "rolling", percent: 3, resetsAt },
        { window: "weekly", percent: 96, resetsAt },
        { window: "monthly", percent: 0, resetsAt: "" },
      ],
      note: "",
    };
  });
  const { root } = await renderRing({ tabId: "tab-442-quota" });
  await openPopover();
  await act(async () => { await wait(); await wait(); });

  eq(usageCalls.length, 1, "an opencode-go provider queries the usage endpoint exactly once");
  eq(usageCalls[0], "https://opencode.ai/zen/go/v1", "the query targets the allow-listed official base");
  const cards = [...document.querySelectorAll(".context-quota__card")];
  eq(cards.length, 3, "three quota windows render (5h / weekly / monthly)");
  const cardValue = (win: string) =>
    cards.find((card) => card.getAttribute("data-window") === win)?.querySelector(".context-quota__value")?.textContent;
  const cardResets = (win: string) =>
    cards.find((card) => card.getAttribute("data-window") === win)?.querySelector(".context-quota__resets")?.textContent;
  eq(cardValue("rolling"), `${t("context.quotaRemaining")} 97%`, "the 5-hour window reports the remaining share (3% used)");
  eq(cardValue("weekly"), `${t("context.quotaRemaining")} 4%`, "the weekly window reports the remaining share (96% used)");
  eq(cardValue("monthly"), `${t("context.quotaRemaining")} 100%`, "an untouched monthly window reads 100%");
  eq(
    (cardResets("rolling") ?? "").startsWith(`${t("settings.opencodeGoUsage.resetsIn")} 3h`),
    true,
    "the countdown line carries the reset instant",
  );
  eq(cardResets("monthly"), "", "a window without a reset instant renders no countdown text");
  eq(
    document.querySelector(".context-quota__head")?.textContent,
    t("context.quotaTitle"),
    "the quota block is titled",
  );

  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── acceptance 2b: every other provider issues no query and shows no card ──
{
  const dom = installDom();
  const usageCalls: string[] = [];
  installContextPanelMock(async () => ({ ...baseInfo(), providerName: "zhipu/bigmodel" }));
  installUsageMock(async (baseUrl) => {
    usageCalls.push(baseUrl);
    return { tiers: [], note: "" };
  });
  const { root } = await renderRing({ tabId: "tab-442-other-provider" });
  await openPopover();
  await act(async () => { await wait(); await wait(); });

  eq(usageCalls.length, 0, "a non-opencode-go provider never queries the usage endpoint");
  eq(document.querySelector(".context-quota"), null, "no quota block renders for another provider");
  eq(document.querySelector(".context-quota__note"), null, "and no degradation note either — the block is simply absent");

  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── acceptance 2c: an opencode-go provider without a subscription shows why ─
{
  const dom = installDom();
  installContextPanelMock(async () => ({ ...baseInfo(), providerName: "opencode-go-responses" }));
  installUsageMock(async () => ({ tiers: [], note: "no-subscription" }));
  const { root } = await renderRing({ tabId: "tab-442-403" });
  await openPopover();
  await act(async () => { await wait(); await wait(); });

  const note = document.querySelector(".context-quota__note")?.textContent ?? "";
  eq(note, t("settings.opencodeGoUsage.note.noSubscription"), "a 403 degrades to the entitlement note, not an empty card row");

  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── acceptance 3 + 4: open on click, close on outside click, 「更多」event ──
{
  const dom = installDom();
  installContextPanelMock(async () => baseInfo());
  const { root } = await renderRing({ tabId: "tab-442-interaction" });
  let overviewRequests = 0;
  const onOverview = () => { overviewRequests += 1; };
  window.addEventListener(OPEN_CONTEXT_OVERVIEW_EVENT, onOverview);

  const trigger = await openPopover();
  eq(trigger.getAttribute("aria-expanded"), "true", "clicking the gauge opens the popup");
  ok(Boolean(document.querySelector(".context-ring-popover")), "the popover is mounted while open");

  await act(async () => {
    (document.querySelector(".context-ring-popover__more") as HTMLButtonElement).click();
    await wait();
    await wait(260);
  });
  eq(overviewRequests, 1, "「更多 >」 requests the detail surface through the App-level event");
  eq(
    document.querySelector(".context-ring")?.getAttribute("aria-expanded"),
    "false",
    "「更多 >」 closes the popup on the way out",
  );

  await act(async () => { trigger.click(); await wait(); await wait(); });
  eq(trigger.getAttribute("aria-expanded"), "true", "the popup reopens for the outside-click check");
  await act(async () => {
    document.body.click();
    await wait();
    await wait(260);
  });
  eq(
    document.querySelector(".context-ring")?.getAttribute("aria-expanded"),
    "false",
    "an outside click closes the popup",
  );

  window.removeEventListener(OPEN_CONTEXT_OVERVIEW_EVENT, onOverview);
  await act(async () => { root.unmount(); });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
