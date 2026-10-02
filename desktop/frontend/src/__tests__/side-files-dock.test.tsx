// Run: tsx src/__tests__/side-files-dock.test.tsx
// Task 260: the two session side-files dock tabs (artifacts = write path,
// references = read path) behind the same experimental sidebar family switch
// as the todos tab (task 259).
//
// Contract under test:
// - SideFilesDockPanel: one variant per tab, same task-114 aggregation,
//   friendly empty state, references tab keeps the inject-into-composer action;
// - dockModeWithinSidebarGates: todos/artifacts/references fall back to files
//   while the switch is off; every other mode passes through; switch on = no
//   rewrite (the switch-off tab row is byte-for-byte the pre-260 dock);
// - dockTabs: the two new ids are hideable like todos, and the last-renderable
//   guard counts them while the switch is on;
// - persistence: a saved "artifacts"/"references" mode survives normalization
//   so toggling the switch off/on does not lose the user's tab;
// - App wiring (source-shape assertions): the two tab buttons and the dock
//   body branch exist and are gated by todoSidebarEnabled, like todos.

import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

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

const { SideFilesDockPanel } = await import("../components/SessionSideFilesPanel");
const { dockModeWithinSidebarGates, loadRightDockMode } = await import("../store/layout");
const dockTabs = await import("../lib/dockTabs");
const { LocaleProvider } = await import("../lib/i18n");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

const t = ((key: string, vars?: Record<string, unknown>) => {
  if (vars && typeof vars.count === "number") return `${key}:${vars.count}`;
  return key;
}) as never;

function item(kind: string, name?: string, args?: string) {
  return { kind, name, args };
}

const WRITE = item("tool", "write_file", JSON.stringify({ path: "C:/proj/out.txt", content: "x" }));
const EDIT = item("tool", "edit_file", JSON.stringify({ path: "C:/proj/app.ts" }));
const READ = item("tool", "read_file", JSON.stringify({ path: "C:/proj/in.txt" }));
const BASH = item("tool", "bash", JSON.stringify({ command: "ls" }));

function renderPanel(variant: "artifacts" | "references", items: readonly unknown[], onInject?: (text: string) => void): string {
  const el = createElement(
    LocaleProvider,
    { locale: "en" },
    createElement(SideFilesDockPanel, { items, variant, onInjectReferences: onInject, t } as never),
  );
  return renderToStaticMarkup(el);
}

console.log("\nside-files dock tabs (task 260)");

// 1. Artifacts tab: write path only.
{
  const markup = renderPanel("artifacts", [WRITE, EDIT, READ, BASH]);
  eq(markup.includes("out.txt"), true, "artifacts: lists the written file");
  eq(markup.includes("in.txt"), false, "artifacts: does not list read files");
  eq(markup.includes("1 produced"), true, "artifacts: count meta shows 1");
  eq(markup.includes("No session file activity yet"), false, "artifacts: no empty state when a write exists");
}
{
  const markup = renderPanel("artifacts", [READ, BASH]);
  eq(markup.includes("No session file activity yet"), true, "artifacts: empty state when nothing was written");
}

// 2. References tab: read path (reads + modifications the session opened).
{
  const markup = renderPanel("references", [WRITE, EDIT, READ, BASH], () => {});
  eq(markup.includes("in.txt"), true, "references: lists the read file");
  eq(markup.includes("app.ts"), true, "references: lists the modified file (task 114 semantics)");
  eq(markup.includes("out.txt"), false, "references: does not list written files");
  eq(markup.includes("2 read"), true, "references: count meta shows 2");
  eq(markup.includes("Add references to message"), true, "references: offers the inject action");
}
{
  const markup = renderPanel("references", [WRITE]);
  eq(markup.includes("No session file activity yet"), true, "references: empty state when nothing was read");
}

// 3. Inject action payload (interactive).
{
  let injected = "";
  const container = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(
      LocaleProvider,
      { locale: "en" },
      createElement(SideFilesDockPanel, {
        items: [READ],
        variant: "references",
        onInjectReferences: (text: string) => { injected = text; },
        t,
      } as never),
    ));
  });
  const button = Array.from(container.querySelectorAll("button")).find((b) => b.textContent?.includes("Add references to message"));
  eq(typeof button, "object", "references: inject button renders into a live root");
  await act(async () => { button!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  eq(injected.includes("in.txt"), true, "references: click injects the formatted reference block");
  await act(async () => root.unmount());
  container.remove();
}

// 4. Gate matrix.
{
  eq(dockModeWithinSidebarGates("todos", false), "files", "gate: todos falls back to files when switch off (259 unchanged)");
  eq(dockModeWithinSidebarGates("artifacts", false), "files", "gate: artifacts falls back to files when switch off");
  eq(dockModeWithinSidebarGates("references", false), "files", "gate: references falls back to files when switch off");
  eq(dockModeWithinSidebarGates("files", false), "files", "gate: files passes through when switch off");
  eq(dockModeWithinSidebarGates("changed", false), "changed", "gate: changed passes through when switch off");
  eq(dockModeWithinSidebarGates("context", false), "context", "gate: context passes through when switch off");
  eq(dockModeWithinSidebarGates("remote", false), "remote", "gate: remote passes through when switch off");
  for (const mode of ["todos", "artifacts", "references", "files", "changed", "context", "remote"] as const) {
    eq(dockModeWithinSidebarGates(mode, true), mode, `gate: ${mode} passes through when switch on`);
  }
}

// 5. dockTabs: new ids registered, hideable, counted by the last-tab guard.
{
  eq(dockTabs.DOCK_TAB_IDS.includes("artifacts"), true, "dockTabs: artifacts id registered");
  eq(dockTabs.DOCK_TAB_IDS.includes("references"), true, "dockTabs: references id registered");
  eq(dockTabs.isDockTabHidden("artifacts"), false, "dockTabs: artifacts visible by default");
  const renderable = dockTabs.renderableDockTabs({ todoSidebar: true, remoteAvailable: false, creation: false });
  eq(renderable.includes("artifacts") && renderable.includes("references"), true, "dockTabs: guard counts both tabs while the switch is on");
  eq(dockTabs.renderableDockTabs({ todoSidebar: false, remoteAvailable: false, creation: false }).length, 0, "dockTabs: guard counts nothing while the switch is off");
  dockTabs.setDockTabHidden("artifacts", true);
  eq(dockTabs.isDockTabHidden("artifacts"), true, "dockTabs: artifacts can be hidden");
  // The guard locks a checkbox only when it is the LAST visible renderable tab;
  // with files/changed still visible nothing is locked yet.
  eq(dockTabs.isLastRenderableVisibleTab("references", { todoSidebar: true, remoteAvailable: false, creation: false }), false, "dockTabs: guard does not lock while other tabs are visible");
  for (const id of ["context", "files", "changed", "todos"] as const) dockTabs.setDockTabHidden(id, true);
  eq(dockTabs.isLastRenderableVisibleTab("references", { todoSidebar: true, remoteAvailable: false, creation: false }), true, "dockTabs: guard locks references as the last visible tab");
  for (const id of ["context", "files", "changed", "todos", "artifacts"] as const) dockTabs.setDockTabHidden(id, false);
  eq(dockTabs.isDockTabHidden("artifacts"), false, "dockTabs: artifacts can be shown again");
}

// 6. Persistence: the new modes survive normalization.
{
  dom.window.localStorage.setItem("reasonix.rightDockMode", "artifacts");
  eq(loadRightDockMode(""), "artifacts", "persistence: saved artifacts mode loads");
  dom.window.localStorage.setItem("reasonix.rightDockMode", "references");
  eq(loadRightDockMode(""), "references", "persistence: saved references mode loads");
  dom.window.localStorage.removeItem("reasonix.rightDockMode");
}

// 7. App wiring (source-shape, App is too heavy to mount here).
{
  const app = readFileSync(new URL("../App.tsx", import.meta.url), "utf8");
  eq(app.includes('dockTabVisible("artifacts")'), true, "app: artifacts tab is gated by dockTabVisible");
  eq(app.includes('dockTabVisible("references")'), true, "app: references tab is gated by dockTabVisible");
  eq(app.includes("dockModeWithinSidebarGates(rightDockMode, todoSidebarEnabled)"), true, "app: effective mode uses the shared gate helper");
  eq(app.includes('effectiveRightDockMode === "artifacts" || effectiveRightDockMode === "references"'), true, "app: dock body branches for both modes");
  eq(app.includes("SideFilesDockPanel"), true, "app: dock body references the dock panel");
  eq(app.includes('t("workspace.artifactsTab")'), true, "app: artifacts tab label wired");
  eq(app.includes('t("workspace.referencesTab")'), true, "app: references tab label wired");
}

// 8. Locales: the two tab keys exist in all three languages.
{
  for (const file of ["zh", "en", "zh-TW"]) {
    const src = readFileSync(new URL(`../locales/${file}.ts`, import.meta.url), "utf8");
    eq(src.includes('"workspace.artifactsTab"'), true, `locales/${file}: artifactsTab key present`);
    eq(src.includes('"workspace.referencesTab"'), true, `locales/${file}: referencesTab key present`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
