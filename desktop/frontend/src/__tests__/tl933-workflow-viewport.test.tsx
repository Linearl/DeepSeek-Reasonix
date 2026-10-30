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
import {
  anchoredScroll,
  clampZoom,
  exceedsPanSlop,
  WORKFLOW_PAN_LONG_PRESS_MS,
  WORKFLOW_PAN_SLOP_PX,
  WORKFLOW_ZOOM_DEFAULT,
  WORKFLOW_ZOOM_MAX,
  WORKFLOW_ZOOM_MIN,
  zoomFactorFromWheel,
} from "../lib/workflowViewport";

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

// ── ③ 纯函数面：缩放数学 ────────────────────────────────────────────
{
  eq(clampZoom(0.1), WORKFLOW_ZOOM_MIN, "zoom below 25% clamps to the floor");
  eq(clampZoom(9), WORKFLOW_ZOOM_MAX, "zoom above 300% clamps to the ceiling");
  eq(clampZoom(Number.NaN), WORKFLOW_ZOOM_DEFAULT, "non-finite zoom falls back to the default");
  ok(zoomFactorFromWheel(-100, 0) > 1, "wheel up zooms in (factor above 1)");
  ok(zoomFactorFromWheel(100, 0) < 1, "wheel down zooms out (factor below 1)");
  ok(zoomFactorFromWheel(-1, 1) === zoomFactorFromWheel(-16, 0), "line deltaMode converts to pixel deltas");
  // 锚点保持：锚点下的内容点缩放前后是同一个点。
  const contentPoint = 40 + 130;
  const ratio = 1.5;
  eq(anchoredScroll(40, ratio, 130) + 130, contentPoint * ratio, "the content point under the cursor stays put after zoom");
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

interface WheelInit {
  clientX: number;
  clientY: number;
  deltaY: number;
  deltaMode?: number;
  ctrlKey?: boolean;
}
function fireWheel(el: Element, init: WheelInit) {
  const evt = new window.WheelEvent("wheel", { bubbles: true, cancelable: true, ...init });
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

// ── ③ 缩放：Ctrl+滚轮以光标为锚（Chrome 式），25%~300% ──────────────
{
  globalThis.WheelEvent = dom.window.WheelEvent;
  const { root, view } = await mountPanel();
  const canvas = view.querySelector<HTMLElement>(".wf-panel__canvas");
  const frame = view.querySelector<HTMLElement>(".wf-panel__canvas-frame");
  if (!canvas || !frame) throw new Error("canvas/frame did not render");

  // 基线：zoom=1，transform 无缩放。
  eq(canvas.style.transform, "scale(1)", "canvas starts unscaled");

  // Ctrl+上滚 @ (130,90)：以光标为锚放大，锚点下内容点不动。
  view.scrollLeft = 40;
  view.scrollTop = 30;
  const expectedNext = clampZoom(WORKFLOW_ZOOM_DEFAULT * zoomFactorFromWheel(-100, 0));
  await act(async () => {
    fireWheel(view, { clientX: 130, clientY: 90, deltaY: -100, ctrlKey: true });
  });
  const ratio = expectedNext / WORKFLOW_ZOOM_DEFAULT;
  eq(canvas.style.transform, `scale(${expectedNext})`, "ctrl+wheel scales the canvas by the wheel factor");
  eq(view.scrollLeft, anchoredScroll(40, ratio, 130), "zoom keeps the content point under the cursor anchored (x)");
  eq(view.scrollTop, anchoredScroll(30, ratio, 90), "zoom keeps the content point under the cursor anchored (y)");
  const frameW = Number.parseFloat(frame.style.width);
  const canvasW = Number.parseFloat(canvas.style.width);
  ok(Math.abs(frameW / canvasW - expectedNext) < 1e-9, "the frame tracks the scaled size (real scrollable range)");

  // 无 Ctrl 不接管：普通滚轮保持原生滚动（缩放不变）。
  await act(async () => {
    fireWheel(view, { clientX: 130, clientY: 90, deltaY: -100 });
  });
  eq(canvas.style.transform, `scale(${expectedNext})`, "plain wheel (no ctrl) never zooms — native scroll stays");

  // 边界：连滚 40 次放大顶到 300%，再连滚 80 次缩小落回 25%。
  for (let i = 0; i < 40; i += 1) {
    await act(async () => {
      fireWheel(view, { clientX: 10, clientY: 10, deltaY: -120, ctrlKey: true });
    });
  }
  eq(canvas.style.transform, `scale(${WORKFLOW_ZOOM_MAX})`, "zoom caps at 300% after repeated zoom-in");
  for (let i = 0; i < 80; i += 1) {
    await act(async () => {
      fireWheel(view, { clientX: 10, clientY: 10, deltaY: 120, ctrlKey: true });
    });
  }
  eq(canvas.style.transform, `scale(${WORKFLOW_ZOOM_MIN})`, "zoom floors at 25% after repeated zoom-out");
  ok(Number.isFinite(view.scrollLeft) && Number.isFinite(view.scrollTop), "anchored scrolling stays finite at both bounds");

  await act(async () => { root.unmount(); });
}

// ── ④ 持久化（commit 4 接续）────────────────────────────────────────

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
