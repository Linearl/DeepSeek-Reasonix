// Run: tsx src/__tests__/session-versions-flat-session-1173s2.test.tsx
// tl1173s2 落点①销案钉子：tl1173s1（新会话默认扁平写）+ tl1227（ListSessionHeads
// 垫片恒空、读侧单路径化）之后，扁平会话（无头数据）的版本面必须保持优雅隐藏——
// 入口可点但后端血缘只有会话自身一个成员，宿主门控（可见版本 > 1）不开对话框、
// 不弹报错、不出现空态面板。本文件把这一现状钉成契约：谁改坏门控即红。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { normalizeRecoveryLineageView, userVisibleRecoveryVersions } from "../lib/sessionRecoveryVersions";
import { sessionRecoveryDisplay } from "../lib/sessionInfoPanel";
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

// --- pure logic: 扁平会话血缘（tl1227 后 GetRecoveryLineage 的真实形态） -------

console.log("\nflat session version face — pure gating");

// 后端对扁平会话的返回：file lineage 单成员（会话自身），state 为空，
// sessionHeadLineage 因垫片恒空而不合并 heads 视图。
const flatLineage = {
  groupId: "b1a2c3d4",
  state: "",
  branchCount: 1,
  unresolved: 0,
  cleanupEligible: 0,
  members: [
    { path: "/s/flat.jsonl", role: "normal", canonical: true, turns: 4, open: true, running: false },
  ],
} as RecoveryLineageView;

const normalized = normalizeRecoveryLineageView(flatLineage);
eq(normalized.state, "", "flat lineage keeps the empty file-lineage state (no heads surface)");
eq(userVisibleRecoveryVersions(normalized).length, 1, "flat session has exactly one visible version — host gate (> 1) stays closed");
eq(sessionRecoveryDisplay(normalized, "/s/flat.jsonl"), null, "context panel recovery row stays hidden for a flat session");

// --- rendered host: 入口点击 → 安静不打开（无对话框、无报错、无空态） --------

console.log("\nflat session version face — host stays quiet");

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;

type RuntimeHandler = (...data: unknown[]) => void;
const handlers = new Map<string, Set<RuntimeHandler>>();
window.runtime = {
  EventsOn: (name: string, callback: RuntimeHandler) => {
    const listeners = handlers.get(name) ?? new Set<RuntimeHandler>();
    listeners.add(callback);
    handlers.set(name, listeners);
    return () => listeners.delete(callback);
  },
  BrowserOpenURL: () => {},
};

const lineageReads: Array<Record<string, unknown>> = [];
window.go = {
  main: {
    App: {
      GetRecoveryLineage: async (key: Record<string, unknown>) => {
        lineageReads.push(key);
        return flatLineage;
      },
    },
  },
};

async function flushMicrotasks() {
  await Promise.resolve();
  await Promise.resolve();
}

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
await preloadLocale("en");
await import("../lib/sessionRecoveryRuntime");
const { SessionRecoveryVersionsHost } = await import("../components/SessionRecoveryVersionsHost");
const { requestTopicVersionsForSession } = await import("../lib/sessionRecoveryVersionHostBridge");

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <ToastProvider>
        <SessionRecoveryVersionsHost
          sessions={[]}
          onResumeSession={async () => {}}
          onRecoveryCreated={() => {}}
          onLineageChanged={() => {}}
        />
      </ToastProvider>
    </LocaleProvider>,
  );
  await flushMicrotasks();
});

await act(async () => {
  // ProjectTree「管理版本」等固定入口对扁平会话的调用形态。
  requestTopicVersionsForSession({ scope: "global", topicId: "topic-flat", path: "/s/flat.jsonl" });
  await flushMicrotasks();
  await wait(10);
});

eq(lineageReads.length, 1, "the entry point still resolves the lineage read");
eq(document.querySelector(".recovery-lineage-dialog"), null, "no version dialog opens for a flat session");
eq(document.querySelector(".toast"), null, "no error toast is raised for the quiet outcome");

// 多成员血缘（恢复副本分叉场景）仍照常打开——门控只挡扁平单版本，不误伤恢复面。
// 注意 covered_copy 不计入可见版本（既有口径），所以这里用 diverged 对。
const divergedLineage = {
  groupId: "lg1",
  state: "diverged",
  branchCount: 2,
  unresolved: 1,
  cleanupEligible: 0,
  members: [
    { path: "/s/main.jsonl", role: "normal", canonical: true, turns: 5, open: false, running: false },
    { path: "/s/fork.jsonl", role: "diverged", canonical: false, turns: 3, open: false, running: false },
  ],
} as RecoveryLineageView;
window.go.main.App.GetRecoveryLineage = async () => divergedLineage;

await act(async () => {
  requestTopicVersionsForSession({ scope: "global", topicId: "topic-copy", path: "/s/fork.jsonl" });
  await flushMicrotasks();
  await wait(10);
});
// 对话框是 lazy chunk：首次真正 setState 后还要等动态 import 落一次渲染。
for (let frame = 0; frame < 20 && !document.querySelector(".recovery-lineage-dialog"); frame += 1) {
  await act(async () => {
    await wait(10);
  });
}

ok(document.querySelector(".recovery-lineage-dialog") !== null, "multi-version lineage still opens the dialog (recovery face unaffected)");

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
