// Run: tsx src/__tests__/todo-sidebar-dock.test.tsx
// Task 259: the experimental todo sidebar — a fifth right-dock tab behind one
// switch, with the tab row wrapping and per-tab visibility under the same flag.
//
// Contract under test:
// - switch off  → the tab row is the original four literals, no wrap class,
//   no todos tab (the footer todo list stays untouched, verified in
//   todo-panel-lifecycle + the App-level gating);
// - switch on   → five tabs, wrap classes present, hidden tabs filtered live;
// - mode "todos" renders the empty state without a payload (the panel itself
//   is lazy, so its expanded/collapsed behaviour is covered directly below);
// - TodoPanel's defaultOpen only shifts the initial state; undefined keeps the
//   composer-shelf default from todo-panel-lifecycle;
// - CSS cascade (task 447, fixing the task 259 wrap layer): the theme layer
//   ships its own tools/tabs/tab rules at 0-3-0 and data-theme-style is
//   written before React mounts, so the wrap contract only holds if
//   theme-scoped --wrap variants exist AND follow their plain theme
//   counterparts in source order (equal specificity, last rule wins).

import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
// jsdom does not implement scrollIntoView; TodoPanel scrolls the active row into view on expand.
dom.window.HTMLElement.prototype.scrollIntoView = () => {};

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

const { WorkspaceDockRegion } = await import("../app-shell/WorkspaceDockRegion");
const dockTabs = await import("../lib/dockTabs");
const { TodoPanel } = await import("../components/TodoPanel");
const { LocaleProvider } = await import("../lib/i18n");
const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { flushSync } = await import("react-dom");
const { act } = await import("react");

const t = ((key: string) => key) as Parameters<typeof WorkspaceDockRegion>[0]["t"];
const noop = () => {};

function renderDock(overrides: Partial<Parameters<typeof WorkspaceDockRegion>[0]> = {}): string {
  const props = {
    visible: true, overlay: false, mode: "files" as const,
    creation: false, remoteAvailable: true, showContext: true, todoSidebar: false,
    t, onMode: noop, onRemote: noop,
    remote: { onClose: noop },
    context: {},
    workspaceKey: "fixture",
    workspace: { open: true, maximized: false, onClose: noop, onToggleMaximized: noop },
    ...overrides,
  } as Parameters<typeof WorkspaceDockRegion>[0];
  return renderToStaticMarkup(createElement(WorkspaceDockRegion, props));
}

console.log("\ntodo sidebar dock (task 259)");

// 1. Switch off: the original four literals, byte-for-byte shape.
{
  const markup = renderDock({ todoSidebar: false });
  for (const label of ["rightDock.overview", "workspace.filesTab", "workspace.changedTab", "rightDock.remote"]) {
    eq(markup.includes(label), true, `off: renders the original ${label} tab`);
  }
  eq(markup.includes("workspace.todosTab"), false, "off: no todos tab");
  eq(markup.includes("workbench-dock__tabs--wrap"), false, "off: no wrap class on the tab row");
  eq(markup.includes("workbench-dock__tools--wrap"), false, "off: no wrap class on the tools row");
}

// 2. Switch on: five tabs and the wrap classes.
{
  const markup = renderDock({ todoSidebar: true });
  eq(markup.includes("workbench-dock__tabs--wrap"), true, "on: tab row carries the wrap class");
  eq(markup.includes("workbench-dock__tools--wrap"), true, "on: tools row carries the wrap class");
  for (const label of ["rightDock.overview", "workspace.filesTab", "workspace.changedTab", "rightDock.remote", "workspace.todosTab"]) {
    eq(markup.includes(label), true, `on: renders the ${label} tab`);
  }
}

// 3. Visibility applies live: a hidden tab disappears, the others stay.
//    Write through the module (the same instance WorkspaceDockRegion reads),
//    not raw localStorage — storage edits alone never re-read the cached list.
{
  dockTabs.setDockTabHidden("files", true);
  const markup = renderDock({ todoSidebar: true });
  eq(markup.includes("workspace.filesTab"), false, "on+hidden(files): files tab is gone");
  eq(markup.includes("workspace.changedTab"), true, "on+hidden(files): changed tab stays");
  eq(markup.includes("workspace.todosTab"), true, "on+hidden(files): todos tab stays");
  dockTabs.setDockTabHidden("files", false);
}

// 4. mode "todos" without a payload renders the empty state.
{
  const markup = renderDock({ todoSidebar: true, mode: "todos" });
  eq(markup.includes("workbench-dock__todo--empty"), true, "todos mode: empty-state container present");
  eq(markup.includes("rightDock.todoEmpty"), true, "todos mode: empty-state copy present");
}

// 5. Switch off while mode is "todos" (a stale persisted mode): the dock never
// renders the todos body — it falls through to the workspace branch.
{
  const markup = renderDock({ todoSidebar: false, mode: "todos" });
  eq(markup.includes("workbench-dock__todo--empty"), false, "off+todos mode: no todo body");
  eq(markup.includes("workspace.todosTab"), false, "off+todos mode: no todos tab");
}

// 6. dockTabs module: persistence + live notification. Raw storage edits are
//    deliberately NOT re-read by the cached list (the module snapshots at load,
//    WorkspaceDockRegion subscribes instead) — the unknown-id filter is still
//    exercised at load time by the initial module state in this process.
{
  const seen: Array<readonly string[]> = [];
  const unsubscribe = dockTabs.onHiddenDockTabsChange((tabs) => seen.push([...tabs]));

  dockTabs.setDockTabHidden("changed", true);
  eq(dockTabs.isDockTabHidden("changed"), true, "module: hide applies immediately");
  eq(JSON.parse(dom.window.localStorage.getItem("rightDockTabs:hidden") ?? "[]").includes("changed"), true, "module: hide persists");
  eq(seen.at(-1)?.includes("changed"), true, "module: subscribers see the change");

  dockTabs.setDockTabHidden("changed", false);
  eq(dockTabs.isDockTabHidden("changed"), false, "module: show applies immediately");
  unsubscribe();
}

// 7. TodoPanel defaultOpen: the dock passes true so the list starts expanded;
//    explicit false starts collapsed. (Undefined keeps the composer-shelf
//    default — covered by todo-panel-lifecycle.)
{
  const todos = [
    { content: "first task", status: "pending", activeForm: "doing first" },
    { content: "second task", status: "in_progress", activeForm: "doing second" },
  ] as Parameters<typeof TodoPanel>[0]["todos"];

  const mountPanel = (props: Parameters<typeof TodoPanel>[0]): { host: HTMLDivElement; unmount: () => void } => {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const root = createRoot(host);
    act(() => {
      root.render(createElement(LocaleProvider, null, createElement(TodoPanel, props)));
    });
    // The caller asserts on the mounted host first, then unmounts.
    return { host, unmount: () => act(() => root.unmount()) };
  };

  const openMount = mountPanel({ stateKey: "probe-open", todos, running: false, pendingPrompt: false, onDismiss: noop, defaultOpen: true });
  eq(openMount.host.querySelector(".todobar__list") !== null, true, "TodoPanel: defaultOpen starts expanded");
  openMount.unmount();
  openMount.host.remove();

  const closedMount = mountPanel({ stateKey: "probe-closed", todos, running: false, pendingPrompt: false, onDismiss: noop, defaultOpen: false });
  eq(closedMount.host.querySelector(".todobar__list") === null, true, "TodoPanel: defaultOpen=false starts collapsed");
  closedMount.unmount();
  closedMount.host.remove();
}

// 8. Audit-2 minor c: the keep-at-least-one guard counts only RENDERABLE tabs.
//    With no remote host the remote tab cannot render, so hiding the rest must
//    lock the last renderable tab instead of letting the dock go empty.
{
  const dockTabs2 = await import("../lib/dockTabs");

  eq(JSON.stringify(dockTabs2.renderableDockTabs({ todoSidebar: false, remoteAvailable: true, creation: false })), "[]",
    "renderable set: empty while the switch is off");
  // Task 260: artifacts/references join the family, so the on-switch sets grow
  // by the two tabs (order: after todos, matching the tab row).
  eq(JSON.stringify(dockTabs2.renderableDockTabs({ todoSidebar: true, remoteAvailable: true, creation: false })),
    JSON.stringify(["context", "files", "changed", "remote", "todos", "artifacts", "references"]),
    "renderable set: seven tabs on workbench with a remote host");
  eq(JSON.stringify(dockTabs2.renderableDockTabs({ todoSidebar: true, remoteAvailable: false, creation: false })),
    JSON.stringify(["context", "files", "changed", "todos", "artifacts", "references"]),
    "renderable set: no remote host drops the remote tab");
  eq(JSON.stringify(dockTabs2.renderableDockTabs({ todoSidebar: true, remoteAvailable: false, creation: true })),
    JSON.stringify(["files", "changed", "todos", "artifacts", "references"]),
    "renderable set: creation drops the overview too");

  // The reported break: no remote host, everything but "todos" hidden. The old
  // static-DOCK_TAB_IDS guard kept the switch unlocked (remote was "visible")
  // and let the dock render zero tabs.
  dockTabs2.setDockTabHidden("context", true);
  dockTabs2.setDockTabHidden("files", true);
  dockTabs2.setDockTabHidden("changed", true);
  // Task 260: with the two side-files tabs still visible the dock is not empty,
  // so nothing locks yet - the guard only fires on the actual last visible tab.
  eq(dockTabs2.isLastRenderableVisibleTab("todos", { todoSidebar: true, remoteAvailable: false, creation: false }), false,
    "audit-2 c: side-files tabs visible keeps every checkbox free");
  dockTabs2.setDockTabHidden("artifacts", true);
  dockTabs2.setDockTabHidden("references", true);
  eq(dockTabs2.isLastRenderableVisibleTab("todos", { todoSidebar: true, remoteAvailable: false, creation: false }), true,
    "audit-2 c: no remote + rest hidden locks the last renderable tab");
  eq(dockTabs2.isLastRenderableVisibleTab("remote", { todoSidebar: true, remoteAvailable: false, creation: false }), false,
    "audit-2 c: the unrenderable remote tab never satisfies the guard");
  dockTabs2.setDockTabHidden("context", false);
  dockTabs2.setDockTabHidden("files", false);
  dockTabs2.setDockTabHidden("changed", false);
  dockTabs2.setDockTabHidden("artifacts", false);
  dockTabs2.setDockTabHidden("references", false);

  // Original behaviour kept: with a remote host present, the remote tab is the
  // one that locks once everything else is hidden.
  dockTabs2.setDockTabHidden("context", true);
  dockTabs2.setDockTabHidden("files", true);
  dockTabs2.setDockTabHidden("changed", true);
  dockTabs2.setDockTabHidden("todos", true);
  dockTabs2.setDockTabHidden("artifacts", true);
  dockTabs2.setDockTabHidden("references", true);
  eq(dockTabs2.isLastRenderableVisibleTab("remote", { todoSidebar: true, remoteAvailable: true, creation: false }), true,
    "with remote host: the remote tab locks as the last visible one");
  dockTabs2.setDockTabHidden("context", false);
  dockTabs2.setDockTabHidden("files", false);
  dockTabs2.setDockTabHidden("changed", false);
  dockTabs2.setDockTabHidden("todos", false);
  dockTabs2.setDockTabHidden("artifacts", false);
  dockTabs2.setDockTabHidden("references", false);
}

// 9. Audit-2 fast-verify: an all-hidden state must not deadlock the checkboxes.
//    Upgrade path is real — 14278614f could persist every renderable tab as
//    hidden (hand-edited storage or the old static-list guard), and the next
//    boot then loads that state. Hidden tabs must never trip the guard, so
//    every checkbox stays checkable and the user can always climb back out.
{
  const ctx = { todoSidebar: true, remoteAvailable: false, creation: false };
  // Simulate the carried-over state: every renderable tab hidden
  // (task 260: that now includes artifacts/references).
  dockTabs.setDockTabHidden("context", true);
  dockTabs.setDockTabHidden("files", true);
  dockTabs.setDockTabHidden("changed", true);
  dockTabs.setDockTabHidden("todos", true);
  dockTabs.setDockTabHidden("artifacts", true);
  dockTabs.setDockTabHidden("references", true);

  for (const id of ["context", "files", "changed", "todos", "artifacts", "references"] as const) {
    eq(dockTabs.isLastRenderableVisibleTab(id, ctx), false,
      `all-hidden upgrade: ${id} is not locked and can be checked back`);
  }

  // Checking one back restores the guard: that one becomes the locked last tab,
  // the still-hidden ones stay free to check.
  dockTabs.setDockTabHidden("files", false);
  eq(dockTabs.isLastRenderableVisibleTab("files", ctx), true,
    "all-hidden upgrade: the single visible tab locks again");
  eq(dockTabs.isLastRenderableVisibleTab("context", ctx), false,
    "all-hidden upgrade: remaining hidden tabs stay checkable");
  eq(dockTabs.isLastRenderableVisibleTab("todos", ctx), false,
    "all-hidden upgrade: remaining hidden tabs stay checkable (todos)");
  eq(dockTabs.isLastRenderableVisibleTab("artifacts", ctx), false,
    "all-hidden upgrade: remaining hidden tabs stay checkable (artifacts)");
  eq(dockTabs.isLastRenderableVisibleTab("references", ctx), false,
    "all-hidden upgrade: remaining hidden tabs stay checkable (references)");

  dockTabs.setDockTabHidden("context", false);
  dockTabs.setDockTabHidden("files", false);
  dockTabs.setDockTabHidden("changed", false);
  dockTabs.setDockTabHidden("todos", false);
  dockTabs.setDockTabHidden("artifacts", false);
  dockTabs.setDockTabHidden("references", false);
}

// 10. CSS cascade contract (task 447 fix for the task 259 wrap layer). Static
//     text assertions on styles.css - same finalDeclaration pattern as
//     app-chrome-tabs.test.ts. What broke: the theme layer's
//     ':root[data-theme-style]' rules sit at 0-3-0 and are always active, so
//     the plain wrap selectors (0-1-0/0-2-0) never won: quarter cells
//     collapsed, the tools row stayed pinned at 45px, and the fifth tab
//     wrapped onto a row that never grew. The fix must therefore satisfy two
//     properties - the variant blocks exist with the right final
//     declarations, and they appear AFTER their theme counterparts (the
//     equal-specificity tie-break that a specificity bump alone cannot buy).
{
  const fs = await import("node:fs");
  const path = await import("node:path");
  const url = await import("node:url");
  const stylesSource = fs.readFileSync(path.resolve(path.dirname(url.fileURLToPath(import.meta.url)), "../styles.css"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");

  function matchingBlocks(selector: string): string[] {
    const blocks: string[] = [];
    const rule = /([^{}]+)\{([^{}]*)\}/g;
    let match: RegExpExecArray | null;
    while ((match = rule.exec(stylesSource)) !== null) {
      const selectors = match[1].split(",").map((part) => part.trim());
      if (selectors.includes(selector)) blocks.push(match[2]);
    }
    return blocks;
  }

  function finalDeclaration(selector: string, property: string): string | undefined {
    let value: string | undefined;
    for (const block of matchingBlocks(selector)) {
      const declaration = new RegExp("(?:^|;)\\s*" + property + "\\s*:\\s*([^;]+)", "g");
      let match: RegExpExecArray | null;
      while ((match = declaration.exec(block)) !== null) {
        value = match[1].trim();
      }
    }
    return value;
  }

  function blockIndex(selector: string): number {
    return stylesSource.indexOf(selector + " {");
  }

  const themeToolsIdx = blockIndex(":root[data-theme-style] .workbench-dock__tools");
  const themeTabsIdx = blockIndex(":root[data-theme-style] .workbench-dock__tabs");
  const wrapToolsIdx = blockIndex(":root[data-theme-style] .workbench-dock__tools--wrap");
  const wrapTabsIdx = blockIndex(":root[data-theme-style] .workbench-dock__tabs--wrap");
  const wrapCellIdx = blockIndex(":root[data-theme-style] .workbench-dock__tabs--wrap .workbench-dock__tab");
  const framelessToolsIdx = blockIndex(":root[data-theme-style] .app--windows-frameless.app--workbench .workbench-dock__tools");
  const framelessWrapIdx = blockIndex(":root[data-theme-style] .app--windows-frameless.app--workbench .workbench-dock__tools--wrap");

  eq(themeToolsIdx > 0 && themeTabsIdx > 0, true, "css: theme-layer base dock rules present");
  eq(wrapToolsIdx > themeToolsIdx, true, "css: tools--wrap variant follows the theme tools rule (tie-break order)");
  eq(wrapTabsIdx > themeTabsIdx, true, "css: tabs--wrap variant follows the theme tabs rule (tie-break order)");
  eq(wrapCellIdx > wrapTabsIdx, true, "css: quarter-cell rule present after the tabs variant");

  eq(finalDeclaration(":root[data-theme-style] .workbench-dock__tools--wrap", "height"), "auto",
    "css: wrap tools row height is auto (was pinned at 45px by the theme layer)");
  eq(finalDeclaration(":root[data-theme-style] .workbench-dock__tabs--wrap", "flex-wrap"), "wrap",
    "css: wrap tab strip keeps flex-wrap");
  eq(finalDeclaration(":root[data-theme-style] .workbench-dock__tabs--wrap", "width"), "100%",
    "css: wrap tab strip spans the row (theme layer declares width:auto)");
  eq(finalDeclaration(":root[data-theme-style] .workbench-dock__tabs--wrap .workbench-dock__tab", "flex"), "0 0 25%",
    "css: quarter-width cells survive the theme layer (it declares flex: 0 0 auto)");
  eq(finalDeclaration(":root[data-theme-style] .workbench-dock__tabs--wrap .workbench-dock__tab", "max-width"), "25%",
    "css: quarter-width cap survive the theme layer (it declares max-width: none)");
  eq(finalDeclaration(":root[data-theme-style] .workbench-dock__tabs--wrap .workbench-dock__tab-label", "text-overflow"), "ellipsis",
    "css: label truncates inside a quarter cell (theme layer turns overflow off)");

  // Windows frameless Workbench pins the same row to calc(40px + caption) at
  // an equal 0-5-0 - its wrap variant must both exist and follow that rule.
  eq(framelessToolsIdx > 0, true, "css: frameless workbench tools rule present");
  eq(framelessWrapIdx > framelessToolsIdx, true, "css: frameless wrap variant follows its pinned-height rule");
  eq(finalDeclaration(":root[data-theme-style] .app--windows-frameless.app--workbench .workbench-dock__tools--wrap", "height"), "auto",
    "css: frameless wrap tools row height is auto");
}

console.log(`\ntodo sidebar dock: ${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
