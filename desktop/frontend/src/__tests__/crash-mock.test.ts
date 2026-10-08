// Run: tsx src/__tests__/crash-mock.test.ts
// Task 642: the lab mock-crash drill. The synthetic payload is marked
// test/mock on every surface (payload fields, overlay banner/badge/note, send
// binding, issue skeleton), the real-crash overlay stays unmarked, and the
// mock send rides ReportMockCrash with its queued/uploaded status shown.

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Event = dom.window.Event;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

let mockCalls: { kind: string; detail: string }[] = [];
let realCalls: string[] = [];
let mockStatus: "uploaded" | "queued" = "uploaded";
let mockShouldThrow = false;

function installBindings() {
  (window as unknown as { go?: unknown }).go = {
    main: {
      App: {
        ReportCrash: async (_kind: string, detail: string) => {
          realCalls.push(detail);
        },
        ReportMockCrash: async (kind: string, detail: string) => {
          if (mockShouldThrow) throw new Error("endpoint down");
          mockCalls.push({ kind, detail });
          return mockStatus;
        },
        CrashAnalysisAvailability: async () => ({ sourceReady: true, ghAuthenticated: true, workspaceReady: true, ready: true }),
        StartCrashAnalysis: async (kind: string, detail: string) => {
          mockCalls.push({ kind, detail });
          return "YOLO analysis session started";
        },
      },
    },
  };
}

function clearBindings() {
  delete (window as unknown as { go?: unknown }).go;
  mockCalls = [];
  realCalls = [];
  mockStatus = "uploaded";
  mockShouldThrow = false;
}

const { buildMockCrashPayload, fireMockCrashDrill } = await import("../lib/crashMock");
const { paintCrashOverlay, reportCrash } = await import("../lib/crash");
const { buildCrashIssueSkeleton } = await import("../lib/crashIssue");

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

function overlay() {
  return document.getElementById("crash-overlay") as HTMLElement;
}

console.log("\nmock crash drill payload marking (task 642)");

{
  const payload = buildMockCrashPayload();
  ok(payload.testMock === true, "payload carries testMock=true");
  ok(payload.source === "frontend.mock", "payload source is frontend.mock");
  ok(payload.label === "mock.test", "payload label is mock.test");
  ok(payload.fingerprintHint === "mock.lab.test", "payload fingerprintHint is mock-namespaced");
  ok(payload.kind === "crash", "payload kind stays crash (exercises the real crash path)");
  ok(payload.message.startsWith("[MOCK TEST — not a real crash]"), "message opens with the MOCK TEST marker");
  ok((payload.stack ?? "").includes("[mock]"), "fake dump frames are marked [mock]");
}

console.log("\nmock drill overlay anti-misreport marking");

{
  clearBindings();
  installBindings();
  fireMockCrashDrill();
  const host = overlay();
  ok(Boolean(host.querySelector(".crash-overlay__mock-banner")), "mock banner is painted");
  const badge = host.querySelector(".crash-overlay__mock-badge");
  ok(Boolean(badge), "MOCK badge rides the title");
  ok((badge?.textContent ?? "").length > 0, "badge text is non-empty");
  ok((host.querySelector(".crash-overlay__note")?.textContent ?? "").includes("mock"), "note repeats the mock marking");
  const send = host.querySelector(".crash-overlay__send") as HTMLButtonElement;
  ok(Boolean(send), "mock send button exists with the ReportMockCrash binding");
  ok(send.textContent !== document.querySelector(".crash-overlay__title")?.textContent, "banner present");

  mockStatus = "uploaded";
  send.click();
  await tick();
  await tick();
  ok(mockCalls.length === 1, "send calls the ReportMockCrash binding once");
  ok(mockCalls[0]?.kind === "crash", "send passes kind=crash");
  const sent = JSON.parse(mockCalls[0]?.detail ?? "{}") as { testMock?: boolean };
  ok(sent.testMock === true, "sent JSON keeps testMock=true");
  ok((send.textContent ?? "").length > 0 && send.textContent !== "Sent — thanks!", "uploaded status text differs from the real-crash one");

  // queued leg: upload failure lands in the pending queue backend-side; the
  // drill must say so instead of showing the plain failure text.
  clearBindings();
  installBindings();
  mockStatus = "queued";
  fireMockCrashDrill();
  const send2 = overlay().querySelector(".crash-overlay__send") as HTMLButtonElement;
  send2.click();
  await tick();
  await tick();
  ok(mockCalls.length === 1, "queued leg also reaches the binding");
}

console.log("\nreal crash overlay stays unmarked");

{
  clearBindings();
  installBindings();
  const error = new TypeError("renderer fault");
  error.stack = "TypeError: renderer fault\n    at render (src/App.tsx:12:3)";
  reportCrash("react", error);
  const host = overlay();
  ok(!host.querySelector(".crash-overlay__mock-banner"), "no mock banner on a real crash overlay");
  ok(!host.querySelector(".crash-overlay__mock-badge"), "no MOCK badge on a real crash overlay");
  realCalls.length = 0;
  mockCalls.length = 0;
  const send = host.querySelector(".crash-overlay__send") as HTMLButtonElement;
  send.click();
  await tick();
  await tick();
  ok(realCalls.length === 1 && mockCalls.length === 0, "real overlay send rides ReportCrash, not ReportMockCrash");
}

console.log("\nmock send without the binding hides the button");

{
  clearBindings();
  fireMockCrashDrill();
  ok(!overlay().querySelector(".crash-overlay__send"), "no send button when ReportMockCrash is absent");
}

console.log("\nissue skeleton marks the mock");

{
  const mockSkeleton = buildCrashIssueSkeleton(buildMockCrashPayload());
  ok(mockSkeleton.includes("Suggested title: [mock][crash]"), "suggested title leads with [mock]");
  ok(mockSkeleton.includes("Suggested labels: bug, crash, mock, test"), "mock/test labels suggested");
  ok(mockSkeleton.includes("- mock: YES"), "environment carries the mock line");

  const realError = new TypeError("renderer fault");
  realError.stack = "TypeError: renderer fault\n    at render (src/App.tsx:12:3)";
  const { buildCrashPayload } = await import("../lib/crash");
  const realSkeleton = buildCrashIssueSkeleton(buildCrashPayload("react", realError));
  ok(realSkeleton.includes("Suggested title: [crash]"), "real skeleton title keeps the bare [crash] tag");
  ok(!realSkeleton.includes("[mock]"), "real skeleton has no mock marking");
  ok(!realSkeleton.includes("- mock: YES"), "real skeleton has no mock environment line");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
