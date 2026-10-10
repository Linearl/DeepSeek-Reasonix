// Run: tsx src/__tests__/history-failure-detail.test.tsx
// Task 400 (upstream #11272 -> #11282): a failed history read must show why it
// failed. The fixed sentence stays the headline; the reader's own error rides
// into hydrateError so the recovery banner's Details names the real cause
// (permission, corruption, version skew) instead of repeating the headline.

import { register } from "node:module";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { hydrateFailureDetail, hydrateFailureReason } from "../lib/hydrateErrorState";
import { applyHydrateErrorState } from "../lib/hydrateErrorState";
import { projectSessionAvailability } from "../lib/sessionAvailability";
import { initialState } from "../lib/useController";
import { SessionRecoveryBanner } from "../components/SessionRecoveryBanner";
import { LocaleProvider } from "../lib/i18n";

register(new URL("../../scripts/svg-loader.mjs", import.meta.url));

const root0 = dirname(fileURLToPath(import.meta.url));
const srcRoot = join(root0, "..");

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label} (got ${JSON.stringify(actual)})`);
}

console.log("\nTask 400 history failure detail");

// ── 1. Pure composition helper ────────────────────────────────────────────────
eq(hydrateFailureReason(new Error("open /x/manifest.json: permission denied")),
  "open /x/manifest.json: permission denied", "Error cause yields its message");
eq(hydrateFailureReason("  store damaged  "), "store damaged", "string cause is trimmed");
eq(hydrateFailureReason(undefined), "", "undefined cause yields nothing");
eq(hydrateFailureReason({ weird: true }), "", "non-error object yields nothing");

const summary = "Failed to load conversation history. Previous content was kept when available — retry to try again.";
const format = (reason: string) => `Failed to load conversation history: ${reason}. Previous content was kept when available — retry to try again.`;
eq(hydrateFailureDetail(summary, new Error("disk gone"), format),
  "Failed to load conversation history: disk gone. Previous content was kept when available — retry to try again.",
  "Error cause composes through the localized template");
eq(hydrateFailureDetail(summary, undefined, format), summary,
  "no cause keeps the plain summary (no dangling separator)");
eq(hydrateFailureDetail(summary, "", format), summary,
  "empty cause keeps the plain summary");
eq(hydrateFailureDetail(summary, summary, format), summary,
  "cause equal to the summary never doubles the text");
eq(hydrateFailureDetail(summary, new Error("boom")), `${summary} boom`,
  "template is optional: default join still carries the reason");

// ── 2. Wires: the controller captures the reader's cause ─────────────────────
const controller = readFileSync(join(srcRoot, "lib/useController.ts"), "utf8");
ok(/const loadTimed = async <T,>\(label: string, load: \(\) => Promise<T>, onError\?: \(err: unknown\) => void\)/.test(controller),
  "loadTimed accepts an error sink");
ok(/\(err\) => \{ historyLoadCause = err; \}/.test(controller),
  "history load records its cause");
ok(/hydrateFailureDetail\(\s*t\("history\.failedLoadHistory"\),\s*historyLoadCause,/.test(controller),
  "hydrate_error composes summary + cause");
// The transcript notice keeps the short summary — it is a status line.
ok(/local_notice", level: "warn", text: t\("history\.failedLoadHistory"\)/.test(controller),
  "transcript notice keeps the fixed summary");
ok(/failSessionNavigation = useCallback\(async \(navigationSeq: number, tabId: string, cause\?: unknown\)/.test(controller),
  "failSessionNavigation accepts a cause");
ok((controller.match(/failSessionNavigation\(navigationSeq, (?:targetTabId|tabId), (?:resumeErr|channelErr|surfaceErr)\)/g) ?? []).length >= 4,
  "resume/channel/surface failures thread their caught error");
ok(/hydrateFailureDetail\(\s*t\("history\.failedOpenSession"\),\s*cause,/.test(controller),
  "failedOpenSession composes summary + cause");

// ── 3. i18n: both detail keys exist in all three locales with {reason} ───────
for (const [file, name] of [["locales/en.ts", "en"], ["locales/zh.ts", "zh"], ["locales/zh-TW.ts", "zh-TW"]] as const) {
  const text = readFileSync(join(srcRoot, file), "utf8");
  const load = text.match(/"history\.failedLoadHistoryDetail": "([^"]*)"/);
  const open = text.match(/"history\.failedOpenSessionDetail": "([^"]*)"/);
  ok(Boolean(load && load[1].includes("{reason}")), `${name}: failedLoadHistoryDetail has {reason}`);
  ok(Boolean(open && open[1].includes("{reason}")), `${name}: failedOpenSessionDetail has {reason}`);
}

// ── 4. Banner shows the reason after expanding Details ──────────────────────
const dom = new JSDOM("<!doctype html><html><body><div id='root'></div></body></html>", {
  pretendToBeVisual: true, url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.localStorage = dom.window.localStorage;

const root = createRoot(document.getElementById("root")!);
const realReason = "open C:\\\\sessions\\\\s1\\\\manifest.json: permission denied";
const failedState = applyHydrateErrorState(
  { ...initialState, items: [] },
  "startup",
  hydrateFailureDetail(summary, new Error(realReason), (reason) =>
    `Failed to load conversation history: ${reason}. Previous content was kept when available — retry to try again.`),
);
const availability = projectSessionAvailability({ local: failedState });
eq(availability.kind, "error", "failed history projects error availability");
eq(availability.source, "history", "failed history keeps the history source");
ok(Boolean(availability.detail?.includes(realReason)), "availability detail carries the real cause");

await act(async () => root.render(
  <LocaleProvider>
    <SessionRecoveryBanner availability={availability} />
  </LocaleProvider>,
));
const banner = document.querySelector(".session-recovery");
ok(Boolean(banner), "banner renders for the failed history");
const detailsButton = document.querySelector<HTMLButtonElement>("button[aria-controls]");
ok(Boolean(detailsButton), "Details toggle appears (detail is present)");
await act(async () => detailsButton!.click());
const detailPre = document.querySelector(".session-recovery__detail");
ok(Boolean(detailPre), "Details expands");
ok(Boolean(detailPre?.textContent?.includes(realReason)), "expanded Details names the real cause");
// Headline stays the fixed sentence — diagnosis rides in Details, not the title.
ok(Boolean(banner?.querySelector("strong")?.textContent?.includes("Unable to load this session")),
  "headline keeps the fixed summary");

// Connection-loss path is untouched (no false positive).
await act(async () => root.render(
  <LocaleProvider>
    <SessionRecoveryBanner availability={{ kind: "error", source: "connection", detail: "tunnel closed" }} />
  </LocaleProvider>,
));
const connBanner = document.querySelector(".session-recovery");
const connHeadline = connBanner?.querySelector("strong")?.textContent ?? "";
ok(connHeadline.includes("Connection interrupted"), "connection branch copy unchanged");
ok(!connHeadline.includes("Unable to load this session"),
  "connection branch does not show the history headline");

// Ready renders nothing.
await act(async () => root.render(
  <LocaleProvider><SessionRecoveryBanner availability={{ kind: "ready", source: "history" }} /></LocaleProvider>,
));
eq(document.querySelector(".session-recovery"), null, "ready availability renders no banner");

await act(async () => root.unmount());
dom.window.close();

process.stdout.write(`\n${passed}/${passed + failed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
