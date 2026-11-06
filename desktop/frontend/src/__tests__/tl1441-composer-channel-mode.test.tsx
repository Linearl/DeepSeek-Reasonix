// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1441-composer-channel-mode.test.tsx
// tl1441 ②（Composer 挂群聊模式）harness——同一组件双态的载体验收：
//   ① 三档隐藏（验收 3「零展示」）：channelMode 下 权限档（approval modebar/
//      select）、模型槽位（ModelSwitcher+上下文环+effort 整块）、胶囊、+ 菜单、
//      分屏目标、计划/目标徽章、改写草稿钮 全部结构性不渲染（DOM 零命中，
//      不依赖挂载点不传 props——传满 props 也隐藏）；
//   ② 通用能力在场：表情/历史提示词/快捷指令菜单/发送键；
//   ③ 发送桥接：Enter → onSend(displayText)（挂载点注入 channel_send 的缝）；
//   ④ draft 按 channel 分键：sessionKey=channel:<ref> 持久化往返——同键重挂
//      草稿恢复、异键重挂草稿独立（composer 既有 draftKey 惯例的复用证明）；
//   ⑤ 双态零回归：非 channelMode 的既有渲染面不感知本件（approval bar 照常）；
//   ⑥ 源码钉：Composer channelMode 门控锚 + ChannelWorkspacePanel 挂载锚。
// Go 侧零改动；本件纯前端面。

import { JSDOM } from "jsdom";
import * as React from "react";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Node: dom.window.Node,
  HTMLElement: dom.window.HTMLElement,
  HTMLTextAreaElement: dom.window.HTMLTextAreaElement,
  HTMLInputElement: dom.window.HTMLInputElement,
  Event: dom.window.Event,
  CustomEvent: dom.window.CustomEvent,
  KeyboardEvent: dom.window.KeyboardEvent,
  InputEvent: dom.window.InputEvent,
  MouseEvent: dom.window.MouseEvent,
  PointerEvent: dom.window.MouseEvent,
  MutationObserver: dom.window.MutationObserver,
  File: dom.window.File,
  FileReader: dom.window.FileReader,
  localStorage: dom.window.localStorage,
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
  cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
// composer-quick-command-bang 同款度量 stub：菜单行虚拟化挂载依赖真实度量，
// jsdom 全 0 会把行裁剪到不挂载（sizer 有高、行 0）。
function menuRect(height: number): DOMRect {
  return { width: 320, height, top: 400, bottom: 400 + height, left: 10, right: 330, x: 10, y: 400, toJSON: () => ({}) } as DOMRect;
}
Object.defineProperty(dom.window.HTMLElement.prototype, "getBoundingClientRect", {
  configurable: true,
  value(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return menuRect(360);
    if (this.classList.contains("slashmenu__row")) return menuRect(34);
    return menuRect(0);
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetWidth", {
  configurable: true,
  get(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return 320;
    if (this.classList.contains("slashmenu__row")) return 300;
    return 0;
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetHeight", {
  configurable: true,
  get(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return 360;
    if (this.classList.contains("slashmenu__row")) return 34;
    return 0;
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
window.matchMedia = (() => ({
  matches: true,
  media: "(prefers-reduced-motion: reduce)",
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeListener() {},
  dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { Composer } = await import("../components/Composer");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
type ComposerProps = Parameters<typeof Composer>[0];
type CollaborationMode = import("../lib/types").CollaborationMode;
type ToolApprovalMode = import("../lib/types").ToolApprovalMode;
type QuickCommandEntry = import("../lib/settingsViewTypes").QuickCommandEntry;

window.localStorage.clear();
// 草稿跨重启持久化是 opt-in（App 从 settings.experimentalComposerDraft 打开；
// 铁律 2 默认关）。分键验收走持久化路径 = 最强面；内存路径（同挂载换键）
// 由 Composer 既有 draftKey 切换链承担。
const { setComposerDraftPersistenceEnabled } = await import("../lib/composerDraftPersistence");
setComposerDraftPersistenceEnabled(true);

let passed = 0;
function ok(cond: unknown, label: string): void {
  if (!cond) throw new Error(label);
  passed += 1;
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }

const rootEl = dom.window.document.getElementById("root")!;
const root = createRoot(rootEl);

const noop = () => undefined;
function baseProps(overrides: Partial<ComposerProps> = {}): ComposerProps {
  return {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    modelLabel: "mimo-v2.6",
    onSend: () => {},
    onCancel: () => Promise.resolve({ receipt: { id: "", status: "rejected", reason: "x" }, restoredText: "", restoredQueuedIDs: [] } as never),
    onCycleMode: noop,
    onSetMode: noop,
    onSetCollaborationMode: noop,
    onSetToolApprovalMode: noop,
    onToggleYoloApprovalMode: () => Promise.resolve(true),
    onClearGoal: noop,
    onPauseGoal: noop,
    onResumeGoal: noop,
    onSwitchModel: () => true,
    onSetEffort: noop,
    ready: true,
    ...overrides,
  };
}

async function paint(props: ComposerProps): Promise<void> {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer {...props} />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flush();
  });
}

function setNativeValue(el: HTMLTextAreaElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value")!.set!;
  setter.call(el, value);
}

// composer-quick-command-bang 同款输入驱动：focus + 原生 setter + 选区 + InputEvent
// + keyup（React 19 受控 tracker 绕过 + token 扫描依赖的事件面）。
async function typeText(el: HTMLTextAreaElement, value: string): Promise<void> {
  await act(async () => {
    el.focus();
    setNativeValue(el, value);
    el.setSelectionRange(value.length, value.length);
    el.dispatchEvent(new dom.window.InputEvent("input", { bubbles: true, data: value, inputType: "insertText", isComposing: false }));
    el.dispatchEvent(new dom.window.KeyboardEvent("keyup", { key: "!", bubbles: true }));
    await flush();
  });
}

// ── ① 三档隐藏：channelMode 结构性零展示（props 传满也隐藏）──────────────
const quickCommands: QuickCommandEntry[] = [
  { title: "日报", text: "请生成今日日报", enabled: true },
  { title: "周报", text: "请生成本周周报", enabled: true },
];
const hiddenProbeProps = baseProps({
  channelMode: true,
  sessionKey: "channel:ch_probe",
  historyPickerEnabled: true,
  quickCommands,
  onInsertQuickCommand: noop,
  // 故意把会话专属 props 传满——隐藏必须是结构性门控，不是「没传就不在」：
  effort: { id: "high", name: "High" },
  qualityFloor: "delivery",
  autopilotEnabled: true,
  splitActive: true,
  showContextWindowRing: true,
});
await paint(hiddenProbeProps);
const wrap = () => dom.window.document.querySelector(".composer-wrap")!;
ok(wrap(), "channel-mode composer mounts");
ok(!dom.window.document.querySelector(".composer-modebar--approval"), "permission tier bar hidden (验收 3 权限档零展示)");
ok(!dom.window.document.querySelector(".composer-approval-select"), "permission tier select hidden too");
ok(!dom.window.document.querySelector(".composer-meta__control--model"), "model slot hidden (ModelSwitcher 整块，验收 3)");
ok(!dom.window.document.querySelector(".composer-effort-control"), "effort control hidden (验收 3)");
ok(!dom.window.document.querySelector(".composer-meta__control--capsule"), "capsule hidden");
ok(!dom.window.document.querySelector(".composer-meta__control--content"), "+ content menu hidden");
ok(!dom.window.document.querySelector(".composer-meta__control--intent"), "plan/goal intent badge hidden");
ok(!dom.window.document.querySelector(".composer-meta__control--target"), "split target hidden");
ok(!dom.window.document.querySelector(".composer-meta__control--delivery"), "quality floor delivery badge hidden");
ok(!dom.window.document.querySelector(".composer__btn--rewrite"), "draft rewrite button hidden in step ② (③接桥后再放)");

// ── ② 通用能力在场 ──────────────────────────────────────────────────────
ok(dom.window.document.querySelector(".composer-emoji-picker"), "emoji picker present (表情)");
ok(dom.window.document.querySelector(".composer-history-picker"), "history picker present (历史提示词)");
ok(dom.window.document.querySelector(".composer__btn--send"), "send button present (发送键)");
const ta = () => dom.window.document.querySelector(".composer__input")! as HTMLTextAreaElement;

// 快捷指令菜单：「!!」token 驱动（与会话同交互，composer-quick-command-bang 同款驱动）。
await typeText(ta(), "!!");
const qcRows = Array.from(dom.window.document.querySelectorAll(".slashmenu [role='option']")).map((node) => node.textContent ?? "");
ok(qcRows.some((row) => row.includes("日报")), "quick command menu lists entries (快捷指令，!! token 同交互)");
ok(dom.window.document.querySelector(".slashmenu"), "the quick command menu surface (.slashmenu) renders");

// 表情面板打开（面板 lazy——只断入口可用态，不动 catalog）。
const emojiBtn = dom.window.document.querySelector(".composer-emoji-picker button") as HTMLButtonElement;
await act(async () => { emojiBtn.click(); await flush(); });
ok(emojiBtn.getAttribute("aria-expanded") === "true", "emoji entry toggles open (aria-expanded)");

// 历史提示词面板打开（tape 数据源是全局提示词档，channel 态照常拉）。
const historyBtn = dom.window.document.querySelector(".composer-history-picker button") as HTMLButtonElement;
ok(historyBtn, "history picker entry is clickable surface");

// ── ③ 发送桥接：onSend(displayText) ────────────────────────────────────
let sent: Array<{ display: string; submit?: string }> = [];
await paint(baseProps({
  channelMode: true,
  sessionKey: "channel:ch_send",
  onSend: (display: string, submit?: string) => { sent.push({ display, submit }); },
}));
await typeText(ta(), "群里的第一条");
await act(async () => {
  ta().dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  await flush();
});
ok(sent.length === 1 && sent[0].display === "群里的第一条", "Enter sends through the injected onSend bridge (发送桥接 channel_send 的缝)");

// ── ④ draft 按 channel 分键（持久化往返）────────────────────────────────
await paint(baseProps({ channelMode: true, sessionKey: "channel:ch_a" }));
await typeText(ta(), "群A的草稿");
// 持久化是 150ms 防抖（composerDraftPersistence DEBOUNCE_MS）——过防抖窗再查档。
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 200)); });
ok((window.localStorage.getItem("composer:drafts:v1") ?? "").includes("channel:ch_a"), "channel:ch_a draft key lands in the draft store (composer:drafts:v1)");

// 换键重挂：channel:ch_b 必须看不到群A草稿。
await paint(baseProps({ channelMode: true, sessionKey: "channel:ch_b" }));
ok(ta().value === "", "a different channel key starts with an empty draft (分键隔离)");
// 同键重挂：channel:ch_a 恢复草稿。
await paint(baseProps({ channelMode: true, sessionKey: "channel:ch_a" }));
ok(ta().value === "群A的草稿", "remounting the same channel key restores its draft (draft 按 channel 分键)");

// ── ⑤ 双态零回归：非 channelMode 既有面不感知 ───────────────────────────
await paint(baseProps({ sessionKey: "tab_session" }));
ok(dom.window.document.querySelector(".composer-modebar--approval") || dom.window.document.querySelector(".composer-approval-select"), "session mode still renders the permission tier (双态零回归)");
ok(dom.window.document.querySelector(".composer-meta__control--model"), "session mode still renders the model slot");
ok(dom.window.document.querySelector(".composer-emoji-picker"), "session mode keeps the emoji entry");
// 清场：把 session 态草稿清掉，防止污染其他用例的 localStorage 视角。
await paint(baseProps({ channelMode: true, sessionKey: "channel:cleanup" }));

root.render(React.createElement("div"));
await act(async () => { await flush(); });

// ── ⑥ 源码钉（接线防漂移）───────────────────────────────────────────────
const composerSrc = fs.readFileSync(path.join(import.meta.dirname, "..", "components", "Composer.tsx"), "utf-8");
for (const needle of [
  "channelMode = false",
  "!heroMode && !channelMode && (",
  "!channelMode && <div className=\"composer-meta__control composer-meta__control--model\">",
  "await app.RewriteDraftForChannel(current, recent, contextBudgetKB)",
  "channelMode ? app.SaveChannelAttachment(dataUrl) : app.SavePastedImage(dataUrl)",
]) {
  ok(composerSrc.includes(needle), `Composer gate pin: ${needle.slice(0, 44)}…`);
}
const workspaceSrc = fs.readFileSync(path.join(import.meta.dirname, "..", "components", "ChannelWorkspacePanel.tsx"), "utf-8");
for (const needle of [
  'data-tl1441-composer="channel-mode"',
  "channelMode",
  "sessionKey={`channel:${channelRef}`}",
  "historyPickerEnabled",
  "onSend={sendViaComposer}",
  "onManageQuickCommands={onManageQuickCommands}",
]) {
  ok(workspaceSrc.includes(needle), `workspace mount pin: ${needle.slice(0, 44)}…`);
}

console.log(`tl1441-composer-channel-mode: ${passed} assertions passed`);
