// Run: tsx src/__tests__/turn-running-degrade.test.tsx
// 任务461-P6: a foreground send bounced with "turn already running" degrades
// into the durable guidance queue instead of failing — exact-turn steer when
// the running turn can be fenced, session-level steer otherwise, durable
// follow-up when even the steer channel refuses, and only a total enqueue
// failure falls back to the Send-failed face (P3's resend stays for it).
// The destination notice names where the message went, and the turn face
// keeps the authoritative running facts (composer stays blocked).

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings, TabMeta, WireEvent } from "../lib/bridge";
import { LocaleProvider } from "../lib/i18n";
import { useController } from "../lib/useController";

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
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function flushPromises(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

const tab: TabMeta = {
  id: "tab-p6",
  scope: "project",
  workspaceRoot: "/repo/p6",
  workspaceName: "p6",
  workspacePath: "/repo/p6",
  gitBranch: "main",
  topicId: "topic-p6",
  topicTitle: "P6",
  label: "model-p6",
  ready: true,
  running: true,
  mode: "normal",
  toolApprovalMode: "ask",
  tokenMode: "full",
  active: true,
  cwd: "/repo/p6",
  turnId: "turn-authoritative",
};

type SteerReceipt = { itemId: string; disposition: string; position: number; paused: boolean; error?: string };

// Scenario knobs; every test re-installs the mock and re-mounts a controller.
let steerExactBehavior: "accept" | "queued" | "throw" = "accept";
let steerPlainBehavior: "accept" | "queued" | "throw" = "throw";
let followupBehavior: "queued" | "throw" = "queued";
const calls: { name: string; args: unknown[] }[] = [];
let eventHandlers: Array<(event: WireEvent) => void> = [];

function installBindings() {
  calls.length = 0;
  eventHandlers = [];
  const record = (name: string, args: unknown[]) => calls.push({ name, args });
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
        MetaForTab: async () => ({
          label: tab.label, ready: true, startupErr: "", eventChannel: "agent:event",
          cwd: tab.cwd, runtime: { phase: "ready", epoch: "e-p6" },
        }),
        ContextUsageForTab: async () => ({}),
        EffortForTab: async () => ({}),
        BalanceForTab: async () => ({}),
        JobsForTab: async () => [],
        CheckpointsForTab: async () => [],
        HistoryForTab: async () => [],
        HistoryPageForTab: async () => ({ messages: [], startTurn: 0, endTurn: 0, totalTurns: 0, hasOlder: false }),
        HistoryCheckpointTurnsForTab: async () => [],
        ReplayPendingPrompts: async () => {},
        ReplayPendingPromptsForTab: async () => {},
        StartTurnForTab: async (tabId: string, _input: string, submissionId: string) => {
          record("StartTurnForTab", [tabId, submissionId]);
          throw new Error("turn already running");
        },
        EnqueueInboxSteerForTurn: async (tabId: string, turnId: string, display: string, submit: string, idempotency: string) => {
          record("EnqueueInboxSteerForTurn", [tabId, turnId, display, submit, idempotency]);
          if (steerExactBehavior === "throw") throw new Error("steer channel unavailable");
          const receipt: SteerReceipt = {
            itemId: "item-exact", disposition: steerExactBehavior === "accept" ? "steer_accepted" : "queued_followup", position: 1, paused: false,
          };
          return receipt;
        },
        EnqueueInboxSteer: async (tabId: string, display: string, submit: string, idempotency: string) => {
          record("EnqueueInboxSteer", [tabId, display, submit, idempotency]);
          if (steerPlainBehavior === "throw") throw new Error("steer channel unavailable");
          const receipt: SteerReceipt = {
            itemId: "item-plain", disposition: steerPlainBehavior === "accept" ? "steer_accepted" : "queued_followup", position: 1, paused: false,
          };
          return receipt;
        },
        EnqueueInboxFollowup: async (tabId: string, display: string, submit: string, idempotency: string) => {
          record("EnqueueInboxFollowup", [tabId, display, submit, idempotency]);
          if (followupBehavior === "throw") throw new Error("queue unavailable");
          const receipt: SteerReceipt = { itemId: "item-followup", disposition: "queued_followup", position: 1, paused: false };
          return receipt;
        },
      } as Partial<AppBindings> as AppBindings,
    },
  };
}

type Controller = ReturnType<typeof useController>;
let controller: Controller | undefined;

async function mount() {
  installBindings();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><HookProbe /></LocaleProvider>);
    await flushPromises();
  });
  return root;
}

function HookProbe() {
  controller = useController();
  return null;
}

async function reset() {
  await act(async () => { /* settle */ });
  controller = undefined;
  document.body.innerHTML = "<div id=\"root\"></div>";
}

const DEGRADE_NOTICE = (kind: "steer" | "queued") =>
  kind === "steer"
    ? "↪ Turn in progress: your message joined the current task as mid-turn guidance"
    : "↪ Turn in progress: your message is queued and will be sent when the turn finishes";

async function main() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
  Object.assign(globalThis, {
    window: dom.window,
    document: dom.window.document,
    HTMLElement: dom.window.HTMLElement,
    HTMLTextAreaElement: dom.window.HTMLTextAreaElement,
    Node: dom.window.Node,
    Event: dom.window.Event,
    KeyboardEvent: dom.window.KeyboardEvent,
    MouseEvent: dom.window.MouseEvent,
    localStorage: dom.window.localStorage,
    IS_REACT_ACT_ENVIRONMENT: true,
  });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: () => ({ matches: true, media: "(prefers-reduced-motion: reduce)", onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false }),
  });

  // 1. Exact-turn steer accepted: no failed face, destination notice, turn face aligned.
  {
    steerExactBehavior = "accept";
    const root = await mount();
    await act(async () => { await controller?.sendToTab("tab-p6", "帮我确认发送链路"); await flushPromises(); });
    const state = controller?.state;
    ok(state?.items.some((item) => item.kind === "user" && item.text === "帮我确认发送链路" && !item.failed) ?? false, "steered send keeps the optimistic bubble delivered");
    eq(state?.items.some((item) => item.kind === "user" && item.failed), false, "steered send never shows the Send-failed face");
    eq(state?.running, true, "steered send keeps the composer blocked (turn is running)");
    eq(state?.turnActive, true, "steered send keeps the turn face active");
    eq(state?.activeTurnId, "turn-authoritative", "steered send adopts the authoritative turn id");
    ok(state?.items.some((item) => item.kind === "notice" && item.text === DEGRADE_NOTICE("steer")) ?? false, "the notice names steer injection (UX 去向)");
    const exact = calls.find((c) => c.name === "EnqueueInboxSteerForTurn");
    eq(exact?.args[1], "turn-authoritative", "the exact-turn fence carries the authoritative turn id");
    eq(exact?.args[3], "帮我确认发送链路", "the steered payload carries the original submit text");
    root.unmount();
    await reset();
  }

  // 2. Steer rejected by the running turn → queued_followup: queued destination text.
  {
    steerExactBehavior = "queued";
    const root = await mount();
    await act(async () => { await controller?.sendToTab("tab-p6", "排队的消息"); await flushPromises(); });
    const state = controller?.state;
    ok(state?.items.some((item) => item.kind === "user" && item.text === "排队的消息" && !item.failed) ?? false, "queued send keeps the optimistic bubble delivered");
    eq(state?.items.some((item) => item.kind === "user" && item.failed), false, "queued send never shows the Send-failed face");
    ok(state?.items.some((item) => item.kind === "notice" && item.text === DEGRADE_NOTICE("queued")) ?? false, "the notice names the queue destination (UX 去向)");
    root.unmount();
    await reset();
  }

  // 3. Steer channel refuses outright → durable follow-up queue keeps the text.
  {
    steerExactBehavior = "throw";
    followupBehavior = "queued";
    const root = await mount();
    await act(async () => { await controller?.sendToTab("tab-p6", "兜底消息"); await flushPromises(); });
    const state = controller?.state;
    ok(calls.some((c) => c.name === "EnqueueInboxFollowup"), "a refused steer channel falls back to the durable follow-up queue");
    ok(state?.items.some((item) => item.kind === "user" && item.text === "兜底消息" && !item.failed) ?? false, "follow-up fallback keeps the bubble delivered");
    eq(state?.items.some((item) => item.kind === "user" && item.failed), false, "follow-up fallback never shows the Send-failed face");
    ok(state?.items.some((item) => item.kind === "notice" && item.text === DEGRADE_NOTICE("queued")) ?? false, "follow-up fallback names the queue destination");
    root.unmount();
    await reset();
  }

  // 4. Total enqueue failure → the ordinary Send-failed face stays (P3 resend).
  {
    steerExactBehavior = "throw";
    steerPlainBehavior = "throw";
    followupBehavior = "throw";
    const root = await mount();
    await act(async () => {
      try {
        await controller?.sendToTab("tab-p6", "真的发不出去");
      } catch { /* sync path rethrows */ }
      await flushPromises();
    });
    const state = controller?.state;
    ok(state?.items.some((item) => item.kind === "user" && item.failed) ?? false, "a total enqueue failure keeps the Send-failed face (P3 resend owns it)");
    root.unmount();
    await reset();
  }

  // 5. Unrelated rejections are untouched: a non-turn-running error still fails.
  {
    steerExactBehavior = "accept";
    installBindings();
    const go = window.go as { main: { App: Record<string, unknown> } };
    go.main.App.StartTurnForTab = async () => { throw new Error("workspace is read only"); };
    const rootEl = document.getElementById("root");
    const root = createRoot(rootEl!);
    await act(async () => {
      root.render(<LocaleProvider><HookProbe /></LocaleProvider>);
      await flushPromises();
    });
    await act(async () => {
      try {
        await controller?.sendToTab("tab-p6", "只读会话");
      } catch { /* rejection propagates */ }
      await flushPromises();
    });
    const state = controller?.state;
    ok(state?.items.some((item) => item.kind === "user" && item.failed) ?? false, "a non-turn-running rejection keeps the Send-failed face");
    ok(!calls.some((c) => c.name === "EnqueueInboxSteerForTurn"), "non-turn-running rejections never touch the guidance queue");
    root.unmount();
  }

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
  dom.window.close();
}

void main();
