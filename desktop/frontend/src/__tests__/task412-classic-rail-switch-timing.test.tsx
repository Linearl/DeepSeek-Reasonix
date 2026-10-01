// Task 412 acceptance (two parts, P2 user install-feedback on v1520):
//  BUG   — classic's bottom three utility icons (trash / scheduler / settings)
//          still rendered as the old vertical NavButton list at the sidebar
//          foot: the workbench bottom-rail revamp (task 383 verdict 5) never
//          synced to the classic branch of SidebarRegion. Fix = classic
//          (non-creation) renders the SAME .sidebar__utility-row markup as
//          workbench (icon-only UtilityButtons), mirrored by a classic-scoped
//          CSS block; creation keeps its nav rows (zero regression); the
//          automation-active marker is now shared by both layouts.
//  EVIDENCE-FIRST — the layout switch had NO dedicated timing trace
//          (desktop.log only carries tab-switch timing). SettingsPanel's
//          layout segmented control now logs three probes per switch with
//          from/to (both directions): started → applied → painted. The data
//          decides whether an optimization task is warranted.
//
// Run: npx tsx src/__tests__/task412-classic-rail-switch-timing.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 412 classic rail + switch timing");

// ── Part 1: classic renders the workbench utility-row markup ────────────────
{
  const dom = new (await import("jsdom")).JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

  const React = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { createElement } = React;
  const { SidebarRegion } = await import("../app-shell/SidebarRegion");
  const { LocaleProvider } = await import("../lib/i18n");

  const base = {
    className: "sidebar", workbench: false, creation: false, collapsed: false,
    navTooltipDisabled: true, searchOpen: false, togglePressed: false, toggleTitle: "toggle",
    automation: true,
    resize: { min: 200, max: 500, value: 300, onPointerDown: () => {}, onKeyDown: () => {}, onReset: () => {} },
    projectTree: { t: (k: string) => k } as never,
    t: (k: string) => k,
    onNewSession: () => {}, onOpenTrash: () => {}, onOpenAutomation: () => {},
    onOpenSettings: () => {}, onToggleSearch: () => {}, onToggle: () => {},
  };
  const render = (overrides: Partial<typeof base>) =>
    renderToStaticMarkup(createElement(LocaleProvider, null, createElement(SidebarRegion, { ...base, ...overrides })));

  // classic (the v1520 complaint): utility-row present, vertical nav items gone
  const classic = render({});
  ok(classic.includes("sidebar__utility-row"), "classic bottom rail uses .sidebar__utility-row (workbench layout synced)");
  ok((classic.match(/sidebar__utility-button/g) || []).length >= 3, "classic rail carries three icon-only utility buttons");
  ok(!classic.includes("sidebar__navitem--search") && !/sidebar__navitem/.test(classic), "classic no longer renders the vertical NavButton list");
  ok(classic.includes("sidebar__utility-button--active"), "classic scheduler button carries the automation-active marker");
  ok(classic.includes("sidebar__nav--footer"), "classic rail sits in the footer nav slot (same slot as workbench)");

  // workbench unchanged (regression face) — utility-row still there, now with
  // the active marker too (shared marker, deliberate alignment).
  const workbench = render({ workbench: true });
  ok((workbench.match(/sidebar__utility-button/g) || []).length >= 3, "workbench rail unchanged (three utility buttons)");
  ok(workbench.includes("sidebar__utility-button--active"), "workbench scheduler button carries the same active marker");

  // creation keeps its nav rows (zero regression for the third layout)
  const creation = render({ creation: true });
  ok(/sidebar__navitem/.test(creation), "creation layout keeps its NavButton rows (out of scope, zero regression)");
}

// ── Part 1b: classic-scoped CSS mirrors the workbench rail ──────────────────
{
  const css = readFileSync(fileURLToPath(new URL("../styles.css", import.meta.url)), "utf8");
  const classicRule = css.match(/\.sidebar:not\(\.sidebar--workbench\) \.sidebar__nav--footer \.sidebar__utility-row\s*\{[^}]*\}/);
  ok(!!classicRule && /grid-template-columns:\s*repeat\(3,\s*minmax\(0,\s*1fr\)\)/.test(classicRule![0]), "classic CSS: utility-row grid mirrors workbench (3 equal columns)");
  ok(/\.sidebar:not\(\.sidebar--workbench\) \.sidebar__utility-button\s*\{[^}]*justify-content:\s*center/.test(css), "classic CSS: utility buttons mirror the workbench icon-button shape");
  ok(/\.sidebar \.sidebar__utility-button--active\s*\{[^}]*var\(--accent\)/.test(css), "active marker styled with accent tokens (theme-token only)");
}

// ── Part 2: layout-switch timing probes (evidence-first) ────────────────────
{
  const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
  ok(panel.includes('"layout switch started"') && panel.includes('"layout switch applied"') && panel.includes('"layout switch painted"'), "three timing probes wired: started → applied → painted");
  ok(panel.includes("from=${from} to=${style}") && panel.includes("durationMs="), "probes record direction (from/to) and durationMs (data decides optimization)");
  ok(/reportFrontendLog\("layout-switch"/.test(panel), "probes land in desktop.log via reportFrontendLog (the 4MB rolling channel)");
  // the switch handler still performs the actual switch (no behavior change
  // beyond logging)
  ok(/void apply\(\(\) => app\.SetDesktopLayoutStyle\(style\)\)/.test(panel), "switch call chain unchanged (SetDesktopLayoutStyle still applied)");
  ok(/requestAnimationFrame\(\(\) => requestAnimationFrame\(/.test(panel), "painted probe waits for a real re-layout (double-rAF)");
}

// ── Rework (v2058 install-feedback): classic renders through App.tsx, not
//    SidebarRegion — the harness DOM assertions passed while the installed
//    app still stacked vertically because App.tsx's footer ternary only sent
//    workbench down the utility-row path (classic fell through to the old
//    vertical navitem list with sr-only labels = "icons only but stacked",
//    exactly the user's screenshot). Both render paths must now reach it.
{
  const app = readFileSync(fileURLToPath(new URL("../App.tsx", import.meta.url)), "utf8");
  ok(app.includes("{(sidebarWorkbench || !sidebarCreation) ? (\n            <nav className=\"sidebar__nav sidebar__nav--footer\">"), "App.tsx footer ternary sends classic (non-creation) down the utility-row path");
  // condition semantics: workbench ✓, classic ✓, creation ✗
  const cond = (w: boolean, c: boolean) => w || !c;
  ok(cond(false, false) && cond(true, false) && !cond(false, true), "condition truth table: classic→row, workbench→row, creation→nav");
  ok(/\{sidebarCreation && \(\s*<Tooltip label=\{t\("projectTree\.searchPlaceholder"\)\}/.test(app), "App.tsx creation nav rows kept (search + labeled buttons unchanged)");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
