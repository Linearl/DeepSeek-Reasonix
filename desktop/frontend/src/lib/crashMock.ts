// Task 642 — lab "mock crash test" drill (settings → lab → dev-debug).
//
// 617/618 cannot be verified by hand without a real crash, so this entry
// synthesizes one: a fully marked test payload (no process ever crashes) that
// then travels the REAL reporting pipeline — the same crash overlay (with a
// loud MOCK banner), the same send binding (own channel → crash-pending queue
// on failure), the same one-click analysis chain (prerequisite probes → spend
// confirmation → YOLO session).
//
// Anti-misreport design: every surface says this is a drill. The payload
// carries testMock:true + source "frontend.mock" + label "mock.test" so the
// receiving end can pin severity to low and keep the fingerprint out of real
// crash groups; the overlay paints a banner; the issue skeleton and the
// analysis instruction repeat the marking.

import { snapshotBreadcrumbs } from "./breadcrumbs";
import { paintCrashOverlay, type CrashPayload } from "./crash";

declare const __BUILD_COMMIT__: string;
declare const __BUILD_CHANNEL__: string;

const MOCK_STACK = [
  "MockCrashError: simulated crash from the lab mock-crash-test entry (task 642)",
  "    at simulateCrash (mock://crashMock.ts:1:1) [mock]",
  "    at mockCrashTestButton (mock://SettingsPanel.tsx:1:1) [mock]",
].join("\n");

/** Synthetic, clearly marked crash payload. English body text on purpose —
 * it feeds the paste-ready GitHub issue skeleton, which must stay
 * English-only (see crashIssue.ts). */
export function buildMockCrashPayload(now = new Date()): CrashPayload {
  const buildCommit = typeof __BUILD_COMMIT__ === "string" ? __BUILD_COMMIT__ : "dev";
  const errorMessage =
    "Simulated crash report from the lab mock-crash-test entry (task 642). This is a test event, not a real failure.";
  const message = [
    "[MOCK TEST — not a real crash]",
    errorMessage,
    "Nothing actually crashed. This drill exercises the real reporting pipeline end to end:",
    "own channel → crash-pending queue on failure → one-click analysis.",
    "",
    "--- breadcrumbs ---",
    dumpCrumbsOrEmpty(),
    `build ${buildCommit}`,
  ].join("\n\n");
  return {
    schemaVersion: 2,
    source: "frontend.mock",
    kind: "crash",
    label: "mock.test",
    message,
    errorType: "MockCrashError",
    errorMessage,
    stack: MOCK_STACK,
    topFrame: "frontend.mock",
    fingerprintHint: "mock.lab.test",
    testMock: true,
    buildCommit,
    channel: typeof __BUILD_CHANNEL__ === "string" ? __BUILD_CHANNEL__ : "",
    language: typeof navigator !== "undefined" ? navigator.language || "" : "",
    view: typeof window !== "undefined" && window.location ? `${window.location.protocol}//${window.location.host}/ (mock drill)` : "",
    breadcrumbs: snapshotBreadcrumbs(),
    occurredAt: now.toISOString(),
  };
}

function dumpCrumbsOrEmpty(): string {
  // Real crashes attach the breadcrumb ring here; a drill does the same so the
  // pipeline shapes match, but an empty ring must not render a dangling header.
  const crumbs = snapshotBreadcrumbs();
  if (!crumbs.length) return "(none)";
  return crumbs.map((crumb) => `[${crumb.cat ?? ""}] ${crumb.msg ?? ""}`.trimEnd()).join("\n");
}

/** Paints the crash overlay for the synthetic payload with the mock marking
 * on. The overlay's own buttons take it from there — send rides
 * ReportMockCrash (queued like a native panic when the channel is down),
 * copy carries the mock-marked issue skeleton, analyze runs the standard
 * prerequisite-gated YOLO chain. */
export function fireMockCrashDrill(): void {
  paintCrashOverlay(buildMockCrashPayload(), { mock: true });
}
