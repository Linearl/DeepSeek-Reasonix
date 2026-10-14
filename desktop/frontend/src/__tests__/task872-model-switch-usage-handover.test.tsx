// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task872-model-switch-usage-handover.test.tsx
//
// 任务 872 验收套——模型切换瞬间用量面板不得出现双 provider 卡并存
// （用户 2026-10-13 反馈：切到 OpenCode Go 的 glm 模型时 GLM·CN Plan 卡
// 未即时下线，两卡同屏被误读为双倍配额）。
//  ① 发射面：createControllerModelCommands 在切换「最新一次落定」时广播
//     ACTIVE_MODEL_CHANGED_EVENT（成功与最新失败都发；被 superseded 不发，
//     由更新的那次负责）；
//  ② store 面：事件 → invalidateForModelSwitch 同步作废旧 provider 快照并
//     重查；交错到达的旧答案（在途查询晚于切换落定才返回）被 refreshSeq
//     守卫丢弃，绝不能复活旧卡；
//  ③ 渲染面：双卡同挂（PlanUsageCard + OpenCodeGoUsageSidebarCard）下走完
//     GLM→OpenCode Go 与反向两条切换序列——每一步断言「GLM 配额卡与
//     OpenCode Go 卡永不同屏」，交接期由骨架态占位，新答案落地后只剩
//     当前 provider 卡。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { JSDOM } from "jsdom";
import { ACTIVE_MODEL_CHANGED_EVENT } from "../lib/activeModelChanged";
import { createControllerModelCommands } from "../lib/controllerModelCommands";
import {
  ensurePlanUsagePolling,
  stopPlanUsagePollingForTest,
  usePlanUsageStore,
} from "../store/planUsage";
import type { PlanUsageResult } from "../lib/planUsage";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

console.log("\ntask 872 model-switch usage handover — no two provider cards ever coexist");

// ── ① 发射面：切换落定广播，superseded 不广播 ──
{
  const dom = new JSDOM("<div id='noop'></div>", { url: "http://localhost" });
  Object.assign(globalThis, { window: dom.window, document: dom.window.document });

  let emitted = 0;
  window.addEventListener(ACTIVE_MODEL_CHANGED_EVENT, () => { emitted += 1; });

  type Ports = Parameters<typeof createControllerModelCommands>[0];
  const makePorts = (enqueue: Ports["enqueueModelSwitch"]) => ({
    statesRef: { current: new Map() },
    modelSwitchSeqByTab: { current: new Map() },
    modelSwitchSuccessVersionByTab: { current: new Map() },
    modelSwitchQueueByTab: { current: new Map() },
    enqueueModelSwitch: enqueue,
    clearBalanceForTab: () => {},
    dispatchTo: () => {},
    refreshBalanceForTab: async () => {},
    refreshMetaForTab: async () => {},
  });

  // 成功落定 → 广播一次。
  {
    const cmd = createControllerModelCommands(makePorts(async () => "applied" as const));
    const switched = await cmd.setModelForTab("tab-a", "go/glm-5");
    ok(switched === true, "success: the switch reports applied");
    ok(emitted === 1, "success settle broadcasts ACTIVE_MODEL_CHANGED_EVENT exactly once");
  }

  // 最新一次尝试失败落定 → 也广播（后端仍是旧 provider，用量面重查收敛）。
  {
    const before = emitted;
    const cmd = createControllerModelCommands(makePorts(async () => { throw new Error("boom"); }));
    const switched = await cmd.setModelForTab("tab-a", "go/glm-5");
    ok(switched === false, "failure: the switch reports false");
    ok(emitted === before + 1, "latest failed settle also broadcasts (surfaces converge back)");
  }

  // 被更新的尝试接管（superseded）→ 旧尝试不广播。
  {
    const before = emitted;
    const cmd = createControllerModelCommands(makePorts(async () => "superseded" as const));
    const switched = await cmd.setModelForTab("tab-a", "go/glm-5");
    ok(switched === false, "superseded: the switch reports false");
    ok(emitted === before, "a superseded attempt does not broadcast (the newer one owns the settle)");
  }

  // 交错：旧尝试在新尝试接管后才完成 → 只有新尝试的落定广播一次。
  {
    const before = emitted;
    let releaseOld: (() => void) | undefined;
    const gate = new Promise<void>((done) => { releaseOld = done; });
    let call = 0;
    const cmd = createControllerModelCommands(makePorts(async () => {
      call += 1;
      if (call === 1) { await gate; return "applied" as const; }
      return "applied" as const;
    }));
    const first = cmd.setModelForTab("tab-a", "zhipu/glm");
    const second = await cmd.setModelForTab("tab-a", "go/glm-5");
    ok(second === true, "interleaved: the newer attempt applies");
    releaseOld!();
    const firstResult = await first;
    ok(firstResult === false, "interleaved: the superseded older attempt resolves false");
    ok(emitted === before + 1, "interleaved: only the newer attempt's settle broadcasts once");
  }
}

// ── ②③ 渲染面：JSDOM 双卡同挂 + 可控桩桥 ──
const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  Event: dom.window.Event,
  IS_REACT_ACT_ENVIRONMENT: true,
});

// Backend truth the stub bridge reports; plan-usage answers are deferred so
// the interleaved-arrival window (plan-usage query still in flight while the
// switch already landed) is fully controllable.
let backendFamily = "zhipu";
const planQueue: Array<(view: PlanUsageResult) => void> = [];
const glmView: PlanUsageResult = {
  supported: true,
  provider: "GLM",
  region: "cn",
  windows: [{ window: "five_hour", percent: 42, resetsAt: "" }],
  note: "",
  queriedAt: Date.now(),
};
const unsupportedView: PlanUsageResult = {
  supported: false, provider: "", region: "", windows: [], note: "unsupported", queriedAt: 0,
};
Object.assign(window, {
  go: {
    main: {
      App: {
        GetActivePlanFamily: async () => backendFamily,
        GetOpenCodeGoUsage: async () => ({ tiers: [{ window: "rolling", percent: 12, resetsAt: "" }], note: "" }),
        GetProviderPlanUsage: async () => new Promise<PlanUsageResult>((resolve) => { planQueue.push(resolve); }),
      },
    },
  },
});

const flushPlanUsage = async (view: PlanUsageResult) => {
  await act(async () => {
    while (planQueue.length > 0) planQueue.shift()!(view);
    await Promise.resolve();
  });
};

const { applyLabFlags } = await import("../lib/labFlags");
const { LocaleProvider } = await import("../lib/i18n");
const { PlanUsageCard } = await import("../components/PlanUsageCard");
const { OpenCodeGoUsageSidebarCard } = await import("../components/OpenCodeGoUsageSidebarCard");

applyLabFlags({ opencodeGoUsage: true });
const container = document.createElement("div");
document.body.appendChild(container);
let root: Root | null = null;
const renderBoth = () => (
  <LocaleProvider>
    <PlanUsageCard tabId="tab-a" />
    <OpenCodeGoUsageSidebarCard tabId="tab-a" />
  </LocaleProvider>
);

// 单卡不变式：GLM 配额卡与 OpenCode Go 卡永不同屏。骨架/空态不计入配额卡。
function assertNeverBothCards(label: string) {
  const planQuota = container.querySelector('[data-testid="plan-usage-card"]') !== null;
  const goCard = container.querySelector('[data-testid="opencode-go-usage-sidebar"]') !== null;
  ok(!planQuota || !goCard, `${label}: the GLM quota card and the OpenCode Go card never coexist`);
}

await act(async () => {
  root = createRoot(container);
  root.render(renderBoth());
  ensurePlanUsagePolling();
});
await act(async () => { await Promise.resolve(); });

// 初始：GLM provider。落定 GLM 配额答案 → GLM 卡在、OpenCode 卡无。
backendFamily = "zhipu";
await flushPlanUsage(glmView);
ok(container.querySelector('[data-testid="plan-usage-card"]') !== null, "setup: the GLM plan card renders");
ok(container.querySelector('[data-testid="opencode-go-usage-sidebar"]') === null, "setup: no OpenCode Go card on the GLM model");
assertNeverBothCards("setup");

// 切换命令（桩端口：enqueue 直接 applied；真正的广播路径就是生产代码）。
type Ports = Parameters<typeof createControllerModelCommands>[0];
const cmd = createControllerModelCommands({
  statesRef: { current: new Map() },
  modelSwitchSeqByTab: { current: new Map() },
  modelSwitchSuccessVersionByTab: { current: new Map() },
  modelSwitchQueueByTab: { current: new Map() },
  enqueueModelSwitch: async () => "applied" as const,
  clearBalanceForTab: () => {},
  dispatchTo: () => {},
  refreshBalanceForTab: async () => {},
  refreshMetaForTab: async () => {},
} as Ports);
const settle = async () => { await act(async () => { await Promise.resolve(); }); };

// 序列 A：GLM → OpenCode Go。切换落定瞬间 family（零 I/O 扫描）先回、
// GLM 配额查询仍在途 —— 这正是用户截图里的交错窗口。
backendFamily = "opencode-go";
let switchedA = false;
await act(async () => {
  switchedA = await cmd.setModelForTab("tab-a", "go/glm-5");
});
await settle();
ok(switchedA, "sequence A: the switch applies");
ok(container.querySelector('[data-testid="opencode-go-usage-sidebar"]') !== null,
  "sequence A: the OpenCode Go card appears immediately after the switch lands");
ok(container.querySelector('[data-testid="plan-usage-card"]') === null,
  "sequence A: the GLM quota card is already gone in the interleaved window");
ok(container.querySelector('[data-testid="plan-usage-card-skeleton"]') !== null,
  "sequence A: the handover window shows the skeleton, not the outgoing quota");
assertNeverBothCards("sequence A interleaved");

// GLM 配额的迟到答案（unsupported）落地 → 只剩 OpenCode 卡，骨架退场。
await flushPlanUsage(unsupportedView);
ok(container.querySelector('[data-testid="opencode-go-usage-sidebar"]') !== null,
  "sequence A settled: only the OpenCode Go card remains");
ok(container.querySelector('[data-testid="plan-usage-card"]') === null,
  "sequence A settled: no GLM quota card");
ok(container.querySelector('[data-testid="plan-usage-card-skeleton"]') === null,
  "sequence A settled: the skeleton retires once the answer lands");
assertNeverBothCards("sequence A settled");

// 序列 B：OpenCode Go → GLM（反向）。family 先回 zhipu（OpenCode 卡即刻
// 下线），GLM 配额查询在途（骨架），交错期依旧单卡。
backendFamily = "zhipu";
let switchedB = false;
await act(async () => {
  switchedB = await cmd.setModelForTab("tab-a", "zhipu/glm");
});
await settle();
ok(switchedB, "sequence B: the switch applies");
ok(container.querySelector('[data-testid="opencode-go-usage-sidebar"]') === null,
  "sequence B: the OpenCode Go card is gone immediately (no stale family answer resurrects it)");
ok(container.querySelector('[data-testid="plan-usage-card"]') === null,
  "sequence B: no GLM quota card while its query is still in flight");
ok(container.querySelector('[data-testid="plan-usage-card-skeleton"]') !== null,
  "sequence B: the handover window shows the skeleton");
assertNeverBothCards("sequence B interleaved");

// GLM 配额答案落地 → 只剩 GLM 卡。
await flushPlanUsage(glmView);
ok(container.querySelector('[data-testid="plan-usage-card"]') !== null,
  "sequence B settled: the GLM plan card renders");
ok(container.querySelector('[data-testid="opencode-go-usage-sidebar"]') === null,
  "sequence B settled: no OpenCode Go card");
assertNeverBothCards("sequence B settled");

// ── ② 交错到达钉：旧模型的在途答案晚于切换落定返回，必须被 seq 守卫丢弃 ──
{
  // 一次在途轮询查询（旧模型时代发出、尚未落定），随后模型切换作废旧快照。
  await act(async () => { usePlanUsageStore.getState().refresh(); });
  const staleResolve = planQueue.shift();
  ok(Boolean(staleResolve), "interleave setup: a plan-usage query is in flight");
  await act(async () => { usePlanUsageStore.getState().invalidateForModelSwitch(); });
  const freshResolve = planQueue.shift();
  ok(Boolean(freshResolve), "interleave setup: the model switch starts its own query");
  ok(usePlanUsageStore.getState().view === null && usePlanUsageStore.getState().switching === true,
    "invalidate drops the outgoing snapshot synchronously and flags the handover");
  // 交错到达：旧查询的答案（GLM 配额）晚于切换落定才返回 → 必须整份丢弃，
  // 骨架不得被旧数据顶掉，更不得渲染旧 provider 卡。
  await act(async () => { staleResolve!(glmView); await Promise.resolve(); });
  ok(usePlanUsageStore.getState().view === null && usePlanUsageStore.getState().switching === true,
    "the stale late answer for the old model is dropped (view stays blank, handover stays flagged)");
  ok(container.querySelector('[data-testid="plan-usage-card"]') === null,
    "the stale late answer renders no GLM card");
  // 切换后新查询的答案落定 → 交接期结束。
  await act(async () => { freshResolve!(unsupportedView); await Promise.resolve(); });
  ok(usePlanUsageStore.getState().switching === false,
    "the handover flag clears once the newest answer lands");
}

await act(async () => { root!.unmount(); });
container.remove();
stopPlanUsagePollingForTest();

// ── 源码钉：store 监听切换事件；卡的骨架分支存在 ──
{
  const storeSrc = readFileSync(fileURLToPath(new URL("../store/planUsage.ts", import.meta.url)), "utf8");
  ok(storeSrc.includes("ACTIVE_MODEL_CHANGED_EVENT") && storeSrc.includes("invalidateForModelSwitch"),
    "the plan-usage store listens for the model-switch event and invalidates");
  const cardSrc = readFileSync(fileURLToPath(new URL("../components/PlanUsageCard.tsx", import.meta.url)), "utf8");
  ok(cardSrc.includes("plan-usage-card-skeleton"), "the card renders the skeleton during the handover");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
