// Run: tsx src/__tests__/pending-crash-entry.test.ts
// Task 663 gap ④: a real crash (Go panic) leaves a pending queue file for the
// next launch; the startup entry paints that boot snapshot as an explicit
// banner with the shared one-click analysis flow, instead of the old silent
// ship-or-drop. Dismissal persists for the current run (sessionStorage).

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Event = dom.window.Event;
globalThis.sessionStorage = dom.window.sessionStorage;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

const newestPayload = JSON.stringify({
  schemaVersion: 2,
  kind: "crash",
  source: "go",
  label: "scheduler.tick",
  message: "[go panic] scheduler.tick\n\npanic: runtime error",
  errorType: "runtime error",
  errorMessage: "Go panic captured at scheduler.tick.",
});

let snapshot: Record<string, unknown> | null = { count: 1, reports: [newestPayload] };
let startCalls: Array<[string, string]> = [];

function installBindings() {
  (window as unknown as { go?: unknown }).go = {
    main: {
      App: {
        CrashAnalysisAvailability: async () => ({
          sourceReady: true,
          ghAuthenticated: true,
          workspaceReady: true,
          ready: true,
        }),
        PendingCrashSnapshot: async () => (snapshot ? { ...snapshot } : Promise.reject(new Error("probe failed"))),
        StartCrashAnalysis: async (kind: string, detail: string) => {
          startCalls.push([kind, detail]);
          return "YOLO 分析会话已启动";
        },
      },
    },
  };
}

const {
  installPendingCrashAnalysisEntry,
  shouldSurfacePendingCrash,
  pendingCrashPreview,
  pendingCrashDismissedThisRun,
  resetPendingCrashEntryForTest,
} = await import("../lib/pendingCrashEntry");

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

console.log("\npending-crash startup entry (task 663 gap ④)");

// Pure surface decision.
ok(shouldSurfacePendingCrash({ count: 1, reports: [newestPayload] }, { dismissed: false }) === true, "a non-empty snapshot surfaces the banner");
ok(shouldSurfacePendingCrash({ count: 0 }, { dismissed: false }) === false, "an empty queue never surfaces");
ok(shouldSurfacePendingCrash({ count: 1, reports: [newestPayload] }, { dismissed: true }) === false, "a dismissed banner stays down for the run");
ok(shouldSurfacePendingCrash(null, { dismissed: false }) === false, "a null snapshot never surfaces");

// Preview: the newest payload's error line, clipped.
ok(pendingCrashPreview({ count: 1, reports: [newestPayload] }).includes("Go panic captured at scheduler.tick."), "the preview names the panic");
ok(
  pendingCrashPreview({ count: 1, reports: [JSON.stringify({ message: "x".repeat(400) })] }).length === 241,
  "the preview clips long messages",
);
ok(pendingCrashPreview({ count: 1 }) === "", "a snapshot without reports previews empty");

// Install flow: probe → banner → shared gates → StartCrashAnalysis("crash", payload).
installBindings();
resetPendingCrashEntryForTest();
installPendingCrashAnalysisEntry();
await tick();
await tick();
let host = document.getElementById("pending-crash-entry");
ok(host !== null, "the banner mounts at startup");
ok(host?.querySelector(".pending-crash__title")?.textContent?.includes("（1）") === true, "the banner names the report count");
ok(host?.querySelector(".pending-crash__body")?.textContent?.includes("scheduler.tick") === true, "the banner previews the newest payload");

const analyze = host?.querySelector(".pending-crash__analyze") as HTMLButtonElement;
analyze.click();
await tick();
await tick();
ok((host?.querySelector(".pending-crash__note")?.textContent ?? "").includes("consumes token quota"), "the entry reaches the shared spend confirmation");
ok(startCalls.length === 0, "the confirmation alone does not start the analysis");

const go = host?.querySelector(".pending-crash__note button") as HTMLButtonElement;
go.click();
await tick();
await tick();
ok(startCalls.length === 1, "confirming starts exactly one analysis");
ok(startCalls[0]?.[0] === "crash", "the started analysis carries the crash kind");
ok(startCalls[0]?.[1] === newestPayload, "the queued payload travels to StartCrashAnalysis verbatim");

// Dismiss persists for the run: a second install does not re-paint.
const dismiss = host?.querySelector(".pending-crash__dismiss") as HTMLButtonElement;
dismiss.click();
ok(document.getElementById("pending-crash-entry") === null, "dismiss removes the banner");
ok(pendingCrashDismissedThisRun(), "dismiss persists in sessionStorage");
installPendingCrashAnalysisEntry();
await tick();
ok(document.getElementById("pending-crash-entry") === null, "a dismissed run does not re-paint");

// No queue: no banner.
snapshot = { count: 0 };
resetPendingCrashEntryForTest();
installPendingCrashAnalysisEntry();
await tick();
ok(document.getElementById("pending-crash-entry") === null, "an empty queue paints nothing");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
