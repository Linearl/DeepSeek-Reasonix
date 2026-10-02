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

const events: { switched?: string; deleted?: string; closed?: boolean; staging?: boolean } = {};
let deletingNow: string | null = null;

function Harness() {
  return (
    <LocaleProvider>
      <VersionSwitchDialog
        open
        versions={versions}
        switching={null}
        deleting={deletingNow}
        error={null}
        onSwitch={(version) => { events.switched = version; }}
        onDelete={(version) => { events.deleted = version; }}
        onClose={() => { events.closed = true; }}
        onPublishStaging={() => { events.staging = true; }}
        formatTime={(unix) => new Date(unix * 1000).toISOString().slice(0, 10)}
      />
    </LocaleProvider>
  );
}

function clickButtonByLabel(label: string | RegExp) {
  const buttons = Array.from(document.querySelectorAll('[role="dialog"] button'));
  const hit = (b: Element) =>
    typeof label === "string" ? (b.textContent ?? "").includes(label) : label.test(b.textContent ?? "");
  const target = buttons.find(hit);
  if (!target) throw new Error(`button not found: ${label}`);
  target.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  return target;
}

function click(el: Element) {
  el.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
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

// Task 411: per-row delete entry with an in-row confirm step. The active row
// has no delete entry at all; a non-active row must not fire onDelete until
// its confirm button is clicked, and cancel backs out without any call.
// Bilingual matchers: the runner pins en_US (task-210 convention), a direct
// run under a zh locale must pass the same assertions.
{
  const isDeleteEntry = (b: Element) => /^(删除|Delete)$/.test((b.textContent ?? "").trim());
  const confirmPromptGone = () => !/删除该版本|Delete this version/.test(document.body.textContent ?? "");
  const deleteButtons = Array.from(document.querySelectorAll('[role="dialog"] button'))
    .filter(isDeleteEntry);
  ok(deleteButtons.length === 2, "non-active rows carry a delete entry (active row has none)");
  const firstDelete = deleteButtons[0];
  await act(async () => {
    click(firstDelete);
    await flush();
  });
  ok(events.deleted === undefined, "clicking delete only opens the confirm, no delete fires yet");
  ok(/删除该版本|Delete this version/.test(document.body.textContent ?? ""), "confirm prompt appears in the row");

  // Cancel backs out.
  await act(async () => {
    clickButtonByLabel(/^(取消|Cancel)$/);
    await flush();
  });
  ok(events.deleted === undefined, "cancel closes the confirm without deleting");
  ok(confirmPromptGone(), "confirm prompt gone after cancel");

  // Delete again and confirm this time.
  const del2 = Array.from(document.querySelectorAll('[role="dialog"] button'))
    .find(isDeleteEntry);
  await act(async () => {
    click(del2 as Element);
    await flush();
  });
  await act(async () => {
    clickButtonByLabel(/^(确认删除|Confirm delete)$/);
    await flush();
  });
  ok(events.deleted === "v1.38.3-20260922-0100", "confirm fires onDelete with the row's version");
  ok(confirmPromptGone(), "confirm prompt cleared after firing");
}

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
