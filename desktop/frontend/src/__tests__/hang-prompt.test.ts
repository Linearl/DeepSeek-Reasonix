// Run: tsx src/__tests__/hang-prompt.test.ts
// Task 663 gap ①/⑥: the hang watch surfaces the shared one-click analysis for
// a stuck session. The surface decision is pure (backend-hung only, visible +
// focused, not already showing, not cooling down from a dismissal), and the
// painted prompt runs the same gates → StartHangAnalysis flow as the crash
// overlay (task 674: straight through, no spend confirmation), with an
// explicit dismiss cooldown. Task 769: the prompt also carries the「复制」
// escape hatch (copyButton + issue skeleton) its failure note names.

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

// Painted prompt: the analyze button runs the shared gates → StartHangAnalysis
// chain (task 674 semantics: no spend confirmation and no source gate — the
// same straight-through flow as the crash/performance prompts; no payload
// argument — the backend measures the active session itself).
installBindings();
paintHangPromptForTest(hungReport as never);
let host = document.getElementById("hang-analysis-prompt");
ok(host !== null, "the painted prompt mounts its host");
ok(host?.querySelector(".hang-analysis__body")?.textContent?.includes("verdict: silent") === true, "the prompt names the verdict");

// Task 769: the analyzeFailed note tells the user to fall back to「复制」, so
// the prompt must paint the copy button unconditionally (674 three-slot layout).
const copy = host?.querySelector(".hang-analysis__copy") as HTMLButtonElement;
ok(copy !== null && copy.textContent === "Copy", "the prompt paints the copy button the failure note names");

const analyze = host?.querySelector(".hang-analysis__analyze") as HTMLButtonElement;
analyze.click();
await tick();
await tick();
ok(hangStartCalls === 1, "a gated-and-ready backend starts the hang analysis straight away (674: no spend gate)");
ok((host?.querySelector(".hang-analysis__note")?.textContent ?? "").includes("YOLO 卡顿分析会话已启动"), "the note reports the started hang analysis");

// Task 769: copy puts the issue skeleton on the clipboard — the zero-dependency
// feedback path the failure note points at, with the hang payload's own markers.
let clipboardText = "";
(window as unknown as { runtime: Record<string, unknown> }).runtime = {
  ClipboardSetText: async (value: string) => {
    clipboardText = value;
    return true;
  },
  BrowserOpenURL: () => {},
};
copy.click();
await tick();
await tick();
ok(clipboardText.includes("Issue target:") && clipboardText.includes("session.hang") && clipboardText.includes("doctor.responsiveness:silent"), "copy puts the hang issue skeleton on the clipboard");
ok(clipboardText.includes("verdict: silent") && clipboardText.includes("ledger silent 8m"), "the skeleton carries the hang verdict and detail");

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
