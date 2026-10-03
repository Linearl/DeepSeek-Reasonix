// Run: tsx src/__tests__/user-message-resend.test.tsx
// 任务461-P3: a failed submission renders a one-click 「重发」 beside the failure
// line. The button re-submits the ORIGINAL display/submit payload through the
// onResend channel (the same rewind+submit path edit-and-resend uses), and is
// disabled while a resend is in flight so a double click cannot queue two.

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { UserMessage } from "../components/Message";
import { LocaleProvider } from "../lib/i18n";

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

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

type ResendCall = { turn: number; displayText: string; submitText?: string };

function render(props: React.ComponentProps<typeof UserMessage>): { root: ReturnType<typeof createRoot>; dom: JSDOM; calls: ResendCall[]; hangResend: (value: boolean) => void } {
  const dom = installDom();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const calls: ResendCall[] = [];
  let hang = false;
  let release: (() => void) | null = null;
  const onResend = async (turn: number, displayText: string, submitText?: string) => {
    calls.push({ turn, displayText, submitText });
    if (hang) await new Promise<void>((resolve) => { release = resolve; });
  };
  const root = createRoot(rootEl);
  act(() => {
    root.render(
      <LocaleProvider>
        <UserMessage {...props} onResend={onResend} />
      </LocaleProvider>,
    );
  });
  return {
    root,
    dom,
    calls,
    hangResend: (value: boolean) => {
      hang = value;
      if (!value && release) release();
    },
  };
}

function findResendButton(): HTMLButtonElement | null {
  return document.querySelector(".msg__resend");
}

async function main() {
  const text = "帮我看一下收件箱";

  // 1. A failed message shows the resend button; clicking it re-sends the
  //    original payload with the original turn number.
  {
    const ctx = render({ text, failed: true, turn: 3, submitText: text + " [原始投递参数]" });
    const button = findResendButton();
    ok(Boolean(button), "failed message renders the resend button");
    // The test DOM's navigator resolves the locale to en; accept either face.
    ok(button?.textContent === "重发" || button?.textContent === "Resend", `button reads the resend label, got ${button?.textContent}`);
    if (button) {
      await act(async () => {
        button.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
        await flushTimers();
      });
    }
    eq(ctx.calls.length, 1, "one resend per click");
    eq(ctx.calls[0]?.turn, 3, "resend carries the original turn");
    eq(ctx.calls[0]?.displayText, text, "resend reuses the original display text");
    eq(ctx.calls[0]?.submitText, text + " [原始投递参数]", "resend reuses the original submit payload");
    ctx.root.unmount();
    ctx.dom.window.close();
  }

  // 2. While a resend is in flight the button is disabled (double-click guard).
  {
    const ctx = render({ text, failed: true, turn: 1 });
    ctx.hangResend(true);
    const button = findResendButton();
    if (button) {
      await act(async () => {
        button.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
        await flushTimers();
      });
      eq(button.disabled, true, "button is disabled while the resend is in flight");
      // A second click during flight must not queue a second submission.
      button.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
      await flushTimers();
    }
    ctx.hangResend(false);
    await act(async () => { await flushTimers(); });
    eq(ctx.calls.length, 1, "no duplicate submission during flight");
    ctx.root.unmount();
    ctx.dom.window.close();
  }

  // 3. A delivered (non-failed) message shows no resend button.
  {
    const ctx = render({ text, turn: 2 });
    eq(findResendButton(), null, "non-failed message has no resend button");
    ctx.root.unmount();
    ctx.dom.window.close();
  }

  // 4. Without the onResend channel (older host) the button never renders —
  //    the pre-461 face, byte for byte.
  {
    const dom = installDom();
    const rootEl = document.getElementById("root");
    const root = createRoot(rootEl!);
    act(() => {
      root.render(<LocaleProvider><UserMessage text={text} failed turn={1} /></LocaleProvider>);
    });
    eq(findResendButton(), null, "no resend button without the onResend channel");
    root.unmount();
    dom.window.close();
  }

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

void main();
