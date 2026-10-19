// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task991-ask-background-toast.test.tsx
// 任务991 验收钉：后台 tab 的 ask 感知 toast。
//   ① 事件链：ask:background 通知到达 → 右下角 toast 出现（含问题摘要）；
//   ② 限时与后端同源（无硬编码）：toast 寿命跟随事件携带的 waitMs ——
//      两个不同 waitMs 的 toast 各自按自己的窗口消失，任何 15s/12s 硬编码
//      都会让这条断言失败；
//   ③ 交互语义：点击 = 切 tab + NotifyAskReattachForTab（178 重置窗口，
//      非停表）+ 移除；hover = NotifyAskActivityForTab（844 重置，预算封顶
//      在后端）+ 恢复满亮；手动关闭 = 仅移除，零后端调用；
//   ④ 终态收尾：prompt_answered/prompt_closed（含后端自动决策）一并移除。
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import type { ReactNode } from "react";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

// ---- bridge 注入：window.go 绑定 + window.runtime 事件通道假件 ----
type Listener = (payload: unknown) => void;
const listeners = new Map<string, Set<Listener>>();
const reattachCalls: Array<[string, string]> = [];
const activityCalls: Array<[string, string]> = [];
Object.defineProperty(dom.window, "go", {
  configurable: true,
  value: {
    main: {
      App: {
        NotifyAskReattachForTab: (tabID: string, id: string) => {
          reattachCalls.push([tabID, id]);
          return Promise.resolve();
        },
        NotifyAskActivityForTab: (tabID: string, id: string) => {
          activityCalls.push([tabID, id]);
          return Promise.resolve();
        },
      },
    },
  },
});
Object.defineProperty(dom.window, "runtime", {
  configurable: true,
  value: {
    EventsOn: (name: string, cb: Listener) => {
      let set = listeners.get(name);
      if (!set) listeners.set(name, (set = new Set()));
      set.add(cb);
      return () => set.delete(cb);
    },
  },
});

const fire = (name: string, payload: unknown) => listeners.get(name)?.forEach((cb) => cb(payload));
const askBackground = (tabId: string, askId: string, waitMs: number, summary = "Ship it?") =>
  fire("ask:background", { tabId, askId, waitMs, summary });
const wireEvent = (kind: string, itemId: string) => fire("agent:event", { kind, itemId, tabId: "tab-a" });

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { AskBackgroundToasts } = await import("../components/AskBackgroundToasts");
const { LocaleProvider } = await import("../lib/i18n");

let root = createRoot(document.getElementById("root")!);
let switchedTo: string[] = [];
// 每个 pin 前 fresh() 重挂载：waitMs=0 的 toast 设计上常驻（attended ask 无
// 自动到期），跨 pin 残留会让计数断言错位——重挂载即清场。
async function fresh(activeTabId = "tab-active") {
  await act(async () => root.unmount());
  root = createRoot(document.getElementById("root")!);
  await render(activeTabId);
}
async function render(activeTabId?: string) {
  const body: ReactNode = (
    <AskBackgroundToasts activeTabId={activeTabId} onSwitchTab={(tabId) => switchedTo.push(tabId)} />
  );
  await act(async () => root.render(<LocaleProvider>{body}</LocaleProvider>));
}
const toasts = () => Array.from(document.querySelectorAll<HTMLElement>(".ask-bg-toast"));
const sleep = (ms: number) => act(async () => new Promise((resolve) => setTimeout(resolve, ms)));
const opacityOf = (el: HTMLElement) => Number(el.style.opacity);

try {
  // 钉① 事件链：后台 ask 通知 → toast 出现，摘要与标题就位。
  await fresh();
  await act(async () => askBackground("tab-a", "ask-1", 0));
  assert.equal(toasts().length, 1, "ask:background 通知必须产出一条 toast");
  assert.ok(toasts()[0].textContent?.includes("Ship it?"), "toast 携带问题摘要");
  assert.ok(toasts()[0].textContent?.includes("另一个标签页有问待答") || toasts()[0].querySelector(".ask-bg-toast__title"), "toast 携带标题节点");
  console.log("  PASS  ① 事件链：后台 ask 通知 → toast 出现");

  // 钉② 同源限时（无硬编码）：两个不同 waitMs 各按自己的窗口消失。
  await fresh();
  await act(async () => {
    askBackground("tab-a", "ask-short", 500, "short window");
    askBackground("tab-b", "ask-long", 2400, "long window");
  });
  assert.equal(toasts().length, 2, "两条通知各出一条 toast（多 tab 堆叠）");
  await sleep(1100);
  const texts = toasts().map((el) => el.textContent ?? "");
  assert.equal(toasts().length, 1, `500ms 窗口的 toast 应已消失（硬编码 15s 会仍在此），剩 ${toasts().length}`);
  assert.ok(texts[0].includes("long window"), "2400ms 窗口的 toast 仍在——寿命跟随事件 waitMs");
  assert.equal(opacityOf(toasts()[0]) < 1, true, "长窗口 toast 已进入按比例渐隐（剩余 3s 内）");
  await sleep(1700);
  assert.equal(toasts().length, 0, "2400ms 窗口到期后 toast 全部消失");
  console.log("  PASS  ② 同源限时：寿命跟随事件 waitMs，无硬编码");

  // 钉③a 点击：切 tab + 178 重置窗口（非停表）+ 移除。
  await fresh();
  await act(async () => askBackground("tab-a", "ask-click", 0, "click me"));
  reattachCalls.length = 0;
  switchedTo = [];
  await act(async () => {
    toasts()[0].dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  assert.deepEqual(switchedTo, ["tab-a"], "点击必须切到 ask 所在 tab");
  assert.deepEqual(reattachCalls, [["tab-a", "ask-click"]], "点击必须触发 178 重置后端窗口（非停表）");
  assert.equal(toasts().length, 0, "点击后 toast 移除");
  console.log("  PASS  ③a 点击：切 tab + 重置窗口 + 移除");

  // 钉③b hover：844 重置后端窗口 + 恢复满亮（渐隐期「我在看」）。
  await fresh();
  await act(async () => askBackground("tab-a", "ask-hover", 2000, "hover me"));
  assert.equal(opacityOf(toasts()[0]) < 1, true, "2000ms 窗口 < 渐隐阈值 3000ms，初始即渐隐");
  activityCalls.length = 0;
  await act(async () => {
    // React 的 onMouseEnter 由 mouseover 委托（relatedTarget=null = 从元素外
    // 进入），与 composer-run-strip / context-window-ring 两测试的触发一致。
    toasts()[0].dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
  });
  assert.deepEqual(activityCalls, [["tab-a", "ask-hover"]], "hover 必须同步重置后端窗口（844 信号）");
  assert.equal(opacityOf(toasts()[0]), 1, "hover 恢复满亮");
  console.log("  PASS  ③b hover：844 重置 + 恢复满亮");

  // 钉③c 手动关闭：仅移除，零后端调用（关闭≠撤回决策）。
  await fresh();
  await act(async () => askBackground("tab-a", "ask-close", 0, "close me"));
  reattachCalls.length = 0;
  activityCalls.length = 0;
  switchedTo = [];
  await act(async () => {
    document.querySelector<HTMLElement>(".ask-bg-toast__close")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  assert.equal(toasts().length, 0, "手动关闭移除 toast");
  assert.deepEqual(reattachCalls, [], "手动关闭零重置窗口调用");
  assert.deepEqual(activityCalls, [], "手动关闭零 activity 调用");
  assert.deepEqual(switchedTo, [], "手动关闭不切 tab");
  console.log("  PASS  ③c 手动关闭：零后端调用");

  // 钉④ 终态收尾：后端回答/关闭（含自动决策）→ toast 一并消失。
  await fresh();
  await act(async () => askBackground("tab-a", "ask-final", 0, "final"));
  assert.equal(toasts().length, 1);
  await act(async () => wireEvent("prompt_answered", "ask-final"));
  assert.equal(toasts().length, 0, "prompt_answered 后 toast 收尾移除");
  await act(async () => askBackground("tab-a", "ask-final-2", 0, "final 2"));
  await act(async () => wireEvent("prompt_closed", "ask-final-2"));
  assert.equal(toasts().length, 0, "prompt_closed 后 toast 收尾移除");
  console.log("  PASS  ④ 终态收尾：prompt_answered/prompt_closed 移除");

  // 附钉：自切到该 tab → toast 静默退场（完成使命）。
  await fresh("tab-other");
  await act(async () => askBackground("tab-a", "ask-switch", 0, "switch"));
  assert.equal(toasts().length, 1);
  await render("tab-a");
  assert.equal(toasts().length, 0, "用户自切到该 tab 后 toast 静默退场");
  console.log("  PASS  附钉：自切 tab 后 toast 退场");

  await act(async () => root.unmount());
  console.log("task991 ask-background-toast: all pins passed");
} finally {
  dom.window.close();
}
