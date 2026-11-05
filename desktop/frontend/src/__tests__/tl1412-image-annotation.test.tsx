// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1412-image-annotation.test.tsx
//
// tl1412（P1，用户拍板）：图片查看器标注编辑器——画笔/文字/形状标注 + 保存 PNG
// 回填对话输入框（作为新附件），对标 zcode image-preview-editor。
//
// 可证伪断言（与任务书验收一一对应）：
//  ① 入口与取消：查看器工具栏「标注」按钮进入编辑态（canvas 叠加原图之上）；
//     Esc/取消按钮退回预览，原图 img src 不变；再次进入画布零残留；
//     标注态点背景不关闭（不丢进行中的标注会话）；预览态点背景照常关闭；
//  ② 三形态：画笔（折线点集提交）、矩形（strokeRect 几何）、箭头（主线+双箭头
//     线段）、圆圈（ellipse）、文字（点击落 textarea，Enter 提交 fillText）；
//     粗细三档透传（lineWidth 记录）；undo/redo/清空按钮态与保存门控一致
//     （零标注时保存禁用——空图不产生附件）；
//  ③ 导出合成：保存走新画布（natural 分辨率 drawImage + ctx.scale 显示空间→
//     图像空间，坐标与线宽同比放大）+ toDataURL PNG；合成结果经
//     imageAnnotationAttach 事件携带 dataUrl+文件名出编辑器，随后查看器关闭；
//  ④ 附件回填：Composer 订阅事件后 SavePastedImage 落盘→AttachmentDataURL
//     预览→附件 chip 进 active draft；重复事件去重不重复入草稿；
//     attachmentInputEnabled 关闭时不落盘且 toast 可见（不静默丢）。
//
// DOM/桥接模式复用 tl1289/composer-screenshot：JSDOM + css-stub-register +
// LocaleProvider(preloadLocale("zh")) + 录制型 2d context + 假 Image/toDataURL。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { requestAnnotatedImageAttachment, onAnnotatedImageAttachment } from "../lib/imageAnnotationAttach";
import type { AnnotatedImageAttachment } from "../lib/imageAnnotationAttach";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// ---- 录制型 2d context：所有断言读调用序列，不读像素 --------------------

export interface RecordingContext {
  calls: string[];
  reset: () => void;
}

let recording: RecordingContext | null = null;

function installRecordingContext(dom: JSDOM): RecordingContext {
  const calls: string[] = [];
  const ctx = {
    clearRect: () => calls.push("clearRect"),
    drawImage: (...args: unknown[]) => calls.push(`drawImage:${args.length}`),
    strokeRect: (x: number, y: number, w: number, h: number) =>
      calls.push(`strokeRect:${Math.round(x)},${Math.round(y)},${Math.round(w)},${Math.round(h)}`),
    beginPath: () => calls.push("beginPath"),
    moveTo: (x: number, y: number) => calls.push(`moveTo:${Math.round(x)},${Math.round(y)}`),
    lineTo: (x: number, y: number) => calls.push(`lineTo:${Math.round(x)},${Math.round(y)}`),
    stroke: () => calls.push("stroke"),
    fill: () => calls.push("fill"),
    arc: () => calls.push("arc"),
    ellipse: () => calls.push("ellipse"),
    fillText: (text: string) => calls.push(`fillText:${text}`),
    save: () => calls.push("save"),
    restore: () => calls.push("restore"),
    scale: (x: number, y: number) => calls.push(`scale:${x}x${y}`),
    getImageData: () => ({ data: new Uint8ClampedArray(4) }),
    putImageData: () => calls.push("putImageData"),
    // width 字段透传断言走 setter 记录
    set lineWidth(value: number) {
      calls.push(`lineWidth:${value}`);
    },
  };
  Object.defineProperty(dom.window.HTMLCanvasElement.prototype, "getContext", {
    configurable: true,
    value: () => ctx,
  });
  recording = { calls, reset: () => { calls.length = 0; } };
  return recording;
}

// 显示 800×600、natural 1600×1200 → 导出缩放恒为 2 倍（scale:2x2 可断言）。
const DISPLAY_W = 800;
const DISPLAY_H = 600;
const NATURAL_W = 1600;
const NATURAL_H = 1200;
const FINAL_DATA_URL = "data:image/png;base64,TL1412_FINAL";

function installImageStubs(dom: JSDOM) {
  class FakeImage {
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    set src(_value: string) {
      queueMicrotask(() => this.onload?.());
    }
    get naturalWidth() {
      return NATURAL_W;
    }
    get naturalHeight() {
      return NATURAL_H;
    }
  }
  Object.defineProperty(dom.window, "Image", { configurable: true, value: FakeImage });
  Object.defineProperty(globalThis, "Image", { configurable: true, value: FakeImage });
  Object.defineProperty(dom.window.HTMLImageElement.prototype, "clientWidth", {
    configurable: true,
    get: () => DISPLAY_W,
  });
  Object.defineProperty(dom.window.HTMLImageElement.prototype, "clientHeight", {
    configurable: true,
    get: () => DISPLAY_H,
  });
  Object.defineProperty(dom.window.HTMLCanvasElement.prototype, "getBoundingClientRect", {
    configurable: true,
    value: () => ({ left: 0, top: 0, width: DISPLAY_W, height: DISPLAY_H, right: DISPLAY_W, bottom: DISPLAY_H, x: 0, y: 0 }),
  });
  Object.defineProperty(dom.window.HTMLCanvasElement.prototype, "toDataURL", {
    configurable: true,
    value: () => FINAL_DATA_URL,
  });
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div><div class=\"chat-pane\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  // jsdom 的 activeElement/focus 探针缺 attachEvent（composer-screenshot 同款折中）。
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window, "matchMedia", {
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

function installBridgeApp(methods: Record<string, unknown>) {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        ...methods,
      },
    },
  } as never;
}

async function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(check: () => boolean, attempts = 20): Promise<void> {
  for (let i = 0; i < attempts; i++) {
    if (check()) return;
    await act(async () => {
      await flushTimers();
    });
  }
}

const IMG_URL = "data:image/png;base64,TL1412_SRC";

// 查看器宿主：onClose 真实翻转 open 状态（生产里宿主也是这样接的），
// 使「保存完成 → onClose」这条链路可断言（backdrop 真消失）。
const viewerControl = { close: () => {} };

function ViewerHost(): React.ReactElement {
  const [open, setOpen] = React.useState(true);
  viewerControl.close = () => setOpen(false);
  return <ImageViewer open={open} imageUrl={IMG_URL} imageName="shot.png" onClose={() => viewerControl.close()} />;
}

async function renderViewer(): Promise<void> {
  const rootEl = document.getElementById("root")!;
  const root = (renderViewer as unknown as { _root?: ReturnType<typeof createRoot> })._root ?? createRoot(rootEl);
  (renderViewer as unknown as { _root?: ReturnType<typeof createRoot> })._root = root;
  await act(async () => {
    root.render(<LocaleProvider><ViewerHost /></LocaleProvider>);
    for (let i = 0; i < 5; i++) await flushTimers();
  });
}

async function unmountViewer(): Promise<void> {
  const holder = renderViewer as unknown as { _root?: ReturnType<typeof createRoot> };
  if (holder._root) {
    const root = holder._root;
    holder._root = undefined;
    await act(async () => {
      root.unmount();
      await flushTimers();
    });
  }
}

// 指针手势：React 的 onPointerDown 系列按原生事件类型名分发，MouseEvent 载荷
// 带 clientX/clientY 即可（composer-screenshot 同款 PointerEvent=MouseEvent 折中）。
async function pointer(canvas: Element, type: "pointerdown" | "pointermove" | "pointerup", x: number, y: number): Promise<void> {
  await act(async () => {
    canvas.dispatchEvent(new window.MouseEvent(type, { bubbles: true, clientX: x, clientY: y, button: 0 }));
    await flushTimers();
  });
}

async function clickButton(label: string): Promise<HTMLButtonElement | null> {
  const button = document.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
  await act(async () => {
    button?.click();
    await flushTimers();
  });
  return button;
}

function editor(): Element | null {
  return document.querySelector(".image-annotation");
}

function editorCanvas(): Element | null {
  return document.querySelector(".image-annotation__canvas");
}

function viewerImage(): Element | null {
  return document.querySelector(".image-viewer-backdrop .image-viewer__image");
}

// 文字输入走 React props 直调（ask-card-layout 同款）：portal 容器里 React
// 合成 keydown/input 在 jsdom 下不可达，直调 handler 是同一契约的最短路径。
function reactProps(el: Element): Record<string, ((event: unknown) => void) | undefined> {
  const key = Object.keys(el).find((k) => k.startsWith("__reactProps"));
  return key ? (el as unknown as Record<string, Record<string, ((event: unknown) => void) | undefined>>)[key] : {};
}

async function typeIntoTextarea(text: string): Promise<HTMLTextAreaElement | null> {
  const ta = document.querySelector<HTMLTextAreaElement>(".image-annotation__text-input");
  if (!ta) return null;
  await act(async () => {
    reactProps(ta)?.onChange?.({ target: { value: text } });
    await flushTimers();
  });
  return ta;
}

async function pressEnter(ta: HTMLTextAreaElement): Promise<void> {
  await act(async () => {
    reactProps(ta)?.onKeyDown?.({ key: "Enter", shiftKey: false, preventDefault: () => {}, stopPropagation: () => {} });
    await flushTimers();
  });
}

const { ImageViewer } = await import("../components/ImageViewer");
const { LocaleProvider, preloadLocale } = await import("../lib/i18n");

console.log("\ntl1412 image annotation editor");

// ═══ ① 入口与取消 ═══
{
  const dom = installDom();
  installBridgeApp({});
  const rec = installRecordingContext(dom);
  installImageStubs(dom);
  await preloadLocale("zh");
  await renderViewer();

  const annotateBtn = document.querySelector<HTMLButtonElement>(".image-viewer__annotate");
  ok(annotateBtn !== null, "① 查看器工具栏渲染标注按钮");
  eq(viewerImage()?.getAttribute("src"), IMG_URL, "① 预览态原图 src 为源图");
  eq(annotateBtn?.getAttribute("aria-label"), "标注", "① 标注按钮带本地化标签");

  await act(async () => {
    annotateBtn!.click();
    await flushTimers();
  });
  ok(editor() !== null, "① 点击标注按钮进入编辑态");
  ok(editorCanvas() !== null, "① 编辑态渲染叠加 canvas");
  ok(document.querySelector(".image-viewer__annotate") === null, "① 编辑态隐藏标注按钮（避免重入）");
  ok(document.querySelector(".image-viewer__close") === null, "① 编辑态隐藏预览关闭键（Esc/取消分层退出）");

  // Esc 第一层：编辑态退回预览，原图不动
  await act(async () => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  ok(editor() === null, "① Esc 退出编辑态");
  ok(document.querySelector(".image-viewer-backdrop") !== null, "① Esc 只退编辑态，查看器仍开");
  eq(viewerImage()?.getAttribute("src"), IMG_URL, "① 取消后原图 src 不变（原图零改动）");

  // 再次进入：画布零残留（新一轮 repaint 只有 clearRect，无绘制调用）
  rec.reset();
  await act(async () => {
    document.querySelector<HTMLButtonElement>(".image-viewer__annotate")!.click();
    await flushTimers();
  });
  ok(editor() !== null, "① 可再次进入编辑态");
  ok(rec.calls.every((call) => call === "clearRect"), "① 重入后画布零残留（无历史绘制重放）");
  const saveBtn = document.querySelector<HTMLButtonElement>("button[aria-label=\"保存到输入框\"]");
  eq(saveBtn?.disabled, true, "① 零标注时保存禁用（空图不产附件）");

  // 标注态点背景不关；取消按钮回预览；预览态点背景照常关
  const backdrop = document.querySelector<HTMLElement>(".image-viewer-backdrop")!;
  await act(async () => {
    backdrop.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  ok(editor() !== null && document.querySelector(".image-viewer-backdrop") !== null, "① 标注态点背景不关闭（不丢会话）");
  await clickButton("取消");
  ok(editor() === null, "① 取消按钮退回预览");
  ok(document.querySelector(".image-viewer-backdrop") !== null, "① 取消后查看器仍开");
  await act(async () => {
    backdrop.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
  ok(document.querySelector(".image-viewer-backdrop") === null, "① 预览态点背景照常关闭（既有行为保持）");
  await unmountViewer();
}

// ═══ ② 三形态 + 粗细 + undo/redo/清空 ═══
{
  const dom = installDom();
  installBridgeApp({});
  const rec = installRecordingContext(dom);
  installImageStubs(dom);
  await preloadLocale("zh");
  await renderViewer();
  await act(async () => {
    document.querySelector<HTMLButtonElement>(".image-viewer__annotate")!.click();
    await flushTimers();
  });
  const canvas = editorCanvas()!;

  // 画笔：折线点集
  await pointer(canvas, "pointerdown", 100, 100);
  eq(editorCanvas()?.getAttribute("data-state"), "drawing", "② 画笔按下进入 drawing 态");
  await pointer(canvas, "pointermove", 150, 120);
  await pointer(canvas, "pointermove", 200, 160);
  await pointer(canvas, "pointerup", 200, 160);
  eq(editorCanvas()?.getAttribute("data-state"), "idle", "② 抬笔回 idle 态");
  ok(rec.calls.includes("moveTo:100,100") && rec.calls.includes("lineTo:150,120") && rec.calls.includes("lineTo:200,160"), "② 画笔折线按点集绘制（moveTo+lineTo 序列）");
  ok(rec.calls.includes("lineWidth:4"), "② 默认中档（4）透传到渲染（lineWidth:4）");
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"保存到输入框\"]")?.disabled, false, "② 提交画笔后保存解锁");

  // 粗细：切「粗」档后画第二笔，lineWidth 透传
  await clickButton("粗");
  await pointer(canvas, "pointerdown", 10, 10);
  await pointer(canvas, "pointermove", 60, 10);
  await pointer(canvas, "pointerup", 60, 10);
  ok(rec.calls.includes("lineWidth:8"), "② 粗档（8）透传到渲染（lineWidth:8）");

  // 矩形：切工具后拖拽
  await clickButton("矩形");
  await pointer(canvas, "pointerdown", 300, 300);
  await pointer(canvas, "pointermove", 400, 380);
  await pointer(canvas, "pointerup", 400, 380);
  ok(rec.calls.includes("strokeRect:300,300,100,80"), "② 矩形按拖拽极值绘制（strokeRect 300,300,100,80）");

  // 箭头：主线 + 双箭头线段
  await clickButton("箭头");
  await pointer(canvas, "pointerdown", 50, 300);
  await pointer(canvas, "pointermove", 250, 320);
  await pointer(canvas, "pointerup", 250, 320);
  ok(rec.calls.includes("moveTo:50,300") && rec.calls.includes("lineTo:250,320"), "② 箭头主线绘制");
  eq(rec.calls.filter((call) => call === "moveTo:250,320").length >= 2, true, "② 箭头端双翼线段绘制（双 moveTo 汇于端点）");

  // 圆圈
  await clickButton("圆圈");
  await pointer(canvas, "pointerdown", 500, 100);
  await pointer(canvas, "pointermove", 600, 200);
  await pointer(canvas, "pointerup", 600, 200);
  ok(rec.calls.includes("ellipse"), "② 圆圈走 ellipse 绘制");

  // 文字：切工具，点击落 textarea，Enter 提交
  await clickButton("文字");
  rec.reset();
  await pointer(canvas, "pointerdown", 100, 450);
  await pointer(canvas, "pointerup", 100, 450);
  let ta = document.querySelector<HTMLTextAreaElement>(".image-annotation__text-input");
  ok(ta !== null, "② 文字工具点击弹出锚定输入框");
  ta = await typeIntoTextarea("故障在这里");
  await pressEnter(ta!);
  ok(document.querySelector(".image-annotation__text-input") === null, "② Enter 提交后输入框收起");
  ok(rec.calls.includes("fillText:故障在这里"), "② 文字以 fillText 落画布");
  ok(rec.calls.includes("moveTo:100,450") === false, "② 文字点击不产生形状拖拽");

  // 零拖拽点击（形状工具）不提交退化形状：拖拽中草稿会绘制（可见反馈），
  // 但抬笔后 <3px 被 buildAnnotation 门卫丢弃——断言口径=抬笔后重绘尾段无矩形。
  await clickButton("矩形");
  const undoBtn = await clickButton("撤销");
  eq(undoBtn?.disabled, false, "② 有标注时撤销可用");
  rec.reset();
  await pointer(canvas, "pointerdown", 5, 5);
  await pointer(canvas, "pointerup", 5, 5);
  const tail = rec.calls.slice(rec.calls.lastIndexOf("clearRect") + 1);
  ok(!tail.includes("strokeRect:5,5,0,0"), "② 零拖拽点击不提交退化矩形（抬笔后重绘无 5,5,0,0 退化签名）");

  // undo/redo/清空 与保存门控
  await act(async () => {
    undoBtn!.click();
    await flushTimers();
  });
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"撤销\"]")?.disabled, false, "② 撤销一步后仍可继续撤销（还有存量标注）");
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"重做\"]")?.disabled, false, "② 撤销后重做解锁");
  // 连撤到空：撤销 4 次（当前存量=画笔2+矩形+箭头+圆圈+文字-已撤1 → 撤到 0 需 5 次）
  for (let i = 0; i < 5; i++) {
    await act(async () => {
      document.querySelector<HTMLButtonElement>("button[aria-label=\"撤销\"]")!.click();
      await flushTimers();
    });
  }
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"保存到输入框\"]")?.disabled, true, "② 撤空后保存重新禁用");
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"撤销\"]")?.disabled, true, "② 撤空后撤销禁用（历史底）");
  const redoBtn = await clickButton("重做");
  eq(redoBtn?.disabled, false, "② 撤空后重做可用（栈仍在）");
  await clickButton("重做");
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"保存到输入框\"]")?.disabled, false, "② 重做恢复标注，保存解锁");
  await clickButton("清空");
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"保存到输入框\"]")?.disabled, true, "② 清空后保存禁用");
  eq(document.querySelector<HTMLButtonElement>("button[aria-label=\"重做\"]")?.disabled, true, "② 清空后重做禁用（历史截断）");
  await unmountViewer();
}

// ═══ ③ Esc 分层（文字框 → 编辑态 → 预览）+ ③⑤ 导出合成与回填 ═══
{
  const dom = installDom();
  const savedWith: string[] = [];
  installBridgeApp({});
  const rec = installRecordingContext(dom);
  installImageStubs(dom);
  await preloadLocale("zh");

  // 订阅回填事件（编辑器出数据通道）——覆盖两个子场景，块尾统一退订
  const received: AnnotatedImageAttachment[] = [];
  const unsubscribe = onAnnotatedImageAttachment((a) => received.push(a));

  await renderViewer();
  await act(async () => {
    document.querySelector<HTMLButtonElement>(".image-viewer__annotate")!.click();
    await flushTimers();
  });
  const canvas = editorCanvas()!;

  // Esc 第一层：文字输入框开着 → 只收输入框
  await clickButton("文字");
  await pointer(canvas, "pointerdown", 80, 80);
  await pointer(canvas, "pointerup", 80, 80);
  let ta = document.querySelector<HTMLTextAreaElement>(".image-annotation__text-input");
  ok(ta !== null, "③ 文字输入框打开");
  await act(async () => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  ok(document.querySelector(".image-annotation__text-input") === null, "③ Esc 只关文字输入框");
  ok(editor() !== null, "③ 编辑态保持");
  // Esc 第二层：退回预览；第三层（预览关查看器）留给既有 Esc 语义
  await act(async () => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  ok(editor() === null && document.querySelector(".image-viewer-backdrop") !== null, "③ 第二层 Esc 退回预览（查看器不连关）");
  await unmountViewer();

  // 导出合成：画一笔 → 保存 → 断言新画布 drawImage + scale:2x2 + PNG dataUrl 出通道
  await renderViewer();
  await act(async () => {
    document.querySelector<HTMLButtonElement>(".image-viewer__annotate")!.click();
    await flushTimers();
  });
  const exportCanvas = editorCanvas()!;
  await pointer(exportCanvas, "pointerdown", 20, 20);
  await pointer(exportCanvas, "pointermove", 120, 60);
  await pointer(exportCanvas, "pointerup", 120, 60);
  rec.reset();
  await clickButton("保存到输入框");
  ok(received.length === 1, "⑤ 导出经 imageAnnotationAttach 事件出编辑器");
  eq(received[0]?.dataUrl, FINAL_DATA_URL, "⑤ 导出 dataUrl 为合成 PNG（toDataURL 产物）");
  eq(received[0]?.name, "shot.png", "⑤ 导出携带源图文件名");
  ok(rec.calls.includes("drawImage:5"), "③ 导出先在新画布 drawImage 原图（5 参调用）");
  ok(rec.calls.includes("scale:2x2"), "③ 导出经 ctx.scale(2,2) 把显示空间映射到 natural 分辨率（1600/800）");
  ok(rec.calls.includes("moveTo:20,20") && rec.calls.includes("lineTo:120,60"), "③ 导出重放标注笔画");
  ok(rec.calls.indexOf("scale:2x2") < rec.calls.indexOf("moveTo:20,20"), "③ 标注重放在 scale 变换内（坐标同比）");
  eq(Boolean(document.querySelector(".image-viewer-backdrop")), false, "⑤ 保存完成后查看器关闭（完成动作语义）");
  await unmountViewer();
  unsubscribe();
}

// ═══ ④ Composer 回填端到端 ═══
{
  const dom = installDom();
  const { ToastProvider } = await import("../lib/toast");
  const { Composer } = await import("../components/Composer");
  const savedImages: string[] = [];
  const previewed: string[] = [];
  installBridgeApp({
    SavePastedImage: async (dataUrl: string) => {
      savedImages.push(dataUrl);
      return ".reasonix/attachments/annotated-1.png";
    },
    AttachmentDataURL: async (path: string) => {
      previewed.push(path);
      return `data:image/png;base64,PREVIEW_OF_${path}`;
    },
  });
  installRecordingContext(dom);
  installImageStubs(dom);
  await preloadLocale("zh");

  const rootEl = document.getElementById("root")!;
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <div className="chat-pane">
            <Composer
              running={false}
              collaborationMode={"normal" as never}
              toolApprovalMode={"ask" as never}
              goal=""
              cwd="/repo"
              modelLabel="reasonix"
              imageInputEnabled={true}
              tabId="tl1412-tab"
              sessionKey="session:project:/repo:topic-a:session-a"
              onSend={() => {}}
              onCancel={async () => ({ discardedItemIds: [] })}
              onCycleMode={() => {}}
              onSetMode={() => {}}
              onSetCollaborationMode={() => {}}
              onSetToolApprovalMode={() => {}}
              onToggleYoloApprovalMode={() => {}}
              onClearGoal={() => {}}
              onSwitchModel={() => true}
              onSetEffort={() => {}}
              ready={true}
            />
          </div>
        </ToastProvider>
      </LocaleProvider>,
    );
    for (let i = 0; i < 5; i++) await flushTimers();
  });

  ok(document.querySelector(".composer-context__item") === null, "④ 初始草稿无附件");

  requestAnnotatedImageAttachment({ dataUrl: FINAL_DATA_URL, name: "annotated.png" });
  await waitFor(() => document.querySelector(".composer-context__item") !== null);
  ok(savedImages.includes(FINAL_DATA_URL), "④ SavePastedImage 落盘标注 PNG");
  eq(previewed[0], ".reasonix/attachments/annotated-1.png", "④ AttachmentDataURL 读取落盘路径生成预览");
  const thumb = document.querySelector<HTMLImageElement>(".composer-context__item img");
  eq(thumb?.getAttribute("src"), "data:image/png;base64,PREVIEW_OF_.reasonix/attachments/annotated-1.png", "④ 附件 chip 预览指向落盘文件");

  // 去重：同一产物重复派发不重复入草稿
  requestAnnotatedImageAttachment({ dataUrl: FINAL_DATA_URL, name: "annotated.png" });
  await act(async () => {
    await flushTimers();
  });
  eq(document.querySelectorAll(".composer-context__item").length, 1, "④ 重复事件去重（hash+source 双键）不重复入草稿");

  await act(async () => {
    root.unmount();
    await flushTimers();
  });
}

// ═══ ④b attachmentInputEnabled 关闭：不落盘 + toast 可见 ═══
{
  const dom = installDom();
  const { ToastProvider } = await import("../lib/toast");
  const { Composer } = await import("../components/Composer");
  const savedImages: string[] = [];
  installBridgeApp({
    SavePastedImage: async (dataUrl: string) => {
      savedImages.push(dataUrl);
      return ".reasonix/attachments/should-not-exist.png";
    },
    AttachmentDataURL: async () => "data:image/png;base64,SHOULD_NOT_PREVIEW",
  });
  installRecordingContext(dom);
  installImageStubs(dom);
  await preloadLocale("zh");

  const rootEl = document.getElementById("root")!;
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <div className="chat-pane">
            <Composer
              running={false}
              collaborationMode={"normal" as never}
              toolApprovalMode={"ask" as never}
              goal=""
              cwd="/repo"
              modelLabel="reasonix"
              imageInputEnabled={true}
              attachmentInputEnabled={false}
              tabId="tl1412-tab-disabled"
              sessionKey="session:project:/repo:topic-a:session-b"
              onSend={() => {}}
              onCancel={async () => ({ discardedItemIds: [] })}
              onCycleMode={() => {}}
              onSetMode={() => {}}
              onSetCollaborationMode={() => {}}
              onSetToolApprovalMode={() => {}}
              onToggleYoloApprovalMode={() => {}}
              onClearGoal={() => {}}
              onSwitchModel={() => true}
              onSetEffort={() => {}}
              ready={true}
            />
          </div>
        </ToastProvider>
      </LocaleProvider>,
    );
    for (let i = 0; i < 5; i++) await flushTimers();
  });

  requestAnnotatedImageAttachment({ dataUrl: FINAL_DATA_URL, name: "annotated.png" });
  await waitFor(() => document.querySelector(".toast--warn") !== null);
  ok(savedImages.length === 0, "④b 附件关闭时不落盘（零副作用）");
  const toastText = document.querySelector(".toast--warn .toast__text")?.textContent ?? "";
  ok(toastText.includes("未开启图片附件"), "④b toast 指明图片附件未开启（不静默丢）");

  await act(async () => {
    root.unmount();
    await flushTimers();
  });
}

console.log(`\ntl1412: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
