// Run: tsx src/__tests__/composer-stop-unknown.test.tsx
//
// 任务510（用户反馈：运行中无终止按钮）：runtime 投影处于 unknown 降级态
// （远端 freshness 抖动 / 全局同步失败）时，停止按钮曾被整体隐藏 +
// handleCancel 硬早退——turn 在远端继续跑而用户无从停止。钉住修复契约：
// ① unknown 态停止入口仍可见可用（aria 明示状态未知），点击确实发出取消；
// ② 全局同步失败（store.fail）不再波及本 tab 的停止入口（b 面隔离）；
// ③ 快照明确不可取消 / finishing 的防误操作语义不回归。

import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { en } from "../locales/en";
import { runtimeStateStore, type RuntimeProjection, type RuntimeState } from "../lib/runtimeStateStore";
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

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
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
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(window, "matchMedia", {
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

const stopState = (over: Partial<RuntimeState> = {}): RuntimeState => ({
  schemaVersion: 1, runtimeEpoch: "e510", revision: 1, phase: "executing", running: true,
  turnId: "turn-510", turnStatus: "in_progress", turnEventSeq: 1, pendingPrompt: false,
  cancelRequested: false, cancellable: true, backgroundJobs: 0, activity: "thinking", ...over,
});
const projection = (freshness: "synced" | "unknown", over: Partial<RuntimeState> = {}): RuntimeProjection => ({
  epoch: "app-510", revision: 2, topics: [],
  sessions: [{ tabId: "t510-stop", scope: "project", workspaceRoot: "/fixture", topicId: "topic", sessionPath: "/fixture/s.jsonl",
    sessionGeneration: 1, open: true, remote: false, freshness, state: stopState(over) }],
});

async function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const calls = { cancel: 0 };
  let currentProps: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "DeepSeek-R1",
    onSend: () => {},
    onCancel: () => {
      calls.cancel += 1;
      return undefined;
    },
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
  const paint = async (nextProps: Partial<Parameters<typeof Composer>[0]> = {}) => {
    currentProps = { ...currentProps, ...nextProps };
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
  };
  await paint();
  return { root, calls, rerender: paint };
}

const stopButton = () => document.querySelector<HTMLButtonElement>(".composer__btn--stop");
// tl1847 去文字: the button renders the Square icon only — no text child
// nodes at all (the Square svg paths carry no text, so textContent === ""
// is the authoritative check; a stray label span would light this up).
const stopButtonIsTextFree = () => (stopButton()?.textContent ?? "x") === "";

console.log("\ncomposer stop entry under unknown runtime state (task 510)");

// ① synced 基线：运行中可见可点（防回归锚）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush(); // 981 二轮：单例 commit 已合批，测试同步断言前显式落地
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "synced running composer renders the stop button");
  eq(stopButton()?.disabled, false, "synced stop button is enabled");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "synced stop click fires onCancel");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ② unknown 降级态：按钮仍在、aria 明示状态未知、点击发出取消（修复本体）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("unknown"));
  runtimeStateStore.flush();
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "unknown state keeps the stop button visible");
  eq(stopButton()?.disabled, false, "unknown stop button is not disabled by a stale projection");
  eq(stopButton()?.getAttribute("aria-label"), en["composer.stopUnknownState"], "unknown stop aria-label discloses the degraded state");
  eq(stopButton()?.getAttribute("title"), en["composer.stopUnknownState"], "tl1847 unknown stop hover title matches the degraded-state hint (no tier hints while unknown)");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "unknown stop click still fires onCancel (no hard early-return)");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ③ unknown + 过期快照 cancellable=false：过期数据不锁死出口。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("unknown", { cancellable: false }));
  runtimeStateStore.flush();
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  eq(stopButton()?.disabled, false, "stale cancellable=false does not disable the stop exit while unknown");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "stale cancellable=false still lets the stop request through");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ④ tl1765 语义换代：synced 投影 cancellable=false + 本地 running=true 正是
// turn_done 断链的分叉现场（20261122 GC专员截图：按钮渲染但点击无声吞掉，
// 三档 tooltip 正常却整体不可达）。投影否决只在「本地同样不在跑」的静止
// 场景保留防误操作语义——而按钮的渲染条件（running && !finishing / settled
// 定格）已把静止场景挡在外面（settled 另有独立禁用）。分叉态放行：Cancel
// 对无活轮 controller 幂等无害（cancelTurnLocked 快进快出），停止三档
// （点1 L1 / 点2 15s 强制 / 点3 立即）保持可达。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { cancellable: false }));
  runtimeStateStore.flush();
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "diverged state (local running, projection idle) still renders the stop button");
  eq(stopButton()?.disabled, false, "diverged cancellable=false no longer disables the stop exit (tl1765)");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "diverged stop click fires onCancel (three-tier ladder stays reachable)");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑤ finishing：不出按钮（P7 阶梯语义未动）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { phase: "finishing" }));
  runtimeStateStore.flush();
  const { root } = await renderComposer({ running: true, tabId: "t510-stop" });
  eq(stopButton(), null, "finishing composer renders no stop button");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑤b tl1304 ①（根治取消链）：cancelRequested=true（停止已受理但未生效）不再
// 锁死停止按钮——这正是升级阶梯（461-P7 L2/L3）必须可达的现场；此前 cancellable
// 被否决成 false，按钮禁用 + handleCancel 早退，用户后续每一次按停止都被无声吞掉。
// 受理 ≠ 不可取消；连点风暴由 cancelSettling 在途禁用兜底（不入此断言面）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { cancelRequested: true }));
  runtimeStateStore.flush();
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "cancelling state keeps the stop button visible");
  eq(stopButton()?.disabled, false, "cancelRequested=true no longer disables the stop button (tl1304 ①)");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "cancelRequested=true click still fires onCancel (escalation stays reachable)");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑥ 全局 failed 不波及本 tab（b 面隔离，UI 级）：任一其他 tab 的同步异常曾经让
// 全部 composer 一起丢按钮；现在全局失败只留在 store.getFailed 供树图降级。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush(); // 981 二轮：单例 commit 已合批，测试同步断言前显式落地
  runtimeStateStore.fail();
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "global sync failure no longer hides this tab's stop button");
  eq(stopButton()?.disabled, false, "global sync failure leaves the stop exit enabled");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "global sync failure still lets the stop request through");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── 任务1015：五态 queued_followup（输入区排队态）与长工具链可见性 ──
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { phase: "executing" }));
  runtimeStateStore.flush();
  const { root, calls } = await renderComposer({
    running: true,
    tabId: "t1015-queued",
    turnRuntimeState: "queued_followup",
    currentToolLabel: "bash",
    lastTurnEventAt: Date.now() - 5000,
  });
  // 排队态与 running 可区分：run strip + placeholder 呈现排队文案。
  ok(Boolean(document.querySelector(".composer-run-strip")?.textContent?.includes(en["composer.queuedStrip"])),
    "queued_followup run strip announces the queued state");
  const input = document.querySelector("textarea.composer__input:not([aria-hidden=true])") as HTMLTextAreaElement;
  eq(input?.getAttribute("placeholder"), en["composer.queuedPlaceholder"], "queued_followup placeholder announces the queued state");
  // 四信号一致（验收1）：turn 真在跑 → 停止按钮在场 + steer 形态。
  ok(stopButton() !== null, "queued_followup keeps the stop button visible (turn is genuinely running)");
  ok(Boolean(document.querySelector(".composer__btn--send.composer__btn--steer")), "queued_followup keeps the steer send form");
  eq(stopButton()?.disabled, false, "queued_followup stop button is enabled");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "queued_followup stop click fires onCancel");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── 任务1015：长工具链「正在执行 <工具>」+ 心跳（无输出期不呈现为静止）──
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { phase: "executing" }));
  runtimeStateStore.flush();
  const { root } = await renderComposer({
    running: true,
    tabId: "t1015-tool",
    turnRuntimeState: "running",
    currentToolLabel: "bash",
    lastTurnEventAt: Date.now() - 5000,
  });
  const stripText = document.querySelector(".composer-run-strip")?.textContent ?? "";
  ok(stripText.includes(en["composer.executingTool"].replace("{tool}", "bash")),
    "running strip names the executing tool");
  ok(stripText.includes("last event") && /[0-9]/.test(stripText),
    "running strip shows the last-event heartbeat");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── 任务1020：心跳单位回归兜底——秒/毫秒混用或超 24h 不得呈现荒谬大数 ──
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { phase: "executing" }));
  runtimeStateStore.flush();
  const { root } = await renderComposer({
    running: true,
    tabId: "t1020-epoch-seconds",
    turnRuntimeState: "running",
    currentToolLabel: "bash",
    lastTurnEventAt: Math.floor(Date.now() / 1000), // 模拟 epoch 秒被当毫秒混入（1020 回归形态）
  });
  const stripText = document.querySelector(".composer-run-strip")?.textContent ?? "";
  ok(!/[0-9]{6,}/.test(stripText), "epoch-seconds heartbeat renders no 10^9-scale number");
  ok(stripText.includes("—"), "epoch-seconds heartbeat falls back to the em-dash guard");
  await act(async () => { root.unmount(); });
  dom.window.close();
}
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { phase: "executing" }));
  runtimeStateStore.flush();
  const { root } = await renderComposer({
    running: true,
    tabId: "t1020-stale-day",
    turnRuntimeState: "running",
    currentToolLabel: "bash",
    lastTurnEventAt: Date.now() - 25 * 60 * 60 * 1000, // 超 24h 的陈旧心跳
  });
  const stripText = document.querySelector(".composer-run-strip")?.textContent ?? "";
  ok(!/[0-9]{6,}/.test(stripText), "stale (>24h) heartbeat renders no huge number");
  ok(stripText.includes("—"), "stale (>24h) heartbeat falls back to the em-dash guard");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── tl1420 三级终止档位显形 → tl1847 去文字：主反馈在按钮本体的色阶
// （三档三色 class），按钮零文字，hover title/卡片接管全部文案，aria 保留
// 完整档位语义 ──
// ⑦ L1 已受理：首击后按钮仍零文字（tl1847），只有 stopping 档 class（琥珀）
// + aria 全文案 + title 档位说明；1s 无可见效果 armed 加脉冲 class（再点一
// 次进强制的视觉信号；脉冲本体 CSS 动画 jsdom 不可验，这里钉 class 配对）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush(); // 981 二轮：单例 commit 已合批，测试同步断言前显式落地
  const { root } = await renderComposer({ running: true, tabId: "tl1420-l1", stopLevel: 0, stopDeadlineUnix: 0 });
  eq(stopButton()?.classList.contains("composer__btn--stop-stopping"), false, "tl1420 idle keeps the resting look (no tier class)");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(stopButton()?.textContent, "", "tl1847 L1 button is text-free (no 停止中 copy, no compact tag)");
  eq(stopButton()?.querySelector("span"), null, "tl1847 L1 button carries no label span child");
  eq(stopButton()?.getAttribute("aria-label"), en["composer.stopStopping"], "tl1420 L1 aria-label still reads the full tier text 停止中… (tl1847: SR unaffected by the de-texting)");
  eq(stopButton()?.getAttribute("title"), en["composer.stopNormal"], "tl1847 L1 hover title reads the first-press semantics");
  eq(stopButton()?.classList.contains("composer__btn--stop-stopping"), true, "tl1420 L1 carries the stopping tier class right after the click");
  eq(stopButton()?.classList.contains("composer__btn--stop-armed"), false, "tl1420 fresh L1 does not pulse yet");
  await act(async () => { await new Promise((r) => setTimeout(r, 1100)); });
  ok(Boolean(stopButton()?.classList.contains("composer__btn--stop-armed")), "tl1420 armed adds the pulse class 1s after the click without visible effect");
  eq(stopButton()?.classList.contains("composer__btn--stop-stopping"), true, "tl1420 armed stays on the stopping (amber) tier");
  ok(stopButtonIsTextFree(), "tl1847 armed button stays text-free");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑧ L2 强制宽限：倒计时数字不上按钮（tl1847 零文字），改走 aria-label 每秒
// 更新（红色渐深档 class 不动，state 机零改动只改呈现）；hover title 挂
// 15s 宽限说明。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush();
  const deadline = Math.floor(Date.now() / 1000) + 9;
  const { root } = await renderComposer({ running: true, tabId: "tl1420-l2", stopLevel: 2, stopDeadlineUnix: deadline });
  const btn = stopButton();
  ok(Boolean(btn?.classList.contains("composer__btn--stop-grace")), "tl1420 L2 grace carries the grace tier class");
  ok(!btn?.classList.contains("composer__btn--stop-stopping"), "tl1420 grace is not the amber step (red escalation)");
  ok(stopButtonIsTextFree(), "tl1847 grace button is text-free (countdown digits live in aria + hover card, not on the button)");
  const readNs = (): number => {
    const aria = stopButton()?.getAttribute("aria-label") ?? "";
    const m = aria.match(/(\d+)\s*s/);
    return m ? Number(m[1]) : -1;
  };
  const n0 = readNs();
  ok(n0 >= 7 && n0 <= 9, `tl1847 grace aria-label carries the live countdown (got ${n0}s)`);
  eq(btn?.getAttribute("aria-label"), en["composer.stopForceCountdown"].replace("{n}", String(n0)), "tl1847 grace aria-label keeps the full 强制停止 · Ns text");
  eq(btn?.getAttribute("title"), en["composer.stopForceHint"], "tl1847 grace hover title reads the 15s-grace hint (点击两次…)");
  await act(async () => { await new Promise((r) => setTimeout(r, 1100)); });
  const n1 = readNs();
  ok(n1 >= 0 && n1 < n0, `tl1847 grace countdown still updates every second via aria (${n0}s → ${n1}s); the tier class (渐深) follows the same state`);
  eq(stopButton()?.classList.contains("composer__btn--stop-grace"), true, "tl1847 grace tier class holds while the countdown ticks");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑨ L3 已终止：force 档 class（最深红阶）+ 零文字（tl1847），aria/title 接管
// 「已终止」语义（还在 running 期间即显形）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush();
  const { root } = await renderComposer({ running: true, tabId: "tl1420-l3", stopLevel: 3, stopDeadlineUnix: 0 });
  const btn = stopButton();
  ok(Boolean(btn?.classList.contains("composer__btn--stop-force")), "tl1420 L3 carries the force tier class");
  eq(btn?.textContent, "", "tl1847 L3 button is text-free (已终止 lives in aria + the hover card)");
  eq(btn?.getAttribute("aria-label"), en["composer.stopTerminated"], "tl1582 L3 aria-label keeps the full 已终止 text");
  eq(btn?.getAttribute("title"), en["composer.stopImmediate"], "tl1847 L3 hover title reads the immediate-stop hint (点击三次…)");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑩ L3 定格回 idle：turn 终局后按钮短暂定格「已终止」（settle linger），到时
// 回 idle 并卸载；定格期内按钮 inert（没有可停的东西了）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush();
  const { root, rerender } = await renderComposer({ running: true, tabId: "tl1420-settle", stopLevel: 1, stopDeadlineUnix: 0 });
  eq(stopButton()?.classList.contains("composer__btn--stop-stopping"), true, "tl1420 settle pre: stop engaged (L1 visible)");
  await act(async () => {
    runtimeStateStore.commit(projection("synced", { running: false, phase: "idle", turnStatus: "completed" }));
    runtimeStateStore.flush();
    await rerender({ running: false });
  });
  const btn = stopButton();
  ok(btn !== null, "tl1420 settled keeps the button mounted during the hold");
  ok(Boolean(btn?.classList.contains("composer__btn--stop-settled")), "tl1420 settled carries the settled tier class");
  eq(btn?.textContent, "", "tl1847 settled button is text-free during the hold");
  eq(btn?.getAttribute("title"), en["composer.stopImmediate"], "tl1847 settled hover title keeps the immediate-stop hint during the hold");
  eq(btn?.disabled, true, "tl1420 settled button is inert (nothing left to stop)");
  await act(async () => { await new Promise((r) => setTimeout(r, 1900)); });
  eq(stopButton(), null, "tl1420 after the linger the button is back to idle and unmounts");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── tl1582 hover 卡片（tl1847 起为文案唯一完整载体之一）：完整三段档位文案
// 上既有 Tooltip 载体 ──
// 验收②：hover 悬浮卡片完整显示三段文案+可读性（三行结构、当前档高亮、
// note 说明齐备）；验收③：载体与 hover 体系一致（还是任务1033 的 .tooltip，
// 不新造弹层）。tl1847：按钮本体零文字，卡片 + 原生 title 双通道接管文案。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush();
  const { root } = await renderComposer({ running: true, tabId: "tl1582-l1-card", stopLevel: 0, stopDeadlineUnix: 0 });
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  const btn = stopButton();
  eq(btn?.textContent, "", "tl1847 L1 button carries no text at all (compact tag gone with tl1847)");
  const trigger = btn?.closest(".tooltip-trigger");
  ok(trigger !== null, "tl1582 stop button sits in the tooltip trigger (carrier unchanged)");
  await act(async () => {
    trigger?.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await new Promise((r) => setTimeout(r, 250)); // tooltip show delay is 180ms
    await flushTimers();
  });
  const card = document.querySelector<HTMLElement>(".tooltip");
  ok(card !== null, "tl1582 hovering pops the hover card");
  const cardText = card?.textContent ?? "";
  ok(cardText.includes(en["composer.stopStopping"]), "tl1582 card lists tier 1 停止中…");
  ok(cardText.includes(en["composer.stopForceCountdown"].replace("{n}", "15")), "tl1582 card lists tier 2 强制停止 · 15s (ceiling copy off-grace)");
  ok(cardText.includes(en["composer.stopTerminated"]), "tl1582 card lists tier 3 已终止");
  ok(cardText.includes(en["composer.stopNormal"]), "tl1582 card carries the tier-1 note");
  ok(cardText.includes(en["composer.stopForceHint"]), "tl1582 card carries the tier-2 note (the press-again copy)");
  const rows = card?.querySelectorAll(".tooltip__stopcard-row");
  eq(rows?.length, 3, "tl1582 card renders exactly three tier rows");
  ok(rows?.[0]?.classList.contains("tooltip__stopcard-row--current") === true, "tl1582 L1 hover marks row 1 as current");
  ok(rows?.[1]?.classList.contains("tooltip__stopcard-row--current") === false, "tl1582 L1 hover does not mark the force row");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// tl1582 grace 态：当前档 = 强制行，卡片主文案带实时倒计时数字（tl1847：
// 按钮零文字，实时数字从 aria-label 读）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.flush();
  const deadline = Math.floor(Date.now() / 1000) + 9;
  const { root } = await renderComposer({ running: true, tabId: "tl1582-grace-card", stopLevel: 2, stopDeadlineUnix: deadline });
  await act(async () => {
    stopButton()?.closest(".tooltip-trigger")?.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await new Promise((r) => setTimeout(r, 250));
    await flushTimers();
  });
  const card = document.querySelector<HTMLElement>(".tooltip");
  ok(card !== null, "tl1582 grace hover pops the card");
  const rows = card?.querySelectorAll(".tooltip__stopcard-row");
  ok(rows?.[1]?.classList.contains("tooltip__stopcard-row--current") === true, "tl1582 grace hover marks the force row as current");
  ok(rows?.[0]?.classList.contains("tooltip__stopcard-row--current") === false, "tl1582 grace hover does not mark row 1");
  const n = Number(stopButton()?.getAttribute("aria-label")?.match(/(\d+)\s*s/)?.[1] ?? -1);
  ok(n >= 7 && n <= 9, `tl1847 grace pre: live countdown readable from aria-label (${n}s)`);
  ok((card?.textContent ?? "").includes(en["composer.stopForceCountdown"].replace("{n}", String(n))), "tl1582 grace card shows the live countdown in the force row");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// tl1582 unknown 态不回归（任务510 契约）：tooltip 仍是「状态未知」提示，
// 不出三段档位卡片。tabId 必须用 projection 工厂写死的 "t510-stop"，
// session 对上号 unknown 才会生效（与块②同键）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("unknown"));
  runtimeStateStore.flush();
  const { root } = await renderComposer({ running: true, tabId: "t510-stop" });
  eq(stopButton()?.getAttribute("aria-label"), en["composer.stopUnknownState"], "tl1582 unknown pre: aria already reads the degraded state");
  await act(async () => {
    stopButton()?.closest(".tooltip-trigger")?.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await new Promise((r) => setTimeout(r, 250));
    await flushTimers();
  });
  const card = document.querySelector<HTMLElement>(".tooltip");
  eq(card?.textContent, en["composer.stopUnknownState"], "tl1582 unknown tooltip stays the state-unknown hint (no tier card)");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// tl1847 CSS 防线（上限+新增即红）：按钮零文字后，三档可区分性完全压在
// 色阶/动画上——这里把守 ① 72px 物理上限（残留回归守卫）② 三档渐深色值
// （--stop-tier-pct 阶梯）③ armed 脉冲动画 ④ 主题包守卫规则 ⑤ 去文字死规则
// 不复活（.composer__stop-label / .composer__stop-countdown 一旦回来即红）。
// compact 文案预算仍由 stop-button.test.ts 的字符断言把守（键保留防回滚）。
{
  const css = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), "..", "styles.css"),
    "utf8",
  );
  const stopBlock = /\.composer__btn--stop \{[^}]*\}/.exec(css);
  ok(stopBlock !== null, "tl1582 the .composer__btn--stop block exists in styles.css");
  ok(stopBlock?.[0].includes("max-width: 72px") === true, "tl1847 stop button keeps the 72px physical cap (regression guard)");
  // 渐深阶梯（tl1420 色阶 = tl1847 后唯一档位信号，动一档即红）：
  // L1 琥珀 16% → L2 红 34% → L3 红 48%（定格 26%）。
  const tierPct = (cls: string): string | null =>
    new RegExp(`\\.composer__btn--stop-${cls} \\{[^}]*--stop-tier-pct: ([^;]+);`).exec(css)?.[1] ?? null;
  eq(tierPct("stopping"), "16%", "tl1847 L1 stopping tier keeps the amber 16% step");
  eq(tierPct("grace"), "34%", "tl1847 L2 grace tier keeps the red 34% step (deeper than L1)");
  eq(tierPct("force"), "48%", "tl1847 L3 force tier keeps the deepest red 48% step");
  eq(tierPct("settled"), "26%", "tl1847 settled tier keeps its 26% hold tone");
  // L1 armed 脉冲动画（无文字后的 armed 升级信号本体）。
  ok(/@keyframes composer-stop-pulse \{/.test(css), "tl1847 the armed pulse keyframes exist (escalation signal without text)");
  ok(/\.composer__btn--stop-armed \{\s*animation: composer-stop-pulse/.test(css), "tl1847 the armed class still drives the pulse animation");
  // 主题包场景下三档色阶不被压平（守卫规则仍引用档位变量）。
  ok(css.includes('.composer__btn--stop[class*="composer__btn--stop-"]') && css.includes("var(--stop-tier-color"), "tl1847 the theme-pack guard still re-applies the tier hue");
  // 死规则不复活：去文字后标签 span 的 CSS 一旦回来即红（防静默回滚）。
  ok(!css.includes(".composer__stop-label"), "tl1847 the dead .composer__stop-label rule stays removed");
  ok(!css.includes(".composer__stop-countdown"), "tl1847 the dead .composer__stop-countdown rule stays removed");
}

process.stdout.write(`\ncomposer stop unknown: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
