// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1275-wall-shortcut-gating.test.tsx
// tl 1275: Ctrl+P (palette.gotoTopic) and Ctrl+Shift+T (palette.gotoTab) must
// share the SAME lab gating. Contract under test (task 505「switch off →
// never opens」, shortcut face):
// - with the sessionWall lab switch OFF, neither combo opens the session wall
//   (the shortcut is not even registered — enabled=false ⇒ no listener);
// - with it ON, both combos open the wall as before (behaviour unchanged);
// - the App.tsx wiring itself carries `sessionWallEnabled` as the 4th
//   `useGlobalShortcut` argument for BOTH actions (source pin, task-563
//   style), each action registered exactly once in the whole src tree —
//   so the harness below cannot drift from the real registration.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

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

const { useGlobalShortcut, resolvedShortcutCombo, detectShortcutPlatform } = await import("../lib/keyboardShortcuts");
const { act } = await import("react");
const { createElement, useCallback } = await import("react");
const { createRoot } = await import("react-dom/client");

// Harness mirrors the App.tsx registrations verbatim (gotoTopic stacks the
// palette before the wall, gotoTab opens the wall directly), driven by the
// lab flag exactly like App.tsx drives them by sessionWallEnabled.
function Harness({ wallEnabled, onOpen }: { wallEnabled: boolean; onOpen: (entry: "gotoTab" | "gotoTopic") => void }) {
  const openPalette = useCallback(async () => {}, []);
  useGlobalShortcut("palette.gotoTab", () => onOpen("gotoTab"), [], wallEnabled);
  useGlobalShortcut("palette.gotoTopic", () => {
    void openPalette().then(() => onOpen("gotoTopic"));
  }, [openPalette], wallEnabled);
  return null;
}

const fire = (action: Parameters<typeof resolvedShortcutCombo>[0]) => {
  const combo = resolvedShortcutCombo(action, detectShortcutPlatform());
  document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: combo.key, ctrlKey: combo.ctrl, metaKey: combo.meta, altKey: combo.alt, shiftKey: combo.shift, bubbles: true, cancelable: true }));
};

console.log("\ntl 1275 wall shortcut lab gating");

// 1+2. Behavioural: switch off → Ctrl+P / Ctrl+Shift+T both dead; switch on →
//      both alive. Both states constructed.
{
  const opened: Array<"gotoTab" | "gotoTopic"> = [];
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(createElement(Harness, { wallEnabled: false, onOpen: (e) => opened.push(e) }));
  });
  fire("palette.gotoTopic");
  fire("palette.gotoTab");
  ok(opened.length === 0, `switch off: neither Ctrl+P nor Ctrl+Shift+T opens the wall (got ${JSON.stringify(opened)})`);

  await act(async () => {
    root.render(createElement(Harness, { wallEnabled: true, onOpen: (e) => opened.push(e) }));
  });
  fire("palette.gotoTopic");
  // The gotoTopic face stacks the palette before the wall (openPalette().then),
  // so the open lands on a microtask — flush before asserting.
  await new Promise((resolve) => setTimeout(resolve, 0));
  ok(opened.length === 1 && opened[0] === "gotoTopic", `switch on: Ctrl+P opens the wall (got ${JSON.stringify(opened)})`);
  fire("palette.gotoTab");
  await new Promise((resolve) => setTimeout(resolve, 0));
  ok(opened.length === 2 && opened[1] === "gotoTab", `switch on: Ctrl+Shift+T opens the wall (got ${JSON.stringify(opened)})`);

  await act(async () => root.unmount());
  dom.window.close();
}

// 3. Source pin: the real App.tsx wiring carries sessionWallEnabled for BOTH
//    wall shortcuts, each registered exactly once in src (no bypass route).
{
  const appSource = readFileSync(fileURLToPath(new URL("../App.tsx", import.meta.url)), "utf8");
  const registrationSlice = (action: string): string => {
    const start = appSource.indexOf(`useGlobalShortcut("${action}"`);
    assert.ok(start >= 0, `App.tsx registers ${action}`);
    const next = appSource.indexOf("useGlobalShortcut(", start + 1);
    return appSource.slice(start, next === -1 ? undefined : next);
  };
  const gatedTail = /\}\s*,\s*\[[^\]]*\]\s*,\s*sessionWallEnabled\s*\)\s*;/;
  ok(gatedTail.test(registrationSlice("palette.gotoTopic")), "App.tsx: palette.gotoTopic (Ctrl+P) passes sessionWallEnabled as enabled");
  ok(gatedTail.test(registrationSlice("palette.gotoTab")), "App.tsx: palette.gotoTab (Ctrl+Shift+T) passes sessionWallEnabled as enabled");

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
  for (const action of ["palette.gotoTopic", "palette.gotoTab"]) {
    const count = allSource.reduce((n, s) => n + (s.match(new RegExp(`useGlobalShortcut\\("${action}"`, "g"))?.length ?? 0), 0);
    ok(count === 1, `${action} is registered exactly once in src (got ${count})`);
  }
}

process.stdout.write(`\ntl 1275: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
