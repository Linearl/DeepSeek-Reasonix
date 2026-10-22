// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tool-recovery-panel-dismiss.test.tsx
//
// 任务 1061（中断的工具调用记录加「忽略」按钮）前端契约。
//
// 钉住四条验收：
//   ① 每条记录出现「忽略」按钮，点击即从块中移除，无残留占位；
//   ② 全部忽略后折叠块整体不渲染；
//   ③ 忽略是纯前端状态：点击不触发任何 binding 调用（不发起新一轮对话、
//      不改转录数据、不写会话事件日志）；
//   ④ 同会话内重探查（running / refreshKey 变化）不复活已忽略记录；会话
//      重挂载（切会话/重启）记录按设计重现——in-memory per-session 语义。

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import { act } from "react";
import { createRoot } from "react-dom/client";

// ToolRecoveryPanel imports its own CSS; redirect like the sibling
// tool-recovery-panel-turn-end-cleanup test does (see its header for the why).
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css") || specifier.endsWith(".svg")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

import type { ToolRecoveryBindings, ToolRecoverySnapshot } from "../lib/toolRecovery";

const { LocaleProvider } = await import("../lib/i18n");
const { ToolRecoveryPanel } = await import("../components/ToolRecoveryPanel");

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
  globalThis.Event = dom.window.Event;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

const dom = installDom();
const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");

function snapshotWith(attemptIds: string[]): ToolRecoverySnapshot {
  return {
    silent: false, statistics: {}, sessionPath: "/s/a.jsonl", runtimeEpoch: "e1", revision: "r1", retryEnabled: false,
    calls: attemptIds.map(attempt_id => ({
      identity: { attempt_id, canonical_tool: `tool-${attempt_id}`, argument_digest: "d", resource_scope: "session:s" },
      state: "unknown", read_only: false,
    })),
  } as unknown as ToolRecoverySnapshot;
}

// 1061 验收③的核心探针：忽略点击若偷调了任何 binding（尤其 ResolveToolRecovery
// ——后端 dismiss 动作面），就是「忽略一次 = 发起一轮后端交互」，与任务书相悖。
let probeCalls = 0;
let resolveCalls = 0;
const probeResult: { snapshot: ToolRecoverySnapshot } = { snapshot: snapshotWith(["a1", "a2"]) };
const bindings: ToolRecoveryBindings = {
  GetToolRecoveryForTab: async () => { probeCalls += 1; return probeResult.snapshot; },
  ResolveToolRecoveryForTab: async () => { resolveCalls += 1; return probeResult.snapshot; },
};

const host = document.createElement("div");
rootEl.appendChild(host);
const root = createRoot(host);

type PanelProps = { running: boolean; refreshKey: number; sessionKey?: string };
async function renderPanel(props: PanelProps) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToolRecoveryPanel key={props.sessionKey ?? "k1"} tabId="tab-1" sessionKey={props.sessionKey ?? "k1"} running={props.running} refreshKey={props.refreshKey} bindings={bindings} />
      </LocaleProvider>,
    );
    // Let the probe promise (microtask) and the setState it triggers land.
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

const panelHtml = () => host.querySelector(".tool-recovery-panel")?.innerHTML ?? "";
const visible = () => host.innerHTML.includes("tool-recovery-panel");
const recordCount = () => host.querySelectorAll(".tool-recovery-panel__record-head").length;
async function clickDismiss(index: number) {
  const buttons = host.querySelectorAll<HTMLButtonElement>(".tool-recovery-panel__dismiss");
  if (!buttons[index]) throw new Error(`dismiss button ${index} not found`);
  await act(async () => {
    buttons[index].dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

// ── 验收①：按钮出现，点击即消，无残留占位 ─────────────────────────────
await renderPanel({ running: false, refreshKey: 0 });
ok(visible(), "two pending records render the fold-out");
ok(recordCount() === 2, "each interrupted record gets its own dismiss button");
ok(panelHtml().includes("tool-a1") && panelHtml().includes("tool-a2"), "both records render their tool name");

await clickDismiss(0);
ok(visible(), "dismissing one record keeps the fold-out for the rest");
ok(recordCount() === 1, "dismissed record leaves no residual placeholder");
ok(!panelHtml().includes("tool-a1") && panelHtml().includes("tool-a2"), "the remaining record is the undismissed one");

// ── 验收③：忽略是纯前端状态，不触发任何后端交互 ───────────────────────
const probesAfterDismiss = probeCalls;
const resolvesAfterDismiss = resolveCalls;
await clickDismiss(0);
ok(probeCalls === probesAfterDismiss && resolveCalls === resolvesAfterDismiss,
  "dismiss clicks fire zero bindings (no new turn, no transcript mutation)");

// ── 验收②：全部忽略后折叠块整体不显示 ─────────────────────────────────
ok(!visible(), "dismissing the last record hides the whole fold-out");
ok(host.querySelector(".tool-recovery-panel") === null, "the fold-out node is gone from the DOM, not just emptied");

// ── 同会话重探查：已忽略记录不复活 ─────────────────────────────────────
await renderPanel({ running: true, refreshKey: 0 }); // 用户发消息，面板隐藏（717 时序，回归保护）
await renderPanel({ running: false, refreshKey: 7 }); // turn 结束重探查，快照仍是两条
ok(!visible(), "re-probe with the same snapshot does not resurrect dismissed records");

// ── 会话重挂载：per-session in-memory，记录按设计重现 ──────────────────
await renderPanel({ running: false, refreshKey: 0, sessionKey: "k2" });
ok(visible() && recordCount() === 2, "a fresh session mount starts with a clean dismissed set");

await act(async () => { root.unmount(); });
host.remove();
dom.window.close();
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
