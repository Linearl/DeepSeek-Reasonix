// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1468b-side-files-menu-alignment.test.tsx
// tl1468b: the dock row menu (产物/参考) catches up with the 【文件】tab's
// (WorkspaceTreeMenu) remaining items. One live-root section per contract:
// - item-set alignment: open / reveal / TERMINAL / copy pair / RENAME /
//   TRASH / DELETE-PERMANENT / add-reference render in the tree's order;
// - the ops gate: sideFileWorkspaceRelativePath — relative shapes and
//   absolute-inside-the-root shapes are workspace-managed (all four new
//   items render); absolute-outside rows keep the tl1437 read-only set
//   (the backend fences Trash/Rename/DeletePermanent to the workspace root
//   and the terminal bridge refuses absolute shapes — no dead items);
// - the ACTION layer is the tree's own bridges (same functions, no second
//   copy): TrashWorkspaceEntriesForTab / DeleteWorkspaceEntriesPermanentForTab /
//   RenameWorkspaceEntryForTab receive the CANONICAL path bytes (the tl1468
//   display pin holds — display paths never leak into operations), and the
//   terminal rides the tree's onOpenInTerminal channel (App passes the same
//   openTerminalForPath callback WorkspacePanel gets) with the workspace-
//   RELATIVE slice the CreateTerminalForTab bridge accepts;
// - confirms: trash gets the dock confirm that says the row is a REAL file
//   on disk (the tree stays confirm-less for trash); delete-permanent gets
//   the danger dialog with the heavier dock wording (skips the Trash,
//   cannot be undone); both cancel before any bridge call, and both re-check
//   the tab-scope guard after the dialog resolves;
// - rename: the tree's inline input (Enter commits through the rename
//   bridge, Escape cancels with no bridge call);
// - probe-none keeps the tl1421 rule for the opener family AND the terminal
//   (removed — no empty-panel flash) while rename/trash/delete remain
//   gated by the workspace fence only;
// - wiring: App passes onOpenInTerminal={openTerminalForPath}; the three
//   new locale keys exist in zh / zh-TW / en and the reused keys still do.

import { readFileSync } from "node:fs";
import { createElement } from "react";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
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
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

const WS = "C:\\ws";

// Bridge mocks (tl1437 harness shape) — one recorder per bridge so every
// assertion names the exact call. Trash/Delete/Rename/Resolve are the TREE's
// same ForTab bridges; a missing mock would throw, which is a pin in itself.
const bridge = {
  probeResult: "C:\\ws\\out\\report.md",
  probeCalls: [] as Array<{ tabId: string; path: string }>,
  resolveCalls: [] as Array<{ tabId: string; path: string }>,
  trashCalls: [] as Array<{ tabId: string; paths: string[] }>,
  deletePermanentCalls: [] as Array<{ tabId: string; paths: string[] }>,
  renameCalls: [] as Array<{ tabId: string; path: string; newName: string }>,
  terminalCalls: [] as string[],
};
(window as unknown as { go: unknown }).go = {
  main: {
    App: {
      ResolveOpenablePathForTab: async (tabID: string, path: string) => {
        bridge.probeCalls.push({ tabId: tabID, path });
        return bridge.probeResult;
      },
      ResolveWorkspacePathForTab: async (tabID: string, path: string) => {
        bridge.resolveCalls.push({ tabId: tabID, path });
        return `${WS}\\${path.replace(/^[/\\]+/, "")}`;
      },
      OpenLocalPath: async () => {},
      RevealPath: async () => {},
      TrashWorkspaceEntriesForTab: async (tabID: string, paths: string[]) => {
        bridge.trashCalls.push({ tabId: tabID, paths });
        return { succeeded: [...paths], failed: [] };
      },
      DeleteWorkspaceEntriesPermanentForTab: async (tabID: string, paths: string[]) => {
        bridge.deletePermanentCalls.push({ tabId: tabID, paths });
        return { succeeded: [...paths], failed: [] };
      },
      RenameWorkspaceEntryForTab: async (tabID: string, path: string, newName: string) => {
        bridge.renameCalls.push({ tabId: tabID, path, newName });
        return path.replace(/[^\\/]+$/, newName);
      },
    },
  },
};

let clipboardText = "";
Object.defineProperty(dom.window.navigator, "clipboard", {
  configurable: true,
  value: { writeText: async (text: string) => { clipboardText = text; } },
});

const flushTimers = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("en");
const { sideFileWorkspaceRelativePath } = await import("../lib/sideFilePathDisplay");
const { SideFilesDockPanel } = await import("../components/SessionSideFilesPanel");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

console.log("\ntl1468b 0: the ops gate helper (sideFileWorkspaceRelativePath)");

// Relative shapes pass through untouched; absolute-inside yields the
// root-relative slice with the entry's own separators; anything else is "".
eq(sideFileWorkspaceRelativePath("out/report.md", WS), "out/report.md", "gate: relative shape passes the canonical bytes through");
eq(sideFileWorkspaceRelativePath(`${WS}\\out\\a.md`, WS), "out\\a.md", "gate: absolute-inside yields the root-relative slice (own separators)");
eq(sideFileWorkspaceRelativePath(`C:/WS/out/a.md`, WS), "out/a.md", "gate: folded containment (tl1414) still slices relative");
eq(sideFileWorkspaceRelativePath("D:\\logs\\trace.txt", WS), "", "gate: absolute outside the root is not workspace-managed");
eq(sideFileWorkspaceRelativePath(`${WS}\\a.md`, ""), "", "gate: unknown workspace root never claims an absolute path");
eq(sideFileWorkspaceRelativePath("out/report.md", ""), "out/report.md", "gate: relative shape stays manageable even with no root info");

console.log("\ntl1468b 1: menu item-set alignment with the 【文件】tab");

function item(kind: string, name: string, args: string) {
  return { kind, name, args };
}
const REL_ROW = item("tool", "write_file", JSON.stringify({ path: "out\\report.md", content: "x" }));
const ABS_IN = item("tool", "write_file", JSON.stringify({ path: `${WS}\\out\\a.md`, content: "x" }));
const ABS_OUT = item("tool", "write_file", JSON.stringify({ path: "D:\\logs\\trace.txt", content: "y" }));

interface PanelHandle {
  host: HTMLElement;
  cleanup: () => Promise<void>;
}

async function renderPanel(items: readonly unknown[], options: {
  tabId?: string;
  workspaceRoot?: string;
  sessionPath?: string;
  variant?: "artifacts" | "references";
  onInject?: (text: string) => void;
  onTerminal?: (path: string) => void;
}): Promise<PanelHandle> {
  const host = document.createElement("div");
  document.getElementById("root")!.appendChild(host);
  const root = createRoot(host);
  await act(async () => {
    root.render(createElement(
      LocaleProvider,
      { locale: "en" },
      createElement(SideFilesDockPanel, {
        items,
        variant: options.variant ?? "artifacts",
        tabId: options.tabId,
        workspaceRoot: options.workspaceRoot,
        sessionPath: options.sessionPath,
        onInjectReferences: options.onInject,
        onOpenInTerminal: options.onTerminal,
      } as never),
    ));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  return {
    host,
    cleanup: async () => {
      await act(async () => { root.unmount(); });
      host.remove();
    },
  };
}

function rowByPath(host: HTMLElement, path: string): HTMLButtonElement | undefined {
  return Array.from(host.querySelectorAll<HTMLButtonElement>(".side-files__row"))
    .find((b) => b.getAttribute("data-side-file-path") === path);
}
function menuButtons(): HTMLButtonElement[] {
  return Array.from(document.body.querySelectorAll<HTMLButtonElement>(".floating-menu.workspace-tree-menu button"));
}
function menuButtonByLabel(label: string): HTMLButtonElement | undefined {
  return menuButtons().find((b) => b.textContent?.includes(label));
}
function menuLabels(): string[] {
  return menuButtons().map((b) => b.textContent?.trim() ?? "");
}
function separators(): Element[] {
  return Array.from(document.body.querySelectorAll(".floating-menu.workspace-tree-menu .floating-menu__separator"));
}
async function openMenu(handle: PanelHandle, path: string): Promise<void> {
  const row = rowByPath(handle.host, path)!;
  await act(async () => {
    row.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 12, clientY: 34 }));
    await flushTimers();
  });
}
async function clickMenuItem(label: string): Promise<void> {
  await act(async () => {
    menuButtonByLabel(label)!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
}
function dialogElement(): HTMLElement | undefined {
  return document.body.querySelector<HTMLElement>(".reasonix-confirm-dialog") ?? undefined;
}
function dialogButtons(): HTMLButtonElement[] {
  return Array.from(document.body.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog__actions button"));
}
function dialogButtonByLabel(label: string): HTMLButtonElement | undefined {
  return dialogButtons().find((b) => b.textContent?.trim() === label);
}

// The full tree-aligned set for a workspace-managed row, in the tree's order.
{
  const terminalPaths: string[] = [];
  const { host, cleanup } = await renderPanel([REL_ROW], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: "C:\\sessions\\s.jsonl",
    onTerminal: (p) => terminalPaths.push(p),
    onInject: () => {},
  });
  eq(document.body.querySelectorAll(".floating-menu").length, 0, "default view: no menu until a right-click (nothing new visible)");
  await openMenu({ host, cleanup } as PanelHandle, "out\\report.md");
  const labels = menuLabels();
  const find = (label: string) => labels.findIndex((l) => l.includes(label));
  for (const label of [
    "Open with default app",
    "Show in file manager",
    "Open in integrated terminal",
    "Copy relative path",
    "Copy absolute path",
    "Rename",
    "Move to Trash",
    "Delete Permanently…",
    "Add file reference",
  ]) {
    eq(find(label) !== -1, true, `alignment: "${label}" present (tree parity)`);
  }
  eq(
    find("Open with default app") < find("Open in integrated terminal")
      && find("Open in integrated terminal") < find("Copy relative path")
      && find("Copy relative path") < find("Copy absolute path")
      && find("Copy absolute path") < find("Rename")
      && find("Rename") < find("Move to Trash")
      && find("Move to Trash") < find("Delete Permanently…")
      && find("Delete Permanently…") < find("Add file reference"),
    true,
    "alignment: items keep the tree menu's order (open/reveal/terminal/copy pair/rename/trash/delete/reference)",
  );
  eq(separators().length, 3, "alignment: exactly the three tree-shaped separators");
  // Terminal channel: the tree's own prop — a relative-shaped row passes the
  // canonical bytes through (the terminal bridge resolves them itself).
  bridge.terminalCalls.length = 0;
  await clickMenuItem("Open in integrated terminal");
  eq(JSON.stringify(terminalPaths), JSON.stringify(["out\\report.md"]), "terminal: relative shape routes the canonical path through onOpenInTerminal (App.openTerminalForPath)");
  eq(document.body.querySelectorAll(".floating-menu").length, 0, "terminal: selecting the item closes the menu");
  await cleanup();
}

console.log("\ntl1468b 2: the workspace gate (backend fence parity — no dead items)");

{
  const { host, cleanup } = await renderPanel([ABS_IN, ABS_OUT], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: "",
    onTerminal: (p) => bridge.terminalCalls.push(p),
    onInject: () => {},
  });
  await openMenu({ host, cleanup } as PanelHandle, `${WS}\\out\\a.md`);
  for (const label of ["Open in integrated terminal", "Rename", "Move to Trash", "Delete Permanently…"]) {
    eq(menuButtonByLabel(label) !== undefined, true, `gate: absolute-inside row still gets "${label}"`);
  }
  // Terminal receives the ROOT-RELATIVE slice — the terminal bridge refuses
  // absolute shapes (resolveTerminalStartDir → errTerminalOutside).
  bridge.terminalCalls.length = 0;
  await clickMenuItem("Open in integrated terminal");
  eq(JSON.stringify(bridge.terminalCalls), JSON.stringify(["out\\a.md"]), "gate: terminal gets the root-relative slice for an absolute-inside row");
  await openMenu({ host, cleanup } as PanelHandle, "D:\\logs\\trace.txt");
  for (const label of ["Open in integrated terminal", "Rename", "Move to Trash", "Delete Permanently…"]) {
    eq(menuButtonByLabel(label) === undefined, true, `gate: absolute-outside row drops "${label}" (backend fence would reject it)`);
  }
  for (const label of ["Open with default app", "Show in file manager", "Copy relative path", "Copy absolute path", "Add file reference"]) {
    eq(menuButtonByLabel(label) !== undefined, true, `gate: outside row keeps the read-only "${label}"`);
  }
  await cleanup();
  // Unknown workspace root: relative shapes stay manageable, absolute shapes
  // lose the ops block (safe default — never offer what cannot be proven).
  const noRoot = await renderPanel([REL_ROW, ABS_OUT], { tabId: "tab-1", onTerminal: () => {} });
  await openMenu(noRoot, "out\\report.md");
  eq(menuButtonByLabel("Move to Trash") !== undefined, true, "gate: relative row without root info still gets the ops block");
  await openMenu(noRoot, "D:\\logs\\trace.txt");
  eq(menuButtonByLabel("Move to Trash") === undefined, true, "gate: absolute row without root info drops the ops block");
  await noRoot.cleanup();
}

console.log("\ntl1468b 3: trash — dock confirm says REAL FILE, tree bridge, canonical bytes");

{
  const { host, cleanup } = await renderPanel([ABS_IN], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: "",
  });
  await openMenu({ host, cleanup } as PanelHandle, `${WS}\\out\\a.md`);
  await clickMenuItem("Move to Trash");
  const dialog = dialogElement();
  eq(dialog !== undefined, true, "trash: the confirm dialog opens (the tree's confirm-less trash is not copied — dock rows read like list entries)");
  eq(dialog!.querySelector(".reasonix-confirm-dialog__title")?.textContent, "Move to Trash", "trash: dialog title is the Move-to-Trash action");
  const message = dialog!.querySelector(".reasonix-confirm-dialog__message")?.textContent ?? "";
  eq(message.includes(`${WS}\\out\\a.md`), true, "trash: dialog names the CANONICAL path (display path never leaks in)");
  eq(message.includes("real file on disk"), true, "trash: dialog says the row is a real file on disk (user ruling: 确认框必须明示)");
  // Cancel first: no bridge call.
  await act(async () => {
    dialogButtonByLabel("Cancel")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(dialogElement() === undefined, true, "trash: cancel dismisses the dialog");
  eq(bridge.trashCalls.length, 0, "trash: cancel calls no bridge");
  // Confirm: the tree's TrashWorkspaceEntriesForTab, canonical path, tab scope.
  await openMenu({ host, cleanup } as PanelHandle, `${WS}\\out\\a.md`);
  await clickMenuItem("Move to Trash");
  await act(async () => {
    dialogButtonByLabel("Move to Trash")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(dialogElement() === undefined, true, "trash: confirming dismisses the dialog");
  eq(bridge.trashCalls.length, 1, "trash: exactly one trash bridge call after confirm");
  eq(bridge.trashCalls[0].tabId, "tab-1", "trash: bridge is the tree's tab-scoped TrashWorkspaceEntriesForTab");
  eq(JSON.stringify(bridge.trashCalls[0].paths), JSON.stringify([`${WS}\\out\\a.md`]), "trash: bridge receives the canonical path");
  await cleanup();
}

console.log("\ntl1468b 4: delete-permanent — heavier dock wording, danger tone, tree bridge");

{
  const { host, cleanup } = await renderPanel([ABS_IN], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: "",
  });
  await openMenu({ host, cleanup } as PanelHandle, `${WS}\\out\\a.md`);
  await clickMenuItem("Delete Permanently…");
  const dialog = dialogElement();
  eq(dialog !== undefined, true, "delete: the confirm dialog opens");
  eq(dialog!.querySelector(".reasonix-confirm-dialog__title")?.textContent, "Permanently delete", "delete: title reuses the tree's delete-permanent title key");
  const message = dialog!.querySelector(".reasonix-confirm-dialog__message")?.textContent ?? "";
  eq(message.includes(`${WS}\\out\\a.md`), true, "delete: dialog names the canonical path");
  eq(message.includes("does NOT go to the Trash"), true, "delete: dock wording is heavier — says it skips the Trash");
  eq(message.includes("cannot be undone"), true, "delete: dock wording says it cannot be undone");
  const confirmButton = dialogButtonByLabel("Delete Permanently");
  eq(confirmButton?.className.includes("btn--danger"), true, "delete: confirm button carries the danger tone");
  await act(async () => {
    dialogButtonByLabel("Cancel")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(bridge.deletePermanentCalls.length, 0, "delete: cancel calls no bridge");
  await openMenu({ host, cleanup } as PanelHandle, `${WS}\\out\\a.md`);
  await clickMenuItem("Delete Permanently…");
  await act(async () => {
    dialogButtonByLabel("Delete Permanently")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(bridge.deletePermanentCalls.length, 1, "delete: exactly one delete bridge call after confirm");
  eq(bridge.deletePermanentCalls[0].tabId, "tab-1", "delete: bridge is the tree's tab-scoped DeleteWorkspaceEntriesPermanentForTab");
  eq(JSON.stringify(bridge.deletePermanentCalls[0].paths), JSON.stringify([`${WS}\\out\\a.md`]), "delete: bridge receives the canonical path");
  await cleanup();
}

console.log("\ntl1468b 5: rename — the tree's inline input through the tree's bridge");

{
  const { host, cleanup } = await renderPanel([ABS_IN], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: "",
  });
  await openMenu({ host, cleanup } as PanelHandle, `${WS}\\out\\a.md`);
  await clickMenuItem("Rename");
  eq(document.body.querySelectorAll(".floating-menu").length, 0, "rename: picking the item closes the menu");
  const renaming = host.querySelector("[data-side-file-renaming]");
  eq(renaming !== null, true, "rename: the row swaps to the inline input (the tree's rename row shape)");
  const input = renaming!.querySelector("input")!;
  eq(input.getAttribute("aria-label"), "Rename", "rename: the input carries the tree's rename label");
  eq((input as HTMLInputElement).value, "a.md", "rename: the input seeds with the entry's basename");
  eq(bridge.renameCalls.length, 0, "rename: nothing commits before Enter");
  // Escape cancels — no bridge call, the row button comes back.
  await act(async () => {
    input.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    await flushTimers();
  });
  eq(host.querySelector("[data-side-file-renaming]") === null, true, "rename: Escape leaves the inline editor");
  eq(bridge.renameCalls.length, 0, "rename: Escape calls no bridge");
  eq(rowByPath(host, `${WS}\\out\\a.md`) !== undefined, true, "rename: the row button is back after cancel");
  // Enter commits through RenameWorkspaceEntryForTab with the canonical path.
  await openMenu({ host, cleanup } as PanelHandle, `${WS}\\out\\a.md`);
  await clickMenuItem("Rename");
  const input2 = host.querySelector("[data-side-file-renaming] input") as HTMLInputElement;
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!;
  await act(async () => {
    setter.call(input2, "renamed.md");
    input2.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
    await flushTimers();
  });
  await act(async () => {
    input2.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(bridge.renameCalls.length, 1, "rename: Enter commits exactly once");
  eq(bridge.renameCalls[0].tabId, "tab-1", "rename: bridge is the tree's tab-scoped RenameWorkspaceEntryForTab");
  eq(bridge.renameCalls[0].path, `${WS}\\out\\a.md`, "rename: bridge receives the canonical path");
  eq(bridge.renameCalls[0].newName, "renamed.md", "rename: bridge receives the typed name");
  await cleanup();
}

console.log("\ntl1468b 6: probe-none keeps the tl1421 rule; the ops block follows the fence only");

{
  bridge.probeResult = "";
  const { host, cleanup } = await renderPanel([REL_ROW], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: "",
    onTerminal: () => {},
    onInject: () => {},
  });
  await openMenu({ host, cleanup } as PanelHandle, "out\\report.md");
  for (const label of ["Open with default app", "Show in file manager", "Open in integrated terminal"]) {
    eq(menuButtonByLabel(label) === undefined, true, `probe-none: "${label}" removed (no dead opener/terminal buttons)`);
  }
  for (const label of ["Copy relative path", "Copy absolute path", "Rename", "Move to Trash", "Delete Permanently…", "Add file reference"]) {
    eq(menuButtonByLabel(label) !== undefined, true, `probe-none: "${label}" survives (gated by the fence, not the opener probe)`);
  }
  eq(separators().length, 2, "probe-none: separators collapse with their blocks (no stacked separators)");
  await cleanup();
  bridge.probeResult = "C:\\ws\\out\\report.md";
}

console.log("\ntl1468b 7: the references variant gets the same aligned menu (shared DockSideFileList)");

{
  const READ_REL = item("tool", "read_file", JSON.stringify({ path: "src/config.json" }));
  const { host, cleanup } = await renderPanel([READ_REL], {
    variant: "references",
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: "",
    onTerminal: () => {},
  });
  await openMenu({ host, cleanup } as PanelHandle, "src/config.json");
  for (const label of ["Open in integrated terminal", "Rename", "Move to Trash", "Delete Permanently…"]) {
    eq(menuButtonByLabel(label) !== undefined, true, `references: "${label}" present too (shared list, same alignment)`);
  }
  await cleanup();
}

console.log("\ntl1468b 8: wiring — App passes the tree's terminal callback; locales carry the keys");

{
  const app = readFileSync(new URL("../App.tsx", import.meta.url), "utf8");
  eq(/<SideFilesDockPanel[\s\S]{0,600}?onOpenInTerminal=\{openTerminalForPath\}/.test(app), true, "app: the dock gets the SAME openTerminalForPath callback the WorkspacePanel tree menu gets");
  const panel = readFileSync(new URL("../components/SessionSideFilesPanel.tsx", import.meta.url), "utf8");
  // The ACTION layer is the tree's bridges — no second copy anywhere.
  for (const bridgeName of [
    "TrashWorkspaceEntriesForTab",
    "DeleteWorkspaceEntriesPermanentForTab",
    "RenameWorkspaceEntryForTab",
  ]) {
    eq(panel.includes(`app.${bridgeName}`), true, `bridges: the dock calls the tree's ${bridgeName} (same function, not a copy)`);
  }
  for (const key of [
    "sideFiles.rowTrashConfirmTitle",
    "sideFiles.rowTrashConfirmMessage",
    "sideFiles.rowDeletePermanentConfirmMessage",
  ]) {
    for (const locale of ["zh", "zh-TW", "en"]) {
      const src = readFileSync(new URL(`../locales/${locale}.ts`, import.meta.url), "utf8");
      eq(src.includes(`"${key}"`), true, `locales/${locale}: ${key} present`);
    }
    eq(panel.includes(key), true, `panel: uses ${key}`);
  }
  for (const key of [
    "workspace.openInTerminal",
    "workspace.renameEntry",
    "workspace.trashEntry",
    "workspace.deletePermanentEntry",
    "workspace.deletePermanentConfirmTitle",
    "workspace.deletePermanentConfirmLabel",
    "workspace.fileOpFailed",
    "workspace.fileOpsFailedCount",
    "workspace.renameFailed",
    "common.cancel",
  ]) {
    for (const locale of ["zh", "zh-TW", "en"]) {
      const src = readFileSync(new URL(`../locales/${locale}.ts`, import.meta.url), "utf8");
      eq(src.includes(`"${key}"`), true, `locales/${locale}: reused key ${key} still present`);
    }
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
