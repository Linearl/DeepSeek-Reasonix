// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1777-context-strip-budget.test.tsx
//
// tl1777 输入框溢出复发（tl1578 后新形态）——验收钉：
//   复现=侧栏展开+图片输入+多行文本时内容溢出卡外。取证结论（两嫌疑均被
//   代码级重建）：
//   ① 嫌疑「图片异步加载完成后 richInput.scrollHeight 变化不触发重测」字面
//     证伪：onPaste 全分支 preventDefault（图片二进制永不进 contentEditable），
//     图片=attachments（独立 state），渲染为卡外 .composer-context chip（与
//     卡片同级、位于卡上方），缩略图是固定盒+object-fit:cover，<img> onLoad
//     不改变盒高——richInput.scrollHeight 只随文本/宽度变化，触发面已全覆盖。
//   ② 实质机制：「图片加载完成」对应的真实异步几何事件=previewUrl 回填
//     （chip 变体切换 196×64 → image-only 76×76 + previewUrl 前置重排），
//     侧栏收窄同时改变条内换行数——条高变化不在任何重测触发面、不入任何
//     高度预算。wrap（条+卡）总高失控的出口=footer（flex:0 0 auto 不可收缩）
//     溢出中栏网格行（tl1578 形态②同一溢出通道），statusbar 掩码只盖状态栏
//     本身，条带以下/下拉区域仍可叠印。
//   修法（复用既有机制，非重做测量链）：条高实测（offsetHeight+marginBottom，
//   同 meta 增量「早退前持久化」判例）入 composer 纵向预算（窗口预算−条高），
//   输入测量与卡高写手两消费方共用；条挂 ResizeObserver 走共用重测路径
//   （metaObserver 同构）；挂载/卸载经 hasContextArea 键强制重测（清零/建档）。
//   jsdom 语义：条不在场或 offsetHeight=0 → 预算=composerMaxHeight() → 既有
//   读数逐字节不变（tl1578/tl1508/tl1522 钉零回退的保证）。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { composerReservedHeight, resolveComposerContentSizing } from "../lib/composerSizing";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
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

// 共用重测路径把测量包在 rAF 里；jsdom 的 rAF 是 ~16ms 定时器，setTimeout(0)
// 等不到——与 tl1578 套件同法，直接泵两帧。
function flushRaf(): Promise<void> {
  return new Promise((resolve) => {
    window.requestAnimationFrame(() => window.requestAnimationFrame(() => resolve()));
  });
}

async function waitFor(predicate: () => boolean, label: string): Promise<void> {
  const deadline = Date.now() + 5000;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error(`waitFor timeout: ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

type ROCallback = (entries: unknown[], observer: unknown) => void;

class TestResizeObserver {
  static instances: TestResizeObserver[] = [];
  callback: ROCallback;
  targets = new Set<Element>();
  constructor(callback: ROCallback) {
    this.callback = callback;
    TestResizeObserver.instances.push(this);
  }
  observe(target: Element) { this.targets.add(target); }
  unobserve(target: Element) { this.targets.delete(target); }
  disconnect() { this.targets.clear(); TestResizeObserver.instances = TestResizeObserver.instances.filter((i) => i !== this); }
}

function fireResize(target: Element) {
  for (const instance of TestResizeObserver.instances) {
    if (instance.targets.has(target)) {
      instance.callback([{ target }], instance);
    }
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
  TestResizeObserver.instances = [];
  globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
  // jsdom 默认 innerHeight=768 → composerMaxHeight=min(360, floor(768*0.4))=307
  // （tl1578 纯函数钉用 307 正是此因）。本套件钉「360 窗口预算被条高让位」
  // 的形态——用户复现窗口（高窗口+卡在预算上沿+条再叠高）正是 360 为绑定
  // 约束的空间，故覆盖 innerHeight=900 使窗口预算回到 360，算术可读。
  Object.defineProperty(dom.window, "innerHeight", { configurable: true, value: 900 });
  Object.defineProperty(dom.window, "innerWidth", { configurable: true, value: 1280 });
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeListener() {},
      addListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

// 附件桥：SavePastedImage（粘贴图落盘）+ AttachmentDataURL（previewUrl 回填
// ——即用户口径「图片加载完成」的真实异步事件面）。与 composer-image-capability
// 套件同一 stub 形态。
function installBridgeApp(methods: Record<string, unknown>) {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        ...methods,
      },
      Directory: {
        ListAddressableSessions: async () => [],
      },
    },
  };
}

type ComposerProps = Parameters<typeof Composer>[0];

async function renderComposer(props: Partial<ComposerProps> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const currentProps: ComposerProps = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "DeepSeek-R1",
    imageInputEnabled: true,
    tabId: "single-surface-tab",
    onSend: () => {},
    onCancel: async () => ({ discardedItemIds: [] }),
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
  return { root };
}

function mockOffsetHeight(el: HTMLElement, value: number) {
  Object.defineProperty(el, "offsetHeight", { configurable: true, get: () => value });
}

function cardHeight(): string | null {
  const card = document.querySelector(".composer-card") as HTMLElement | null;
  return card?.style.getPropertyValue("--composer-height") ?? null;
}

function contextStrip(): HTMLElement | null {
  return document.querySelector(".composer-context") as HTMLElement | null;
}

function imagePasteEvent(file: File): Event {
  const event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    configurable: true,
    value: {
      files: [file],
      items: [],
      types: [file.type],
      getData: () => "",
    },
  });
  return event;
}

console.log("\ntl1777 composer context strip budget");

{
  const dom = installDom();
  // resized 模式入口：手定 340（窗口预算 360 之下）经 layoutPreferences.v1 落盘，
  // 挂载即水合（tl1578 form3 同法）。jsdom innerHeight 1024 → composerMaxHeight
  // =min(360, floor(1024*0.4))=360。
  localStorage.setItem("reasonix.layoutPreferences.v1", JSON.stringify({ sizes: { composerHeight: 340 } }));
  installBridgeApp({
    SavePastedImage: async () => ".reasonix/attachments/mock.png",
    AttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
  });
  const { root } = await renderComposer();

  const textarea = document.querySelector(".composer__input") as HTMLTextAreaElement | null;
  ok(textarea !== null, "tl1777: composer mounts in resized mode with textarea");

  // 基线（条不在场）：预算=窗口预算 → 卡=340，既有读数逐字节不变。
  eq(cardHeight(), "340px", "tl1777: no context strip keeps the saved 340px card byte-identically");

  // 粘贴图片 → 落盘+previewUrl 回填 → chip（image-only 变体）上屏，条挂载。
  const file = new File(["img"], "photo.png", { type: "image/png", lastModified: 1 });
  await act(async () => {
    textarea!.dispatchEvent(imagePasteEvent(file));
    await flushTimers();
    await flushTimers();
  });
  await waitFor(() => Boolean(document.querySelector(".composer-context__thumb img")), "attachment chip with previewUrl");
  const strip = contextStrip();
  ok(strip !== null, "tl1777: context strip mounts above the card after image attach");

  // 条在场但未实测（jsdom offsetHeight 恒 0）→ 预算不变 → 卡仍 340。
  eq(cardHeight(), "340px", "tl1777: unmeasured strip keeps the card byte-identically (jsdom early-return face)");

  // 图片加载完成后条高实测（image-only 76px）→ RO 触发共用重测 → 预算
  // 360−76=284 → 卡让位 340→284。修前：无观察者、条高不入预算，此值恒 340（红）。
  mockOffsetHeight(strip!, 76);
  await act(async () => {
    fireResize(strip!);
    await flushRaf();
    await flushTimers();
  });
  eq(cardHeight(), "284px", "tl1777: measured strip height feeds the vertical budget and the card yields (360-76=284)");

  // 触发面可重复：侧栏收窄/多 chip 换行使条再长（mock 152）→ RO 仍在元素上
  // （React 复用同一 DOM 节点，重渲染不丢观察者）→ 卡再让位 360−152=208。
  mockOffsetHeight(strip!, 152);
  await act(async () => {
    fireResize(strip!);
    await flushRaf();
    await flushTimers();
  });
  eq(cardHeight(), "208px", "tl1777: the strip observer survives re-renders and re-fires the shared re-measure (360-152=208)");

  // 移除附件 → 条卸载 → hasContextArea 键重测 → 实测归零 → 预算回收 → 卡回 340。
  const removeTrigger = document.querySelector(".composer-context__remove") as HTMLButtonElement | null;
  ok(removeTrigger !== null, "tl1777: chip remove trigger present");
  await act(async () => {
    removeTrigger!.click();
    await flushTimers();
    await flushTimers();
  });
  await waitFor(() => contextStrip() === null, "strip unmounts after removal");
  await act(async () => {
    await flushRaf();
    await flushTimers();
  });
  eq(cardHeight(), "340px", "tl1777: removing the attachment frees the budget and restores the saved 340px card");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// 纯函数钉：预算让位的 sizing 数学（与组件级读数同式同值——卡=预算上限、
// 输入=预算−预留，226+58=284 与 mounted card 一致；manual 340 只在下限侧）。
{
  const budgeted = resolveComposerContentSizing({
    contentHeight: 0,
    manualLogicalHeight: 340,
    maxLogicalHeight: 284,
    reservedHeight: composerReservedHeight(58, 0),
  });
  eq(budgeted.inputHeight, 226, "tl1777 (pure): budgeted max caps the input height 282→226 (284-58)");
  eq(budgeted.logicalHeight, 284, "tl1777 (pure): logical card height yields to the budget (matches the mounted card)");
  // 字节兼容钉：条不在场（预算=窗口预算）时与旧路径同值——manual 340 → 卡 340。
  const legacy = resolveComposerContentSizing({
    contentHeight: 0,
    manualLogicalHeight: 340,
    maxLogicalHeight: 360,
    reservedHeight: composerReservedHeight(58, 0),
  });
  eq(legacy.logicalHeight, 340, "tl1777 (pure): zero strip keeps the legacy max path (340) byte-identically");
}

console.log(`\ntl1777 composer context strip budget: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
