// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task991-ask-background-toast.test.tsx
// 任务991 验收钉：后台 tab 的 ask 感知 toast。
//   ① 事件链：ask:background 通知到达 → 右下角 toast 出现（含问题摘要）；
//   ② 限时与后端同源 + tl1464 10s 下限（用户修正2）：展示窗口 =
//      max(waitMs, 下限)——只延长不缩短，attended（waitMs=0）无自动到期；
//      纯函数数学 + 小下限缝（300ms）行为实证双语义；
//   ③ 交互语义：点击 = 切 tab + NotifyAskReattachForTab（178 重置窗口，
//      非停表）+ 移除；hover = NotifyAskActivityForTab（844 重置，预算封顶
//      在后端）+ 恢复满亮；手动关闭 = 仅移除，零后端调用；
//   ④ 终态收尾：prompt_answered/prompt_closed（含后端自动决策）一并移除；
//   ⑤ tl1464：渲染面入右下统一堆叠 #rt-notice-stack（data-kind=action，
//      与 C 类 diagnostic 区分，.ask-bg-toast-container 容器退役）；
//   ⑥ tl1458/tl1464：事件到达即落通知留档（source=ask-bg），留档≠拦截。
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

  // 钉② 同源限时 + tl1464 10s 下限（用户修正2：停留3~5s→延长10s）。
  // 语义改为「只延长不缩短」：deadline = max(waitMs, 下限)。下限默认 10s 没
  // 法等表，经测试缝收小到 300ms 后行为实证双语义；默认值与纯函数数学另钉。
  await fresh();
  await act(async () => askBackground("tab-a", "ask-floor", 500, "floor window"));
  await sleep(600);
  assert.equal(toasts().length, 1, "500ms 窗口在默认 10s 下限下不消失（tl1464 延长语义，旧 500ms 即逝行为已按用户批准变更）");
  {
    const { setAskBgToastMinMsForTest, askBgDeadlineAt, ASK_BG_TOAST_MIN_MS } = await import("../components/AskBackgroundToasts");
    assert.equal(ASK_BG_TOAST_MIN_MS, 10_000, "下限常量 = 10s（用户修正2）");
    const t0 = Date.now();
    assert.equal(askBgDeadlineAt(t0, 0), null, "attended ask（waitMs=0）无自动到期");
    assert.equal(askBgDeadlineAt(t0, 500) - t0, 10_000, "waitMs<下限 → 抬到 10s（延长）");
    assert.equal(askBgDeadlineAt(t0, 15_000) - t0, 15_000, "waitMs>下限 → 原样跟随（只延长不缩短）");
    setAskBgToastMinMsForTest(300);
  }
  await fresh();
  await act(async () => {
    askBackground("tab-a", "ask-short", 500, "short window");
    askBackground("tab-b", "ask-long", 2400, "long window");
  });
  assert.equal(toasts().length, 2, "两条通知各出一条 toast（多 tab 堆叠）");
  await sleep(650);
  assert.equal(toasts().length, 1, `waitMs=500（高于缝下限 300）按自身窗口消失——寿命跟随事件 waitMs，剩 ${toasts().length}`);
  await sleep(500);
  assert.equal(toasts().length, 1, "waitMs=2400 仍在");
  await act(async () => {
    askBackground("tab-c", "ask-lifted", 150, "lifted window");
  });
  await sleep(120);
  assert.equal(toasts().length, 2, "waitMs=150 低于缝下限 300 → 抬到 300，此刻仍在（下限接线实证）");
  await sleep(400);
  assert.equal(toasts().length, 1, "抬升窗口到期消失，长窗口不受影响");
  assert.ok(toasts()[0].textContent?.includes("long window"), "仍存的是 2400ms 长窗口 toast");
  await act(async () => wireEvent("prompt_closed", "ask-long"));
  assert.equal(toasts().length, 0, "终态信号收尾长窗口");
  await act(async () => {
    (await import("../components/AskBackgroundToasts")).setAskBgToastMinMsForTest(null);
  });
  console.log("  PASS  ② 同源限时 + 10s 下限：max(waitMs, 下限)，attended 无到期");

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

  // 钉③b hover：844 重置后端窗口 + 恢复满亮（渐隐期「我在看」）。初始渐隐断
  // 言经缝下限=窗口恢复旧前提（默认 10s 下限下初始不在渐隐段，属 tl1464 预期
  // 变更；hover 重置口径=含下限的满窗口，纯函数钉已覆盖）。
  await fresh();
  {
    const { setAskBgToastMinMsForTest } = await import("../components/AskBackgroundToasts");
    setAskBgToastMinMsForTest(2000);
  }
  await render();
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
  await act(async () => {
    (await import("../components/AskBackgroundToasts")).setAskBgToastMinMsForTest(null);
  });
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

  // 钉⑤ tl1464：迁入右下统一堆叠——portal 目标 = #rt-notice-stack，
  // data-kind="action" 与 C 类诊断告警（data-kind=diagnostic）同栈区分。
  await fresh();
  await act(async () => askBackground("tab-a", "ask-stack", 0, "in the stack"));
  const stack = document.getElementById("rt-notice-stack");
  assert.ok(stack, "统一堆叠容器存在（ensureRtNoticeStack 幂等建栈）");
  assert.equal(toasts()[0].parentElement, stack, "ask toast 条目是统一堆叠的直接子元素（.ask-bg-toast-container 容器退役）");
  assert.equal(toasts()[0].dataset.kind, "action", "ask toast 挂 data-kind=action（A 类操作反馈）");
  {
    const { rtNoticeHost } = await import("../lib/rtNotice");
    const diag = rtNoticeHost("tl1464-ask-coexist", "danger");
    assert.equal(diag.parentElement, stack, "C 类宿主同栈");
    assert.equal(diag.dataset.kind, "diagnostic", "C 类挂 data-kind=diagnostic——A/C 同栈以 data-kind 区分");
    assert.notEqual(toasts()[0].dataset.kind, diag.dataset.kind, "两类 data-kind 互异");
    diag.remove();
  }
  console.log("  PASS  ⑤ tl1464：入右下统一堆叠，data-kind=action 与 C 类区分");

  // 钉⑥ tl1458/tl1464：事件到达即落通知留档（source=ask-bg），展示与留档两通路独立。
  {
    const { resetNotifyArchiveForTest, listNotifications } = await import("../lib/notifyArchive");
    dom.window.localStorage.clear();
    resetNotifyArchiveForTest();
    await fresh();
    await act(async () => askBackground("tab-a", "ask-archived", 0, "archive me"));
    const entry = listNotifications().find((e) => e.source === "ask-bg");
    assert.ok(entry, "ask:background 到达即写留档（source=ask-bg）");
    assert.equal(entry?.level, "warn", "留档级别 = warn（需用户处置的挂起问题）");
    assert.equal(toasts().length, 1, "留档≠拦截：展示通路原样");
    resetNotifyArchiveForTest();
  }
  console.log("  PASS  ⑥ 事件到达即落留档（ask-bg），展示独立");

  await act(async () => root.unmount());
  console.log("task991 ask-background-toast: all pins passed");
} finally {
  dom.window.close();
}
