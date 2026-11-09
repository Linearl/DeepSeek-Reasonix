// Run: tsx src/__tests__/workspace-truncation-titles.test.tsx
// tl1510 sub②: every truncation-prone text in the right-dock file panel
// carries a hover title with the full workspace-relative path — tree rows
// (long names + compact dir chains), search rows (clipped dir tail), change
// rows (both the working-tree scope and the scoped list), the recent-files
// menu, and the git-history file list.

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

// Single-click preview fires after SIDE_DOCK_CLICK_DOUBLE_MS (200ms) of real
// timers; every wait sleeps past it so clicks never chain into the
// double-click "pin" arm or the "second click closes the preview" arm.
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

function typeIntoFilter(query: string) {
  const input = document.querySelector<HTMLInputElement>(".workspace-search input");
  if (!input) throw new Error("missing workspace filter input");
  // React 19 controlled input: bypass the value tracker through the
  // prototype setter, then bubble a native input event.
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
  if (!setter) throw new Error("missing HTMLInputElement value setter");
  setter.call(input, query);
  input.dispatchEvent(new window.Event("input", { bubbles: true }));
}

const rowTitle = (path: string) =>
  document.querySelector<HTMLElement>(`[data-workspace-path="${path}"]`)?.getAttribute("title") ?? null;

console.log("\nworkspace panel truncation titles");

// --- Panel 1: tree rows, recent menu, search rows, working-tree change rows.
resetWorkspaceTreeMemoryForTests();
const dom = installDom();
window.go = {
  main: {
    App: {
      ListDirForTab: async (_tabId, dir) => {
        // Dir keys keep the tree's trailing-slash spelling (dir row paths end
        // with "/", and the compact probe loads exactly the row path).
        if (dir === "") return [
          { name: "docs", isDir: true },
          { name: "README.md", isDir: false },
        ];
        if (dir === "docs/") return [{ name: "api", isDir: true }];
        if (dir === "docs/api/") return [{ name: "deep", isDir: true }];
        if (dir === "docs/api/deep/") return [{ name: "guide.md", isDir: false }];
        return [];
      },
      SearchFileRefsForTab: async () => [],
      WorkspaceGitHistory: async () => [],
      WorkspaceChanges: async () => ({
        files: [
          {
            path: "src/very/deep/nested/renamed-long-file-name.ts",
            sources: ["session"],
            latestPrompt: "",
            canSessionRevert: false,
          },
        ],
        gitAvailable: true,
      }),
      WorkspaceChangeDetail: async () => ({}),
      ReadFileForTab: async (_tabId, path) => ({ path, body: "", size: 0, truncated: false, binary: false }),
      ResolveWorkspacePathForTab: async (_tabId, path) => `/repo/${path}`,
      RevealWorkspacePathForTab: async () => {},
      GetPinnedFilesForTab: async () => [],
      OpenWorkspacePathForTab: async () => {},
    } as Partial<AppBindings> as AppBindings,
  },
};

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
// Globals must exist BEFORE react-dom loads: the static-import pattern
// initializes react-dom against the empty host environment and its delegated
// input/change listeners then never fire under jsdom (channel-chat-panel
// harness precedent). act/createRoot/components load dynamically here and
// stay cached for the second panel below.
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

// The compact-dir probe chain settles as docs / api / deep — one row whose
// visible text is a joined chain; the row's canonical path keeps the dir
// trailing slash ("docs/api/deep/"), and the title carries exactly it.
await waitFor("compact dir row", () => rowTitle("docs/api/deep/") !== null);
ok(rowTitle("docs/api/deep/") === "docs/api/deep/", "compact dir chain row title carries the full relative path");
ok(rowTitle("README.md") === "README.md", "plain file row title carries its relative path");

// Select a file (single click), then open the recent-files menu: its items
// clip both name and parent dir, so the hover title shows the full path.
await act(async () => {
  document.querySelector<HTMLButtonElement>('[data-workspace-path="README.md"]')
    ?.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
});
await waitFor("preview header after selection", () => document.querySelector(".workspace-current-file__recent") != null);
await act(async () => {
  document.querySelector<HTMLButtonElement>(".workspace-current-file__recent")?.click();
  await settlePastClickTimer();
});
const recentItem = document.querySelector<HTMLElement>(".workspace-recent-menu__item");
ok(recentItem != null, "recent files menu opened after selection");
ok(recentItem?.getAttribute("title") === "README.md", "recent menu item title carries the full relative path");

// Search rows replace the tree while the filter is active; their dir tail is
// clipped, so the title must carry the full relative path.
await act(async () => {
  typeIntoFilter("guide");
});
await waitFor("search result row", () => rowTitle("docs/api/deep/guide.md") !== null);
ok(rowTitle("docs/api/deep/guide.md") === "docs/api/deep/guide.md", "search row title carries the full relative path");

// Working-tree scope change rows clip name and dir — title = full path.
await act(async () => {
  const tabs = document.querySelectorAll<HTMLButtonElement>(".workspace-files__tab");
  tabs[1]?.click();
  await settlePastClickTimer();
});
await waitFor("change row", () => document.querySelector(".workspace-change") != null);
const changeRow = document.querySelector<HTMLElement>(".workspace-change");
ok(
  changeRow?.getAttribute("title") === "src/very/deep/nested/renamed-long-file-name.ts",
  "working-tree change row title carries the full relative path",
);

await act(async () => {
  root.unmount();
});
dom.window.close();

// --- Panel 2: the scoped change list (changeListRequest) renders its own row
// family — same title contract there.
resetWorkspaceTreeMemoryForTests();
const dom2 = installDom();
window.go = {
  main: {
    App: {
      ListDirForTab: async () => [],
      SearchFileRefsForTab: async () => [],
      WorkspaceGitHistory: async () => [],
      WorkspaceChanges: async () => ({ files: [], gitAvailable: true }),
      WorkspaceChangeDetail: async () => ({}),
      ReadFileForTab: async (_tabId, path) => ({ path, body: "", size: 0, truncated: false, binary: false }),
      ResolveWorkspacePathForTab: async (_tabId, path) => `/repo/${path}`,
      RevealWorkspacePathForTab: async () => {},
      GetPinnedFilesForTab: async () => [],
      OpenWorkspacePathForTab: async () => {},
    } as Partial<AppBindings> as AppBindings,
  },
};
const rootElement2 = document.getElementById("root");
if (!rootElement2) throw new Error("missing root");
const root2 = createRoot(rootElement2);
await act(async () => {
  root2.render(
    <LocaleProvider>
      <WorkspacePanel
        open
        tabId="workspace-tab"
        cwd="/repo"
        maximized={false}
        initialViewMode="files"
        changeListRequest={{
          id: 1,
          changes: [{ key: "k1", path: "a/b/c.ts", meta: "", time: "", detail: "" }],
        }}
        onClose={() => {}}
        onToggleMaximized={() => {}}
        onOpenInTerminal={() => {}}
      />
    </LocaleProvider>,
  );
  await settlePastClickTimer();
});
await waitFor("scoped change row", () => document.querySelector(".workspace-change-scope .workspace-change") != null);
const scopedRow = document.querySelector<HTMLElement>(".workspace-change-scope .workspace-change");
ok(scopedRow?.getAttribute("title") === "a/b/c.ts", "scoped change list row title carries the full relative path");

await act(async () => {
  root2.unmount();
});
dom2.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
