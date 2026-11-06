// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1468-side-files-display.test.tsx
// tl1468: the side-files dock lists (产物/参考 tabs) — two fixes, one shared
// component (DockSideFileList), covering tl1428 (same scrollbar complaint):
//
// A. Display paths: rows render sideFileDisplayPath (relative to the
//    workspace root, then the session directory; tail segments + "…" prefix
//    outside both, filename always last; Windows case/separator folding
//    before containment — the tl1414 lesson) while the OPERATION layer keeps
//    the canonical path: data-side-file-path, the row menu probe/copy/
//    open/reveal payloads, the @path injection and the hover title (which
//    still carries the full absolute path) all assert the canonical bytes.
// B. Scrollbar/fold: the dock list renders every row (no "show more" fold)
//    and the 240px list cap is scoped to the combined task-114 panel only —
//    the dock panel (.workbench-dock__body > .side-files--dock) is the single
//    scroll container, so the inner list can no longer scroll while the dock
//    body below is empty. CSS ratchet pins the scoping; the jsdom suite
//    cannot measure layout, so the three-state scrollHeight/clientHeight
//    readings live in bench/side-files-dock-layout.mjs (real Chromium).

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

// Bridge mocks (tl1437 harness shape) — recorders prove the operation layer
// still consumes the canonical path even though the row text now displays a
// relative/tail shape.
const bridge = {
  probeResult: "C:\\ws\\out\\a.txt",
  probeCalls: [] as Array<{ tabId: string; path: string }>,
  resolveCalls: [] as Array<{ tabId: string; path: string }>,
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
        return `C:/resolved/${path.replace(/^[/\\]+/, "")}`;
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

let clipboardText = "";
Object.defineProperty(dom.window.navigator, "clipboard", {
  configurable: true,
  value: { writeText: async (text: string) => { clipboardText = text; } },
});

const flushTimers = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("en");
const { sideFileDisplayPath, looksAbsolutePath, parentDirectory } = await import("../lib/sideFilePathDisplay");
const { SideFilesDockPanel, SessionSideFilesPanel } = await import("../components/SessionSideFilesPanel");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

console.log("\ntl1468 A: display-path rules (sideFileDisplayPath)");

const WS = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace";
const SESSION = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\sessions\\s-abc123.jsonl";
const SESSION_DIR = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\sessions";

// 1. Workspace-relative shapes pass through untouched.
eq(sideFileDisplayPath("out/report.txt", WS, SESSION), "out/report.txt", "relative shape: passthrough");
eq(sideFileDisplayPath("src\\lib\\x.ts", WS, SESSION), "src\\lib\\x.ts", "relative shape: backslash passthrough");
eq(sideFileDisplayPath("  out/report.txt  ", WS, SESSION), "out/report.txt", "relative shape: trimmed");

// 2. Absolute inside the workspace root -> relative (case + separator folding).
eq(
  sideFileDisplayPath(`${WS}\\tl1468\\report.md`, WS, SESSION),
  "tl1468\\report.md",
  "workspace: absolute under root renders relative (backslashes)",
);
eq(
  sideFileDisplayPath("C:/USERS/YINJI/APPDATA/ROAMING/REASONIX/GLOBAL-WORKSPACE/tl1468/report.md", WS, SESSION),
  "tl1468/report.md",
  "workspace: upper-case + forward-slash path still folds to the root (tl1414); display keeps the entry's own separators",
);
eq(
  sideFileDisplayPath(`${WS}/sub/deep/file.json`, WS + "\\", SESSION),
  "sub/deep/file.json",
  "workspace: root with trailing separator still matches; display keeps the entry's separators",
);
eq(sideFileDisplayPath(`${WS}\\leaf.txt`, WS, SESSION), "leaf.txt", "workspace: file directly under the root");

// 3. Segment boundary: a sibling with a longer name is NOT inside the root.
const boundaryDisplay = sideFileDisplayPath("C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\global-workspace-old\\f.txt", WS, SESSION);
eq(boundaryDisplay.startsWith("…"), true, "boundary: sibling root renders collapsed (not workspace-relative)");
eq(boundaryDisplay.includes("global-workspace\\\\") || boundaryDisplay === "global-workspace-old\\f.txt", false, "boundary: no fake relative under the real root (prefix never half-matches)");
eq(boundaryDisplay.endsWith("f.txt"), true, "boundary: filename survives");
eq(
  sideFileDisplayPath("C:\\ws2\\f.txt", "C:\\ws", SESSION),
  "C:\\ws2\\f.txt",
  "boundary: ws2 is not under ws (short absolute shown in full)",
);

// 4. Session directory: files next to the session .jsonl render relative to it.
eq(sideFileDisplayPath(`${SESSION_DIR}\\plot.png`, "", SESSION), "plot.png", "session dir: sibling file renders by name");
eq(sideFileDisplayPath(`${SESSION_DIR}\\exports\\plot.png`, "", SESSION), "exports\\plot.png", "session dir: nested file renders relative");
eq(
  sideFileDisplayPath(`${SESSION_DIR}\\plot.png`, "", SESSION.toUpperCase()),
  "plot.png",
  "session dir: session path case-folded (tl1414)",
);

// 5. Workspace wins over the session dir when both contain the file.
eq(
  sideFileDisplayPath(`${WS}\\out\\a.txt`, WS, SESSION),
  "out\\a.txt",
  "precedence: workspace-relative beats the session-dir frame",
);

// 6. Outside both: short absolute stays complete; long absolute collapses to
//    tail segments with an ellipsis prefix — the filename is always last.
const LONG = "C:\\Users\\yinji\\AppData\\Roaming\\reasonix\\some\\very\\deep\\nested\\tree\\folder\\name.tar.gz";
const longDisplay = sideFileDisplayPath(LONG, "", "");
eq(longDisplay.startsWith("…"), true, "outside: collapsed display carries the ellipsis prefix");
eq(longDisplay.endsWith("name.tar.gz"), true, "outside: the filename survives the collapse (never tail-truncated)");
eq(longDisplay.includes("Users"), false, "outside: dropped prefix segments are gone (not prefix-only)");
eq(sideFileDisplayPath("D:\\logs\\trace.txt", "", ""), "D:\\logs\\trace.txt", "outside: short absolute shows in full");
eq(sideFileDisplayPath("\\\\srv\\share\\doc.txt", "", ""), "\\\\srv\\share\\doc.txt", "outside: short UNC path shows in full");

// 7. Degenerate inputs.
eq(sideFileDisplayPath("", WS, SESSION), "", "degenerate: empty path stays empty");
eq(sideFileDisplayPath("f.txt", "", ""), "f.txt", "degenerate: relative shape with no bases");
eq(looksAbsolutePath("C:\\x"), true, "looksAbsolutePath: drive letter");
eq(looksAbsolutePath("\\\\srv\\share"), true, "looksAbsolutePath: UNC");
eq(looksAbsolutePath("/usr/bin"), true, "looksAbsolutePath: posix");
eq(looksAbsolutePath("out/x.txt"), false, "looksAbsolutePath: relative shape");
eq(parentDirectory("C:\\dir\\file.json"), "C:\\dir", "parentDirectory: windows");
eq(parentDirectory("C:\\file.json"), "C:", "parentDirectory: drive-root file keeps the drive as base");

console.log("\ntl1468 B: dock rows render display paths, operations keep canonical");

function item(kind: string, name: string, args: string) {
  return { kind, name, args };
}
const CANON_WS = item("tool", "write_file", JSON.stringify({ path: `${WS}\\tl1468\\report.md`, content: "x" }));
const CANON_OUT = item("tool", "write_file", JSON.stringify({ path: "D:\\logs\\trace.txt", content: "y" }));
const READ_REL = item("tool", "read_file", JSON.stringify({ path: "src/config.json" }));

interface PanelHandle {
  host: HTMLElement;
  cleanup: () => Promise<void>;
}

async function renderPanel(component: typeof SideFilesDockPanel, items: readonly unknown[], options: {
  tabId?: string;
  workspaceRoot?: string;
  sessionPath?: string;
  variant?: "artifacts" | "references";
  onInject?: (text: string) => void;
}): Promise<PanelHandle> {
  const host = document.createElement("div");
  document.getElementById("root")!.appendChild(host);
  const root = createRoot(host);
  await act(async () => {
    root.render(createElement(
      LocaleProvider,
      { locale: "en" },
      createElement(component, {
        items,
        variant: options.variant ?? "artifacts",
        tabId: options.tabId,
        workspaceRoot: options.workspaceRoot,
        sessionPath: options.sessionPath,
        onInjectReferences: options.onInject,
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

function rowButtons(host: HTMLElement): HTMLButtonElement[] {
  return Array.from(host.querySelectorAll<HTMLButtonElement>(".side-files__row"));
}
function rowByCanonical(host: HTMLElement, path: string): HTMLButtonElement | undefined {
  return rowButtons(host).find((b) => b.getAttribute("data-side-file-path") === path);
}
function menuButtonByLabel(label: string): HTMLButtonElement | undefined {
  return Array.from(document.body.querySelectorAll<HTMLButtonElement>(".floating-menu.workspace-tree-menu button"))
    .find((b) => b.textContent?.includes(label));
}

// 8. Row text vs canonical identity vs hover title (artifacts + references).
{
  const { host, cleanup } = await renderPanel(SideFilesDockPanel, [CANON_WS, CANON_OUT], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: SESSION,
  });
  const wsRow = rowByCanonical(host, `${WS}\\tl1468\\report.md`)!;
  eq(wsRow !== undefined, true, "rows: found by the canonical path (data-side-file-path keeps canonical)");
  eq(wsRow.querySelector("span")?.textContent, "tl1468\\report.md", "rows: workspace file renders the relative display path");
  eq(wsRow.getAttribute("title"), `${WS}\\tl1468\\report.md · write_file`, "rows: hover title carries the full absolute path + via");
  const outRow = rowByCanonical(host, "D:\\logs\\trace.txt")!;
  eq(outRow.querySelector("span")?.textContent, "D:\\logs\\trace.txt", "rows: outside file short enough renders in full");
  await cleanup();
  const refs = await renderPanel(SideFilesDockPanel, [READ_REL], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: SESSION,
    variant: "references",
  });
  const relRow = rowByCanonical(refs.host, "src/config.json")!;
  eq(relRow.querySelector("span")?.textContent, "src/config.json", "rows: relative-shaped read renders as-is (references variant)");
  eq(relRow.getAttribute("title"), "src/config.json · read_file", "rows: read hover title keeps the canonical path + via");
  await refs.cleanup();
}

// 9. Operation layer stays canonical: probe / copy / open / inject.
{
  let injected = "";
  const { host, cleanup } = await renderPanel(SideFilesDockPanel, [CANON_WS], {
    tabId: "tab-1",
    workspaceRoot: WS,
    sessionPath: SESSION,
    onInject: (text) => { injected = text; },
  });
  const row = rowByCanonical(host, `${WS}\\tl1468\\report.md`)!;
  await act(async () => {
    row.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 12, clientY: 34 }));
    await flushTimers();
  });
  eq(bridge.probeCalls.at(-1)?.path, `${WS}\\tl1468\\report.md`, "menu: probe receives the CANONICAL path, not the display path");
  clipboardText = "";
  await act(async () => {
    menuButtonByLabel("Copy relative path")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(clipboardText, `${WS}\\tl1468\\report.md`, "menu: copy-relative copies the canonical path (display layer does not leak)");
  await act(async () => {
    row.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 12, clientY: 34 }));
    await flushTimers();
  });
  await act(async () => {
    menuButtonByLabel("Copy absolute path")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(bridge.resolveCalls.length, 0, "menu: absolute-shaped canonical path resolves locally (no bridge)");
  eq(clipboardText, `${WS}\\tl1468\\report.md`, "menu: copy-absolute copies the canonical path as-is");
  await act(async () => {
    row.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 12, clientY: 34 }));
    await flushTimers();
  });
  await act(async () => {
    menuButtonByLabel("Add file reference")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  eq(injected, `@${WS}\\tl1468\\report.md`, "menu: add-reference injects the canonical @path");
  await cleanup();
}

// 10. Dock lists render EVERY row — no "show more" fold in either variant.
{
  const fourteen = Array.from({ length: 14 }, (_, i) =>
    item("tool", "write_file", JSON.stringify({ path: `C:\\ws\\gen\\file-${String(i).padStart(2, "0")}.txt`, content: "x" })));
  const { host, cleanup } = await renderPanel(SideFilesDockPanel, fourteen, {
    tabId: "tab-1",
    workspaceRoot: "C:\\ws",
    sessionPath: SESSION,
  });
  eq(rowButtons(host).length, 14, "dock artifacts: all 14 rows render (no INITIAL slice)");
  eq(host.querySelector(".side-files__foot"), null, "dock artifacts: no show-more footer while the dock can scroll naturally");
  await cleanup();
  const refs = await renderPanel(SideFilesDockPanel, fourteen.map((it) =>
    item("tool", "read_file", (it as { args: string }).args)), {
    tabId: "tab-1",
    workspaceRoot: "C:\\ws",
    sessionPath: SESSION,
    variant: "references",
  });
  eq(rowButtons(refs.host).length, 14, "dock references: all 14 rows render too (shared DockSideFileList)");
  eq(refs.host.querySelector(".side-files__foot"), null, "dock references: no show-more footer either");
  await refs.cleanup();
}

// 11. The COMBINED task-114 panel keeps its fold (default view untouched).
{
  const fourteen = Array.from({ length: 14 }, (_, i) =>
    item("tool", "write_file", JSON.stringify({ path: `C:\\ws\\gen\\file-${String(i).padStart(2, "0")}.txt`, content: "x" })));
  const host = document.createElement("div");
  document.getElementById("root")!.appendChild(host);
  const root = createRoot(host);
  await act(async () => {
    root.render(createElement(LocaleProvider, { locale: "en" },
      createElement(SessionSideFilesPanel, { items: fourteen } as never)));
    for (let i = 0; i < 3; i++) await flushTimers();
  });
  eq(host.querySelectorAll(".side-files__item").length, 12, "combined panel: artifacts list still folds at 12 (task-260 pacing intact)");
  eq(host.querySelector(".side-files__foot button")?.textContent, "Show 2 more", "combined panel: show-more button still present");
  await act(async () => { root.unmount(); });
  host.remove();
}

// 12. CSS ratchet (source-shape): the cap exists in exactly one scoped rule;
//     the dock panel stays the single scroll container. Upper bound + any new
//     unscoped cap turns red.
{
  const css = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
  eq(/\.side-files__list\s*\{[^}]*\}/.test(css), true, "ratchet: base .side-files__list rule present");
  const baseRule = css.match(/\.side-files__list\s*\{[^}]*\}/)![0];
  eq(baseRule.includes("max-height"), false, "ratchet: base rule carries NO max-height (dock must grow naturally)");
  const scopedMatches = css.match(/\.side-files:not\(\.side-files--dock\) \.side-files__list\s*\{[^}]*\}/g) ?? [];
  eq(scopedMatches.length, 1, "ratchet: exactly one scoped cap rule for the combined panel");
  eq(scopedMatches[0]?.includes("max-height: 240px"), true, "ratchet: the scoped rule keeps the 240px cap");
  const dockRule = css.match(/\.workbench-dock__body > \.side-files--dock\s*\{[^}]*\}/)![0];
  eq(dockRule.includes("height: 100%"), true, "ratchet: dock panel fills the dock body");
  eq(dockRule.includes("overflow: auto"), true, "ratchet: dock panel remains the single scroll container");
  eq(dockRule.includes("display: block"), true, "ratchet: dock panel stays block flow (grid compressed the group and overflow:hidden clipped rows without a scrollbar — real-Chromium verified)");
  // A direct ".side-files--dock .side-files__list" descendant selector (i.e.
  // NOT inside :not()) would re-cap the dock list — its absence is the
  // ratchet; the only dock mention beside the scoping rule is the tl1468
  // comment prose, which has no selector shape.
  eq(/(^|[\s,}])\.side-files--dock\s+[^{]*\.side-files__list/.test(css), false, "ratchet: no dock-scoped list cap exists (would revive the inner scrollbar)");
}

// 13. App wiring: the dock panel receives the display bases from the active tab.
{
  const app = readFileSync(new URL("../App.tsx", import.meta.url), "utf8");
  eq(/<SideFilesDockPanel[\s\S]{0,500}?workspaceRoot=\{activeTab\?\.workspaceRoot \?\? ""\}/.test(app), true, "app: workspaceRoot prop wired from the active tab");
  eq(/<SideFilesDockPanel[\s\S]{0,500}?sessionPath=\{activeTab\?\.sessionPath \?\? ""\}/.test(app), true, "app: sessionPath prop wired from the active tab");
}

// 14. Locale keys: no new keys introduced; the pacing key the combined panel
//     still uses exists in all three languages.
{
  for (const key of ["sideFiles.showMore", "sideFiles.empty", "sideFiles.artifacts", "sideFiles.references"]) {
    for (const locale of ["zh", "zh-TW", "en"]) {
      const src = readFileSync(new URL(`../locales/${locale}.ts`, import.meta.url), "utf8");
      eq(src.includes(`"${key}"`), true, `locales/${locale}: ${key} present (unchanged)`);
    }
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
