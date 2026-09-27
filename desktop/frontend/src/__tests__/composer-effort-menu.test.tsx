// Run: tsx src/__tests__/composer-effort-menu.test.tsx
//
// Task 301 (user ruling): the composer effort picker shows only the four
// honest tiers (none/low/medium/high) that the MiMo model actually implements.
// minimal/xhigh/max/ultra are wire compatibility aliases (task 254); this
// display surface folds them via normalizeEffortForMenu — stored=xhigh shows
// as high — and never offers the aliases. Wire normalization is untouched.

import { JSDOM } from "jsdom";
import React from "react";
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
    modelLabel: "mimo-v2.6",
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

// The MiMo wire set: four honest tiers plus four compatibility aliases.
const mimoEffortOptions = [
  { id: "none", name: "None" },
  { id: "minimal", name: "Minimal" },
  { id: "low", name: "Low" },
  { id: "medium", name: "Medium" },
  { id: "high", name: "High" },
  { id: "xhigh", name: "Xhigh" },
  { id: "max", name: "Max" },
  { id: "ultra", name: "Ultra" },
];

{
  const dom = installDom();
  const picked: string[] = [];
  const { root } = await renderComposer({
    effort: { supported: true, current: "xhigh", default: "high", options: mimoEffortOptions, aliasFold: true },
    onSetEffort: level => picked.push(level),
  });

  // stored=xhigh folds to the tier it means: the trigger reads High, not Xhigh.
  const trigger = document.querySelector<HTMLButtonElement>(".composer-effort-control button");
  if (!trigger) throw new Error("missing composer effort selector");
  ok(trigger.textContent?.includes("High") ?? false, "stored=xhigh displays as High");
  ok(!(trigger.textContent?.includes("Xhigh") ?? false), "trigger never shows the xhigh alias");

  await act(async () => { trigger.click(); await flushTimers(); });
  const items = [...document.querySelectorAll('[role="menuitemradio"]')].map(node => node.textContent ?? "");
  eq(items.join(","), "auto,None,Low,Medium,High",
    "mimo menu offers only auto + the four honest tiers");
  for (const alias of ["Minimal", "Xhigh", "Max", "Ultra"]) {
    ok(!items.some(item => item.includes(alias)), `menu never offers the ${alias} alias`);
  }

  // xhigh already displays as high, so re-picking high is a no-op…
  const high = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    .find(node => node.textContent === "High");
  if (!high) throw new Error("missing high tier option");
  await act(async () => { high.click(); await flushTimers(); });
  eq(picked.join(","), "", "re-selecting the folded tier does not re-send the effort");

  // …while a genuinely different tier still travels the wire unchanged.
  await act(async () => { trigger.click(); await flushTimers(); });
  const medium = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    .find(node => node.textContent === "Medium");
  if (!medium) throw new Error("missing medium tier option");
  await act(async () => { medium.click(); await flushTimers(); });
  eq(picked.join(","), "medium", "picking a different tier sends its honest value");

  await act(async () => root.unmount());
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
