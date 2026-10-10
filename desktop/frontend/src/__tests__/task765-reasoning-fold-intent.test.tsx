// Run: tsx src/__tests__/task765-reasoning-fold-intent.test.tsx
// 任务 765: a user's explicit reasoning fold choice must survive streaming
// updates and panel remounts. The intent table (reasoningFoldOverrides) is the
// per-session memory; the panels seed their local state and userOverridden
// guard from it, so the tier-driven auto-expand branches cannot overturn what
// the user chose — no matter how many times the panel remounts mid-stream.

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { AssistantMessage } from "../components/Message";
import { InlineAssistantReasoning } from "../components/InlineAssistantReasoning";
import {
  clearReasoningFoldOverrideSessionForTest,
  readReasoningFoldOverride,
  writeReasoningFoldOverride,
} from "../lib/reasoningFoldOverrides";
import { hydrateSessionExperience, applySessionExperience } from "../lib/sessionExperience";

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

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

console.log("\ntask765 reasoning fold intent");

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: { ...dom.window.navigator, language: "en-US" } });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

const SESSION = "test-session-765";

// Mirror of the Transcript.tsx wiring: read the persisted table on every
// render, write it from the panel callbacks. Not React state on purpose —
// the table only serves future mounts.
function intentOf(itemId: string): boolean | undefined {
  return readReasoningFoldOverride(SESSION, itemId);
}
function onIntent(itemId: string, open: boolean): void {
  writeReasoningFoldOverride(SESSION, itemId, open);
}

type ReasoningItem = React.ComponentProps<typeof AssistantMessage>["item"];

function makeItem(id: string, overrides: Partial<ReasoningItem> = {}): ReasoningItem {
  return {
    kind: "assistant",
    id,
    text: "",
    reasoning: "first thought line\n\nsecond thought line",
    streaming: false,
    reasoningComplete: true,
    reasoningDurationMs: 2_600,
    ...overrides,
  } as ReasoningItem;
}

async function flushSuspense() {
  // The reasoning panel mounts through React.lazy; flush until the fallback
  // is gone (bounded, mirrors message-reasoning-panel.test.tsx).
  for (let tick = 0; tick < 100; tick += 1) {
    if (!document.querySelector(".reasoning--loading")) break;
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  }
  if (document.querySelector(".reasoning--loading")) {
    throw new Error("reasoning panel never left its Suspense fallback");
  }
}

async function renderMessage(item: ReasoningItem, opts: { withIntent?: boolean } = {}) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <AssistantMessage
          key={item.id}
          item={item}
          defaultExpanded={false}
          explicitFold={opts.withIntent === false ? undefined : intentOf(item.id)}
          onExplicitFoldChange={(open) => onIntent(item.id, open)}
        />
      </LocaleProvider>,
    );
  });
  await flushSuspense();
}

async function unmountAll() {
  await act(async () => {
    root.render(<LocaleProvider></LocaleProvider>);
  });
}

async function click(el: Element | null | undefined) {
  await act(async () => {
    el?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function bodyOpen(): boolean {
  return Boolean(document.querySelector(".reasoning__body"));
}

// ── B1: answer-message reasoning panel ────────────────────────────────────────

hydrateSessionExperience("standard");

// 1. Completed panel: user expands → remount keeps it expanded.
{
  clearReasoningFoldOverrideSessionForTest(SESSION);
  const item = makeItem("765-b1-expand");
  await renderMessage(item);
  ok(!bodyOpen(), "B1 completed reasoning is collapsed by default (standard tier)");
  await click(document.querySelector(".reasoning-summary"));
  ok(bodyOpen(), "B1 user expansion opens the body");
  ok(intentOf(item.id) === true, "B1 expansion is written to the intent table");
  await unmountAll();
  await renderMessage(item);
  ok(bodyOpen(), "B1 expansion survives a remount (was: collapsed again)");
  await click(document.querySelector(".reasoning__head"));
  ok(!bodyOpen(), "B1 user collapse closes the body");
  ok(intentOf(item.id) === false, "B1 collapse overwrites the intent");
  await unmountAll();
  await renderMessage(item);
  ok(!bodyOpen(), "B1 collapse survives a remount");
  await unmountAll();
}

// 2. Streaming panel (standard live-expands): user collapses mid-stream, the
// panel remounts while the turn is still running — the auto-expand branch
// must not reopen it (the 765 device symptom).
{
  clearReasoningFoldOverrideSessionForTest(SESSION);
  const item = makeItem("765-b1-stream", { streaming: true, reasoningComplete: false, reasoningDurationMs: undefined });
  await renderMessage(item);
  ok(bodyOpen(), "B1 streaming reasoning live-expands under standard");
  await click(document.querySelector(".reasoning__head"));
  ok(!bodyOpen(), "B1 user collapses the streaming panel");
  ok(intentOf(item.id) === false, "B1 streaming collapse is persisted");
  await unmountAll();
  await renderMessage({ ...item, reasoning: "first thought line\n\nsecond thought line\n\nmore streamed content" });
  ok(!bodyOpen(), "B1 remount mid-stream stays collapsed (was: re-expanded)");
  // More streaming ticks on the same mounted instance must not flip it either.
  await renderMessage({ ...item, reasoning: "first thought line\n\nsecond thought line\n\nmore streamed content\n\neven more" });
  ok(!bodyOpen(), "B1 streaming tick on the remounted instance keeps the collapse");
  await unmountAll();
}

// 3. Fresh streaming cycle on the SAME item: a persisted intent is the user's
// explicit word — the cycle reset must not resurrect the old tier default.
{
  clearReasoningFoldOverrideSessionForTest(SESSION);
  const item = makeItem("765-b1-cycle", { streaming: true, reasoningComplete: false, reasoningDurationMs: undefined });
  await renderMessage(item);
  await click(document.querySelector(".reasoning__head"));
  ok(!bodyOpen() && intentOf(item.id) === false, "B1 collapse before the cycle reset is persisted");
  await renderMessage({ ...item, reasoningComplete: false, reasoning: "restarted reasoning" });
  await renderMessage({ ...item, streaming: false, reasoningComplete: true });
  // Settle completes the panel; the completion-collapse branch must keep the
  // intent-honoring state instead of erroring either way.
  ok(intentOf(item.id) === false, "B1 intent survives streaming/completion transitions");
  await unmountAll();
  await renderMessage({ ...item, streaming: true, reasoningComplete: false });
  ok(!bodyOpen(), "B1 fresh cycle after remount still honors the collapse intent");
  await unmountAll();
}

// ── B2: inline work-process reasoning panel ──────────────────────────────────

async function renderInline(item: ReasoningItem, autoFollowActive: boolean) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <InlineAssistantReasoning
          key={item.id}
          item={item}
          autoFollowActive={autoFollowActive}
          explicitFold={intentOf(item.id)}
          onExplicitFoldChange={(open) => onIntent(item.id, open)}
        />
      </LocaleProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function inlineOpen(): boolean {
  return Boolean(document.querySelector(".turn-collapse__reasoning-phase--open"));
}

// 4. standard + running: B2 live-expands, user collapses, remount keeps it.
{
  clearReasoningFoldOverrideSessionForTest(SESSION);
  applySessionExperience("standard");
  const item = makeItem("765-b2-stream", { streaming: true, reasoningComplete: false, reasoningDurationMs: undefined });
  await renderInline(item, true);
  ok(inlineOpen(), "B2 running reasoning live-expands under standard");
  await click(document.querySelector(".turn-collapse__reasoning-head"));
  ok(!inlineOpen(), "B2 user collapses the running panel");
  ok(intentOf(item.id) === false, "B2 collapse is persisted");
  await unmountAll();
  await renderInline({ ...item, reasoning: "first thought line\n\nsecond thought line\n\nstreamed tail" }, true);
  ok(!inlineOpen(), "B2 remount mid-run stays collapsed (was: re-expanded)");
  await unmountAll();
}

// 5. concise: B2 starts collapsed, user opens it, remount keeps it open.
{
  clearReasoningFoldOverrideSessionForTest(SESSION);
  applySessionExperience("concise");
  const item = makeItem("765-b2-concise", { streaming: true, reasoningComplete: false, reasoningDurationMs: undefined });
  await renderInline(item, true);
  ok(!inlineOpen(), "B2 concise keeps the running panel collapsed");
  await click(document.querySelector(".turn-collapse__reasoning-head"));
  ok(inlineOpen(), "B2 concise user expansion opens the panel");
  ok(intentOf(item.id) === true, "B2 concise expansion is persisted");
  await unmountAll();
  await renderInline(item, true);
  ok(inlineOpen(), "B2 concise expansion survives a remount");
  await unmountAll();
}

// 6. Tier switch clears the table (Transcript wiring contract): a fresh
// session state must not leak choices across tiers.
{
  applySessionExperience("standard");
  clearReasoningFoldOverrideSessionForTest(SESSION);
  const item = makeItem("765-b2-tier", { streaming: true, reasoningComplete: false, reasoningDurationMs: undefined });
  await renderInline(item, true);
  await click(document.querySelector(".turn-collapse__reasoning-head"));
  ok(!inlineOpen() && intentOf(item.id) === false, "B2 standard collapse persisted");
  applySessionExperience("concise");
  await unmountAll();
  await renderInline(item, true);
  ok(!inlineOpen(), "B2 concise after switch is collapsed by tier semantics");
  // The stale intent is still in the table (clearing is Transcript's job on
  // preferenceChanged); simulate the Transcript-side clear and verify the
  // default collapses.
  clearReasoningFoldOverrideSessionForTest(SESSION);
  await unmountAll();
  await renderInline(item, true);
  ok(!inlineOpen(), "B2 concise collapses with no intent present");
  await unmountAll();
  applySessionExperience("standard");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
