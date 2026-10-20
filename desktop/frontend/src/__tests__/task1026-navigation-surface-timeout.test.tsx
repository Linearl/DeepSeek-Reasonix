// 任务 1026（fork issue #61）：导航遮罩超时兜底。
//
// 现场：后端导航回调不返回 ⇒ 遮罩（navigationSurfaceIntent）永久悬挂且导航
// 队列被 runningRef 卡死，用户「加载中」遮罩只能强退重启两次。本文件验证
// 三层兜底件：
// 1. useNavigationSurfaceTimeoutWatch：遮罩挂起超时限触发 onTimeout(intent)，
//    settle/intent 替换时计时器被清理或重置，回调引用不稳定不顺延时限；
// 2. forceUnlockNavigationQueue：挂死 run 永不返回时，解锁后新导航立即执行、
//    挂死 run 日后返回不夺回队列（stale 守卫）；
// 3. settleNavigationSurfaceIntent：超时清遮罩与 settle 同判据（intent 匹配才放行）。
import { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import assert from "node:assert/strict";
import { NAVIGATION_SURFACE_TIMEOUT_MS, settleNavigationSurfaceIntent } from "../lib/navigationSurfaceTransition";
import { useNavigationSurfaceTimeoutWatch } from "../app-runtime/navigationSurfaceTimeoutWatch";
import { enqueueNavigationRequest, forceUnlockNavigationQueue, type NavigationCoalescingRefs } from "../lib/openTopicCoalescing";

const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
const root = createRoot(document.getElementById("root")!);

const sleep = (ms: number) => new Promise<void>((yes) => setTimeout(yes, ms));

function TimeoutProbe({ intent, onTimeout, timeoutMs }: {
  intent: number | null;
  onTimeout: (intent: number) => void;
  timeoutMs: number;
}) {
  useNavigationSurfaceTimeoutWatch(intent, onTimeout, timeoutMs);
  return null;
}

try {
  // 1. 到时触发，且默认导出常量为 20s（验收口径）。
  assert.equal(NAVIGATION_SURFACE_TIMEOUT_MS, 20_000, "fallback window is the agreed 20s");
  {
    const fired: number[] = [];
    await act(async () => root.render(<TimeoutProbe intent={7} onTimeout={(i) => fired.push(i)} timeoutMs={20} />));
    await act(async () => { await sleep(60); });
    assert.deepEqual(fired, [7], "a hung surface fires the timeout with its own intent");
    await act(async () => root.render(<TimeoutProbe intent={null} onTimeout={(i) => fired.push(i)} timeoutMs={20} />));
    await act(async () => root.render(<TimeoutProbe intent={null} onTimeout={(i) => fired.push(i)} timeoutMs={20} />));
    assert.deepEqual(fired, [7], "settling to null clears the pending timer");
  }

  // 2. intent 替换重置计时：旧 intent 的到点不再触发，新 intent 从零起算。
  // （窗口取 120ms：act 包装有额外开销，名义时间需留足余量防误触发。）
  {
    const fired: number[] = [];
    await act(async () => root.render(<TimeoutProbe intent={1} onTimeout={(i) => fired.push(i)} timeoutMs={120} />));
    await act(async () => { await sleep(30); });
    await act(async () => root.render(<TimeoutProbe intent={2} onTimeout={(i) => fired.push(i)} timeoutMs={120} />));
    await act(async () => { await sleep(50); });
    assert.deepEqual(fired, [], "replacing the intent restarts the window, the old deadline never fires");
    await act(async () => { await sleep(120); });
    assert.deepEqual(fired, [2], "the replacement intent fires on its own full window");
  }

  // 3. 回调引用不稳定不重置计时器（ref 透传）：每次渲染新函数，时限仍按原窗。
  // （窗口 200ms 覆盖 act 渲染开销：重渲染只换回调身份，不触碰计时器。）
  {
    const fired: number[] = [];
    const Probe = ({ tick }: { tick: number }) => (
      <TimeoutProbe intent={3} onTimeout={(i) => fired.push(i, tick)} timeoutMs={200} />
    );
    await act(async () => root.render(<Probe tick={0} />));
    for (let tick = 1; tick <= 3; tick++) {
      await act(async () => { await sleep(10); });
      await act(async () => root.render(<Probe tick={tick} />));
    }
    await act(async () => { await sleep(220); });
    assert.deepEqual(fired, [3, 3], "unstable callback identity never postpones the fallback deadline");
  }

  // 4. 队列解锁：挂死 run 永不返回 ⇒ 解锁后新导航立即执行；挂死 run 返回不夺回队列。
  {
    type Req = { navigationIntentSeq: number };
    const refs: NavigationCoalescingRefs<Req> = { seqRef: { current: 0 }, runningRef: { current: false }, pendingRef: { current: null } };
    const ran: string[] = [];
    let releaseA: (() => void) | undefined;
    const hungA = new Promise<void>((yes) => { releaseA = yes; });
    const enqueue = (intent: number, body: (r: Req) => Promise<void>) =>
      enqueueNavigationRequest(refs, { navigationIntentSeq: intent }, body);

    const a = enqueue(5, async () => { ran.push("A:start"); await hungA; ran.push("A:end"); });
    await Promise.resolve();
    assert.deepEqual(ran, ["A:start"], "the first request runs immediately");
    const b = enqueue(6, async () => { ran.push("B"); });
    await Promise.resolve();
    assert.deepEqual(ran, ["A:start"], "a second request queues behind the hung run");

    forceUnlockNavigationQueue(refs);
    const c = enqueue(7, async () => { ran.push("C"); });
    await act(async () => { await c; });
    assert.deepEqual(ran, ["A:start", "C"], "after the unlock the fresh navigation runs at once");

    releaseA!();
    await act(async () => { await a; await b; });
    assert.deepEqual(ran, ["A:start", "C", "A:end"], "the late hung run completes without reclaiming the queue (C already ran)");
    assert.equal(refs.runningRef.current, false, "the queue ends idle");
    assert.equal(refs.pendingRef.current, null);
  }

  // 5. 超时清遮罩与 settle 同判据：只有遮罩仍停在超时 intent 上才放行。
  assert.equal(settleNavigationSurfaceIntent(5, 5), null, "matching intent releases the surface");
  assert.equal(settleNavigationSurfaceIntent(6, 5), 6, "a newer intent is never released by an older timeout");

  // 6. 默认时限参数存在（hook 签名烟测；20s 常量已由第 1 段断言，测试时钟内不等待）。
  {
    const fired: number[] = [];
    function DefaultProbe({ intent, onTimeout }: { intent: number | null; onTimeout: (i: number) => void }) {
      useNavigationSurfaceTimeoutWatch(intent, onTimeout);
      return null;
    }
    await act(async () => root.render(<DefaultProbe intent={9} onTimeout={(i) => fired.push(i)} />));
    assert.deepEqual(fired, [], "default 20s window has not elapsed inside the test clock");
  }

  root.unmount();
  console.log("PASS task1026 navigation surface timeout: fires, resets, unlocks the queue, releases only the matching intent");
} finally {
  dom.window.close();
}
