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
globalThis.MutationObserver = dom.window.MutationObserver;
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
let clearCalls = 0;
// When true, the clear binding rejects — used to pin the failure path (button
// stays enabled, the reason surfaces in the note, banner stays up).
let clearRejects = false;

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
        // Task 1051: 「清空」binding — returns the dropped count like the Go side.
        ClearPendingCrashReports: async () => {
          clearCalls += 1;
          if (clearRejects) throw new Error("clear failed: disk locked");
          snapshot = { count: 0 };
          return 1;
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
ok(
  (host?.querySelector(".pending-crash__body")?.previousElementSibling?.textContent ?? "").includes(
    "Panic summary captured by the previous run (kept verbatim)",
  ),
  "the verbatim panic block carries a caption line (task 695)",
);

const analyze = host?.querySelector(".pending-crash__analyze") as HTMLButtonElement;
// Task 1051: the banner now renders three action buttons — 查看 / 清空 / 关闭 —
// with 「清空」 immediately right of 「关闭」's left neighbour, i.e. the middle slot
// of [查看, 清空, 关闭].
const clearBtn = host?.querySelector(".pending-crash__clear") as HTMLButtonElement;
const dismissBtn = host?.querySelector(".pending-crash__dismiss") as HTMLButtonElement;
ok(Boolean(analyze && clearBtn && dismissBtn), "the banner renders all three action buttons (查看/清空/关闭)");
ok(
  clearBtn.previousElementSibling === analyze && dismissBtn.previousElementSibling === clearBtn,
  "「清空」 sits between 查看 and 关闭 (right of nothing but preceding 关闭)",
);
ok(clearBtn.textContent === "Clear all", "the clear button carries the en locale copy");

analyze.click();
// The availability probe and the spend-confirmation are both async; the click
// handler awaits two ticks before the confirm button lands in the note.
await tick();
await tick();
await tick();
await tick();
ok((host?.querySelector(".pending-crash__note")?.textContent ?? "").includes("consumes token quota"), "the entry reaches the shared spend confirmation");
ok(startCalls.length === 0, "the confirmation alone does not start the analysis");

// NOTE (pre-existing red, NOT task 1051): this block asserts a spend-confirmation
// step that task 674 removed from analyzeEntryButton (the copy "consumes token
// quota" is gone from en.ts). Verified against a clean HEAD checkout of this same
// file — the two assertions below fail there too. Guarded with ?. so the crash
// does not halt the rest of the suite (the clear-path checks further down run
// regardless).
const go = host?.querySelector(".pending-crash__note button") as HTMLButtonElement | null;
go?.click();
await tick();
await tick();
ok(startCalls.length === 1, "confirming starts exactly one analysis (pre-existing red)");
ok(startCalls[0]?.[0] === "crash", "the started analysis carries the crash kind (pre-existing red)");
ok(startCalls[0]?.[1] === newestPayload, "the queued payload travels to StartCrashAnalysis verbatim (pre-existing red)");

// Task 694: the banner paints before LocaleProvider settles the locale at boot
// (imperative DOM, painted once), so it must re-render when <html lang> flips.
// zh rides the real lazy-loaded dictionary via preloadLocale — the same path
// the provider uses — so this exercises the exact boot race end to end.
const { preloadLocale } = await import("../lib/i18n");
await preloadLocale("zh");
document.documentElement.lang = "zh-CN";
await tick();
await tick();
host = document.getElementById("pending-crash-entry");
ok(
  host?.querySelector(".pending-crash__title")?.textContent === "检测到上次运行遗留的崩溃报告（1）",
  "a locale flip re-renders the banner title in the settled locale",
);
ok(
  host?.querySelector(".pending-crash__dismiss")?.textContent === "关闭",
  "the dismiss button re-renders localized instead of freezing in English",
);
ok(
  host?.querySelector(".pending-crash__clear")?.textContent === "清空",
  "the clear button re-renders localized (zh) too",
);
ok(
  (host?.querySelector(".pending-crash__body")?.previousElementSibling?.textContent ?? "").includes("技术原文保留"),
  "the panic caption re-renders localized while the verbatim block stays original (task 695)",
);
ok(
  host?.querySelector(".pending-crash__body")?.textContent?.includes("scheduler.tick") === true,
  "the panic preview itself stays verbatim across a locale flip",
);

// Dismiss persists for the run: a second install does not re-paint.
const dismiss = host?.querySelector(".pending-crash__dismiss") as HTMLButtonElement;
dismiss.click();
ok(document.getElementById("pending-crash-entry") === null, "dismiss removes the banner");
ok(pendingCrashDismissedThisRun(), "dismiss persists in sessionStorage");
document.documentElement.lang = "zh-TW";
await tick();
ok(document.getElementById("pending-crash-entry") === null, "a locale flip does not resurrect a dismissed banner");
installPendingCrashAnalysisEntry();
await tick();
ok(document.getElementById("pending-crash-entry") === null, "a dismissed run does not re-paint");

// No queue: no banner.
snapshot = { count: 0 };
resetPendingCrashEntryForTest();
installPendingCrashAnalysisEntry();
await tick();
ok(document.getElementById("pending-crash-entry") === null, "an empty queue paints nothing");

// Task 1051: the clear path — a failure keeps the banner up and the button live,
// then a success wipes the queue and closes with the same tail as dismiss.
resetPendingCrashEntryForTest();
snapshot = { count: 1, reports: [newestPayload] };
clearCalls = 0;
installPendingCrashAnalysisEntry();
await tick();
await tick();
ok(document.getElementById("pending-crash-entry") !== null, "the banner paints for the clear-path scenario");

clearRejects = true;
const clearFail = document.querySelector(".pending-crash__clear") as HTMLButtonElement;
clearFail.click();
await tick();
await tick();
ok(clearCalls === 1, "the clear button calls ClearPendingCrashReports exactly once per click");
ok(document.getElementById("pending-crash-entry") !== null, "a failed clear keeps the banner up");
ok(clearFail.disabled === false, "a failed clear re-enables the button for a retry");
ok(
  (document.querySelector(".pending-crash__note")?.textContent ?? "").includes("disk locked"),
  "a failed clear surfaces the backend reason in the note",
);

clearRejects = false;
const clearOkBtn = document.querySelector(".pending-crash__clear") as HTMLButtonElement;
clearOkBtn.click();
await tick();
await tick();
ok(clearCalls === 2, "the retry calls the binding again");
ok(document.getElementById("pending-crash-entry") === null, "a successful clear removes the banner");
ok(pendingCrashDismissedThisRun(), "a successful clear persists the dismissed marker (no repaint this run)");
installPendingCrashAnalysisEntry();
await tick();
ok(document.getElementById("pending-crash-entry") === null, "a cleared run does not re-paint");

// Task 1051: no clear binding (older backend) — the button still renders but
// says so instead of throwing into the boot path.
resetPendingCrashEntryForTest();
snapshot = { count: 1, reports: [newestPayload] };
installBindings();
delete ((window as unknown as { go?: { main?: { App?: Record<string, unknown> } } }).go?.main?.App as Record<string, unknown>).ClearPendingCrashReports;
installPendingCrashAnalysisEntry();
await tick();
await tick();
const noBindBtn = document.querySelector(".pending-crash__clear") as HTMLButtonElement;
ok(Boolean(noBindBtn), "the clear button renders even without the backend binding");
noBindBtn?.click();
await tick();
ok(
  (document.querySelector(".pending-crash__note")?.textContent ?? "").includes("not available in this version"),
  "a missing binding surfaces the 'not available' copy instead of throwing",
);
ok(document.getElementById("pending-crash-entry") !== null, "a missing binding leaves the banner up");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
