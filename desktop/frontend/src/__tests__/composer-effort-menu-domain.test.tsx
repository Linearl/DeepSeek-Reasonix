// Run: tsx src/__tests__/composer-effort-menu-domain.test.tsx
//
// Task 1161a: the composer effort menu renders the backend's EffortMenuDomain
// (desktop/app_effort.go effortForTabDirect now filters levels AND the
// labelled options through the same source the dispatch path validates
// against). The frontend adds no filtering of its own — these harnesses pin
// the contract that a filtered EffortInfo renders verbatim (a thinking-locked
// entry offers only the lock's own value) and that a healthy vocabulary still
// passes through untouched (deepseek keeps its honest max; the task-301 fold
// stays MiMo-only via aliasFold).

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
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
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
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

async function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "deepseek-flash",
    onSend: () => {},
    onCancel: () => undefined,
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
    ...props,
  };
  const paint = async (nextProps: Partial<Parameters<typeof Composer>[0]> = {}) => {
    currentProps = { ...currentProps, ...nextProps };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToastProvider>
            <Composer {...currentProps} />
          </ToastProvider>
        </LocaleProvider>,
      );
      await flushTimers();
    });
  };
  await paint();
  return { root, rerender: paint };
}

async function menuItems(): Promise<string[]> {
  const trigger = document.querySelector<HTMLButtonElement>(".composer-effort-control button");
  if (!trigger) throw new Error("missing composer effort selector");
  await act(async () => { trigger.click(); await flushTimers(); });
  return [...document.querySelectorAll('[role="menuitemradio"]')].map((node) => node.textContent ?? "");
}

// Scenario 1 (1161a): the backend already filtered a thinking-locked entry
// down to the wire-legal depths. The composer must render exactly that — no
// client-side widening, the locked depths never render as pickable.
{
  const dom = installDom();
  const { root } = await renderComposer({
    effort: {
      supported: true,
      current: "disabled",
      default: "high",
      levels: ["auto", "disabled"],
      options: [{ id: "disabled", name: "Disabled" }],
    },
  });
  const items = await menuItems();
  eq(items.join(","), "auto,Disabled",
    "thinking-locked menu offers only auto + the lock's own value");
  for (const banned of ["Low", "High", "Max"]) {
    ok(!items.some((item) => item.includes(banned)), `locked menu never offers ${banned}`);
  }
  await act(async () => root.unmount());
  dom.window.close();
}

// Scenario 2 (1161a non-interference): a healthy deepseek vocabulary arrives
// complete and passes through byte-for-byte — the honest max stays (aliasFold
// is off; the task-301 fold must never trigger on it), and every depth the
// dispatch path honours stays pickable.
{
  const dom = installDom();
  const picked: string[] = [];
  const { root } = await renderComposer({
    effort: {
      supported: true,
      current: "max",
      default: "high",
      levels: ["auto", "disabled", "low", "high", "max"],
      options: [
        { id: "disabled", name: "Disabled" },
        { id: "low", name: "Low" },
        { id: "high", name: "High" },
        { id: "max", name: "Max" },
      ],
    },
    onSetEffort: (level) => picked.push(level),
  });
  const trigger = document.querySelector<HTMLButtonElement>(".composer-effort-control button");
  ok(trigger?.textContent?.includes("Max") ?? false, "deepseek current=max displays verbatim (no fold)");
  const items = await menuItems();
  eq(items.join(","), "auto,Disabled,Low,High,Max",
    "healthy deepseek menu keeps every wire-legal depth in backend order");
  const max = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    .find((node) => node.textContent === "Max");
  if (!max) throw new Error("missing max option");
  await act(async () => { max.click(); await flushTimers(); });
  eq(picked.join(","), "", "re-selecting the current depth does not re-send");
  await act(async () => root.unmount());
  dom.window.close();
}

// Scenario 3 (1161b companion): the fold notice rides the backend notice
// channel, so the composer only needs to keep rendering the folded current
// value the backend now reports (mimo current=high after a max→high fold).
{
  const dom = installDom();
  const { root } = await renderComposer({
    modelLabel: "mimo-v2.5-pro",
    effort: {
      supported: true,
      current: "high",
      default: "auto",
      levels: ["auto", "none", "low", "medium", "high"],
      options: [
        { id: "none", name: "None" },
        { id: "low", name: "Low" },
        { id: "medium", name: "Medium" },
        { id: "high", name: "High" },
      ],
      aliasFold: true,
    },
  });
  const trigger = document.querySelector<HTMLButtonElement>(".composer-effort-control button");
  ok(trigger?.textContent?.includes("High") ?? false, "post-fold current=high displays as High");
  const items = await menuItems();
  eq(items.join(","), "auto,None,Low,Medium,High",
    "folded mimo menu offers only auto + the four honest tiers");
  await act(async () => root.unmount());
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
