// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tool-recovery-panel-dismiss.test.tsx
//
// 任务 1061（中断的工具调用记录加「忽略」按钮）前端契约 + 任务 1068 返修。
//
// 钉住验收（1061 四条 + 1068 三修，零回归）：
//   ① 每条记录出现「忽略」按钮，点击即从块中移除，无残留占位（1061）；
//   ② 全部忽略后折叠块整体不渲染（1061）；
//   ③ 忽略是纯前端状态：点击不触发任何 binding 调用（不发起新一轮对话、
//      不改转录数据、不写会话事件日志）（1061）；
//   ④ 忽略集合 per-session（1068③）：同会话内重探查（running / refreshKey
//      变化）不复活已忽略记录；同一会话的另一 tab（不同 tabId、同一
//      sessionPath）忽略态不丢——1061 返修点（用户实测「点忽略→切其他 tab→
//      切回记录重现」；1061 组件内 state 在卸载重挂/切 tab 时会清零）；
//   ⑤ 不同会话互不影响：另一会话（sessionPath 不同）的记录照常渲染
//      （per-session 隔离语义）；
//   ⑥ 折叠态可点（1068②）：记录详情 details 未展开时忽略按钮已可见可点，
//      不需先展开「查看操作详情」；按钮挂全局 .btn--ghost.btn--small
//      （1068①，与协作中心「关闭」同族）；
//   ⑦ 后端结算后新快照（新 sessionPath/新 revision）如实再显示：旧快照的
//      忽略集合不跨快照继承——「新 turn 留下的中断如实显示」的 1061 语义
//      不被旧账拖累（per-snapshot 记账的语义对齐，717/turn-end-cleanup 家族契约）。

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
const store = await import("../lib/toolRecoveryDismissalStore");
const { __resetToolRecoveryDismissalsForTests } = store;

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

function snapshotWith(attemptIds: string[], sessionPath = "/s/a.jsonl", revision = "r1"): ToolRecoverySnapshot {
  return {
    silent: false, statistics: {}, sessionPath, runtimeEpoch: "e1", revision, retryEnabled: false,
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

type PanelProps = { running: boolean; refreshKey: number; sessionKey?: string; tabId?: string };
async function renderPanel(props: PanelProps) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        {/* key=sessionKey only: switching tabs (or re-probing after a turn) is the SAME
            surface instance re-rendering with a different tabId — exactly what Transcript
            does. 1061 keyed the panel by resolvedSessionKey alone; 1068 keeps that and
            moves the dismissal memory into the module-level per-snapshot store. */}
        <ToolRecoveryPanel key={props.sessionKey ?? "k1"} tabId={props.tabId ?? "tab-1"} sessionKey={props.sessionKey ?? "k1"} running={props.running} refreshKey={props.refreshKey} bindings={bindings} />
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

// ── 1068③ 同会话重探查：已忽略记录不复活（卸载重挂不清零） ────────────
await renderPanel({ running: true, refreshKey: 0 }); // 用户发消息，面板隐藏（717 时序，回归保护）
await renderPanel({ running: false, refreshKey: 7 }); // turn 结束重探查，快照仍是两条
ok(!visible(), "re-probe with the same snapshot does not resurrect dismissed records");

// ── 1068③ 同一会话的另一 tab：忽略态不丢（1061 返修点） ────────────────
// 前置：清掉前段遗留（tab-1 全忽略 + k2 无关会话），把 tab-1 单独放回「两条
// 待忽略」状态，再模拟用户在 tab-1 忽略 a1 后切到同会话 tab-2。
// 同一会话（同一 sessionPath）另一 tab = 另一个面板实例（不同 tabId、不同
// 挂载 key）探查同一份后端快照——1061 的组件内 state 在卸载重挂/切 tab 时会
// 清零（用户实测「点忽略→切其他 tab→切回记录重现」）；per-session 记账下
// 这份忽略集合必须命中（同 sessionPath 恒同一份集合，与实例无关）。
// 重置：换一个干净的会话（sessionPath 唯一），tab-1 先忽略 a1（折叠态点击），
// 再切到同会话 tab-2——1061 组件内 state 在这里会被卸载重挂清零。
probeResult.snapshot = snapshotWith(["a1", "a2"], "/s/tab-switch.jsonl");
await renderPanel({ running: false, refreshKey: 0, tabId: "tab-1" });
const tab1Probes = probeCalls;
await clickDismiss(0); // 折叠态（记录详情未展开）直接点——1068② 的可点性在此真实交互里验证
ok(visible() && recordCount() === 1 && !panelHtml().includes("tool-a1") && panelHtml().includes("tool-a2"),
  "tab-1 dismisses in the collapsed record head row with no residual placeholder");
ok(probeCalls === tab1Probes, "tab-1 dismiss still fires zero bindings");

await renderPanel({ running: false, refreshKey: 0, tabId: "tab-2", sessionKey: "k1-tab2" });
ok(visible() && recordCount() === 1 && !panelHtml().includes("tool-a1") && panelHtml().includes("tool-a2"),
  "the other tab of the same session renders the same dismissed set (a1 dismissed on tab-1 survives the switch)");
await clickDismiss(0);
ok(!visible(), "dismissing the remaining record on the other tab hides the fold-out too");
await renderPanel({ running: false, refreshKey: 3, tabId: "tab-1" });
ok(!visible(), "switching back to tab-1: the dismissed set survives the tab switch (1068 regression)");

// ── 1068⑦ 后端结算后新快照（新 revision）如实再显示 ───────────────────
// per-session 记账：结算后的新快照（新 revision）不继承旧快照的忽略集合
// ——「新 turn 留下的中断如实显示」的 1061 语义不被旧账拖累。
probeResult.snapshot = snapshotWith(["a1", "a2"], "/s/new-turn.jsonl", "r2");
await renderPanel({ running: false, refreshKey: 5 });
ok(visible() && recordCount() === 2,
  "a settled-then-revised snapshot (new revision) re-shows its records: the old dismissed set does not leak across snapshots");

// ── 1068⑤ 不同会话互不影响（per-session 隔离） ─────────────────────────
probeResult.snapshot = snapshotWith(["a1", "a2"], "/s/b.jsonl");
await renderPanel({ running: false, refreshKey: 0, sessionKey: "k2", tabId: "tab-1" });
ok(visible() && recordCount() === 2, "a different session starts with a clean dismissed set");

// ── 1068② 折叠态可点 + 1068① 复用全局 ghost 族 ────────────────────────
// 记录详情 details 未展开时忽略按钮已在记录行右侧可见可点——上面每一次
// clickDismiss 都发生在未展开状态，这里再钉一次结构与类名契约。
const firstHead = host.querySelector(".tool-recovery-panel__record-head");
const firstDismiss = firstHead?.querySelector<HTMLButtonElement>("button.tool-recovery-panel__dismiss");
ok(Boolean(firstDismiss), "the dismiss button sits on the collapsed record head row");
ok(firstHead?.querySelector("details")?.hasAttribute("open") !== true,
  "the per-record details fold-out is collapsed when the dismiss button is clickable");
ok(firstDismiss?.className.split(/\s+/).includes("btn--ghost") === true
  && firstDismiss?.className.split(/\s+/).includes("btn--small") === true,
  "the dismiss button reuses the global btn--ghost btn--small family (task 591)");

// 收尾：清理模块级集合，避免对其他串行 harness 的隐性泄漏（本 harness 自身
// 到此为止不再断言）。
await act(async () => { root.unmount(); });
__resetToolRecoveryDismissalsForTests();
host.remove();
dom.window.close();
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
