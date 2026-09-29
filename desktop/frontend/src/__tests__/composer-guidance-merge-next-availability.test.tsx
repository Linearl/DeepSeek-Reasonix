// Run: npx tsx src/__tests__/composer-guidance-merge-next-availability.test.tsx
//
// Task 366: the manual merge-next button kept its original gate byte-for-byte
// (zero relaxation), but under the task-221 merge-all tier it can never fire —
// so the row now shows a DISABLED control with a cross-hint (S1) and every
// hidden render leaves a reason-tagged debug line (S2).
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

function makeItem(id: string, state = "queued") {
  return { id, text: `guidance ${id}`, submitText: "", state };
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

const mergeButtons = () => Array.from(document.querySelectorAll('button[aria-label*="合并"], button[aria-label*="merge"], button[aria-label*="Merge"], button[aria-label*="不可用"], button[aria-label*="unavailable"], button[aria-label*="Unavailable"]')) as HTMLButtonElement[];
const disabledHintButton = () => document.querySelector('button[aria-label*="不可用"], button[aria-label*="unavailable"], button[aria-label*="Unavailable"]') as HTMLButtonElement | null;

// ── S1: merge-all tier shows the disabled cross-hint control ──
{
  const { dom, root } = await mount({ onMergeNext: () => {}, mergeMode: "all", items: [makeItem("a")] });
  const btn = disabledHintButton();
  ok(btn !== null, "merge-all + 153-on: the disabled cross-hint control renders instead of vanishing");
  ok(btn?.disabled === true, "the cross-hint control is disabled (never fires under merge-all)");
  await cleanup(dom, root);
}

// ── original semantics: 153 on + tier NOT all → active button on a 2-row queue, nothing on 1 row ──
{
  const { dom, root, render } = await mount({ onMergeNext: () => {}, mergeMode: "off", items: [makeItem("a"), makeItem("b")] });
  const active = mergeButtons().find((b) => !b.disabled);
  ok(active !== undefined, "153 on + tier off + queue>=2: the ACTIVE merge button renders (original semantics)");
  await render({ items: [makeItem("a")] });
  ok(mergeButtons().find((b) => !b.disabled) === undefined, "queue of 1 keeps the button hidden (length gate unchanged)");
  await cleanup(dom, root);
}

// ── 153 off (no onMergeNext): no button and no cross-hint ──
{
  const { dom, root } = await mount({ mergeMode: "all", items: [makeItem("a")] });
  ok(mergeButtons().length === 0, "153 off renders neither the active button nor the cross-hint");
  await cleanup(dom, root);
}

// ── S2: hidden renders leave a reason-tagged debug line ──
{
  const debugLines: string[] = [];
  const orig = console.debug;
  console.debug = (...parts: unknown[]) => {
    debugLines.push(parts.map(String).join(" "));
  };
  try {
    const { dom, root } = await mount({ onMergeNext: () => {}, mergeMode: "off", items: [makeItem("a")] });
    ok(debugLines.some((l) => l.includes("[guidance-merge-next] hidden") && l.includes("reasons=length")), "a length-gated row logs reasons=length (task 366 S2)");
    await cleanup(dom, root);

    const { dom: dom2, root: root2 } = await mount({
      onMergeNext: () => {},
      mergeMode: "off",
      items: [makeItem("flying", "steer_accepted"), makeItem("b")],
    });
    ok(debugLines.some((l) => l.includes("reasons=") && l.includes("inFlight")), "an in-flight row logs the inFlight reason");
    await cleanup(dom2, root2);
  } finally {
    console.debug = orig;
  }
}

console.log(`${passed} passed, ${failures.length} failed`);
if (failures.length > 0) {
  for (const f of failures) console.error(`FAIL: ${f}`);
  process.exit(1);
}
