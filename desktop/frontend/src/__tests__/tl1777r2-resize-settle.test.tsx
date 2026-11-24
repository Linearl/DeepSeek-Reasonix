// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1777r2-resize-settle.test.tsx
//
// tl1777 返修（P2·复现条件修正）：主动调小高度（resize 手柄）→ 长文本输入 →
// 内容溢出卡外——与图片/侧栏无关。
//
// 根因（20261124 真浏览器 Edge 探针实锤，tmp/tl1777r2-probe 时序+调用点取证）：
//   pointer 拖动手势经 createRafResizeUpdater（lib/resizeDrag）绕过 React 直写
//   卡片 --composer-height（拖动帧性能通道）；松手 setComposerHeight 触发的
//   render 若算出与上一帧 prop 相同的值（长草稿在场时 logicalHeight 不随手动
//   目标变——floor-not-cap 语义 logicalHeight=max(手动, 内容+预留)，手动从 360
//   改到 160 时内容需要 302+58 仍把 logicalHeight 顶回 360），React style diff
//   判定「无变化」跳过 DOM 写入 → var 停在手势直写残值（160px）——卡定高残值
//   与 textarea inline 高（302px，内容需要）分裂：.composer-card overflow:
//   visible 且输入框透明背景，textarea 从卡底伸出 151px，文字画在卡外
//   （探针实测 taBottom 1011 > cardBottom 860）。派单假设「measure 从手动高度
//   起步只增不减」经取证证伪：measure 现读测量 twin 的 scrollHeight（676），
//   手动高度只是 floor；真正机制是手势直写与 React style diff 基线的写权分裂。
//
// 修法（本钉验收对象）：手势落定写手——composerResizing 结束/写手值变化时
//   useLayoutEffect 绕过 diff 强制对齐 DOM var 一次（值同幂等、值异即权威终写；
//   手势中早退保 liveResize 独占写权）。不重做 tl1578/tl1508 既有测量链
//   （non-goals 保持），tl1777 一期附件条预算不受影响（composerBudgetedMaxHeight
//   与本写手正交）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
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

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  globalThis.MutationObserver = dom.window.MutationObserver;
  Object.defineProperty(dom.window, "innerHeight", { configurable: true, get: () => 900 });
  // React input-event polyfill 探 activeElement.attachEvent（jsdom 无此遗留 API，
  // tl1777 一期钉同 stub）。
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

// 附件/模型桥：Composer 挂载异步拉 Models/Commands（tl1777 一期钉同 stub 形态）。
function installBridgeApp() {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
      },
      Directory: {
        ListAddressableSessions: async () => [],
      },
    },
  };
}

type ComposerProps = Parameters<typeof Composer>[0];

// text 受控桥：Composer 的草稿文本是父级 state（text/setText props），输入触发
// measure 的 useLayoutEffect [text, ...] 依赖它——钉内持本地 state 转发。
function ComposerHarness(props: Omit<ComposerProps, "text" | "setText">) {
  const [text, setText] = useState("");
  return <Composer {...props} text={text} setText={setText} />;
}

type HarnessProps = Omit<ComposerProps, "text" | "setText">;

async function renderComposer(props: HarnessProps) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <ComposerHarness {...props} />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });
  return { root };
}

function makeProps(): HarnessProps {
  return {
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
  };
}

// 草稿落输入通道：jsdom 受控 textarea 的合成 input 事件进不了 React onChange
// （composer-draft-rewrite 判例同一定论），走 paste handler 通道（textPasteEvent
// 同法）——onPaste → setText 草稿落定，measure 的 [text] 重测面照常触发。
function typeIntoTextarea(dom: JSDOM, el: HTMLTextAreaElement, value: string) {
  const event = new dom.window.Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    configurable: true,
    value: {
      files: [],
      items: [],
      types: ["text/plain"],
      getData: (kind: string) => (kind === "text" || kind === "text/plain" ? value : ""),
    },
  });
  el.dispatchEvent(event);
}

// jsdom 无布局：scrollHeight 恒 0。按真浏览器探针读数（30 行草稿 ≈ 676px）
// mock 测量 twin 的 scrollHeight，走 measureTextareaAutoHeight 的正常链路。
function mockTwinScrollHeight(dom: JSDOM, value: number) {
  const twin = document.querySelector("textarea.composer__input--measure") as HTMLTextAreaElement;
  Object.defineProperty(twin, "scrollHeight", { configurable: true, get: () => value });
}

// jsdom 无布局：卡片 rect 高恒 0。mock 为主持手势起点读数（composerLogicalHeight）：
// 拖前卡被长草稿撑到 360（真浏览器探针读数）。
function mockCardRect(card: HTMLElement, height: number) {
  Object.defineProperty(card, "getBoundingClientRect", {
    configurable: true,
    value: () => ({ height, top: 0, bottom: height, width: 600, left: 0, right: 600, x: 0, y: 0, toJSON: () => ({}) }),
  });
}

const LONG_DRAFT_SCROLL = 676;

{
  const dom = installDom();
  installBridgeApp();
  // resized 模式入口：手定高度经 layoutPreferences.v1 落盘，挂载即水合（tl1578 形态③同法）。
  localStorage.setItem("reasonix.layoutPreferences.v1", JSON.stringify({ sizes: { composerHeight: 104 } }));
  const { root } = await renderComposer(makeProps());

  const card = document.querySelector(".composer-card") as HTMLElement;
  const handle = document.querySelector(".composer-resize-handle") as HTMLElement;
  const ta = document.querySelector("textarea.composer__input:not(.composer__input--measure)") as HTMLTextAreaElement;
  ok(card !== undefined && handle !== null && ta !== null, "settle: card/handle/textarea mount");

  // 长草稿输入：measure 现读 twin scrollHeight=676 → 预算 360 下 inputHeight=302
  // （overflow 内滚），卡高写手=max(104, 302+58)=360——React 写 var=360px。
  mockTwinScrollHeight(dom, LONG_DRAFT_SCROLL);
  await act(async () => {
    typeIntoTextarea(dom, ta, Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join("\n"));
    await flushTimers();
  });
  eq(card.style.getPropertyValue("--composer-height"), "360px", "settle: long draft grows the card var to 360px (React render write)");
  eq((ta.style.height || ""), "302px", "settle: textarea inline height clamps at the 302px budget (overflow scroll)");

  // 用户复现操作：拖 resize 手柄把卡调小（360 起步向下 200px → 手动目标 160）。
  // flush 通道（pointerup 里 flush() 同步 apply）直写 DOM var=160px。
  mockCardRect(card, 360);
  await act(async () => {
    handle.dispatchEvent(new dom.window.MouseEvent("pointerdown", { bubbles: true, cancelable: true, clientY: 300, button: 0 }));
    await flushTimers();
  });
  await act(async () => {
    // Composer 手势监听挂 document（onComposerResizeStart 的 addEventListener 面）
    document.dispatchEvent(new dom.window.MouseEvent("pointermove", { bubbles: true, clientY: 500 }));
    await flushTimers();
  });
  // pointermove 的 rAF schedule：泵一帧让 liveResize 直写落 DOM（模拟拖动中最后一帧）
  await act(async () => {
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await flushTimers();
  });
  eq(card.style.getPropertyValue("--composer-height"), "160px", "settle: live gesture writes the manual target 160px past React (drag channel)");

  // 松手：setComposerHeight(160)+composerResizing(false) → render。长草稿仍在场，
  // logicalHeight=max(160, 302+58)=360 与上一帧 prop 相同——React style diff
  // 跳过写入，var 残留 160px（=修前溢出形态），落定写手必须把它对齐回 360px。
  await act(async () => {
    document.dispatchEvent(new dom.window.MouseEvent("pointerup", { bubbles: true }));
    await flushTimers();
  });

  eq(card.style.getPropertyValue("--composer-height"), "360px",
    "settle: pointerup settle-writer realigns the card var to the logical height (was: 160px stale gesture value → textarea overflowed the card)");
  eq((ta.style.height || ""), "302px", "settle: textarea keeps its content-driven 302px after the gesture");
  ok(Number.parseInt(card.style.getPropertyValue("--composer-height"), 10) >= (Number.parseInt(ta.style.height, 10) + 58),
    "settle: invariant card >= textarea + reserved holds after shrinking the card with a long draft present");

  // 回落链：写手值随后续内容变化继续对齐——草稿缩回一行（scrollHeight 26）→
  // inputHeight=floor 102（160−58）→ 卡=max(160, 102+58)=160。
  mockTwinScrollHeight(dom, 26);
  await act(async () => {
    typeIntoTextarea(dom, ta, "short");
    await flushTimers();
  });
  eq(card.style.getPropertyValue("--composer-height"), "160px",
    "settle: settle-writer follows the shrink-back to the saved manual height when the draft clears");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

console.log(`\ntl1777r2 resize settle: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
