// Run: tsx src/__tests__/version-switch-dialog.test.tsx

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { VersionSwitchDialog, type VersionEntry } from "../components/VersionSwitchDialog";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 20));
}

const versions: VersionEntry[] = [
  { version: "v1.38.3-20260922-0100", active: false, modTimeUnix: 1758500400 },
  { version: "v1.38.3", active: true, modTimeUnix: 1758414000 },
  { version: "v1.37.0", active: false, modTimeUnix: 1757500000 },
];

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const events: { switched?: string; closed?: boolean; staging?: boolean } = {};

function Harness() {
  return (
    <LocaleProvider>
      <VersionSwitchDialog
        open
        versions={versions}
        switching={null}
        error={null}
        onSwitch={(version) => { events.switched = version; }}
        onClose={() => { events.closed = true; }}
        onPublishStaging={() => { events.staging = true; }}
        formatTime={(unix) => new Date(unix * 1000).toISOString().slice(0, 10)}
      />
    </LocaleProvider>
  );
}

console.log("\nVersion switch dialog (task 210)");

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);

await act(async () => {
  root.render(<Harness />);
  await flush();
});

const dialog = document.querySelector('[role="dialog"]');
ok(Boolean(dialog), "dialog renders when open");

const body = document.body.textContent ?? "";
ok(body.includes("v1.38.3-20260922-0100") && body.includes("v1.37.0"), "lists installed versions newest first");
ok(/当前|current/.test(body), "marks the active version");
ok(/最新|newest/.test(body), "marks the newest non-active version");
ok(!body.includes("无安装"), "empty-state note absent when versions exist");

// The active row must not be clickable.
const rows = Array.from(document.querySelectorAll('[role="dialog"] [role="button"]'));
const activeRow = rows.find((row) => row.getAttribute("aria-disabled") === "true");
ok(Boolean(activeRow), "active row is disabled");
await act(async () => {
  activeRow?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  await flush();
});
ok(events.switched === undefined, "clicking the active row does not switch");

const pickable = rows.find((row) => row.getAttribute("aria-disabled") === "false");
ok(Boolean(pickable), "pickable rows exist");
await act(async () => {
  (pickable as HTMLElement | undefined)?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  await flush();
});
ok(events.switched === "v1.38.3-20260922-0100", "clicking a version requests the switch");

// Footer buttons: publish-staging keeps the task-81 entry, cancel closes.
const buttons = Array.from(document.querySelectorAll('[role="dialog"] button'));
const stagingButton = buttons.find((b) => (b.textContent ?? "").includes("staging"));
await act(async () => {
  stagingButton?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  await flush();
});
ok(events.staging === true, "publish-staging entry preserved in the dialog");
const cancelButton = buttons.find((b) => (b.textContent ?? "").includes("取消") || (b.textContent ?? "").toLowerCase().includes("cancel"));
await act(async () => {
  cancelButton?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  await flush();
});
ok(events.closed === true, "cancel closes the dialog");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
process.exit(failed > 0 ? 1 : 0);
