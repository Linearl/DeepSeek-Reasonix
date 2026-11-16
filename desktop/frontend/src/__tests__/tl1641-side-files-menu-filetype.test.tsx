// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1641-side-files-menu-filetype.test.tsx
// tl1641: the artifacts dock row menu used to gate ALL three open-type items
// (open with default app / show in file manager / open in integrated
// terminal) behind the tl1421 opener probe — whose gate refuses executable
// extensions (.ps1/.bat/...), so a .ps1 artifact row lost ALL three items
// while a .md row kept nine. The fix splits the probe into two dimensions:
// open rides ResolveOpenablePathForTab (opener gate, unchanged), reveal and
// terminal ride the NEW ResolveRevealablePathForTab (same resolution +
// existence half, no opener gate — explorer /select and a terminal cd never
// execute the target). Sections:
// - .ps1 row: reveal + terminal present and enabled, open REMOVED (only the
//   open item is cut for opener-refused types) → the 8-item menu;
// - .md row: all nine items present (zero regression pin);
// - .exe + extensionless rows: same split holds (type contrast pins);
// - missing file: open/reveal/terminal all removed (dead-button guard kept);
// - in-flight: each probe disables only its own items (no cross-gating);
// - actions: reveal/terminal consume the reveal-probed absolute path and the
//   workspace-relative slice respectively;
// - wiring: App passes onOpenInTerminal; the nine locale keys exist in all
//   three languages.

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

// Bridge mocks — the two probes are independent recorders so each assertion
// names the exact dimension that produced it.
const bridge = {
  openProbeResult: "" as string,
  revealProbeResult: "" as string,
  openProbeCalls: [] as string[],
  revealProbeCalls: [] as string[],
  openCalls: [] as string[],
  revealCalls: [] as string[],
  terminalCalls: [] as string[],
};
(window as unknown as { go: unknown }).go = {
  main: {
    App: {
      ResolveOpenablePathForTab: async (_tabID: string, path: string) => {
        bridge.openProbeCalls.push(path);
        return bridge.openProbeResult;
      },
      ResolveRevealablePathForTab: async (_tabID: string, path: string) => {
        bridge.revealProbeCalls.push(path);
        return bridge.revealProbeResult;
      },
      ResolveWorkspacePathForTab: async (_tabID: string, path: string) =>
        `C:/proj/${path.replace(/^[/\\]+/, "")}`,
      OpenLocalPath: async (path: string) => {
        bridge.openCalls.push(path);
      },
      RevealPath: async (path: string) => {
        bridge.revealCalls.push(path);
      },
    },
  },
};

const flushTimers = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("en");
const { SideFilesDockPanel } = await import("../components/SessionSideFilesPanel");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

function item(name: string, path: string) {
  return { kind: "tool", name, args: JSON.stringify({ path, content: "x" }) };
}
const PS1_REL = item("write_file", "scripts/deploy.ps1");
const MD_REL = item("write_file", "out/report.md");
const EXE_REL = item("write_file", "tools/build.exe");
const NOEXT_REL = item("write_file", "Makefile");
const GHOST_REL = item("write_file", "out/gone.md");

interface PanelHandle {
  host: HTMLElement;
  root: ReturnType<typeof createRoot>;
  cleanup: () => Promise<void>;
}

// App wiring shape (App.tsx:6925): tabId + workspaceRoot + sessionPath +
// onOpenInTerminal + onInjectReferences are always provided — the judge
// renders the same shape so opsManaged rows get the full menu.
async function renderPanel(items: readonly unknown[]): Promise<PanelHandle> {
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
        tabId: "tab-1",
        workspaceRoot: "C:/proj",
        sessionPath: "C:/sessions/s.jsonl",
        onInjectReferences: () => {},
        onOpenInTerminal: (path: string) => { bridge.terminalCalls.push(path); },
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
async function openMenu(handle: PanelHandle, path: string): Promise<void> {
  const row = rowByPath(handle.host, path)!;
  await act(async () => {
    row.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 12, clientY: 34 }));
    await flushTimers();
  });
}

console.log("\ntl1641 dock row menu — per-type split convergence");

// 1. THE tl1641 pin: a .ps1 row keeps reveal + terminal, loses ONLY open.
{
  bridge.openProbeResult = ""; // opener gate refuses .ps1 (unchanged semantics)
  bridge.revealProbeResult = "C:/proj/scripts/deploy.ps1"; // existence probe ok
  bridge.terminalCalls = [];
  bridge.revealCalls = [];
  const handle = await renderPanel([PS1_REL]);
  await openMenu(handle, "scripts/deploy.ps1");
  eq(bridge.revealProbeCalls.at(-1), "scripts/deploy.ps1", "ps1: reveal probe receives the row path");
  const revealBtn = menuButtonByLabel("Show in file manager");
  const terminalBtn = menuButtonByLabel("Open in integrated terminal");
  eq(revealBtn !== undefined, true, "ps1: reveal item present (tl1641 user ask)");
  eq(revealBtn!.disabled, false, "ps1: reveal item enabled once the reveal probe is ok");
  eq(terminalBtn !== undefined, true, "ps1: terminal item present (cd works for script types)");
  eq(terminalBtn!.disabled, false, "ps1: terminal item enabled");
  eq(menuButtonByLabel("Open with default app") === undefined, true, "ps1: open item REMOVED — only the open item is cut for opener-refused types");
  eq(menuButtons().length, 8, "ps1: menu counts exactly 8 items (reveal/terminal/copy×2/rename/trash/delete-permanent/add-reference)");
  await act(async () => {
    terminalBtn!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(JSON.stringify(bridge.terminalCalls), JSON.stringify(["scripts/deploy.ps1"]), "ps1: terminal click passes the workspace-relative slice (the bridge refuses absolute shapes)");
  await openMenu(handle, "scripts/deploy.ps1");
  await act(async () => {
    menuButtonByLabel("Show in file manager")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(JSON.stringify(bridge.revealCalls), JSON.stringify(["C:/proj/scripts/deploy.ps1"]), "ps1: reveal click consumes the reveal-probed absolute path (explorer /select)");
  await handle.cleanup();
}

// 2. THE zero-regression pin: a .md row keeps all nine items in order.
{
  bridge.openProbeResult = "C:/proj/out/report.md";
  bridge.revealProbeResult = "C:/proj/out/report.md";
  bridge.openCalls = [];
  const handle = await renderPanel([MD_REL]);
  await openMenu(handle, "out/report.md");
  const labels = menuButtons().map((b) => b.textContent ?? "");
  const expectedOrder = [
    "Open with default app",
    "Show in file manager",
    "Open in integrated terminal",
    "Copy relative path",
    "Copy absolute path",
    "Rename",
    "Move to Trash",
    "Delete Permanently…",
    "Add file reference",
  ];
  eq(labels.length, 9, "md: menu counts exactly 9 items (the tl1641 zero-regression pin)");
  eq(JSON.stringify(labels), JSON.stringify(expectedOrder), "md: nine items keep their exact order");
  for (const label of expectedOrder) {
    eq(menuButtonByLabel(label)!.disabled, false, `md: "${label}" enabled`);
  }
  await act(async () => {
    menuButtonByLabel("Open with default app")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(JSON.stringify(bridge.openCalls), JSON.stringify(["C:/proj/out/report.md"]), "md: open click consumes the open-probed absolute path (unchanged)");
  await handle.cleanup();
}

// 3. Type contrast pins: .exe behaves like .ps1; an extensionless file keeps nine.
{
  bridge.openProbeResult = ""; // opener gate refuses .exe
  bridge.revealProbeResult = "C:/proj/tools/build.exe";
  const handle = await renderPanel([EXE_REL]);
  await openMenu(handle, "tools/build.exe");
  eq(menuButtonByLabel("Show in file manager") !== undefined, true, "exe: reveal item present");
  eq(menuButtonByLabel("Open in integrated terminal") !== undefined, true, "exe: terminal item present");
  eq(menuButtonByLabel("Open with default app") === undefined, true, "exe: open item removed");
  eq(menuButtons().length, 8, "exe: menu counts exactly 8 items");
  await handle.cleanup();

  bridge.openProbeResult = "C:/proj/Makefile";
  bridge.revealProbeResult = "C:/proj/Makefile";
  const handle2 = await renderPanel([NOEXT_REL]);
  await openMenu(handle2, "Makefile");
  eq(menuButtons().length, 9, "extensionless: menu keeps the full nine items");
  eq(menuButtonByLabel("Open with default app") !== undefined, true, "extensionless: open item present");
  await handle2.cleanup();
}

// 4. Missing file: both probes empty → all three open-type items removed.
{
  bridge.openProbeResult = "";
  bridge.revealProbeResult = "";
  const handle = await renderPanel([GHOST_REL]);
  await openMenu(handle, "out/gone.md");
  eq(menuButtonByLabel("Open with default app") === undefined, true, "missing: open item removed");
  eq(menuButtonByLabel("Show in file manager") === undefined, true, "missing: reveal item removed (dead-button guard kept)");
  eq(menuButtonByLabel("Open in integrated terminal") === undefined, true, "missing: terminal item removed");
  eq(menuButtonByLabel("Copy relative path") !== undefined, true, "missing: copy items survive");
  eq(menuButtonByLabel("Add file reference") !== undefined, true, "missing: add-reference survives");
  await handle.cleanup();
}

// 5. In-flight independence: a pending open probe must not disable reveal.
{
  let releaseOpen: ((value: string) => void) | null = null;
  const gated = new Promise<string>((resolve) => { releaseOpen = resolve; });
  const originalOpen = (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab;
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab = () => gated;
  bridge.openProbeResult = "";
  bridge.revealProbeResult = "C:/proj/out/report.md";
  const handle = await renderPanel([MD_REL]);
  await openMenu(handle, "out/report.md");
  eq(menuButtonByLabel("Open with default app")!.disabled, true, "in-flight: open item disabled while its probe pends");
  eq(menuButtonByLabel("Show in file manager")!.disabled, false, "in-flight: reveal item enabled — the reveal probe already answered (no cross-gating)");
  eq(menuButtonByLabel("Open in integrated terminal")!.disabled, false, "in-flight: terminal item enabled");
  await act(async () => {
    releaseOpen!(""); // opener gate refuses after all
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(menuButtonByLabel("Open with default app") === undefined, true, "in-flight: open item removed once its probe returns empty");
  eq(menuButtonByLabel("Show in file manager") !== undefined, true, "in-flight: reveal item unaffected by the open verdict");
  await act(async () => { await handle.cleanup(); });
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab = originalOpen;
}

// 6. Wiring + three-locale keys (source-shape assertions).
{
  const app = readFileSync(new URL("../App.tsx", import.meta.url), "utf8");
  eq(/<SideFilesDockPanel[\s\S]{0,400}?onOpenInTerminal=\{openTerminalForPath\}/.test(app), true, "wiring: App passes the tree's openTerminalForPath channel to the dock");
  const panel = readFileSync(new URL("../components/SessionSideFilesPanel.tsx", import.meta.url), "utf8");
  eq(panel.includes("ResolveRevealablePathForTab"), true, "wiring: dock fires the reveal probe bridge");
  eq(panel.includes("ResolveOpenablePathForTab"), true, "wiring: dock still fires the open probe bridge");
  for (const key of [
    "workspace.openWithDefaultApp",
    "workspace.revealInFileManager",
    "workspace.openInTerminal",
    "workspace.copyRelativePath",
    "workspace.copyAbsolutePath",
    "workspace.renameEntry",
    "workspace.trashEntry",
    "workspace.deletePermanentEntry",
    "workspace.addFileReferenceToChat",
  ]) {
    for (const locale of ["zh", "zh-TW", "en"]) {
      const src = readFileSync(new URL(`../locales/${locale}.ts`, import.meta.url), "utf8");
      eq(src.includes(`"${key}"`), true, `locales/${locale}: ${key} present`);
    }
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
