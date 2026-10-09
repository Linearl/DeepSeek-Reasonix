// Run: tsx src/__tests__/crash-analyze.test.ts
// Task 617 route B, reshaped by task 674: the analyze button runs the
// prerequisite gates (gh auth / live workspace) and — per the 674 interaction
// spec — starts the analysis immediately when they pass, with no second
// confirmation click. A missing source checkout is no longer a gate either:
// the backend clones the fork repo automatically, so the frontend goes
// straight to start and only surfaces the failure notice if start itself
// fails. Each hard failure still shows one distinct notice and never starts.
// Task 663 (kept under the 674 semantics): a started analysis switches to the
// live progress face (running → done via CrashAnalysisProgress) and carries
// the restart button (③), whose click paints an explicit confirm before
// RestartDesktop fires.

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
  await tick();
}

const note = (overlay: HTMLElement) => overlay.querySelector(".crash-overlay__analysis")?.textContent ?? "";

console.log("\ncrash one-click analyze (task 617 route B, task 674 no-confirm shape)");

installAnalyzeBindings();
let overlay = paintOverlay();
ok(Boolean(overlay.querySelector(".crash-overlay__analyze")), "overlay offers the analyze button when bindings exist");
// Task 674: the real face has no send button at all, and analyze takes the
// first slot (where the unusable send used to sit).
ok(overlay.querySelector(".crash-overlay__send") === null, "real overlay has no send button (task 674)");
const analyzeBtn = overlay.querySelector(".crash-overlay__analyze") as HTMLButtonElement;
const actions = overlay.querySelector(".crash-overlay__actions") as HTMLElement;
ok(actions.firstElementChild === analyzeBtn, "analyze is the first action button");
// Task 674: copy deep-links the issue tracker after a successful copy.
const copyBtn = overlay.querySelector(".crash-overlay__copy") as HTMLButtonElement;
ok(typeof copyBtn.title === "string" && copyBtn.title.length > 0, "copy button carries the opens-issue-page hint");

// Task 674: a missing source checkout goes straight to start — the backend
// clones the fork repo; the frontend only announces it.
availability = { ...allReady, sourceReady: false, ready: false };
await clickAnalyze(overlay);
ok(startCalls.length === 1, "missing source still starts exactly one analysis (backend auto-clones)");
ok(note(overlay).includes("YOLO analysis session started"), "the started summary lands in the note");

// Prerequisite: gh not authenticated — B must not start.
startCalls = [];
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
// is fine); task 674 removed the confirmation, so it starts directly.
availability = {
  ...allReady,
  ghAuthenticated: true,
  ready: true,
  ghCheckDetail: "gh found outside PATH at C:\\Program Files\\GitHub CLI\\gh.exe (this process inherited an outdated PATH); auth OK",
};
startCalls = [];
await clickAnalyze(overlay);
ok(startCalls.length === 1, "fallback gh discovery starts the analysis without a second click");
ok(startCalls[0]?.includes('"kind":"crash"'), "the started session carries the diagnostic kind");
ok(startCalls[0]?.includes("renderer fault"), "the started session carries the diagnostic payload");
ok(note(overlay).includes("YOLO analysis session started"), "the note reports the started session");

// Workspace not ready — B must not start.
startCalls = [];
availability = { ...allReady, workspaceReady: false, ready: false };
await clickAnalyze(overlay);
ok(note(overlay).includes("No live session"), "no live workspace shows the workspace notice");
ok(startCalls.length === 0, "no live workspace does not start the analysis");

// A failed start paints the failure notice and re-arms the button. The button
// resolves its bindings at creation time, so the throwing binding must be in
// place before the overlay is painted.
availability = { ...allReady };
// Task 663 ②: speed the progress poller up (10ms) so the running→done fold is
// observable without waiting the production 3s cadence.
const realSetInterval = window.setInterval.bind(window);
(window as unknown as { setInterval: typeof window.setInterval }).setInterval = ((fn: () => void) =>
  realSetInterval(fn, 10)) as typeof window.setInterval;
overlay.remove();
(window as unknown as { go: { main: { App: { StartCrashAnalysis: () => Promise<string> } } } }).go.main.App.StartCrashAnalysis =
  async () => {
    throw new Error("clone failed: no network");
  };
overlay = paintOverlay();
await clickAnalyze(overlay);
ok(note(overlay).includes("Failed to start the analysis"), "a failed start paints the failure notice");
ok(note(overlay).includes("clone failed: no network"), "the backend error detail is surfaced verbatim");
ok(!(overlay.querySelector(".crash-overlay__analyze") as HTMLButtonElement).disabled, "the analyze button re-arms after a failure");

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
