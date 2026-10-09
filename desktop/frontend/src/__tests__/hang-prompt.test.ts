// Run: tsx src/__tests__/hang-prompt.test.ts
// Task 663 gap ①/⑥: the hang watch surfaces the shared one-click analysis for
// a stuck session. The surface decision is pure (backend-hung only, visible +
// focused, not already showing, not cooling down from a dismissal), and the
// painted prompt runs the same gates → spend confirm → StartHangAnalysis flow
// as the crash overlay, with an explicit dismiss cooldown.

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Event = dom.window.Event;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

let availability: Record<string, unknown> | null = {
  sourceReady: true,
  ghAuthenticated: true,
  workspaceReady: true,
  ready: true,
};
let hangProbe: Record<string, unknown> | null = { sessionPath: "…", verdict: "silent", detail: "ledger silent", hung: true, ready: true };
let hangStartCalls = 0;

function installBindings() {
  (window as unknown as { go?: unknown }).go = {
    main: {
      App: {
        CrashAnalysisAvailability: async () => (availability ? { ...availability } : Promise.reject(new Error("probe failed"))),
        HangAnalysisAvailability: async () => (hangProbe ? { ...hangProbe } : Promise.reject(new Error("probe failed"))),
        StartHangAnalysis: async () => {
          hangStartCalls += 1;
          return "YOLO 卡顿分析会话已启动";
        },
      },
    },
  };
}

const { shouldSurfaceHangPrompt, paintHangPromptForTest, hangPromptDismissedRecently, markHangPromptDismissedForTest, resetHangWatchForTest } =
  await import("../lib/hangPrompt");

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

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const hungReport = { sessionPath: "…", verdict: "silent", detail: "ledger silent 8m", hung: true, ready: true };

console.log("\nhang analysis entry (task 663 gap ①/⑥)");

// Pure surface decision: only a backend-hung report on a visible, focused,
// not-already-showing, not-cooling-down window surfaces.
ok(shouldSurfaceHangPrompt(hungReport, { hidden: false, unfocused: false, alreadyShowing: false, dismissedRecently: false }) === true, "a hung verdict surfaces the prompt");
ok(shouldSurfaceHangPrompt({ ...hungReport, hung: false }, { hidden: false, unfocused: false, alreadyShowing: false, dismissedRecently: false }) === false, "a not-hung verdict never surfaces");
ok(shouldSurfaceHangPrompt({ ...hungReport, ready: false }, { hidden: false, unfocused: false, alreadyShowing: false, dismissedRecently: false }) === false, "a not-ready probe never surfaces");
ok(shouldSurfaceHangPrompt(null, { hidden: false, unfocused: false, alreadyShowing: false, dismissedRecently: false }) === false, "a null probe never surfaces");
ok(shouldSurfaceHangPrompt(hungReport, { hidden: true, unfocused: false, alreadyShowing: false, dismissedRecently: false }) === false, "a hidden window never surfaces");
ok(shouldSurfaceHangPrompt(hungReport, { hidden: false, unfocused: true, alreadyShowing: false, dismissedRecently: false }) === false, "an unfocused window never surfaces");
ok(shouldSurfaceHangPrompt(hungReport, { hidden: false, unfocused: false, alreadyShowing: true, dismissedRecently: false }) === false, "an already-showing prompt does not re-surface");
ok(shouldSurfaceHangPrompt(hungReport, { hidden: false, unfocused: false, alreadyShowing: false, dismissedRecently: true }) === false, "a cooled-down dismissal does not re-surface");

// Painted prompt: the analyze button runs the shared gates → spend confirm →
// StartHangAnalysis chain (no payload argument — the backend measures the
// active session itself).
installBindings();
paintHangPromptForTest(hungReport as never);
let host = document.getElementById("hang-analysis-prompt");
ok(host !== null, "the painted prompt mounts its host");
ok(host?.querySelector(".hang-analysis__body")?.textContent?.includes("verdict: silent") === true, "the prompt names the verdict");

const analyze = host?.querySelector(".hang-analysis__analyze") as HTMLButtonElement;
analyze.click();
await tick();
await tick();
ok((host?.querySelector(".hang-analysis__note")?.textContent ?? "").includes("consumes token quota"), "the hang entry reaches the shared spend confirmation");
ok(hangStartCalls === 0, "the confirmation alone does not start the hang analysis");

const go = host?.querySelector(".hang-analysis__note button") as HTMLButtonElement;
go.click();
await tick();
await tick();
ok(hangStartCalls === 1, "confirming starts exactly one hang analysis");
ok((host?.querySelector(".hang-analysis__note")?.textContent ?? "").includes("YOLO 卡顿分析会话已启动"), "the note reports the started hang analysis");

// Dismiss removes the prompt and starts the cooldown.
const dismiss = host?.querySelector(".hang-analysis__dismiss") as HTMLButtonElement;
const before = Date.now();
dismiss.click();
ok(document.getElementById("hang-analysis-prompt") === null, "dismiss removes the prompt");
ok(hangPromptDismissedRecently(before), "dismiss starts the cooldown");

markHangPromptDismissedForTest(0);
ok(!hangPromptDismissedRecently(Date.now()), "an old cooldown expiry re-arms the watch");

// Failed availability probe: no prompt, no throw.
hangProbe = null;
resetHangWatchForTest();
host = document.getElementById("hang-analysis-prompt");
ok(host === null, "no prompt is painted without a hung report");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
