// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/workspace-file-ops.test.tsx

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { WorkspacePanel } from "../components/WorkspacePanel";
import type { AppBindings, WorkspaceFileOpsResult } from "../lib/bridge";
import { LocaleProvider } from "../lib/i18n";
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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await act(async () => {
      await flushTimers();
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
  // React's beforeinput polyfill probes IE-only APIs on the active element.
  (dom.window.HTMLElement.prototype as unknown as Record<string, unknown>).attachEvent = () => {};
  (dom.window.HTMLElement.prototype as unknown as Record<string, unknown>).detachEvent = () => {};
  Object.defineProperty(dom.window.HTMLElement.prototype, "scrollIntoView", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "offsetWidth", { configurable: true, get: () => 320 });
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

function menuLabels(): string[] {
  return Array.from(document.querySelectorAll<HTMLButtonElement>(".workspace-tree-menu button")).map(
    (button) => button.textContent?.trim() ?? "",
  );
}

function menuButton(label: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>(".workspace-tree-menu button")).find(
    (button) => button.textContent?.trim() === label,
  );
}

async function clickRow(path: string, modifiers: { ctrl?: boolean; shift?: boolean } = {}) {
  const row = document.querySelector<HTMLButtonElement>(`[data-workspace-path="${path}"]`);
  if (!row) throw new Error(`missing workspace row ${path}`);
  await act(async () => {
    row.dispatchEvent(new window.MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      clientX: 30,
      clientY: 30,
      ctrlKey: modifiers.ctrl ?? false,
      shiftKey: modifiers.shift ?? false,
      metaKey: false,
    }));
    await flushTimers();
  });
}

async function openRowMenu(path: string) {
  const row = document.querySelector<HTMLButtonElement>(`[data-workspace-path="${path}"]`);
  if (!row) throw new Error(`missing workspace row ${path}`);
  await act(async () => {
    row.dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 30 }));
    await flushTimers();
  });
}

async function closeMenu() {
  await act(async () => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    window.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
}

function selectedRows(): string[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".workspace-tree__row--active")).map(
    (row) => row.dataset.workspacePath ?? "",
  ).filter(Boolean).sort();
}

console.log("\nworkspace file ops (rename / trash / delete-permanent / multi-select)");

resetWorkspaceTreeMemoryForTests();
const dom = installDom();
const trashCalls: Array<{ tabId: string; rels: string[] }> = [];
const permanentCalls: Array<{ tabId: string; rels: string[] }> = [];
const renameCalls: Array<{ tabId: string; rel: string; newName: string }> = [];
window.go = {
  main: {
    App: {
      ListDirForTab: async (_tabId, dir) => dir === ""
        ? [
            { name: "docs", isDir: true },
            { name: "a.md", isDir: false },
            { name: "b.md", isDir: false },
            { name: "c.md", isDir: false },
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
      OpenWorkspacePathForTab: async () => {},
      TrashWorkspaceEntriesForTab: async (tabId: string, rels: string[]): Promise<WorkspaceFileOpsResult> => {
        trashCalls.push({ tabId, rels: [...rels] });
        return { succeeded: [...rels], failed: [] };
      },
      DeleteWorkspaceEntriesPermanentForTab: async (tabId: string, rels: string[]): Promise<WorkspaceFileOpsResult> => {
        permanentCalls.push({ tabId, rels: [...rels] });
        return { succeeded: [...rels], failed: [] };
      },
      RenameWorkspaceEntryForTab: async (tabId: string, rel: string, newName: string) => {
        renameCalls.push({ tabId, rel, newName });
        const parent = rel.replace(/\/$/, "").split("/").slice(0, -1).join("/");
        return `${parent}${parent ? "/" : ""}${newName}`;
      },
    } as Partial<AppBindings> as AppBindings,
  },
};

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
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
  await flushTimers();
});

await waitFor("workspace rows", () => document.querySelector('[data-workspace-path="a.md"]') != null);

// --- Ctrl-click accumulates the selection with a visible selected state ---
await clickRow("a.md");
ok(JSON.stringify(selectedRows()) === JSON.stringify(["a.md"]), "plain click selects a single row");

await clickRow("c.md", { ctrl: true });
ok(JSON.stringify(selectedRows()) === JSON.stringify(["a.md", "c.md"]), "ctrl-click adds a row to the selection");

await clickRow("c.md", { ctrl: true });
ok(JSON.stringify(selectedRows()) === JSON.stringify(["a.md"]), "ctrl-click on a selected row deselects it");

// Selection assertions report the visually highlighted rows: the preview
// selection (plain click) union the multi-selection (Ctrl/Shift).
// --- Shift-click selects the range from the anchor (still c.md) back to b.md ---
await clickRow("b.md", { shift: true });
ok(
  JSON.stringify(selectedRows()) === JSON.stringify(["a.md", "b.md", "c.md"]),
  "shift-click selects the anchor-to-row range (preview selection stays highlighted)",
);

// --- Ctrl+A selects every visible row ---
await act(async () => {
  document.querySelector<HTMLElement>('[data-workspace-path="a.md"]')?.focus();
  const tree = document.querySelector<HTMLElement>(".workspace-tree");
  tree?.dispatchEvent(new window.KeyboardEvent("keydown", {
    key: "a",
    bubbles: true,
    cancelable: true,
    ctrlKey: true,
  }));
  await flushTimers();
});
ok(selectedRows().length === 4, "ctrl+A selects all visible rows");

// --- Escape clears the multi-selection but keeps the preview selection ---
await act(async () => {
  document.querySelector<HTMLElement>('[data-workspace-path="a.md"]')?.focus();
  const tree = document.querySelector<HTMLElement>(".workspace-tree");
  tree?.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  await flushTimers();
});
ok(
  JSON.stringify(selectedRows()) === JSON.stringify(["a.md"]),
  "escape clears the multi-selection and keeps the preview selection",
);

// --- Right-click keeps the visually selected set as the menu target ---
// c.md re-opens the preview (a.md's was toggled off above); ctrl-clicks add
// a.md and b.md, so the visual selection is {c.md preview} + {a.md, b.md}.
await clickRow("c.md");
await clickRow("a.md", { ctrl: true });
await clickRow("b.md", { ctrl: true });
ok(
  JSON.stringify(selectedRows()) === JSON.stringify(["a.md", "b.md", "c.md"]),
  "ctrl-click rebuilds the selection around the preview selection",
);
await openRowMenu("b.md");
ok(
  menuLabels().includes("Move 3 items to Trash"),
  "right-click inside the selection offers the batch trash action",
);
const renameButton = menuButton("Rename");
ok(renameButton?.disabled === true, "rename is disabled for a multi-selection");
ok(
  menuLabels().includes("Permanently delete 3 items…"),
  "right-click inside the selection offers the batch permanent delete",
);

const batchTrash = menuButton("Move 3 items to Trash");
await act(async () => {
  batchTrash?.click();
  await flushTimers();
});
ok(
  trashCalls.length === 1 && JSON.stringify(trashCalls[0].rels) === JSON.stringify(["a.md", "b.md", "c.md"]),
  "batch delete sends the whole selection to the trash backend in one call",
);
await closeMenu();

// --- Right-click outside the selection collapses to the single row ---
await openRowMenu("c.md");
ok(menuLabels().includes("Rename") && menuButton("Rename")?.disabled === false, "single-row menu enables rename");
ok(menuLabels().includes("Move to Trash"), "single-row menu offers trash");
ok(menuLabels().includes("Delete Permanently…"), "single-row menu offers permanent delete");

// --- Permanent delete requires the double confirmation ---
const permanentButton = menuButton("Delete Permanently…");
await act(async () => {
  permanentButton?.click();
  await flushTimers();
});
ok(permanentCalls.length === 0, "permanent delete waits for confirmation before calling the backend");
const confirmButton = Array.from(document.querySelectorAll<HTMLButtonElement>(".confirm-dialog button, [role=dialog] button"))
  .find((button) => button.textContent?.trim() === "Delete Permanently");
ok(confirmButton != null, "the double-confirm dialog appears");
const dialogText = document.querySelector("[role=dialog], .confirm-dialog")?.textContent ?? "";
ok(dialogText.includes("cannot be undone"), "the confirm dialog states the action cannot be undone");
ok(dialogText.includes("1"), "the confirm dialog shows the batch size");

await act(async () => {
  confirmButton?.click();
  await flushTimers();
});
ok(
  permanentCalls.length === 1 && JSON.stringify(permanentCalls[0].rels) === JSON.stringify(["c.md"]),
  "confirming performs the permanent delete",
);

// --- Cancel keeps everything untouched ---
await openRowMenu("a.md");
await act(async () => {
  menuButton("Delete Permanently…")?.click();
  await flushTimers();
});
const cancelButton = Array.from(document.querySelectorAll<HTMLButtonElement>("[role=dialog] button, .confirm-dialog button"))
  .find((button) => button.textContent?.trim() === "Cancel");
await act(async () => {
  cancelButton?.click();
  await flushTimers();
});
ok(permanentCalls.length === 1, "cancelling the dialog leaves the backend untouched");

// --- Trash with no confirmation dialog (recycle-bin semantics) ---
const beforeTrash = trashCalls.length;
await openRowMenu("a.md");
await act(async () => {
  menuButton("Move to Trash")?.click();
  await flushTimers();
});
ok(trashCalls.length === beforeTrash + 1, "trash deletes immediately without a confirmation dialog");

// --- Inline rename commits through the backend ---
await closeMenu();
await openRowMenu("a.md");
await act(async () => {
  menuButton("Rename")?.click();
  await flushTimers();
});
let renameInput = document.querySelector<HTMLInputElement>(".workspace-tree__rename-input");
ok(renameInput != null, "rename swaps the row into an inline editor");
await act(async () => { await flushTimers(); });
renameInput = document.querySelector<HTMLInputElement>(".workspace-tree__rename-input");
await act(async () => {
  renameInput = document.querySelector<HTMLInputElement>(".workspace-tree__rename-input");
  if (renameInput) {
    renameInput.addEventListener("input", () => {});
    const setValue = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
    setValue?.call(renameInput, "renamed.md");
    renameInput.dispatchEvent(new window.Event("input", { bubbles: true }));
  }
  await flushTimers();
});
renameInput = document.querySelector<HTMLInputElement>(".workspace-tree__rename-input");
// React flushes the pending controlled-input onChange at the next discrete
// event boundary rather than inside the dispatchEvent call — a harmless key
// first, then Enter, so the commit sees the typed value.
await act(async () => {
  renameInput?.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Shift", bubbles: true, cancelable: true }));
  await flushTimers();
});
await act(async () => {
  renameInput?.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  await flushTimers();
});
ok(
  renameCalls.length === 1 && renameCalls[0].rel === "a.md" && renameCalls[0].newName === "renamed.md",
  `enter commits the rename through the backend (calls=${JSON.stringify(renameCalls)})`,
);

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
