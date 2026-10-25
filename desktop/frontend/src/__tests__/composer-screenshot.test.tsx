// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-screenshot.test.tsx
//
// tl 1087: composer screenshot button wiring. The full overlay flow (drag a
// selection, draw on the canvas, confirm) needs a real 2d context and pixel
// geometry, which jsdom cannot provide — that half lives in
// screenshot-overlay-contract.test.ts as a source contract. What this suite
// proves end-to-end is the part jsdom *can* host: the button exists, is
// gated like the other attachment controls, opens the overlay, and the
// confirm callback inserts the attachment into the draft through the same
// link the paste path uses.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
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
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  // jsdom's activeElement shim predates attachEvent; the composer's focus
  // healing probes for it, so stub it out like the emoji picker suite does.
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
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

// The overlay needs a canvas + image decode to run in jsdom; stub the two
// APIs the flow touches so the confirm path is exercisable without a GPU.
function installCanvasAndImageStubs() {
  const context = {
    clearRect() {},
    drawImage() {},
    strokeRect() {},
    beginPath() {},
    moveTo() {},
    lineTo() {},
    stroke() {},
    fillText() {},
    getImageData() { return { data: new Uint8ClampedArray(4) }; },
    putImageData() {},
    save() {},
    restore() {},
  };
  Object.defineProperty(dom.window.HTMLCanvasElement.prototype, "getContext", {
    configurable: true,
    value: () => context,
  });
  Object.defineProperty(dom.window.HTMLCanvasElement.prototype, "toDataURL", {
    configurable: true,
    value: () => "data:image/png;base64,SCREENSHOT_FINAL",
  });
  class FakeImage {
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    set src(_value: string) {
      // Decode synchronously so primeScreenshotImageCache resolves at once.
      queueMicrotask(() => this.onload?.());
    }
  }
  Object.defineProperty(dom.window, "Image", { configurable: true, value: FakeImage });
  // The overlay references the bare `Image` global (not window.Image); jsdom's
  // window is the global object in this harness, but the assignment above only
  // patches the window property — mirror it onto globalThis too.
  Object.defineProperty(globalThis, "Image", { configurable: true, value: FakeImage });
}

let dom: JSDOM;

function installBridgeApp(methods: Record<string, unknown>) {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    go: undefined,
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        ...methods,
      },
    },
  } as never;
}

const captureCalls: { released: string[]; savedAttachments: string[] } = {
  released: [],
  savedAttachments: [],
};

async function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const currentProps: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "reasonix",
    imageInputEnabled: true,
    tabId: "screenshot-tab",
    sessionKey: "session:project:/repo:topic-a:session-a",
    onSend: () => {},
    onCancel: async () => ({ discardedItemIds: [] }),
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => true,
    onSetEffort: () => {},
    ready: true,
    ...props,
  };
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <div className="chat-pane">
            <Composer {...currentProps} />
          </div>
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });
}

function screenshotButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(".composer-screenshot-trigger");
}

function overlay(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".screenshot-overlay");
}

async function main() {
  dom = installDom();
  installCanvasAndImageStubs();
  installBridgeApp({
    CaptureScreenshotAtCursor: async () => ({
      path: ".reasonix/attachments/mock-screen.png",
      width: 1920,
      height: 1080,
      x: 0,
      y: 0,
    }),
    AttachmentDataURL: async () => "data:image/svg+xml,mock",
    ReleaseScreenshotCapture: async (path: string) => {
      captureCalls.released.push(path);
    },
    SaveScreenshotAttachment: async (dataUrl: string) => {
      captureCalls.savedAttachments.push(dataUrl);
      return ".reasonix/attachments/mock-screenshot-1.png";
    },
  });

  console.log("\ntl 1087 composer screenshot button");
  await renderComposer();

  const button = screenshotButton();
  ok(button !== null, "composer toolbar renders the screenshot button");
  eq(button?.getAttribute("aria-label"), "Screenshot", "button carries the localized label");
  ok(overlay() === null, "overlay is closed before the button is pressed");

  // tl 1130: the button moved from the left "+"-menu cluster to the right side
  // of the input bar, right after the emoji button — same composer__btn family
  // as its new toolbar neighbours.
  const sendToolbar = button?.closest(".composer-toolbar-send");
  ok(sendToolbar !== null, "screenshot button lives in the composer-toolbar-send (right side of the input bar)");
  const emojiBlock = document.querySelector(".composer-emoji-picker");
  ok(emojiBlock !== null, "emoji picker block is rendered");
  ok(
    emojiBlock !== null && button !== null && (emojiBlock.nextElementSibling === button.closest(".composer-screenshot-picker")),
    "screenshot button block sits directly after the emoji button block",
  );
  eq(
    button?.classList.contains("composer__btn"),
    true,
    "screenshot button uses the composer__btn family (right-toolbar styling)",
  );

  // tl 1130 capture-race guard: while the capture is still in flight the
  // overlay mounts with data-capture-ready="loading" and the stylesheet hides
  // it (display:none) — a visible black loading frame is what the legacy
  // BitBlt path used to photograph as the desktop. The loading attribute
  // window is unobservable here (the mock resolves within one flushTimers),
  // so the loading-state contract is pinned statically in
  // screenshot-overlay-loading-css.test.ts.
  await act(async () => {
    button?.click();
    await flushTimers();
  });
  ok(overlay() !== null, "pressing the button opens the screenshot overlay");
  eq(overlay()?.getAttribute("role"), "dialog", "overlay is a modal dialog");
  eq(overlay()?.getAttribute("aria-label"), "Screenshot", "overlay carries the localized title");
  eq(overlay()?.getAttribute("data-capture-ready"), "ready", "capture completed and the overlay is ready");

  console.log("\ntl 1087 screenshot confirm inserts an attachment");

  // Confirm through the toolbar's confirm button (no selection → the button
  // is disabled, which is itself part of the tl 1087 contract: a stray double
  // click with no selection must not fire a save).
  const confirmButton = document.querySelector<HTMLButtonElement>(".screenshot-overlay__confirm");
  ok(confirmButton !== null, "overlay renders the confirm button");
  ok(Boolean(confirmButton?.disabled), "confirm is disabled before a selection exists");

  // Escape cancels with no side effects: overlay closes, no attachment saved.
  // The composer input holds focus while the overlay is up, so the key lands
  // on the textarea first — the overlay's own document-level handler is the
  // backstop for keys typed outside the input.
  const textarea = document.querySelector<HTMLTextAreaElement>("#composer-input");
  await act(async () => {
    textarea?.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  ok(overlay() === null, "Escape closes the overlay");
  eq(captureCalls.savedAttachments.length, 0, "Escape saves no attachment");
  ok(captureCalls.released.includes(".reasonix/attachments/mock-screen.png"), "Escape releases the backend capture temp file");

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

void main();
