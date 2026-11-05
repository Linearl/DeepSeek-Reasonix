// Run: tsx src/__tests__/tl1437-side-files-interactions.test.tsx
// tl1437: the artifacts dock list catches up with the file tree's row
// interactions (the user's 20261105 screenshot gap list). One live-root
// section per interaction:
// - selection: click selects a row (single, aria-selected), click moves it,
//   right-click selects the right-clicked row;
// - row menu: the file tree's FloatingMenu with copy relative/absolute,
//   probe-gated open/reveal (tl1421 ResolveOpenablePathForTab convention —
//   disabled while probing, removed when the probe comes back empty) and
//   add-file-reference reusing the dock's composer-injection channel;
// - copy actions: relative copies the listed path, absolute resolves through
//   ResolveWorkspacePathForTab (absolute-shaped paths copy as-is, no bridge);
// - open/reveal call OpenLocalPath/RevealPath with the probed absolute path;
// - dismiss: Escape and outside mousedown close the menu;
// - fallback: without a tabId the artifacts body is the static task-260 list;
// - wiring: App passes tabId; reused locale keys exist in all three languages.

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

// Bridge mocks (resolved at call time via window.go) — every interaction gets
// its own recorder so each assertion names the exact bridge call.
const bridge = {
  probeResult: "" as string,
  probeCalls: [] as Array<{ tabId: string; path: string }>,
  resolveCalls: [] as Array<{ tabId: string; path: string }>,
  resolvePrefix: "C:/proj",
  openCalls: [] as string[],
  revealCalls: [] as string[],
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
        return `${bridge.resolvePrefix}/${path.replace(/^[/\\]+/, "")}`;
      },
      OpenLocalPath: async (path: string) => {
        bridge.openCalls.push(path);
      },
      RevealPath: async (path: string) => {
        bridge.revealCalls.push(path);
      },
    },
  },
};

// Clipboard recorder for the copy-action assertions (writeClipboardText tries
// the async Clipboard API first).
let clipboardText = "";
Object.defineProperty(dom.window.navigator, "clipboard", {
  configurable: true,
  value: { writeText: async (text: string) => { clipboardText = text; } },
});

const flushTimers = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("en");
const { SideFilesDockPanel } = await import("../components/SessionSideFilesPanel");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

function item(kind: string, name: string, args: string) {
  return { kind, name, args };
}
const WRITE_REL = item("tool", "write_file", JSON.stringify({ path: "out/report.txt", content: "x" }));
const WRITE_ABS = item("tool", "write_file", JSON.stringify({ path: "D:/logs/trace.txt", content: "y" }));

interface PanelHandle {
  host: HTMLElement;
  root: ReturnType<typeof createRoot>;
  cleanup: () => Promise<void>;
}

async function renderPanel(items: readonly unknown[], options: { tabId?: string; onInject?: (text: string) => void }): Promise<PanelHandle> {
  const host = document.createElement("div");
  document.getElementById("root")!.appendChild(host);
  const root = createRoot(host);
  await act(async () => {
    root.render(createElement(
      LocaleProvider,
      { locale: "en" },
      createElement(SideFilesDockPanel, {
        items,
        variant: "artifacts",
        tabId: options.tabId,
        onInjectReferences: options.onInject,
      } as never),
    ));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  return {
    host,
    root,
    cleanup: async () => {
      await act(async () => { root.unmount(); });
      host.remove();
    },
  };
}

function rowButtons(host: HTMLElement): HTMLButtonElement[] {
  return Array.from(host.querySelectorAll<HTMLButtonElement>(".side-files__row"));
}
function rowByPath(host: HTMLElement, path: string): HTMLButtonElement | undefined {
  return rowButtons(host).find((b) => b.getAttribute("data-side-file-path") === path);
}
function menuButtons(): HTMLButtonElement[] {
  return Array.from(document.body.querySelectorAll<HTMLButtonElement>(".floating-menu.workspace-tree-menu button"));
}
function menuButtonByLabel(label: string): HTMLButtonElement | undefined {
  return menuButtons().find((b) => b.textContent?.includes(label));
}
async function openMenu(handle: PanelHandle, path: string): Promise<void> {
  const row = rowByPath(handle.host, path)!;
  await act(async () => {
    row.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 12, clientY: 34 }));
    await flushTimers();
  });
}

console.log("\ntl1437 artifacts row interactions");

// 1. Fallback: without a tabId the artifacts body is the static task-260 list.
{
  const { host, cleanup } = await renderPanel([WRITE_REL], {});
  eq(host.querySelectorAll(".side-files__row").length, 0, "fallback: no interactive rows without a tabId");
  eq(host.querySelector(".side-files__item")?.getAttribute("title"), "out/report.txt · write_file", "fallback: static item keeps the title attribute");
  eq(host.querySelector(".side-files__item span")?.textContent, "out/report.txt", "fallback: static item keeps the path span");
  await cleanup();
}

// 2. Selection: click selects (single), click moves, right-click selects.
{
  const { host, cleanup } = await renderPanel([WRITE_REL, WRITE_ABS], { tabId: "tab-1" });
  const rows = rowButtons(host);
  eq(rows.length, 2, "selection: two interactive rows render");
  eq(rows[0].getAttribute("role"), "option", "selection: rows carry the option role");
  eq(rows[0].getAttribute("aria-selected"), "false", "selection: nothing selected by default");
  await act(async () => {
    rows[0].dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(rowByPath(host, "out/report.txt")!.getAttribute("aria-selected"), "true", "selection: click selects the clicked row");
  eq(rowByPath(host, "out/report.txt")!.className.includes("side-files__row--selected"), true, "selection: selected row carries the selected class");
  await act(async () => {
    rowByPath(host, "D:/logs/trace.txt")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(rowByPath(host, "out/report.txt")!.getAttribute("aria-selected"), "false", "selection: single selection — the previous row deselects");
  eq(rowByPath(host, "D:/logs/trace.txt")!.getAttribute("aria-selected"), "true", "selection: the new row is selected");
  await act(async () => {
    rowByPath(host, "out/report.txt")!.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 1, clientY: 1 }));
    await flushTimers();
  });
  eq(rowByPath(host, "out/report.txt")!.getAttribute("aria-selected"), "true", "selection: right-click selects the right-clicked row");
  eq(document.body.querySelectorAll(".floating-menu.workspace-tree-menu").length, 1, "menu: right-click opens the row menu (file tree's FloatingMenu)");
  await cleanup();
}

// 3. Menu contents: copy pair + probe-gated open/reveal + add reference.
{
  bridge.probeResult = "C:/proj/out/report.txt";
  let injected = "";
  const { host, cleanup } = await renderPanel([WRITE_REL], { tabId: "tab-1", onInject: (text) => { injected = text; } });
  await openMenu({ host, cleanup } as PanelHandle, "out/report.txt");
  eq(menuButtonByLabel("Copy relative path") !== undefined, true, "menu: copy relative path item present (file tree's label)");
  eq(menuButtonByLabel("Copy absolute path") !== undefined, true, "menu: copy absolute path item present (file tree's label)");
  eq(menuButtonByLabel("Add file reference") !== undefined, true, "menu: add file reference item present (file tree's label)");
  const openBtn = menuButtonByLabel("Open with default app");
  const revealBtn = menuButtonByLabel("Show in file manager");
  eq(openBtn !== undefined, true, "menu: probe ok — open item present");
  eq(revealBtn !== undefined, true, "menu: probe ok — reveal item present");
  eq(openBtn!.disabled, false, "menu: probe ok — open item enabled");
  eq(revealBtn!.disabled, false, "menu: probe ok — reveal item enabled");
  eq(bridge.probeCalls.at(-1)?.tabId, "tab-1", "menu: probe runs against the tabId scope");
  eq(bridge.probeCalls.at(-1)?.path, "out/report.txt", "menu: probe receives the listed (relative) path");

  // 3a. Copy relative copies the listed path.
  clipboardText = "";
  await act(async () => {
    menuButtonByLabel("Copy relative path")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(clipboardText, "out/report.txt", "copy: relative copies the listed path");

  // 3b. Copy absolute resolves through the tab-scoped bridge (workspace-relative shape).
  clipboardText = "";
  await openMenu({ host, cleanup } as PanelHandle, "out/report.txt");
  await act(async () => {
    menuButtonByLabel("Copy absolute path")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(bridge.resolveCalls.at(-1)?.tabId, "tab-1", "copy: absolute resolve is tab-scoped (ResolveWorkspacePathForTab)");
  eq(clipboardText, "C:/proj/out/report.txt", "copy: absolute copies the resolved path");

  // 3c. Add reference injects the @path through the dock's composer channel.
  injected = "";
  await openMenu({ host, cleanup } as PanelHandle, "out/report.txt");
  await act(async () => {
    menuButtonByLabel("Add file reference")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(injected, "@out/report.txt", "reference: add-file-reference injects the @path (formatWorkspaceReference payload)");

  // 3d. Escape and outside mousedown dismiss the menu.
  await openMenu({ host, cleanup } as PanelHandle, "out/report.txt");
  eq(document.body.querySelectorAll(".floating-menu").length > 0, true, "dismiss: menu is open before Escape");
  await act(async () => {
    window.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  eq(document.body.querySelectorAll(".floating-menu").length, 0, "dismiss: Escape closes the menu");
  await openMenu({ host, cleanup } as PanelHandle, "out/report.txt");
  await act(async () => {
    window.dispatchEvent(new dom.window.MouseEvent("mousedown", { bubbles: true }));
    await flushTimers();
  });
  eq(document.body.querySelectorAll(".floating-menu").length, 0, "dismiss: outside mousedown closes the menu");
  await cleanup();
}

// 4. Probe states: in-flight disables, empty removes (tl1421: no dead buttons).
{
  let releaseProbe: ((value: string) => void) | null = null;
  const gated = new Promise<string>((resolve) => { releaseProbe = resolve; });
  const originalProbe = (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab;
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab = () => gated;
  const handle = await renderPanel([WRITE_REL], { tabId: "tab-1" });
  const row = rowByPath(handle.host, "out/report.txt")!;
  await act(async () => {
    row.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 2, clientY: 2 }));
    await flushTimers();
  });
  eq(menuButtonByLabel("Open with default app")!.disabled, true, "probe: open item disabled while the probe is in flight");
  eq(menuButtonByLabel("Show in file manager")!.disabled, true, "probe: reveal item disabled while the probe is in flight");
  await act(async () => {
    releaseProbe!(""); // the file moved away / opener gate refuses
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(menuButtonByLabel("Open with default app") === undefined, true, "probe: empty probe removes the open item (no dead button)");
  eq(menuButtonByLabel("Show in file manager") === undefined, true, "probe: empty probe removes the reveal item");
  eq(menuButtonByLabel("Copy relative path") !== undefined, true, "probe: copy items survive an empty probe");
  await act(async () => { await handle.cleanup(); });
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab = originalProbe;
}

// 5. Open/reveal actions use the probed absolute path (opener family).
{
  bridge.probeResult = "C:/proj/out/report.txt";
  bridge.openCalls = [];
  bridge.revealCalls = [];
  const handle = await renderPanel([WRITE_REL], { tabId: "tab-1" });
  await openMenu(handle, "out/report.txt");
  await act(async () => {
    menuButtonByLabel("Open with default app")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(JSON.stringify(bridge.openCalls), JSON.stringify(["C:/proj/out/report.txt"]), "open: OpenLocalPath receives the probed absolute path");
  await openMenu(handle, "out/report.txt");
  await act(async () => {
    menuButtonByLabel("Show in file manager")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(JSON.stringify(bridge.revealCalls), JSON.stringify(["C:/proj/out/report.txt"]), "reveal: RevealPath receives the probed absolute path");
  await handle.cleanup();
}

// 6. Absolute-shaped artifact path: copy-absolute copies as-is, no bridge resolve.
{
  bridge.resolveCalls = [];
  clipboardText = "";
  bridge.probeResult = "D:/logs/trace.txt";
  const handle = await renderPanel([WRITE_ABS], { tabId: "tab-1" });
  await openMenu(handle, "D:/logs/trace.txt");
  await act(async () => {
    menuButtonByLabel("Copy absolute path")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(bridge.resolveCalls.length, 0, "copy: absolute-shaped path does not hit the resolve bridge");
  eq(clipboardText, "D:/logs/trace.txt", "copy: absolute-shaped path copies as-is");
  await handle.cleanup();
}

// 7. App wiring + reused locale keys (source-shape assertions).
{
  const app = readFileSync(new URL("../App.tsx", import.meta.url), "utf8");
  eq(/<SideFilesDockPanel[\s\S]{0,400}?tabId=\{activeTabId\}/.test(app), true, "app: SideFilesDockPanel receives the active tab id");
  const panel = readFileSync(new URL("../components/SessionSideFilesPanel.tsx", import.meta.url), "utf8");
  for (const key of [
    "workspace.openWithDefaultApp",
    "workspace.revealInFileManager",
    "workspace.copyRelativePath",
    "workspace.copyAbsolutePath",
    "workspace.addFileReferenceToChat",
    "sideFiles.artifacts",
  ]) {
    eq(panel.includes(`t("${key}")`) || panel.includes(`t('${key}')`), true, `wiring: panel uses existing key ${key}`);
    for (const locale of ["zh", "zh-TW", "en"]) {
      const src = readFileSync(new URL(`../locales/${locale}.ts`, import.meta.url), "utf8");
      eq(src.includes(`"${key}"`), true, `locales/${locale}: ${key} present`);
    }
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
