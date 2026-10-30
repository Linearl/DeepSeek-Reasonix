// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl933-workflow-viewport.test.tsx
// 任务 tl933 —— 686 工作流面板视口交互（pan / 缩放 / 持久化）行为级验收：
//   ② 视角拖移：长按空白处拖动 = 拨 scrollLeft/Top；节点上指针不启动手势
//      （节点交互优先）；长按生效前拖动超差 = 取消（点按不误平移）。
//   ③ 缩放：Ctrl+滚轮以光标为锚缩放（Chrome 式），范围 25%~300%；无 Ctrl
//      滚轮不接管（既有原生滚动保留）。
//   ④ 持久化：视角+缩放随面板偏好存 localStorage，重开面板恢复。
// 纯函数面（缩放数学/边界钳制/偏好形状校验）直接覆盖，不需要 DOM。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import assert from "node:assert/strict";
import { WorkflowPanel } from "../components/WorkflowPanel";
import { LocaleProvider } from "../lib/i18n";
import { WORKFLOW_PAN_LONG_PRESS_MS, WORKFLOW_PAN_SLOP_PX, exceedsPanSlop } from "../lib/workflowViewport";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected;
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── 纯函数面：长按手势判定 ──────────────────────────────────────────
console.log("\ntl933 workflow viewport: pan + anchored zoom + persistence");
{
  ok(WORKFLOW_PAN_LONG_PRESS_MS > 100, "long-press window is a real hold (>100ms, not a click)");
  ok(exceedsPanSlop(WORKFLOW_PAN_SLOP_PX + 1, 0) === true, "movement beyond the slop radius cancels the gesture");
  ok(exceedsPanSlop(3, 4) === false, "movement within the slop radius keeps the gesture armed");
  ok(exceedsPanSlop(0, 0) === false, "no movement never trips the slop");
}

// ── JSDOM 挂载 harness ──────────────────────────────────────────────
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

interface PointerInit {
  pointerId: number;
  button: number;
  clientX: number;
  clientY: number;
  target?: EventTarget | null;
}
/** jsdom 无 PointerEvent：合成一个带指针属性的冒泡事件（React 读原生属性）。 */
function firePointer(el: Element, type: string, init: PointerInit) {
  const evt = new window.Event(type, { bubbles: true, cancelable: true });
  Object.assign(evt, init);
  el.dispatchEvent(evt);
}

/** jsdom 无布局：把 scrollLeft/Top 换成实例存取器（组件写什么读回什么）。 */
function stubScroll(el: HTMLElement) {
  const state = { scrollLeft: 0, scrollTop: 0 };
  Object.defineProperty(el, "scrollLeft", {
    configurable: true,
    get: () => state.scrollLeft,
    set: (v: number) => { state.scrollLeft = v; },
  });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => state.scrollTop,
    set: (v: number) => { state.scrollTop = v; },
  });
}

async function mountPanel(): Promise<{ root: Root; view: HTMLElement }> {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <WorkflowPanel open onClose={() => {}} />
      </LocaleProvider>,
    );
  });
  const view = document.querySelector<HTMLElement>(".wf-panel__canvas-scroll");
  if (!view) throw new Error("canvas scroll container did not render");
  stubScroll(view);
  return { root, view };
}

const holdMs = WORKFLOW_PAN_LONG_PRESS_MS + 120;

// ── ② 视角拖移：长按空白拖动拨视口 ──────────────────────────────────
{
  const { root, view } = await mountPanel();
  const local = { x: 200, y: 150 };

  // 空白处按下 → 长按生效（--panning 光标态）→ 拖动 → 视口跟随（反向）。
  await act(async () => {
    firePointer(view, "pointerdown", { pointerId: 1, button: 0, clientX: local.x, clientY: local.y });
  });
  await act(async () => { await sleep(30); });
  await act(async () => {
    firePointer(view, "pointermove", { pointerId: 1, button: 0, clientX: local.x + 3, clientY: local.y + 2 });
  });
  eq(view.scrollLeft, 0, "no pan before the long-press fires (slop-sized jiggle is not a drag)");
  await act(async () => { await sleep(holdMs); });
  ok(view.className.includes("wf-panel__canvas-scroll--panning"), "long-press activates the panning cursor state");
  await act(async () => {
    firePointer(view, "pointermove", { pointerId: 1, button: 0, clientX: local.x + 43, clientY: local.y + 42 });
  });
  eq(view.scrollLeft, -40, "dragging right pans the viewport left (scrollLeft follows the drag)");
  eq(view.scrollTop, -40, "dragging down pans the viewport up (scrollTop follows the drag)");
  await act(async () => {
    firePointer(view, "pointerup", { pointerId: 1, button: 0, clientX: local.x + 43, clientY: local.y + 42 });
  });
  ok(!view.className.includes("wf-panel__canvas-scroll--panning"), "releasing the pointer ends the pan gesture");

  // 节点交互优先：指针落在节点上，长按+拖动不拨视口。
  const node = view.querySelector<HTMLElement>(".wf-node");
  ok(Boolean(node), "fixture renders nodes on the canvas");
  if (node) {
    await act(async () => {
      firePointer(node, "pointerdown", { pointerId: 2, button: 0, clientX: 60, clientY: 60 });
    });
    await act(async () => { await sleep(holdMs); });
    await act(async () => {
      firePointer(node, "pointermove", { pointerId: 2, button: 0, clientX: 120, clientY: 130 });
    });
    eq(view.scrollLeft, -40, "pointer starting on a node never pans the viewport (node interaction wins)");
    await act(async () => {
      firePointer(node, "pointerup", { pointerId: 2, button: 0, clientX: 120, clientY: 130 });
    });
  }

  // 长按生效前拖走超差 = 取消手势（点按不会误平移）。
  await act(async () => {
    firePointer(view, "pointerdown", { pointerId: 3, button: 0, clientX: 100, clientY: 100 });
  });
  await act(async () => {
    firePointer(view, "pointermove", { pointerId: 3, button: 0, clientX: 100 + WORKFLOW_PAN_SLOP_PX + 10, clientY: 100 });
  });
  await act(async () => { await sleep(holdMs); });
  await act(async () => {
    firePointer(view, "pointermove", { pointerId: 3, button: 0, clientX: 200, clientY: 200 });
  });
  eq(view.scrollLeft, -40, "a quick drag that outruns the long-press is cancelled (no accidental pan)");
  await act(async () => {
    firePointer(view, "pointerup", { pointerId: 3, button: 0, clientX: 200, clientY: 200 });
  });

  await act(async () => { root.unmount(); });
}

// ── ③ 缩放（commit 3 接续）──────────────────────────────────────────
// ── ④ 持久化（commit 4 接续）────────────────────────────────────────

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
