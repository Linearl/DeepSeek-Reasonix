// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1403-watchdog-move-exemption.test.tsx
//
// tl1403 验收②（watchdog 豁免分支单测）+ 验收①的压缩时标模拟：
// recovery fork/promote 换面窗口内 quiet 递增不得判 stale（2026-11-05 14:10
// 早停现场），窗口结束后恢复原有判定。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { useCallback, useRef, useState } from "react";
import { shouldReconcileStaleTurn, sessionMoveExemptionActive, useStaleTurnWatchdog } from "../lib/useStaleTurnWatchdog";

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

// ---------- 纯函数面 ----------

const runningState = { running: true, turnStartAt: 1_000 };
const quiet40s = 41_000;
ok(shouldReconcileStaleTurn(runningState, 1_000, quiet40s), "past-timeout quiet reconciles without a move");
ok(!shouldReconcileStaleTurn(runningState, 1_000, quiet40s, 30_000, true), "move-in-flight exempts a past-timeout quiet turn");
ok(!shouldReconcileStaleTurn(runningState, 1_000, quiet40s, 30_000, false) === false, "legacy verdict unchanged when no move map");
ok(!sessionMoveExemptionActive(undefined), "absent move marker never exempts");
ok(sessionMoveExemptionActive(40_000, 40_500), "fresh move marker exempts");
ok(!sessionMoveExemptionActive(40_000, 40_000 + 121_000), "move marker expires after the 120s window");
ok(!sessionMoveExemptionActive(0), "zero move marker never exempts");

// ---------- hook 面（JSDOM + fake clock） ----------

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const originalNow = Date.now;
let fakeNow = 1_000;
Date.now = () => fakeNow;
let nextTimerID = 1;
const watchdogTimers = new Map<number, () => void>();
const shortTimers = new Map<number, () => void>();
window.setTimeout = ((handler: TimerHandler, delay?: number) => {
  const id = nextTimerID++;
  if ((delay ?? 0) >= 29_000 && typeof handler === "function") watchdogTimers.set(id, handler);
  else if (typeof handler === "function") shortTimers.set(id, handler);
  return id;
}) as typeof window.setTimeout;
window.clearTimeout = ((id?: number) => {
  if (id === undefined) return;
  watchdogTimers.delete(id);
  shortTimers.delete(id);
}) as typeof window.clearTimeout;

type HarnessProps = { moveAt: number | undefined; lastActivityAt: number };
let reconcileFires = 0;
function Harness({ moveAt, lastActivityAt }: HarnessProps) {
  const statesRef = useRef(new Map([["t1", { running: true, turnStartAt: 1_000 }]]));
  const lastRef = useRef(new Map([["t1", lastActivityAt]]));
  // 渲染期同步 ref 值：hook 的 effect 只订阅一次，后续变化靠 ref 承载。
  lastRef.current.set("t1", lastActivityAt);
  const moveRef = useRef(new Map<string, number>());
  if (moveAt !== undefined) moveRef.current.set("t1", moveAt);
  else moveRef.current.delete("t1");
  const activeRef = useRef("t1");
  const [, bump] = useState(0);
  // reconcile 必须引用稳定：hook deps 含 reconcile，身份漂移会反复重订阅并清掉
  // 已排定的定时器（本测要钉的正是同一订阅下的重排行为）。
  const reconcile = useCallback(async () => {
    reconcileFires += 1;
    bump((v) => v + 1);
  }, []);
  useStaleTurnWatchdog({
    tabId: "t1",
    visibleState: { running: true, turnStartAt: 1_000 },
    activeTabIdRef: activeRef,
    statesRef,
    lastTurnActivityAtByTab: lastRef,
    sessionMoveAtByTab: moveRef,
    reconcile,
  });
  return null;
}

// effect 在 mount 后才注册定时器，扫入口必须在断言时刻取（组件 render 期取不到）。
function currentTick(): (() => void) | undefined {
  let handler: (() => void) | undefined;
  for (const [, fn] of watchdogTimers) handler = fn;
  return handler;
}

async function mountHarness(props: HarnessProps): Promise<Root> {
  const element = document.getElementById("root");
  if (!element) throw new Error("missing root");
  const root = createRoot(element);
  await act(async () => {
    root.render(<Harness {...props} />);
    await flushPromises();
  });
  return root;
}

function flushPromises(): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, 0));
}

console.log("\ntl1403 watchdog move exemption");

// 场景 1：quiet 已超时 + move 窗口内 → 触发判定被豁免，按整周期重排，零 reconcile。
{
  fakeNow = 40_000; // move 打标时刻，quiet 新鲜（首排定时器 = 整周期，落捕获表）
  const root = await mountHarness({ moveAt: 40_000, lastActivityAt: 40_000 });
  fakeNow += 31_000; // quiet = 31s，越过 30s 判定线
  const tick = currentTick();
  ok(typeof tick === "function", "watchdog armed while running");
  reconcileFires = 0;
  await act(async () => {
    tick?.();
    await flushPromises();
  });
  ok(reconcileFires === 0, "exempt quiet turn does not reconcile");
  ok(watchdogTimers.size === 1 || shortTimers.size === 0, "exempt tick re-arms one full period instead of spinning");
  await act(async () => { root.unmount(); });
}

// 场景 2：move 窗口已过期（孤儿标记）→ 恢复原判定，reconcile 照常触发。
{
  reconcileFires = 0;
  fakeNow = 40_000 + 200_000; // 标记时刻 + 200s，远超 120s 时效界
  const root = await mountHarness({ moveAt: 40_000, lastActivityAt: fakeNow });
  fakeNow += 31_000;
  const tick = currentTick();
  await act(async () => {
    tick?.();
    await flushPromises();
  });
  ok(reconcileFires === 1, "expired move marker resumes the stale verdict");
  await act(async () => { root.unmount(); });
}

// 场景 3（验收①压缩模拟）：连续 3 轮 fork 换面，每轮 quiet 拉满，豁免全程兜住，
// 无一次 stale reconcile；标记随 hydrate 落定清除后判定恢复。
{
  reconcileFires = 0;
  let moveAt: number | undefined = undefined;
  let root = await mountHarness({ moveAt, lastActivityAt: fakeNow });
  for (let round = 1; round <= 3; round += 1) {
    // 本轮 fork：写入面换文件，surface 开始跟挂，quiet 继续拉长。
    moveAt = fakeNow;
    await act(async () => {
      root.render(<Harness moveAt={moveAt} lastActivityAt={1_000} />);
      await flushPromises();
    });
    fakeNow += 40_000; // 每轮静默 40s > 30s 判定线
    const tick = currentTick();
    await act(async () => {
      tick?.();
      await flushPromises();
    });
    if (reconcileFires !== 0) {
      ok(false, `round ${round}: no stale reconcile during the move window`);
    }
    // hydrate 落定：清除标记（followSessionMoveForTab 的 finally 语义）。
    moveAt = undefined;
    await act(async () => {
      root.render(<Harness moveAt={moveAt} lastActivityAt={fakeNow} />);
      await flushPromises();
    });
  }
  ok(reconcileFires === 0, "3 consecutive fork rounds fire zero stale reconciles");
  // 换面收尾后：无豁免、quiet 拉满 → 恢复触发。
  fakeNow += 31_000;
  const tick = currentTick();
  await act(async () => {
    tick?.();
    await flushPromises();
  });
  ok(reconcileFires === 1, "verdict resumes after the moves settle");
  await act(async () => { root.unmount(); });
}

Date.now = originalNow;
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
