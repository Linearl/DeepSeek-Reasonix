// Run: npx tsx src/__tests__/composer-guidance-unread.test.tsx
//
// Task 221#6 unread-badge reconciliation on the rendering side: whatever
// UnreadMailCount reports must be exactly what the badge shows — the number
// is the mailbox ground truth (claimed at the store layer in
// internal/sessioncollab), so the shelf must render it verbatim and render
// nothing when it is zero or unset.
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ComposerGuidanceShelf } from "../components/ComposerGuidanceShelf";
import { LocaleProvider } from "../lib/i18n";

let passed = 0;
const failures: string[] = [];
function ok(value: boolean, label: string) {
  if (value) passed++;
  else failures.push(label);
}

function installDom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
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
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function makeItem(id: string) {
  return { id, text: `guidance ${id}`, submitText: "", state: "queued" as const };
}

type ShelfProps = Partial<Parameters<typeof ComposerGuidanceShelf>[0]>;

async function mount(props: ShelfProps) {
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  let current: ShelfProps = {
    recovery: null,
    recoveryDisabled: false,
    items: [makeItem("a")],
    expanded: true,
    running: false,
    disabled: false,
    readOnly: false,
    sendingId: null,
    onReview: () => {},
    onRecoveryResumed: () => {},
    onRecoveryError: () => {},
    onToggleExpanded: () => {},
    onSend: () => {},
    onDismiss: () => {},
    ...props,
  };
  const render = async (next: ShelfProps = {}) => {
    current = { ...current, ...next };
    await act(async () => {
      root.render(
        <React.StrictMode>
          <LocaleProvider>
            <ComposerGuidanceShelf {...(current as Parameters<typeof ComposerGuidanceShelf>[0])} />
          </LocaleProvider>
        </React.StrictMode>,
      );
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  };
  await render();
  return { dom, root, render };
}

async function cleanup(dom: JSDOM, root: Root) {
  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

const badge = () => document.querySelector(".composer-guidance-head__unread");

// Ground truth 2 → badge shows exactly 2 (aria-label carries the count).
{
  const { dom, root, render } = await mount({ unreadMailCount: 2 });
  const el = badge();
  ok(el !== null, "a non-zero mailbox count renders the unread badge");
  ok(el?.getAttribute("aria-label")?.includes("2") === true, "badge aria-label carries the exact count from UnreadMailCount");
  await render({ unreadMailCount: 7 });
  ok(badge()?.getAttribute("aria-label")?.includes("7") === true, "badge re-renders when the probe reports a new ground truth");
  await cleanup(dom, root);
}

// Ground truth 0 / unset → no badge at all (no fake "0 unread" chip).
{
  const { dom, root, render } = await mount({ unreadMailCount: 0 });
  ok(badge() === null, "a zero count must not render the badge");
  await render({ unreadMailCount: undefined });
  ok(badge() === null, "an unset probe must not render the badge either");
  await cleanup(dom, root);
}

console.log(`${passed} passed, ${failures.length} failed`);
if (failures.length > 0) {
  for (const f of failures) console.error(`FAIL: ${f}`);
  process.exit(1);
}
