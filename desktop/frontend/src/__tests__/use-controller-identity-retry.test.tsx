// Task 445 (second wave, fix wave): the identity-retry inside loadOlderHistory
// must actually re-issue its request. The retry re-enters loadOlder with
// isRetry=true while the OUTER call still owns the `historyOlderLoading`
// spinner — the loading gate used to short-circuit the recursive call
// ("older skipped reason=loading-in-progress"), so a page that was in flight
// across a model switch (snapshotTabForAction bumps sessionRevision → the
// served page no longer matches the generation fingerprint) was silently
// DROPPED: the comment promises "the same transcript then simply asks again,
// once", but the ask never happened and the user had to scroll a second time.
//
// This suite drives the real useController against the real transcript store
// (loadOlder overridden only to stage the in-flight era-mismatched page) and
// asserts the retry request is actually issued and the page lands.
//
// Run: npx tsx src/__tests__/use-controller-identity-retry.test.tsx

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";
import { useController } from "../lib/useController";
import { getTranscriptStore } from "../lib/transcriptStore";
import { historySliceFromMessages } from "./mockHistorySlice";
import type { HistorySlice, Meta, TabMeta, WireEvent } from "../lib/types";

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

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function flushPromises(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    await act(async () => { await flushPromises(); });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

console.log("\nuse controller identity-retry (task 445: retry must survive the loading gate)");

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

// 67 turns of history: the hydration page budget is 60 turns, so the first
// window leaves an older cursor page behind — `historyHasOlder` starts true
// and a genuine second page exists for the retry to land.
const historyMessages: Array<{ role: "user" | "assistant"; content: string }> = [];
for (let turn = 1; turn <= 67; turn += 1) {
  historyMessages.push({ role: "user", content: `question ${turn}` });
  historyMessages.push({ role: "assistant", content: `answer ${turn}` });
}

const tab: TabMeta = {
  id: "tab-retry",
  scope: "project",
  workspaceRoot: "/repo",
  workspaceName: "repo",
  workspacePath: "/repo",
  topicId: "topic-retry",
  topicTitle: "General",
  sessionPath: "/repo/sessions/retry.jsonl",
  sessionRevision: 1,
  sessionDigest: "digest-v1",
  label: "model",
  ready: true,
  running: false,
  mode: "normal",
  toolApprovalMode: "ask",
  tokenMode: "full",
  active: true,
  cwd: "/repo",
};
const meta: Meta = {
  label: "model",
  ready: true,
  eventChannel: "agent:event",
  sessionPath: tab.sessionPath,
  sessionRevision: tab.sessionRevision,
  sessionDigest: tab.sessionDigest,
  cwd: "/repo",
  workspaceRoot: "/repo",
  workspaceName: "repo",
  workspacePath: "/repo",
  autoApproveTools: false,
  bypass: false,
  collaborationMode: "normal",
  toolApprovalMode: "ask",
  tokenMode: "full",
  goal: "",
  goalStatus: "stopped",
};

const eventHandlers: Array<(event: WireEvent) => void> = [];
window.runtime = {
  EventsOn: (name: string, callback: (...data: unknown[]) => void) => {
    if (name === "agent:event") eventHandlers.push(callback as (event: WireEvent) => void);
    return () => {};
  },
  BrowserOpenURL: () => {},
};
window.go = {
  main: {
    App: {
      ListTabs: async () => [tab],
      MetaForTab: async () => meta,
      ContextUsageForTab: async () => ({ used: 0, window: 100, sessionTokens: 0 }),
      EffortForTab: async () => ({ supported: true, current: "auto", default: "auto", levels: ["auto"] }),
      BalanceForTab: async () => ({ available: false, display: "" }),
      JobsForTab: async () => [],
      CheckpointsForTab: async () => [],
      HistorySliceForTab: async (_tabId: string, req: { cursor?: string; turns?: number }) =>
        historySliceFromMessages(tab.id, historyMessages, req, { revision: 1, digest: "digest-v1" }),
      HistoryCheckpointTurnsForTab: async () => [],
      ReplayPendingPrompts: async () => {},
    } as Partial<AppBindings> as AppBindings,
  },
};

type Controller = ReturnType<typeof useController>;
let controller: Controller | undefined;
function Probe() {
  controller = useController();
  return null;
}

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);
await act(async () => {
  root.render(<Probe />);
  await flushPromises();
});
await waitFor("hydration completion", () => controller?.state.hydrating === false);

const hydratedItems = controller?.state.items.length ?? 0;
ok(hydratedItems > 0, "history hydrated");
ok(controller?.state.historyHasOlder === true, "older history available after hydration");
ok(controller?.state.historyOlderLoading === false, "no page in flight after hydration");

// Stage the model-switch collision: the first loadOlder page hangs in flight
// while the model switch happens; the backend then answers with a page whose
// revision no longer matches the generation the request was fingerprinted
// against (snapshotTabForAction bumped the session revision server-side).
// The controller must identity-retry — and the retry must actually re-issue
// the request instead of bouncing off its own loading gate.
const store = getTranscriptStore() as unknown as Record<string, unknown>;
const realLoadOlder = (store.loadOlder as (...args: unknown[]) => Promise<unknown>).bind(store);
const loadOlderCalls: number[] = [];
let releaseInFlightPage: (value: unknown) => void = () => {};
const inFlightPage = new Promise<unknown>((resolve) => { releaseInFlightPage = resolve; });
store.loadOlder = (tabId: unknown, sessionPath: unknown, options: unknown) => {
  loadOlderCalls.push(loadOlderCalls.length + 1);
  if (loadOlderCalls.length === 1) {
    return inFlightPage;
  }
  // The retry goes through the REAL store: the session is still primed with
  // hasOlder=true and a live cursor, so this serves a genuine second page.
  return realLoadOlder(tabId, sessionPath, options) as Promise<unknown>;
};

let retryOutcome: boolean | undefined;
await act(async () => {
  const request = controller!.loadOlderHistory(tab.id);
  await flushPromises();
  ok(loadOlderCalls.length === 1, "first page request issued");
  // The model switch lands while the page is in flight; the backend serves
  // the page under the NEW revision (revision 2) — a generation mismatch
  // against the revision-1 fingerprint the request captured.
  releaseInFlightPage({
    kind: "prepend",
    prependItems: [{ kind: "user", id: "era-page-0", text: "page served after the model switch" }],
    removeIds: [],
    startTurn: 8,
    endTurn: 67,
    totalTurns: 67,
    hasOlder: true,
    revisionKnown: true,
    revision: 2,
    digest: "digest-v2",
  });
  retryOutcome = await request;
  await flushPromises();
});

ok(loadOlderCalls.length >= 2, `identity-retry re-issued the request (saw ${loadOlderCalls.length} loadOlder calls)`);
ok(retryOutcome === true, "loadOlderHistory resolves true (page landed)");
ok(controller?.state.historyOlderLoading === false, "loading flag settled");
ok(!controller?.state.historyOlderError, "no error surfaced for a recoverable collision");
ok((controller?.state.items.length ?? 0) > hydratedItems, "older page prepended onto the transcript");

await act(async () => { root.unmount(); });
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
