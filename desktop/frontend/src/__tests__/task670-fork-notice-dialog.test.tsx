// Run: tsx src/__tests__/task670-fork-notice-dialog.test.tsx
//
// Task 670 — the fork first-launch notice dialog. Acceptance (tasklist 670):
//   1. the close button carries the countdown text and the countdown
//      auto-closes the dialog when it hits zero (10s budget, driven here by
//      a steerable Date.now so the wall clock stays fast);
//   2. a click INSIDE the dialog cancels the countdown — the dialog stays up
//      and the countdown text disappears, waiting for an explicit action;
//   3. a click OUTSIDE (the backdrop) closes it immediately;
//   4. the 「前往设置 → 实验室」and「下次不提醒」buttons fire their callbacks
//      (the App wires the mute into the user config and the jump into the
//      lab tab — the Go side pins the state machine, fork_notice_test.go).

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ForkNoticeDialog } from "../components/ForkNoticeDialog";
import { LocaleProvider, t } from "../lib/i18n";

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

function wait(ms = 0): Promise<void> {
  return act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms));
  });
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

/** Steerable clock: the dialog reads Date.now() per interval tick, so the
 *  test advances the virtual clock while the real 200ms tick fires. */
let virtualNow = 0;
const realDateNow = Date.now;

function mountDialog(root: ReturnType<typeof createRoot>, props: {
  open: boolean;
  version: string;
  onClose: () => void;
  onOpenLab: () => void;
  onDismissForever: () => void;
}) {
  return act(async () => {
    root.render(
      <LocaleProvider>
        <ForkNoticeDialog {...props} />
      </LocaleProvider>,
    );
  });
}

function dialogEl(): HTMLElement | null {
  return document.querySelector(".modal.reasonix-confirm-dialog");
}

function backdropEl(): HTMLElement | null {
  return document.querySelector(".modal-backdrop.reasonix-confirm-backdrop");
}

function buttonTexts(): string[] {
  return Array.from(dialogEl()?.querySelectorAll("button") ?? []).map((b) => b.textContent ?? "");
}

function mouseDownOn(target: Element) {
  target.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
}

async function main() {
  const dom = installDom();
  Date.now = () => virtualNow;
  try {
    const rootEl = document.getElementById("root")!;
    const root = createRoot(rootEl);

    // ── Acceptance 1: renders with the countdown on the close button ──
    let closeCalls = 0, labCalls = 0, dismissCalls = 0;
    const props = {
      open: true,
      version: "v1.38.3-20261009-1031",
      onClose: () => { closeCalls += 1; },
      onOpenLab: () => { labCalls += 1; },
      onDismissForever: () => { dismissCalls += 1; },
    };
    await mountDialog(root, props);
    ok(dialogEl() !== null, "open=true renders the dialog");
    ok((dialogEl()?.textContent ?? "").includes(t("forkNotice.title")), "title announces the fork build");
    ok((dialogEl()?.textContent ?? "").includes("v1.38.3-20261009-1031"), "body carries the version name");
    ok(buttonTexts().some((txt) => txt === `${t("forkNotice.close")} (10s)`), "close button carries the fresh 10s countdown");
    ok(buttonTexts().some((txt) => txt === t("forkNotice.openLab")), "lab jump button present");
    ok(buttonTexts().some((txt) => txt === t("forkNotice.dismiss")), "dismiss-forever button present");

    // Advancing past 10s auto-closes (Acceptance: countdown self-close).
    await wait(250); // one real interval tick with virtualNow still 0
    virtualNow += 11_000;
    await wait(250);
    ok(closeCalls === 1, "countdown hitting zero auto-closed the dialog");

    // ── Re-open: click inside cancels the countdown (Acceptance 5) ──
    closeCalls = 0;
    // The parent unmounts (open=false) between launches; the dialog resets
    // its countdown on the next open.
    await mountDialog(root, { ...props, open: false });
    await mountDialog(root, { ...props, open: true });
    await wait(250);
    ok(buttonTexts().some((txt) => txt.startsWith(`${t("forkNotice.close")} (`)), "re-open restarts the countdown");
    mouseDownOn(dialogEl()!); // inside the dialog, on the title element
    await wait(50);
    ok(buttonTexts().some((txt) => txt === t("forkNotice.close")), "inside click strips the countdown text");
    virtualNow += 30_000;
    await wait(300);
    ok(closeCalls === 0, "cancelled countdown never auto-closes");
    // The dialog then waits for an explicit action.
    (dialogEl()?.querySelectorAll("button")[1] as HTMLButtonElement).click();
    await wait(50);
    ok(closeCalls === 1, "explicit close still works after the countdown was cancelled");

    // ── Outside click closes immediately (Acceptance 4) ──
    closeCalls = 0;
    await mountDialog(root, { ...props, open: true });
    await wait(250);
    mouseDownOn(backdropEl()!);
    await wait(50);
    ok(closeCalls === 1, "backdrop click closed the dialog immediately");

    // ── Buttons fire their callbacks ──
    labCalls = 0; dismissCalls = 0;
    await mountDialog(root, { ...props, open: true });
    await wait(250);
    const buttons = dialogEl()!.querySelectorAll("button");
    (buttons[0] as HTMLButtonElement).click(); // 下次不提醒
    await wait(50);
    (buttons[2] as HTMLButtonElement).click(); // 前往实验室
    await wait(50);
    ok(dismissCalls === 1, "「下次不提醒」 fired its callback");
    ok(labCalls === 1, "lab jump fired its callback");

    // ── open=false renders nothing ──
    await mountDialog(root, { ...props, open: false });
    ok(dialogEl() === null && backdropEl() === null, "open=false renders nothing");

    await act(async () => { root.unmount(); });
  } finally {
    Date.now = realDateNow;
    void dom.window.close();
  }

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

void main();
