// Run: tsx src/__tests__/crash-analyze.test.ts
// Task 617 route B: the analyze button runs the three prerequisite gates in
// order, shows one distinct notice per failure (and never starts), and only
// starts the YOLO session after the explicit spend confirmation.
// Task 663: a started analysis switches to the live progress face (running →
// done via CrashAnalysisProgress) and carries the restart button (③), whose
// click paints an explicit confirm before RestartDesktop fires.

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Event = dom.window.Event;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

type Availability = {
  sourceReady: boolean;
  ghAuthenticated: boolean;
  workspaceReady: boolean;
  ready: boolean;
  ghCheckDetail?: string;
};

const allReady: Availability = { sourceReady: true, ghAuthenticated: true, workspaceReady: true, ready: true };
let availability: Availability | null = { ...allReady };
let startCalls: string[] = [];
let restartCalls = 0;
// Task 663 ②: the progress read model the poller folds into the note.
let progressState: { active: boolean; running: boolean; done: boolean } = { active: false, running: false, done: false };

function installAnalyzeBindings() {
  (window as unknown as { go?: unknown }).go = {
    main: {
      App: {
        ReportCrash: async () => {},
        CrashAnalysisAvailability: async () => (availability ? { ...availability } : Promise.reject(new Error("probe failed"))),
        StartCrashAnalysis: async (kind: string, detail: string) => {
          startCalls.push(JSON.stringify({ kind, detail }));
          return "YOLO analysis session started";
        },
        CrashAnalysisProgress: async () => ({ ...progressState }),
        RestartDesktop: async () => {
          restartCalls += 1;
        },
      },
    },
  };
}

function clearBindings() {
  delete (window as unknown as { go?: unknown }).go;
}

const { reportCrash } = await import("../lib/crash");

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

function paintOverlay(): HTMLElement {
  const error = new TypeError("renderer fault");
  error.stack = "TypeError: renderer fault\n    at render (src/App.tsx:12:3)";
  reportCrash("react", error);
  return document.getElementById("crash-overlay") as HTMLElement;
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

async function clickAnalyze(overlay: HTMLElement) {
  const analyze = overlay.querySelector(".crash-overlay__analyze") as HTMLButtonElement;
  analyze.click();
  await tick();
  await tick();
}

const note = (overlay: HTMLElement) => overlay.querySelector(".crash-overlay__analysis")?.textContent ?? "";

console.log("\ncrash one-click analyze (task 617 route B)");

installAnalyzeBindings();
let overlay = paintOverlay();
ok(Boolean(overlay.querySelector(".crash-overlay__analyze")), "overlay offers the analyze button when bindings exist");

// Prerequisite 1: no local source — B must not start.
availability = { ...allReady, sourceReady: false, ready: false };
await clickAnalyze(overlay);
ok(note(overlay).includes("No local source detected"), "missing source shows the source notice");
ok(startCalls.length === 0, "missing source does not start the analysis");

// Prerequisite 3: gh not authenticated — B must not start.
availability = { ...allReady, ghAuthenticated: false, ready: false };
await clickAnalyze(overlay);
ok(note(overlay).includes("No authenticated GitHub CLI detected"), "gh not authenticated shows the auth notice");
ok(startCalls.length === 0, "gh not authenticated does not start the analysis");

// Task 643: the backend GhCheckDetail must surface verbatim so a not-found
// (stale process PATH) is distinguishable from a real auth failure.
availability = {
  ...allReady,
  ghAuthenticated: false,
  ready: false,
  ghCheckDetail: "gh CLI not found on PATH or in known install locations",
};
await clickAnalyze(overlay);
ok(note(overlay).includes("No authenticated GitHub CLI detected"), "generic auth notice still leads when detail exists");
ok(note(overlay).includes("gh CLI not found on PATH or in known install locations"), "gh check detail is surfaced under the notice");
ok(startCalls.length === 0, "gh not found does not start the analysis");

// Task 643: gh found via a fallback location but auth OK — analyze stays
// available (the analysis session runs gh from the user's shell, whose PATH
// is fine); only the spend confirmation may gate it.
availability = {
  ...allReady,
  ghAuthenticated: true,
  ready: true,
  ghCheckDetail: "gh found outside PATH at C:\\Program Files\\GitHub CLI\\gh.exe (this process inherited an outdated PATH); auth OK",
};
await clickAnalyze(overlay);
ok(note(overlay).includes("consumes token quota"), "fallback gh discovery still reaches the spend confirmation");
ok(startCalls.length === 0, "fallback discovery alone does not start the analysis");

// Workspace not ready — task 687: no longer a gate. The analysis self-hosts in
// a fresh Global tab (task 672), so route B must reach the spend confirmation
// with nothing open / no project expanded (the 2026-10-09 17:55 refusal).
availability = { ...allReady, workspaceReady: false, ready: false };
await clickAnalyze(overlay);
ok(note(overlay).includes("consumes token quota"), "no live workspace still reaches the spend confirmation (task 687)");
ok(startCalls.length === 0, "no live workspace alone does not start the analysis");

// A failed availability probe (backend threw / binding vanished) still stops
// route B with the workspace notice — the only remaining consumer of that face.
availability = null;
await clickAnalyze(overlay);
ok(note(overlay).includes("No live session"), "a failed probe shows the fallback notice");
ok(startCalls.length === 0, "a failed probe does not start the analysis");

// All prerequisites pass — the spend confirmation (prerequisite 2) gates the
// actual start.
availability = { ...allReady };
// Task 663 ②: speed the progress poller up (10ms) so the running→done fold is
// observable without waiting the production 3s cadence.
const realSetInterval = window.setInterval.bind(window);
(window as unknown as { setInterval: typeof window.setInterval }).setInterval = ((fn: () => void) =>
  realSetInterval(fn, 10)) as typeof window.setInterval;
await clickAnalyze(overlay);
ok(note(overlay).includes("consumes token quota"), "spend confirmation is shown before starting");
ok(startCalls.length === 0, "confirmation alone does not start the analysis");

const goButton = overlay.querySelector(".crash-overlay__analysis button") as HTMLButtonElement;
goButton.click();
await tick();
await tick();
ok(startCalls.length === 1, "confirming starts exactly one analysis session");
ok(startCalls[0]?.includes('"kind":"crash"'), "the started session carries the diagnostic kind");
ok(startCalls[0]?.includes("renderer fault"), "the started session carries the diagnostic payload");
ok(note(overlay).includes("YOLO analysis session started"), "the note reports the started session");

// Task 663 ②: the note switches to the live progress face instead of a
// one-shot line — running while the backend says running…
progressState = { active: true, running: true, done: false };
await new Promise((resolve) => setTimeout(resolve, 40));
ok(note(overlay).includes("Analysis running"), "the note polls into the running face");

// …and folds to done when the backend run finishes (turn settled).
progressState = { active: true, running: false, done: true };
await new Promise((resolve) => setTimeout(resolve, 40));
ok(note(overlay).includes("Analysis finished"), "the note folds to the done face");

// Task 663 ③: the restart button rides the progress face; a click paints the
// explicit confirm, and only the confirm fires RestartDesktop.
const restart = overlay.querySelector(".crash-overlay__analysis button") as HTMLButtonElement;
ok(restart?.textContent === "Restart Reasonix", "the progress face carries the restart button");
restart.click();
await tick();
ok(note(overlay).includes("auto-resume"), "restart click paints the explicit confirm first");
ok(restartCalls === 0, "the confirm alone does not restart");
const restartGo = overlay.querySelector(".crash-overlay__analysis button") as HTMLButtonElement;
restartGo.click();
await tick();
await tick();
ok(restartCalls === 1, "confirming the restart fires exactly one RestartDesktop");
ok(note(overlay).includes("Restarting Reasonix"), "the note reports the restart in flight");

overlay.remove();
clearBindings();

// Hard constraint: without any bindings the overlay must still paint with the
// copy path intact (and no analyze button).
overlay = paintOverlay();
ok(overlay.querySelector(".crash-overlay__analyze") === null, "no analyze button without bindings");
ok(Boolean(overlay.querySelector(".crash-overlay__copy")), "copy survives without bindings");
overlay.remove();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
