// Run: tsx src/__tests__/side-files-dock.test.tsx
// Task 260: the two session side-files dock tabs (artifacts = write path,
// references = read path) behind the same experimental sidebar family switch
// as the todos tab (task 259). Task 629 revised the aggregation: modified
// files are artifacts too.
//
// Contract under test:
// - SideFilesDockPanel: one variant per tab, the task-114 aggregation as
//   revised by task 629,
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

// 1. Artifacts tab: the write path — creates AND modifications (task 629).
{
  const markup = renderPanel("artifacts", [WRITE, EDIT, READ, BASH]);
  eq(markup.includes("out.txt"), true, "artifacts: lists the written file");
  eq(markup.includes("app.ts"), true, "artifacts: lists the modified file (task 629)");
  eq(markup.includes("in.txt"), false, "artifacts: does not list read files");
  eq(markup.includes("2 produced"), true, "artifacts: count meta shows 2");
  eq(markup.includes("No session file activity yet"), false, "artifacts: no empty state when a write exists");
}
{
  const markup = renderPanel("artifacts", [READ, BASH]);
  eq(markup.includes("No session file activity yet"), true, "artifacts: empty state when nothing was written");
}

// 2. References tab: the read path (task 629 moved modifications to artifacts).
{
  const markup = renderPanel("references", [WRITE, EDIT, READ, BASH], () => {});
  eq(markup.includes("in.txt"), true, "references: lists the read file");
  eq(markup.includes("app.ts"), false, "references: modified file moved to artifacts (task 629)");
  eq(markup.includes("out.txt"), false, "references: does not list written files");
  eq(markup.includes("1 read"), true, "references: count meta shows 1");
  eq(markup.includes("Add references to message"), true, "references: offers the inject action");
}
{
  const markup = renderPanel("references", [WRITE]);
  eq(markup.includes("No session file activity yet"), true, "references: empty state when nothing was read");
}

// 2b. Task 452: hydrated sessions carry NO args (the host archives tool
// arguments for every persisted call — desktop/app.go historyToolCall) but DO
// carry the collapsed subject, which for path-bearing tools IS the path.
// The aggregation must fall back to it or every rehydrated session shows the
// empty state forever.
{
  const HYD_WRITE = { kind: "tool", name: "write_file", args: "", subject: "C:/proj/out.txt" };
  const HYD_READ = { kind: "tool", name: "read_file", args: "", subject: "C:/proj/in.txt" };
  const HYD_EDIT = { kind: "tool", name: "edit_file", args: "", subject: "C:/proj/app.ts" };
  const HYD_MOVE = { kind: "tool", name: "move_file", args: "", subject: "C:/proj/old.txt -> C:/proj/new.txt" };
  const art = renderPanel("artifacts", [HYD_WRITE, HYD_READ, HYD_EDIT]);
  eq(art.includes("out.txt"), true, "hydrated: write_file subject path lands in artifacts");
  eq(art.includes("app.ts"), true, "hydrated: edit_file subject path lands in artifacts (task 629)");
  eq(art.includes("in.txt"), false, "hydrated: read subjects stay out of artifacts");
  const refs = renderPanel("references", [HYD_READ, HYD_EDIT, HYD_WRITE]);
  eq(refs.includes("in.txt"), true, "hydrated: read_file subject path lands in references");
  eq(refs.includes("app.ts"), false, "hydrated: edit subject moved to artifacts (task 629)");
  eq(refs.includes("out.txt"), false, "hydrated: write subject stays out of references");
  const movedRefs = renderPanel("references", [HYD_MOVE]);
  eq(movedRefs.includes("old.txt"), true, "hydrated: move_file subject source lands in references");
  const movedArt = renderPanel("artifacts", [HYD_MOVE]);
  eq(movedArt.includes("new.txt"), true, "hydrated: move_file subject destination lands in artifacts");
  // Args win when both are present (a rewritten subject must not shadow the
  // live dispatch payload).
  const BOTH = { kind: "tool", name: "write_file", args: JSON.stringify({ path: "C:/proj/live.txt" }), subject: "C:/proj/subject.txt" };
  const both = renderPanel("artifacts", [BOTH]);
  eq(both.includes("live.txt"), true, "hydrated: args path wins when args are present");
  eq(both.includes("subject.txt"), false, "hydrated: subject path is not listed alongside a parsed args path");
  // Non-path subjects (bash command, grep pattern) never leak into the lists.
  const NOISE = { kind: "tool", name: "bash", args: "", subject: "pnpm build" };
  eq(renderPanel("artifacts", [NOISE]).includes("pnpm build"), false, "hydrated: bash subject does not leak into artifacts");
  // Task 629: a hydrated whitelist-external writer with no args/subject still
  // lands via its persisted fileDiff header.
  const HYD_DIFF = { kind: "tool", name: "future_writer", args: "", fileDiff: { diff: "--- a/nb/report.md\n+++ b/nb/report.md\n", added: 2, removed: 1 } };
  eq(renderPanel("artifacts", [HYD_DIFF]).includes("nb/report.md"), true, "hydrated: fileDiff header path lands in artifacts (task 629)");
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
  eq(app.includes("dockModeWithinSidebarGates(rightDockMode, todoSidebarEnabled, subagentsPanelEnabled)"), true, "app: effective mode uses the shared gate helper (task 495 added the subagents switch argument)");
  eq(app.includes('effectiveRightDockMode === "artifacts" || effectiveRightDockMode === "references"'), true, "app: dock body branches for both modes");
  eq(app.includes("SideFilesDockPanel"), true, "app: dock body references the dock panel");
  eq(app.includes('t("workspace.artifactsTab")'), true, "app: artifacts tab label wired");
  eq(app.includes('t("workspace.referencesTab")'), true, "app: references tab label wired");
  // Task 452: the workspace panel mount feeds the task-114 accordion the same
  // live payload — before this it was mounted with no sessionItems at all and
  // the accordion was permanently empty in the main path.
  eq(/<WorkspacePanel[\s\S]{0,600}?sessionItems=\{exportItems\}/.test(app), true, "app: WorkspacePanel receives sessionItems (114 accordion fed)");
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
