// Run: tsx src/__tests__/composer-model-applying.test.tsx
//
// tl1389 (P1 frontend): a model switch that took the fallback rebuild path
// (recovery-forked sessions; 12.3s measured) used to leave the composer's
// model name frozen on the outgoing label with zero feedback. This suite
// covers the transition state end to end at the FE layer — the task-1301
// effortApplying suite carried to the model side:
//   1. appear: modelApplying=true renders the localized hint beside the
//      model switcher (出现);
//   2. settle-clear: the SetModelForTab call settling clears the state on
//      both exit paths — applied and failed (消退双路);
//   3. guarded mark: a late "model:applying" event for a settled switch is
//      ignored, so the hint can never stick;
//   4. locale parity: en / zh / zh-TW all carry composer.modelApplying.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import { createControllerModelCommands } from "../lib/controllerModelCommands";
import {
  useModelApplyingStore,
  beginModelSwitch,
  endModelSwitch,
  handleModelApplyingEvent,
  clearModelApplying,
} from "../store/modelApplying";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
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
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

const HINT_TEXT = en["composer.modelApplying"];

async function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "mimo-v2.6",
    onSend: () => {},
    onCancel: () => undefined,
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
    ...props,
  };
  const paint = async (nextProps: Partial<Parameters<typeof Composer>[0]> = {}) => {
    currentProps = { ...currentProps, ...nextProps };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToastProvider>
            <Composer {...currentProps} />
          </ToastProvider>
        </LocaleProvider>,
      );
      await flushTimers();
    });
  };
  await paint();
  return { root, rerender: paint };
}

function storeApplying(tabId: string): boolean {
  return Boolean(useModelApplyingStore.getState().applying[tabId]);
}

// ── 1. appear / disappear at the composer surface ──────────────────────────
{
  const dom = installDom();
  const { root, rerender } = await renderComposer();

  ok(!document.querySelector(".composer-effort-applying"), "no applying hint on a normal session (fast path default)");

  // 出现: the fallback rebuild is in flight — hint appears beside the model name.
  await rerender({ modelApplying: true });
  const hint = document.querySelector<HTMLElement>(".composer-meta__control--model .composer-effort-applying");
  ok(Boolean(hint), "applying hint appears beside the model switcher while the rebuild is in flight");
  eq(hint?.getAttribute("role"), "status", "hint is announced as a live status");
  eq(hint?.textContent, HINT_TEXT, "hint renders the localized text");

  // 消退: the rebuild settles — hint disappears.
  await rerender({ modelApplying: false });
  ok(!document.querySelector(".composer-meta__control--model .composer-effort-applying"), "applying hint disappears once the rebuild settles");

  await act(async () => root.unmount());
  dom.window.close();
}

// ── 2. settle-clear through setModelForTab (both exit paths) ────────────────
{
  const notices: string[] = [];
  const ports = {
    statesRef: { current: new Map() },
    modelSwitchSeqByTab: { current: new Map() },
    modelSwitchSuccessVersionByTab: { current: new Map() },
    modelSwitchQueueByTab: { current: new Map() },
    enqueueModelSwitch: async () => "applied" as const,
    clearBalanceForTab: () => {},
    dispatchTo: (_tabId: string, action: { type: string; level?: string; text?: string }) => {
      if (action.type === "local_notice") notices.push(action.text ?? "");
    },
    refreshBalanceForTab: async () => {},
    refreshMetaForTab: async () => {},
  };
  const { setModelForTab } = createControllerModelCommands(ports);

  // applied: the mock queue resolves → applying must be gone.
  beginModelSwitch("tab-applied");
  handleModelApplyingEvent("tab-applied");
  ok(storeApplying("tab-applied"), "marked while the fallback call is in flight");
  await setModelForTab("tab-applied", "prov-a/model-a2");
  ok(!storeApplying("tab-applied"), "settle (applied) clears the applying state");

  // failed: a rejecting queue must clear through the same finally.
  const failingPorts = {
    ...ports,
    enqueueModelSwitch: async () => { throw new Error("rebuild failed"); },
  };
  const { setModelForTab: setModelForTabFail } = createControllerModelCommands(failingPorts);
  beginModelSwitch("tab-failed");
  handleModelApplyingEvent("tab-failed");
  ok(storeApplying("tab-failed"), "marked before a failing call settles");
  await setModelForTabFail("tab-failed", "prov-b/model-b1");
  ok(!storeApplying("tab-failed"), "settle (failed) clears the applying state");
  ok(notices.length === 1, "failure still surfaces the warn notice");
}

// ── 3. guarded mark: late events cannot stick ───────────────────────────────
{
  handleModelApplyingEvent("tab-late");
  ok(!storeApplying("tab-late"), "event for a switch not in flight is ignored");
  beginModelSwitch("tab-live");
  handleModelApplyingEvent("tab-live");
  ok(storeApplying("tab-live"), "event for an in-flight switch marks the hint");
  endModelSwitch("tab-live");
  ok(!storeApplying("tab-live"), "endModelSwitch clears the hint");
  // runtime:rebuilt backstop.
  beginModelSwitch("tab-backstop");
  handleModelApplyingEvent("tab-backstop");
  clearModelApplying("tab-backstop");
  ok(!storeApplying("tab-backstop"), "runtime:rebuilt backstop clears the hint");

  // No ordered transport between the call response and the sink-lane event
  // (task 1323 pin carried over): an event reordered past its settle is
  // dropped — a missed hint, never a stuck one.
  beginModelSwitch("tab-reordered");
  endModelSwitch("tab-reordered");
  handleModelApplyingEvent("tab-reordered");
  ok(!storeApplying("tab-reordered"), "event reordered past its settle is dropped");
  beginModelSwitch("tab-dup");
  handleModelApplyingEvent("tab-dup");
  endModelSwitch("tab-dup");
  handleModelApplyingEvent("tab-dup");
  ok(!storeApplying("tab-dup"), "late duplicate for a settled switch is dropped");

  // Empty/whitespace ids are inert.
  handleModelApplyingEvent(undefined);
  handleModelApplyingEvent("  ");
  ok(Object.keys(useModelApplyingStore.getState().applying).length === 0, "store is empty at the end of the store block");
}

// ── 4. locale parity (三语) ─────────────────────────────────────────────────
{
  const key = "composer.modelApplying";
  const enValue = (en as Record<string, string>)[key];
  const zhValue = (zh as Record<string, string>)[key];
  const zhTWValue = (zhTW as Record<string, string>)[key];
  ok(typeof enValue === "string" && enValue.length > 0, `en has ${key}`);
  ok(typeof zhValue === "string" && zhValue.length > 0, `zh has ${key}`);
  ok(typeof zhTWValue === "string" && zhTWValue.length > 0, `zh-TW has ${key}`);
  eq(zhValue, "正在应用模型…", "zh copy matches the task wording");
  ok(enValue !== zhValue && zhValue !== zhTWValue, "the three locales carry distinct scripts");
  ok(enValue.endsWith("…") && zhValue.endsWith("…") && zhTWValue.endsWith("…"), "all three end with the ellipsis (in-flight tone)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
