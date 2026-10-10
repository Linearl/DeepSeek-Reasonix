// Run: tsx src/__tests__/context-panel-session-info.test.tsx
// wt-zcode-285: the session info face (ContextPanel) must surface the open
// conversation's session-group title and its recovery-copy role, using the
// existing read-only bindings (GetProjectGroups / GetRecoveryLineage).

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextPanel } from "../components/ContextPanel";
import { LocaleProvider } from "../lib/i18n";
import type { ContextPanelInfo } from "../lib/types";
import { resolveSessionGroupTitle, sessionRecoveryDisplay } from "../lib/sessionInfoPanel";
import type { SessionGroup } from "../lib/sessionCatalogTypes";
import type { RecoveryLineageView } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function wait(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.ResizeObserver = TestResizeObserver;
  return dom;
}

function emptyPanelInfo(): ContextPanelInfo {
  return {
    usedTokens: 0,
    windowTokens: 0,
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    reasoningTokens: 0,
    cacheHitTokens: 0,
    cacheMissTokens: 0,
    sessionCacheHitTokens: 0,
    sessionCacheMissTokens: 0,
    sessionCompletionTokens: 0,
    requestCount: 0,
    elapsedMs: 0,
    sessionCost: 0,
    readFiles: [],
    changedFiles: [],
  };
}

// --- pure logic -------------------------------------------------------------

console.log("\nsession info panel — pure logic");

const groups: SessionGroup[] = [
  { id: "g1", title: "Work", topicIds: ["topic-1", "topic-2"] },
  { id: "g2", title: "  ", topicIds: ["topic-3"] },
  { id: "g3", title: "Research", topicIds: [] },
];
eq(resolveSessionGroupTitle(groups, "topic-1"), "Work", "group title resolved by topic membership");
eq(resolveSessionGroupTitle(groups, "topic-2"), "Work", "second member resolves to the same group");
eq(resolveSessionGroupTitle(groups, "topic-9"), null, "ungrouped topic resolves to null");
eq(resolveSessionGroupTitle(groups, undefined), null, "missing topicId resolves to null");
eq(resolveSessionGroupTitle(groups, "topic-3"), null, "blank group title degrades to null");
eq(resolveSessionGroupTitle(undefined, "topic-1"), null, "missing roster resolves to null");

const copyLineage = {
  groupId: "lg1",
  state: "covered",
  branchCount: 2,
  unresolved: 0,
  cleanupEligible: 1,
  members: [
    { path: "/s/main.jsonl", role: "normal", canonical: true, turns: 5, open: true, running: false, selected: true },
    { path: "/s/copy.jsonl", role: "covered_copy", canonical: false, turns: 3, open: false, running: false, versionKind: "recovery" },
  ],
} as RecoveryLineageView;

eq(sessionRecoveryDisplay(copyLineage, "/s/copy.jsonl")?.role, "covered_copy", "session path picks its own lineage member");
eq(sessionRecoveryDisplay(copyLineage, "/s/copy.jsonl")?.labelKey, "recovery.role.covered_copy", "covered copy maps to the existing label key");
eq(sessionRecoveryDisplay(copyLineage, "/s/main.jsonl")?.labelKey, "recovery.role.normal", "canonical of a multi-version group still reports its role");
eq(sessionRecoveryDisplay(copyLineage, undefined)?.labelKey, "recovery.role.normal", "no path falls back to the selected member");
eq(sessionRecoveryDisplay({ ...copyLineage, members: [copyLineage.members[0]] }, "/s/main.jsonl"), null, "single plain original stays invisible");
eq(sessionRecoveryDisplay({ ...copyLineage, members: [] }, "/s/main.jsonl"), null, "empty lineage stays invisible");
eq(
  sessionRecoveryDisplay({
    groupId: "lg2", state: "diverged", branchCount: 1, unresolved: 0, cleanupEligible: 0,
    members: [{ path: "/s/only.jsonl", role: "diverged", canonical: true, turns: 2, open: true, running: false }],
  }, "/s/only.jsonl")?.labelKey,
  "recovery.role.diverged",
  "diverged role maps to its label key",
);

// --- rendered panel ---------------------------------------------------------

console.log("\ncontext panel session info");

const dom = installDom();
const mainApp: Record<string, (...args: unknown[]) => Promise<unknown>> = {
  ContextPanel: async () => emptyPanelInfo(),
};
(window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = { main: { App: mainApp } };
const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

// Case 1: grouped session whose physical path is a recovery copy.
mainApp.GetProjectGroups = async () => ({
  groups: [{ id: "g1", title: "Work", topicIds: ["topic-1"] }, { id: "g2", title: "Personal", topicIds: [] }],
  revision: 3,
  applied: true,
});
mainApp.GetRecoveryLineage = async () => copyLineage;

await act(async () => {
  root.render(
    <LocaleProvider>
      <ContextPanel
        tabId="tab-info"
        context={{ used: 10, window: 100 }}
        sessionInfo={{ topicId: "topic-1", scope: "project", workspaceRoot: "D:/ws", sessionPath: "/s/copy.jsonl" }}
      />
    </LocaleProvider>,
  );
  await wait(20);
});

const sessionSection = document.querySelector(".context-panel__session-section");
const sectionText = sessionSection?.textContent ?? "";
ok(sectionText.includes("Work"), "group title row shows the owning group name");
ok(sectionText.includes("Group"), "group row carries a label");
ok(sectionText.includes("covered by a fuller version"), "recovery row shows the covered-copy status");
ok(sectionText.includes("Version"), "recovery row carries a label");

// Case 2: ungrouped, single plain original — both rows stay hidden.
mainApp.GetProjectGroups = async () => ({ groups: [], revision: 1, applied: true });
mainApp.GetRecoveryLineage = async () => ({
  groupId: "", state: "", branchCount: 1, unresolved: 0, cleanupEligible: 0,
  members: [{ path: "/s/plain.jsonl", role: "normal", canonical: true, turns: 1, open: true, running: false }],
});

await act(async () => {
  root.render(
    <LocaleProvider>
      <ContextPanel
        tabId="tab-plain"
        context={{ used: 10, window: 100 }}
        sessionInfo={{ topicId: "topic-plain", scope: "global", sessionPath: "/s/plain.jsonl" }}
      />
    </LocaleProvider>,
  );
  await wait(20);
});

const plainSection = document.querySelector(".context-panel__session-section");
const plainText = plainSection?.textContent ?? "";
ok(!plainText.includes("covered by a fuller version"), "plain session hides the recovery row");
ok(!/Group/.test(plainText), "ungrouped session hides the group row");
ok(plainText.length > 0, "session metrics section still renders its base rows");

// Case 3: no session identity at all (creation surface / remote fallback).
let groupCalls = 0;
mainApp.GetProjectGroups = async () => {
  groupCalls += 1;
  return { groups: [], revision: 1, applied: true };
};

await act(async () => {
  root.render(
    <LocaleProvider>
      <ContextPanel tabId="tab-bare" context={{ used: 10, window: 100 }} />
    </LocaleProvider>,
  );
  await wait(20);
});
eq(groupCalls, 0, "no group fetch happens without identity");

const bareText = document.querySelector(".context-panel__session-section")?.textContent ?? "";
ok(!bareText.includes("covered by a fuller version"), "identity-less panel hides the recovery row");

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
