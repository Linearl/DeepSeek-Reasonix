// Run: tsx src/__tests__/crash-collapse.test.ts
// Task 674: the last-resort faces collapse to a capsule so an in-flight
// analysis never blocks ongoing work. The contract pinned here:
//   - both faces (crash overlay, performance prompt) carry the toggle;
//   - collapsing is a CSS-class toggle only — every child stays in the DOM,
//     so the expanded content is byte-identical before/after a round trip
//     (an in-flight analysis note survives collapsed);
//   - the real faces have no send button, and the perf prompt reads
//     [一键分析][复制][关闭] with analyze in the old send slot.

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Event = dom.window.Event;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

const { reportCrash, paintPerformancePromptForTest, buildCrashPayload } = await import("../lib/crash");

let passed = 0;
let failed = 0;

function ok(cond: unknown, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

// The perf face only mounts the analyze button when the bindings exist, so the
// layout assertions below need them installed.
(window as unknown as { go?: unknown }).go = {
  main: {
    App: {
      ReportMockCrash: async () => "uploaded",
      CrashAnalysisAvailability: async () => ({ sourceReady: true, ghAuthenticated: true, workspaceReady: true, ready: true }),
      StartCrashAnalysis: async () => "YOLO analysis session started",
    },
  },
};

function toggleOf(host: HTMLElement): HTMLButtonElement {
  return host.querySelector(".crash-collapse-toggle") as HTMLButtonElement;
}

// The toggle's own caption flips between collapse/expand labels — that is the
// control, not content. Compare everything except the toggle so the assertion
// pins "no face content was unmounted".
function contentWithoutToggle(host: HTMLElement): string {
  const clone = host.cloneNode(true) as HTMLElement;
  clone.querySelector(".crash-collapse-toggle")?.remove();
  return clone.textContent ?? "";
}

function roundTrip(host: HTMLElement, label: string) {
  const before = contentWithoutToggle(host);
  const toggle = toggleOf(host);
  ok(toggle.getAttribute("aria-expanded") === "true", `${label}: toggle starts expanded`);
  toggle.click();
  ok(host.classList.contains("crash-face--collapsed"), `${label}: collapse toggles the class`);
  ok(contentWithoutToggle(host) === before, `${label}: content survives collapsed (nothing unmounted)`);
  ok(toggle.getAttribute("aria-expanded") === "false", `${label}: aria tracks the collapsed state`);
  toggle.click();
  ok(!host.classList.contains("crash-face--collapsed"), `${label}: expand removes the class`);
  ok(contentWithoutToggle(host) === before, `${label}: round trip restores identical content`);
}

console.log("\ncrash overlay collapse round trip (task 674)");

{
  const error = new TypeError("renderer fault");
  error.stack = "TypeError: renderer fault\n    at render (src/App.tsx:12:3)";
  reportCrash("react", error);
  const host = document.getElementById("crash-overlay") as HTMLElement;
  ok(Boolean(toggleOf(host)), "overlay carries the collapse toggle");
  roundTrip(host, "overlay");
  host.remove();
}

console.log("\nperformance prompt layout + collapse round trip (task 674)");

{
  const snapshot = {
    reason: "event-loop-lag",
    uptimeMs: 120_000,
    visibility: "visible",
    focused: true,
    online: true,
  };
  // Minimal payload via the real builder so the skeleton path stays honest.
  const payload = buildCrashPayload("performance", new TypeError("jank"), "");
  const host = paintPerformancePromptForTest(payload, snapshot);
  ok(host.querySelectorAll(".crash-overlay__send, .performance-report__send").length === 0, "real perf face has no send button");
  const actions = host.querySelector(".performance-report__actions") as HTMLElement;
  const analyze = actions.querySelector(".performance-report__analyze");
  const copy = actions.querySelector(".performance-report__copy");
  const dismiss = actions.querySelector(".performance-report__dismiss");
  ok(Boolean(analyze && copy && dismiss), "perf face keeps analyze/copy/dismiss");
  ok(analyze !== null && actions.firstElementChild === analyze, "analyze sits in the old send slot (first action)");
  ok(Boolean(toggleOf(host)), "perf face carries the collapse toggle");

  // An in-flight analysis note must survive a collapse.
  const analysisNote = host.querySelector(".performance-report__analysis") as HTMLElement;
  analysisNote.textContent = "已启动 YOLO 分析会话 —— 分析完成后 issue 链接会出现在该会话中。";
  roundTrip(host, "perf");
  ok((host.querySelector(".performance-report__analysis") as HTMLElement).textContent?.includes("YOLO"), "analysis note intact after the round trip");
  host.remove();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
