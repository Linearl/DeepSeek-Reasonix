// Run: tsx src/__tests__/workspace-preview-open-entry.test.tsx
// tl1510 sub①: a SELECTED file gets a visible open entry in the preview
// header — the same OpenWorkspacePathForTab channel the tree context menu
// already routes (right-click only was the gap: the header now carries the
// same action as an icon button).

import { JSDOM } from "jsdom";

import type { AppBindings } from "../lib/bridge";
import { resetWorkspaceTreeMemoryForTests } from "../lib/workspaceTreeMemory";

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

// The single-click preview fires after SIDE_DOCK_CLICK_DOUBLE_MS (200ms) of
// real timers — flush with a sleep that outlasts it, inside act().
function settlePastClickTimer(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 260));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    await act(async () => {
      await settlePastClickTimer();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
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
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.ResizeObserver = TestResizeObserver;
  dom.window.ResizeObserver = TestResizeObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  // AnchoredPopover (recent files) reads prefers-reduced-motion on its close
  // path; jsdom has no matchMedia.
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
  Object.defineProperty(dom.window.HTMLElement.prototype, "scrollIntoView", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "offsetWidth", { configurable: true, get: () => 320 });
  // virtual-core 3.19 sizes the virtual viewport from clientHeight (getRect in
  // observeElementRect), which jsdom always reports as 0 — the tree would
  // render zero virtual rows. Stub it like the other layout reads below.
  Object.defineProperty(dom.window.HTMLElement.prototype, "clientHeight", {
    configurable: true,
    get: function clientHeight(this: HTMLElement) {
      return this.classList.contains("workspace-tree") ? 300 : 24;
    },
  });
  Object.defineProperty(dom.window.HTMLElement.prototype, "offsetHeight", {
    configurable: true,
    get: function offsetHeight(this: HTMLElement) {
      return this.classList.contains("workspace-tree") ? 300 : this.dataset.index ? 24 : 0;
    },
  });
  Object.defineProperty(dom.window.HTMLElement.prototype, "getBoundingClientRect", {
    configurable: true,
    value: function getBoundingClientRect(this: HTMLElement) {
      const width = 320;
      const height = this.classList.contains("workspace-tree") ? 300 : this.dataset.index ? 24 : 0;
      return { x: 0, y: 0, top: 0, left: 0, right: width, bottom: height, width, height, toJSON: () => ({}) } as DOMRect;
    },
  });
  return dom;
}

console.log("\nworkspace preview header open entry");

resetWorkspaceTreeMemoryForTests();
const dom = installDom();
const openCalls: Array<{ tabId: string; path: string }> = [];
window.go = {
  main: {
    App: {
      ListDirForTab: async (_tabId, dir) => dir === ""
        ? [
            { name: "docs", isDir: true },
            { name: "README.md", isDir: false },
          ]
        : [],
      SearchFileRefsForTab: async () => [],
      WorkspaceGitHistory: async () => [],
      WorkspaceChanges: async () => ({ files: [], gitAvailable: true }),
      WorkspaceChangeDetail: async () => ({}),
      ReadFileForTab: async (_tabId, path) => ({ path, body: "", size: 0, truncated: false, binary: false }),
      ResolveWorkspacePathForTab: async (_tabId, path) => `/repo/${path}`,
      RevealWorkspacePathForTab: async () => {},
      GetPinnedFilesForTab: async () => [],
      OpenWorkspacePathForTab: async (tabId, path) => {
        openCalls.push({ tabId, path });
      },
    } as Partial<AppBindings> as AppBindings,
  },
};

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
// Globals must exist BEFORE react-dom loads: the static-import pattern
// initializes react-dom against the empty host environment and its delegated
// input/change listeners then never fire under jsdom (channel-chat-panel
// harness precedent). act/createRoot/components load dynamically here.
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { WorkspacePanel } = await import("../components/WorkspacePanel");
const root = createRoot(rootElement);
await act(async () => {
  root.render(
    <LocaleProvider>
      <WorkspacePanel
        open
        tabId="workspace-tab"
        cwd="/repo"
        maximized={false}
        initialViewMode="files"
        onClose={() => {}}
        onToggleMaximized={() => {}}
        onOpenInTerminal={() => {}}
      />
    </LocaleProvider>,
  );
  await settlePastClickTimer();
});

ok(
  document.querySelector(".workspace-preview__window-actions") == null,
  "nothing selected: no preview header exists yet, so no open entry either",
);

const openButtonLabel = "Open with default app";
const headerOpenButton = () =>
  document.querySelector<HTMLButtonElement>(
    `.workspace-preview__window-actions button[aria-label="${openButtonLabel}"]`,
  );

await waitFor("workspace rows", () => document.querySelector('[data-workspace-path="README.md"]') != null);
await act(async () => {
  // One plain single click (no modifiers): the tl1108 flow fires selectFile
  // after the 200ms double-click window — the sleep inside waitFor outlasts
  // it. Dispatching again in the wait predicate would land AFTER selection
  // and trip the "second click on the selected row closes the preview" arm.
  const row = document.querySelector<HTMLButtonElement>('[data-workspace-path="README.md"]');
  row?.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
});
await waitFor("selected row preview header", () => headerOpenButton() != null);

ok(headerOpenButton() != null, "selected file: the preview header carries an open entry (tl1510 sub①)");

await act(async () => {
  headerOpenButton()?.click();
  await settlePastClickTimer();
});
ok(
  JSON.stringify(openCalls) === JSON.stringify([{ tabId: "workspace-tab", path: "README.md" }]),
  "header open entry routes the selected relative path through OpenWorkspacePathForTab",
);

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
