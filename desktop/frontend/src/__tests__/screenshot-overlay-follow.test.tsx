// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/screenshot-overlay-follow.test.tsx
//
// tl 1370: cursor-follow capture. jsdom cannot host real monitors, so what
// this suite pins is the follow loop's wiring against a programmable bridge:
// opening captures the cursor's display once (single-display stability — no
// spurious re-captures), a device change re-captures through the same
// release→clear→capture chain the cross-monitor toggle uses, the poll
// interval is the throttle (a rapid back-and-forth crossing is bounded, never
// a storm), the tl 1088 toggle keeps working and pauses the follow loop while
// on, and closing releases the final temp file.

import { JSDOM } from "jsdom";

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ScreenshotOverlay, CURSOR_FOLLOW_POLL_MS } from "../components/ScreenshotOverlay";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";

// The overlay's `open` is owned by the parent (the Composer tears it down on
// Esc); the harness mirrors that so the close path and its temp release are
// exercisable.
function OverlayHarness() {
  const [open, setOpen] = useState(true);
  return <ScreenshotOverlay open={open} onClose={() => setOpen(false)} onConfirm={() => {}} />;
}

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

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

let dom: JSDOM;

function installDom() {
  const d = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = d.window as unknown as Window & typeof globalThis;
  globalThis.document = d.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: d.window.navigator });
  globalThis.Node = d.window.Node;
  globalThis.HTMLElement = d.window.HTMLElement;
  globalThis.Event = d.window.Event;
  globalThis.CustomEvent = d.window.CustomEvent;
  globalThis.KeyboardEvent = d.window.KeyboardEvent;
  globalThis.MouseEvent = d.window.MouseEvent;
  globalThis.PointerEvent = d.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = d.window.MutationObserver;
  globalThis.localStorage = d.window.localStorage;
  globalThis.requestAnimationFrame = d.window.requestAnimationFrame.bind(d.window);
  globalThis.cancelAnimationFrame = d.window.cancelAnimationFrame.bind(d.window);
  globalThis.ResizeObserver = TestResizeObserver;
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
  installCanvasAndImageStubs(d);
  return d;
}

// The overlay needs a canvas + image decode to run in jsdom; stub the two
// APIs the capture flow touches (same approach as composer-screenshot).
function installCanvasAndImageStubs(d: JSDOM) {
  const context = {
    clearRect() {},
    drawImage() {},
    strokeRect() {},
    beginPath() {},
    moveTo() {},
    lineTo() {},
    stroke() {},
    fillText() {},
    arc() {},
    fill() {},
    ellipse() {},
    getImageData() { return { data: new Uint8ClampedArray(4) }; },
    putImageData() {},
    save() {},
    restore() {},
  };
  Object.defineProperty(d.window.HTMLCanvasElement.prototype, "getContext", {
    configurable: true,
    value: () => context,
  });
  Object.defineProperty(d.window.HTMLCanvasElement.prototype, "toDataURL", {
    configurable: true,
    value: () => "data:image/png;base64,SCREENSHOT_FINAL",
  });
  class FakeImage {
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    set src(_value: string) {
      queueMicrotask(() => this.onload?.());
    }
  }
  Object.defineProperty(d.window, "Image", { configurable: true, value: FakeImage });
  Object.defineProperty(globalThis, "Image", { configurable: true, value: FakeImage });
}

// --- programmable bridge ----------------------------------------------------

const calls = {
  atCursor: 0,
  virtual: 0,
  monitorSamples: 0,
  released: [] as string[],
  atCursorPaths: [] as string[],
};
let cursorDevice = "\\\\.\\DISPLAY1";
let captureCounter = 0;

function installBridgeApp() {
  (window as unknown as { go: unknown }).go = {
    main: {
      App: {
        async CaptureScreenshotAtCursor() {
          calls.atCursor += 1;
          const n = ++captureCounter;
          // Real captures take time; the delay makes the in-flight guard
          // observable (a poll landing inside a capture must not stack
          // another one).
          await new Promise((resolve) => setTimeout(resolve, 20));
          const path = `.reasonix/attachments/mock-screen-${n}.png`;
          calls.atCursorPaths.push(path);
          return { path, width: 1920, height: 1080, x: 0, y: 0 };
        },
        async CaptureScreenshotVirtualDesktop() {
          calls.virtual += 1;
          const n = ++captureCounter;
          await new Promise((resolve) => setTimeout(resolve, 20));
          return { path: `.reasonix/attachments/mock-virtual-${n}.png`, width: 320, height: 120, x: 0, y: 0 };
        },
        async ScreenshotCursorMonitor() {
          calls.monitorSamples += 1;
          return { device: cursorDevice, x: 0, y: 0, width: 1920, height: 1080 };
        },
        async ScreenshotPreviewDataURL() {
          return "data:image/png;base64,PREVIEW";
        },
        async ReleaseScreenshotCapture(path: string) {
          calls.released.push(path);
        },
        async SaveScreenshotAttachment() {
          return ".reasonix/attachments/mock-screenshot.png";
        },
      },
    },
  };
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
const flush = async () => {
  await act(async () => {
    await sleep(0);
  });
};

async function renderOverlay(root: Root) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <OverlayHarness />
        </ToastProvider>
      </LocaleProvider>,
    );
    await sleep(80); // initial capture: 20 ms bridge delay + microtasks
  });
}

function crossMonitorButton(): HTMLButtonElement | null {
  // The only toolbar action button with aria-pressed is the tl 1088 toggle
  // (the tool buttons use a different class, the other actions carry none).
  return document.querySelector<HTMLButtonElement>("button.screenshot-overlay__action[aria-pressed]");
}

async function main() {
  dom = installDom();
  installBridgeApp();

  console.log("\ntl 1370 cursor-follow capture");
  console.log(`  (poll interval under test: ${CURSOR_FOLLOW_POLL_MS} ms)`);

  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await renderOverlay(root);

  const overlay = () => document.querySelector<HTMLElement>(".screenshot-overlay");

  ok(overlay() !== null, "the overlay opens with a capture in single-monitor mode");
  eq(calls.atCursor, 1, "opening captures the cursor's display exactly once");

  // tl 1370 acceptance: single-display stability. The follow loop polls, but
  // a stable device identity must never trigger a re-capture.
  const samplesAtBaseline = calls.monitorSamples;
  await act(async () => {
    await sleep(CURSOR_FOLLOW_POLL_MS * 1.6);
  });
  eq(calls.atCursor, 1, "a stable cursor monitor never re-captures (single-display zero regression)");
  ok(calls.monitorSamples > samplesAtBaseline, "the follow loop is actually polling while open");

  // tl 1370 acceptance: cross-screen follow. The cursor moved to another
  // display; the picture must be re-captured for that display through the
  // same release→clear→capture chain the toggle uses.
  cursorDevice = "\\\\.\\DISPLAY2";
  await act(async () => {
    await sleep(CURSOR_FOLLOW_POLL_MS + 120);
  });
  eq(calls.atCursor, 2, "a display boundary crossing re-captures the new display");
  ok(calls.released.includes(".reasonix/attachments/mock-screen-1.png"), "the re-capture released the previous display's temp file");
  ok(overlay()?.getAttribute("data-capture-ready") === "ready", "the follow re-capture lands as a ready picture (box-selectable state)");

  // tl 1370 acceptance: back-and-forth crossing must not storm. Cross the
  // boundary repeatedly inside a bounded window; the poll interval plus the
  // in-flight serialisation cap how many re-captures can happen.
  const beforeStorm = calls.atCursor;
  await act(async () => {
    for (const device of ["\\\\.\\DISPLAY1", "\\\\.\\DISPLAY2", "\\\\.\\DISPLAY1", "\\\\.\\DISPLAY2"]) {
      cursorDevice = device;
      await sleep(CURSOR_FOLLOW_POLL_MS * 0.9);
    }
    await sleep(CURSOR_FOLLOW_POLL_MS + 120);
  });
  const stormDelta = calls.atCursor - beforeStorm;
  ok(stormDelta >= 2, `back-and-forth crossings do follow (delta=${stormDelta})`);
  ok(stormDelta <= 5, `back-and-forth crossings are throttled, not a storm (delta=${stormDelta})`);

  // tl 1088 acceptance: the cross-monitor toggle keeps working, and while it
  // is on the follow loop must stand down (the virtual-desktop picture
  // already covers every display).
  const toggle = crossMonitorButton();
  ok(toggle !== null, "the cross-monitor toggle is present");
  const lastSingleDisplayPath = calls.atCursorPaths[calls.atCursorPaths.length - 1];
  await act(async () => {
    toggle?.click();
    await sleep(80);
  });
  eq(calls.virtual, 1, "flipping the toggle captures the virtual desktop (tl 1088 intact)");
  ok(calls.released.includes(lastSingleDisplayPath), "the toggle re-capture released the single-display temp file");
  ok(toggle?.getAttribute("aria-pressed") === "true", "the toggle reports its on state");

  const samplesWhileVirtual = calls.monitorSamples;
  const atCursorWhileVirtual = calls.atCursor;
  cursorDevice = "\\\\.\\DISPLAY1";
  await act(async () => {
    await sleep(CURSOR_FOLLOW_POLL_MS * 2.4);
  });
  eq(calls.monitorSamples, samplesWhileVirtual, "the follow loop stands down while the toggle is on");
  eq(calls.atCursor, atCursorWhileVirtual, "no single-display re-capture happens while the toggle is on");

  // Flipping back re-captures the single display and the follow loop resumes.
  await act(async () => {
    const toggleAgain = crossMonitorButton();
    toggleAgain?.click();
    await sleep(80);
  });
  eq(calls.atCursor, atCursorWhileVirtual + 1, "flipping the toggle off re-captures the cursor's display");
  eq(calls.virtual, 1, "flipping back does not touch the virtual-desktop capture again");

  // Esc closes and the final temp file is released.
  const finalCapturePath = calls.atCursorPaths[calls.atCursorPaths.length - 1];
  await act(async () => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    await sleep(50);
  });
  ok(overlay() === null, "Escape closes the overlay");
  ok(calls.released.includes(finalCapturePath), "closing releases the final capture temp file");

  await act(async () => {
    root.unmount();
  });

  console.log(`\n${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
}

await main();
