// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1809-panel-shortcuts.test.tsx
// tl1809: command-palette-reachable main sub-panels got batch default shortcuts.
// Pins under test:
// 1. Registry: all 9 panel.* actions exist in SHORTCUT_DEFINITIONS with the
//    documented default combos per platform, collide with no other registered
//    default (all platforms), and stay configurable (visible + rebindable in
//    Settings → Keyboard shortcuts);
// 2. Behaviour: the App.tsx-shaped handlers fire from the workspace page,
//    stay silent when the target page/tab is already open (idempotency),
//    switch between non-workspace pages, and the tasklist chord is gated by
//    the same lab flag as its palette entry (switch off → never registers);
// 3. Source pins: each panel.* action is registered exactly once in src,
//    the tasklist registration carries tasklistPanelEntryEnabled as its
//    enabled arg, idempotency guards live in the handler bodies, and the nine
//    palette rows surface the same default combos via shortcutLabel.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import type { ShortcutAction, ShortcutCombo, ShortcutPlatform } from "../lib/keyboardShortcuts";

const { JSDOM } = await import("jsdom");
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const { defaultShortcutCombo, shortcutConflict, shortcutDefinition, useGlobalShortcut } = await import("../lib/keyboardShortcuts");
const { useAppNavigationStore } = await import("../store/appNavigation");
const { act } = await import("react");
const { createElement, useCallback } = await import("react");
const { createRoot } = await import("react-dom/client");

const PANEL_ACTIONS: Array<{ action: ShortcutAction; section: string; combo: ShortcutCombo }> = [
  { action: "panel.automation", section: "global", combo: { key: "a", ctrl: true, shift: true } },
  { action: "panel.trash", section: "global", combo: { key: "Delete", ctrl: true, shift: true } },
  { action: "panel.appearance", section: "global", combo: { key: "p", ctrl: true, shift: true } },
  { action: "panel.memory", section: "global", combo: { key: "m", ctrl: true, shift: true } },
  { action: "panel.models", section: "global", combo: { key: "c", ctrl: true, shift: true } },
  { action: "panel.usageStats", section: "global", combo: { key: "u", ctrl: true, shift: true } },
  { action: "panel.resourceManager", section: "view", combo: { key: "e", ctrl: true, shift: true } },
  { action: "panel.tasklist", section: "view", combo: { key: "k", ctrl: true, shift: true } },
  { action: "panel.collabHub", section: "view", combo: { key: "h", ctrl: true, shift: true } },
];
const sameCombo = (a: ShortcutCombo, b: ShortcutCombo): boolean =>
  a.key.toLowerCase() === b.key.toLowerCase()
  && Boolean(a.ctrl) === Boolean(b.ctrl)
  && Boolean(a.meta) === Boolean(b.meta)
  && Boolean(a.alt) === Boolean(b.alt)
  && Boolean(a.shift) === Boolean(b.shift);

console.log("\ntl1809 panel shortcuts: registry pins");
for (const { action, section, combo } of PANEL_ACTIONS) {
  const definition = shortcutDefinition(action);
  ok(definition.section === section, `${action} lives in section ${section} (got ${definition.section})`);
  for (const platform of ["darwin", "windows", "linux"] satisfies ShortcutPlatform[]) {
    const expected: ShortcutCombo = { key: combo.key, ctrl: platform !== "darwin", meta: platform === "darwin", shift: true };
    ok(sameCombo(defaultShortcutCombo(action, platform), expected), `${action} default on ${platform} is ${platform === "darwin" ? "Cmd" : "Ctrl"}+Shift+${combo.key}`);
    ok(shortcutConflict(action, defaultShortcutCombo(action, platform), platform) === null, `${action} default collides with no other registered shortcut on ${platform}`);
  }
  ok(definition.configurable !== false, `${action} stays configurable (visible + rebindable in Settings)`);
}

console.log("\ntl1809 panel shortcuts: behaviour pins");

// Harness mirrors the App.tsx handler bodies verbatim (guards included), with
// an event spy replacing the page/panel openers.
function Harness({ labOn, onEvent }: { labOn: boolean; onEvent: (name: string) => void }) {
  const closeTransientOverlays = useCallback(() => {}, []);
  useGlobalShortcut("panel.automation", () => {
    if (useAppNavigationStore.getState().page.kind === "automation") return;
    closeTransientOverlays();
    onEvent("automation");
  }, [closeTransientOverlays, onEvent]);
  useGlobalShortcut("panel.trash", () => {
    if (useAppNavigationStore.getState().page.kind === "trash") return;
    onEvent("trash");
  }, [onEvent]);
  useGlobalShortcut("panel.appearance", () => {
    const page = useAppNavigationStore.getState().page;
    if (page.kind === "settings" && page.tab === "appearance") return;
    closeTransientOverlays();
    onEvent("appearance");
  }, [closeTransientOverlays, onEvent]);
  useGlobalShortcut("panel.tasklist", () => onEvent("tasklist"), [], labOn);
  return null;
}

const fire = (action: ShortcutAction) => {
  const combo = defaultShortcutCombo(action, "windows");
  document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: combo.key, ctrlKey: combo.ctrl, metaKey: combo.meta, altKey: combo.alt, shiftKey: combo.shift, bubbles: true, cancelable: true }));
};

{
  const events: string[] = [];
  const onEvent = (name: string) => events.push(name);
  const root = createRoot(document.getElementById("root")!);

  // a. From the workspace page all page-class chords fire.
  useAppNavigationStore.setState({ page: { kind: "workspace" } });
  await act(async () => {
    root.render(createElement(Harness, { labOn: true, onEvent }));
  });
  fire("panel.automation");
  fire("panel.trash");
  fire("panel.appearance");
  ok(events.join(",") === "automation,trash,appearance", `workspace page: all chords fire (got ${JSON.stringify(events)})`);

  // b. Idempotency: already on the target page → the chord stays silent.
  useAppNavigationStore.setState({ page: { kind: "trash" } });
  fire("panel.trash");
  ok(events.length === 3, `on trash page: Ctrl+Shift+Delete does not re-open (got ${JSON.stringify(events)})`);
  useAppNavigationStore.setState({ page: { kind: "automation" } });
  fire("panel.automation");
  ok(events.length === 3, "on automation page: Ctrl+Shift+A does not re-open");
  useAppNavigationStore.setState({ page: { kind: "settings", tab: "appearance" } });
  fire("panel.appearance");
  ok(events.length === 3, "on settings→appearance: Ctrl+Shift+P does not re-open");

  // c. Switching still works between non-workspace pages/tabs.
  fire("panel.automation");
  ok(events.length === 4 && events[3] === "automation", "settings→appearance: Ctrl+Shift+A switches to automation");
  useAppNavigationStore.setState({ page: { kind: "settings", tab: "memory" } });
  fire("panel.appearance");
  ok(events.length === 5 && events[4] === "appearance", "settings→memory: Ctrl+Shift+P switches to appearance");

  // d. Lab gating: tasklist chord off → dead; on → alive (same flag family as
  //    the palette entry, task 505「switch off → never opens」contract).
  await act(async () => {
    root.render(createElement(Harness, { labOn: false, onEvent }));
  });
  useAppNavigationStore.setState({ page: { kind: "workspace" } });
  fire("panel.tasklist");
  ok(events.length === 5, "lab off: Ctrl+Shift+K does not register/fire");
  await act(async () => {
    root.render(createElement(Harness, { labOn: true, onEvent }));
  });
  fire("panel.tasklist");
  ok(events.length === 6 && events[5] === "tasklist", "lab on: Ctrl+Shift+K opens the tasklist panel");

  await act(async () => root.unmount());
  dom.window.close();
}

console.log("\ntl1809 panel shortcuts: source pins");
{
  const appSource = readFileSync(fileURLToPath(new URL("../App.tsx", import.meta.url)), "utf8");
  const registrationSlice = (action: string): string => {
    const start = appSource.indexOf(`useGlobalShortcut("${action}"`);
    assert.ok(start >= 0, `App.tsx registers ${action}`);
    const next = appSource.indexOf("useGlobalShortcut(", start + 1);
    return appSource.slice(start, next === -1 ? undefined : next);
  };

  // The tasklist chord rides the same lab flag as its palette entry.
  const gatedTail = /,\s*\[\]\s*,\s*tasklistPanelEntryEnabled\s*\)\s*;/;
  ok(gatedTail.test(registrationSlice("panel.tasklist")), "App.tsx: panel.tasklist passes tasklistPanelEntryEnabled as enabled");

  // Idempotency guards live in the real handler bodies (source pins).
  ok(registrationSlice("panel.trash").includes('page.kind === "trash"'), "App.tsx: panel.trash handler guards on trash page");
  ok(registrationSlice("panel.automation").includes('page.kind === "automation"'), "App.tsx: panel.automation handler guards on automation page");
  ok(registrationSlice("panel.appearance").includes('page.tab === "appearance"'), "App.tsx: panel.appearance handler guards on appearance tab");
  ok(registrationSlice("panel.memory").includes('page.tab === "memory"'), "App.tsx: panel.memory handler guards on memory tab");
  ok(registrationSlice("panel.models").includes('page.tab === "models"'), "App.tsx: panel.models handler guards on models tab");
  ok(registrationSlice("panel.usageStats").includes('page.tab === "models"'), "App.tsx: panel.usageStats handler guards on models tab");

  // Palette rows surface the same defaults (9 shortcutLabel rows).
  const labelCount = (appSource.match(/shortcutLabel: formatShortcutCombo\(defaultShortcutCombo\("panel\./g) ?? []).length;
  ok(labelCount === 9, `9 palette rows carry panel.* shortcutLabel (got ${labelCount})`);

  // Each panel.* action is registered exactly once in the whole src tree.
  const { readdirSync, statSync } = await import("node:fs");
  const srcRoot = fileURLToPath(new URL("..", import.meta.url));
  const walk = (dir: string): string[] =>
    readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
      const p = dir + e.name;
      if (e.name === "node_modules" || e.name === "dist" || e.name === "__tests__") return [];
      if (e.isDirectory()) return statSync(p).isDirectory() ? walk(p + "/") : [];
      return /\.(ts|tsx)$/.test(e.name) ? [p] : [];
    });
  const allSource = walk(srcRoot).map((p) => readFileSync(p, "utf8"));
  for (const { action } of PANEL_ACTIONS) {
    const count = allSource.reduce((n, s) => n + (s.match(new RegExp(`useGlobalShortcut\\("${action}"`, "g"))?.length ?? 0), 0);
    ok(count === 1, `${action} is registered exactly once in src (got ${count})`);
  }
}

process.stdout.write(`\ntl1809: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
