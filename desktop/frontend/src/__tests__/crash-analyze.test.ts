// Run: tsx src/__tests__/crash-analyze.test.ts
// Task 617 route B: the analyze button runs the three prerequisite gates in
// order, shows one distinct notice per failure (and never starts), and only
// starts the YOLO session after the explicit spend confirmation.

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

// Workspace not ready — B must not start.
availability = { ...allReady, workspaceReady: false, ready: false };
await clickAnalyze(overlay);
ok(note(overlay).includes("No live session"), "no live workspace shows the workspace notice");
ok(startCalls.length === 0, "no live workspace does not start the analysis");

// All prerequisites pass — the spend confirmation (prerequisite 2) gates the
// actual start.
availability = { ...allReady };
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
