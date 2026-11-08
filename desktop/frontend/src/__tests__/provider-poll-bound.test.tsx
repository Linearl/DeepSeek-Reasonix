// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/provider-poll-bound.test.tsx
//
// fix-provider-poll-bound 验收钉——provider 用量轮询有界化 + 按需查询
// （用户 2026-11-08 两条设计裁定）。修前红→修后绿，逐钉标注裁定条目：
//  ① 刀②（无活跃会话不查询）·侧栏卡：family 已知非 opencode-go（含无
//     活跃 tab 的 family=""）时，60s tick 必须零调用——既不发用量请求，
//     也不每 tick 重发 family 查询（旧行为：每 tick 一次 GetActivePlanFamily，
//     在 Go 侧每次解析全量 config + 弃用键 WARN = 慢性重读源）。对照钉：
//     family=opencode-go 的活跃会话照常每 tick 查用量（tl1487「常开」=
//     功能不撤，不是无会话空转）。
//  ② 刀①（失败有界）·侧栏卡：用量查询连续失败达上限（3 次）即停止本轮
//     轮询（之后 tick 零调用）+ 卡片渲染暂停标记；手动刷新仍可单发；
//     成功即清零并恢复节拍；切换事件（ACTIVE_MODEL_CHANGED_EVENT /
//     model-catalog-changed）立即重查并解除暂停。
//  ③ 刀②·planUsage store：无可见 tab（tabId undefined）时 tick 零桥调用；
//     最近答案 unsupported（当前 tab 当前模型非 plan 家族）时 tick 零调用，
//     目录变更事件强制重查。对照钉：plan 家族活跃会话每 tick 照查。
//  ④ 刀①·planUsage store：连续失败达上限后 paused=true（tick 零调用），
//     PlanUsageCard 渲染暂停标记（含无快照场景）；换 tab 即恢复。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

console.log("\nfix-provider-poll-bound provider usage polling: bounded retry + on-demand gating");

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  Event: dom.window.Event,
  IS_REACT_ACT_ENVIRONMENT: true,
});
// jsdom 的 Page Visibility 默认 hidden=true（visibilityState="prerender"），
// 会让 planUsage store 的 tick 走 document.hidden 早退——轮询节拍钉必须
// 显式置为可见，否则门槛钉在基线上假绿。
Object.defineProperty(dom.window.document, "hidden", { configurable: true, get: () => false });
Object.defineProperty(dom.window.document, "visibilityState", { configurable: true, get: () => "visible" });

// ── 桩桥：计数器 ──
let familyQueries = 0;
let usageQueries = 0;
let planUsageQueries = 0;
let familyAnswer = "";
let usagePayload: unknown = { tiers: [], note: "" };
let planUsagePayload: unknown = { supported: false, provider: "", region: "", windows: [], note: "unsupported", queriedAt: 0 };
const appStub = {
  GetActivePlanFamily: async () => { familyQueries += 1; return familyAnswer; },
  GetOpenCodeGoUsage: async () => {
    usageQueries += 1;
    if (usagePayload instanceof Error) throw usagePayload;
    return usagePayload;
  },
  GetProviderPlanUsage: async () => {
    planUsageQueries += 1;
    if (planUsagePayload instanceof Error) throw planUsagePayload;
    return planUsagePayload;
  },
};
Object.assign(window, { go: { main: { App: appStub } } });

// ── interval 登记垫片：只登记不真跑，按周期分辨 poll(60s) / 倒计时(30s) ──
type IntervalShim = { firePoll(n?: number): void; pollCount(): number };
const realSetInterval = dom.window.setInterval.bind(dom.window);
const registered: Array<{ cb: () => void; ms: number }> = [];
const shim: IntervalShim = {
  firePoll(n = 1) {
    for (let i = 0; i < n; i += 1) {
      // 只触发最近登记的 60s 级回调（30s 倒计时 tick 不算轮询）。
      for (let j = registered.length - 1; j >= 0; j -= 1) {
        if (registered[j].ms >= 50_000) { void registered[j].cb(); break; }
      }
    }
  },
  pollCount() { return registered.filter((r) => r.ms >= 50_000).length; },
};
dom.window.setInterval = ((cb: () => void, ms?: number) => {
  registered.push({ cb, ms: ms ?? 0 });
  return realSetInterval(cb, ms);
}) as typeof dom.window.setInterval;

const flush = async () => { await act(async () => { await Promise.resolve(); }); };

const { LocaleProvider } = await import("../lib/i18n");

// ════ ①② 侧栏卡：OpenCodeGoUsageSidebarCard ════
const { OpenCodeGoUsageSidebarCard } = await import("../components/OpenCodeGoUsageSidebarCard");

const container = document.createElement("div");
document.body.appendChild(container);
let root: Root | null = null;
async function mountCard(tabId?: string) {
  root = createRoot(container);
  await act(async () => {
    root!.render(<LocaleProvider><OpenCodeGoUsageSidebarCard tabId={tabId} /></LocaleProvider>);
  });
  await flush();
}
async function unmountCard() {
  await act(async () => { await root!.unmount(); });
  root = null;
  container.innerHTML = "";
  registered.length = 0;
}
const sidebar = () => container.querySelector('[data-testid="opencode-go-usage-sidebar"]');

// ── ① 刀②：无相关活跃面（family=""）→ 挂载仅一次 family 查询，tick 零调用 ──
{
  familyAnswer = "";
  familyQueries = 0; usageQueries = 0;
  await mountCard("tab-a");
  ok(familyQueries === 1, "无活跃相关面：挂载仅一次 family 查询（tab 存在但非本 provider）");
  ok(usageQueries === 0, "无活跃相关面：挂载不发用量请求");
  ok(sidebar() === null, "无活跃相关面：无卡 DOM");
  shim.firePoll(3);
  await flush();
  ok(familyQueries === 1 && usageQueries === 0,
    `刀②红→绿：3 个 60s tick 零调用（修前 familyQueries=4；实测 family=${familyQueries} usage=${usageQueries}）`);
  await unmountCard();
}

// ── ① 对照钉：活跃 opencode-go 会话 → 每 tick 照常查用量（tl1487 常开语义） ──
{
  familyAnswer = "opencode-go";
  usagePayload = { tiers: [{ window: "rolling", percent: 42, resetsAt: "" }], note: "" };
  familyQueries = 0; usageQueries = 0;
  await mountCard("tab-a");
  ok(usageQueries === 1, "对照：挂载即查用量（活跃相关会话）");
  ok(sidebar() !== null, "对照：卡渲染");
  shim.firePoll(2);
  await flush();
  ok(usageQueries === 3, `对照：活跃会话下 tick 照常轮询（usage=${usageQueries}，挂载 1 + tick 2）`);
  await unmountCard();
}

// ── ② 刀①：连续失败 3 次 → 停轮询 + 暂停标记；手动刷新单发；成功恢复 ──
{
  familyAnswer = "opencode-go";
  usagePayload = new Error("boom");
  familyQueries = 0; usageQueries = 0;
  await mountCard("tab-a");
  ok(usageQueries === 1, "刀①：挂载第 1 次失败");
  shim.firePoll(2);
  await flush();
  ok(usageQueries === 3, "刀①：tick 失败第 2、3 次");
  const pausedLine = container.querySelector('[data-testid="opencode-go-usage-paused"]');
  ok(pausedLine !== null, "刀①红→绿：连续失败 3 次渲染暂停标记（修前无此元素）");
  ok((pausedLine?.textContent ?? "").includes("3"), "刀①：标记携带失败次数");
  shim.firePoll(3);
  await flush();
  ok(usageQueries === 3, `刀①红→绿：暂停后 3 个 tick 零调用（修前 usage=6；实测 ${usageQueries}）`);
  // 手动刷新：暂停中仍可单发；仍失败则维持暂停。
  await act(async () => {
    (container.querySelector('[data-testid="opencode-go-usage-sidebar"] button') as HTMLButtonElement).click();
    await Promise.resolve();
  });
  await flush();
  ok(usageQueries === 4, "刀①：手动刷新在暂停中仍单发一次");
  ok(container.querySelector('[data-testid="opencode-go-usage-paused"]') !== null, "刀①：手动刷新仍失败 → 暂停维持");
  // 成功 → 清零恢复节拍。
  usagePayload = { tiers: [{ window: "rolling", percent: 7, resetsAt: "" }], note: "" };
  await act(async () => {
    (container.querySelector('[data-testid="opencode-go-usage-sidebar"] button') as HTMLButtonElement).click();
    await Promise.resolve();
  });
  await flush();
  ok(usageQueries === 5, "刀①：手动刷新成功（第 5 次调用）");
  ok(container.querySelector('[data-testid="opencode-go-usage-paused"]') === null, "刀①：成功后暂停标记消失");
  shim.firePoll(1);
  await flush();
  ok(usageQueries === 6, "刀①：成功后轮询节拍恢复（下一 tick 照查）");
  await unmountCard();
}

// ── ② 刀①：事件恢复——切换事件立即重查并解除暂停 ──
{
  familyAnswer = "opencode-go";
  usagePayload = new Error("boom");
  familyQueries = 0; usageQueries = 0;
  await mountCard("tab-a");
  shim.firePoll(2);
  await flush();
  ok(usageQueries === 3, "事件恢复铺垫：3 次失败进入暂停");
  ok(container.querySelector('[data-testid="opencode-go-usage-paused"]') !== null, "事件恢复铺垫：暂停标记在");
  usagePayload = { tiers: [{ window: "rolling", percent: 9, resetsAt: "" }], note: "" };
  await act(async () => {
    window.dispatchEvent(new Event("reasonix:active-model-changed"));
    await Promise.resolve();
  });
  await flush();
  ok(usageQueries === 4, "刀①：模型切换事件立即重查（第 4 次）");
  ok(container.querySelector('[data-testid="opencode-go-usage-paused"]') === null, "刀①：事件解除暂停");
  shim.firePoll(1);
  await flush();
  ok(usageQueries === 5, "刀①：事件恢复后节拍照常");
  // catalog 变更事件同样立即重查。
  await act(async () => {
    window.dispatchEvent(new Event("reasonix:model-catalog-changed"));
    await Promise.resolve();
  });
  await flush();
  ok(usageQueries === 6, "刀①：目录变更事件同样立即重查");
  await unmountCard();
}

// ════ ③④ planUsage store ════
const storeModule = await import("../store/planUsage");
const { usePlanUsageStore, ensurePlanUsagePolling, stopPlanUsagePollingForTest } = storeModule;
const planCardModule = await import("../components/PlanUsageCard");
const { PlanUsageCard } = planCardModule;

const planContainer = document.createElement("div");
document.body.appendChild(planContainer);
let planRoot: Root | null = null;
async function mountPlanCard() {
  planRoot = createRoot(planContainer);
  await act(async () => {
    planRoot!.render(<LocaleProvider><PlanUsageCard tabId="tab-a" /></LocaleProvider>);
  });
  await flush();
}
async function unmountPlanCard() {
  if (!planRoot) return;
  await act(async () => { await planRoot!.unmount(); });
  planRoot = null;
  planContainer.innerHTML = "";
}

// ── ③ 刀②：无可见 tab → 零桥调用 ──
{
  stopPlanUsagePollingForTest();
  registered.length = 0;
  planUsageQueries = 0;
  planUsagePayload = { supported: true, provider: "GLM", region: "cn", windows: [{ window: "five_hour", percent: 42, resetsAt: "" }], note: "", queriedAt: 0 };
  ensurePlanUsagePolling();
  await flush();
  ok(planUsageQueries === 0, `刀②红→绿：无可见 tab 时轮询启动零桥调用（修前 =1；实测 ${planUsageQueries}）`);
  shim.firePoll(2);
  await flush();
  ok(planUsageQueries === 0, `刀②红→绿：无可见 tab 的 tick 零调用（修前 =2；实测 ${planUsageQueries}）`);
  usePlanUsageStore.getState().reportTab("tab-a");
  await flush();
  ok(planUsageQueries === 1, "刀②对照：tab 打开（reportTab）→ 立即恢复查询");
  shim.firePoll(1);
  await flush();
  ok(planUsageQueries === 2, "刀②对照：有活跃 tab 时 tick 照常查询");
  await unmountPlanCard();
}

// ── ③ 刀②：unsupported（非 plan 家族）→ tick 零调用；目录变更强制重查 ──
{
  stopPlanUsagePollingForTest();
  registered.length = 0;
  planUsageQueries = 0;
  planUsagePayload = { supported: false, provider: "", region: "", windows: [], note: "unsupported", queriedAt: 0 };
  ensurePlanUsagePolling();
  await flush();
  usePlanUsageStore.getState().reportTab("tab-a");
  await flush();
  ok(planUsageQueries === 1, "刀②：reportTab 触发的首次查询照发（family 判定需要这一次）");
  shim.firePoll(3);
  await flush();
  ok(planUsageQueries === 1, `刀②红→绿：已知 unsupported 的 tick 零调用（修前 =4；实测 ${planUsageQueries}）`);
  await act(async () => {
    window.dispatchEvent(new Event("reasonix:model-catalog-changed"));
    await Promise.resolve();
  });
  await flush();
  ok(planUsageQueries === 2, "刀②：目录变更事件强制重查（门槛刷新）");
  await unmountPlanCard();
}

// ── ④ 刀①：store 连续失败 3 次 → paused + 卡片标记；换 tab 恢复 ──
{
  stopPlanUsagePollingForTest();
  registered.length = 0;
  planUsageQueries = 0;
  planUsagePayload = { supported: true, provider: "GLM", region: "cn", windows: [], note: "network", queriedAt: 0 };
  ensurePlanUsagePolling();
  await flush();
  usePlanUsageStore.getState().reportTab("tab-a");
  await flush();
  ok(planUsageQueries === 1, "刀①铺垫：首次查询失败（network note）");
  shim.firePoll(2);
  await flush();
  ok(planUsageQueries === 3, "刀①铺垫：tick 失败第 2、3 次");
  const paused = usePlanUsageStore.getState().paused;
  ok(paused === true, `刀①红→绿：连续失败 3 次 store.paused=true（实测 ${String(paused)}）`);
  // 暂停后 tick 零调用。
  shim.firePoll(3);
  await flush();
  ok(planUsageQueries === 3, `刀①红→绿：暂停后 tick 零调用（修前 =6；实测 ${planUsageQueries}）`);
  // 无快照（windows 空且 note）场景也要能看见标记：渲染卡 → 暂停标记在。
  await mountPlanCard();
  ok(planContainer.querySelector('[data-testid="plan-usage-paused"]') !== null, "刀①：PlanUsageCard 渲染暂停标记");
  ok((planContainer.querySelector('[data-testid="plan-usage-paused"]')?.textContent ?? "").includes("3"), "刀①：卡片标记携带失败次数");
  await unmountPlanCard();
  // 换 tab → 恢复。
  planUsagePayload = { supported: true, provider: "GLM", region: "cn", windows: [{ window: "five_hour", percent: 42, resetsAt: "" }], note: "", queriedAt: 0 };
  usePlanUsageStore.getState().reportTab("tab-b");
  await flush();
  ok(planUsageQueries === 4, "刀①：换 tab 立即重查（第 4 次）");
  ok(usePlanUsageStore.getState().paused === false, "刀①红→绿：换 tab 解除暂停（修前无 paused 字段）");
  shim.firePoll(1);
  await flush();
  ok(planUsageQueries === 5, "刀①：恢复后节拍照常");
  await unmountPlanCard();
}

// ── 源码钉：三语 locale 携带暂停文案键 ──
{
  const frontendRoot = fileURLToPath(new URL("../../", import.meta.url));
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const keys = ["planUsage.paused", "planUsage.pausedShort", "settings.opencodeGoUsage.paused"];
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} 携带暂停标记三语键（missing: ${JSON.stringify(missing)}）`);
  }
  const cardSrc = readFileSync(fileURLToPath(new URL("../components/OpenCodeGoUsageSidebarCard.tsx", import.meta.url)), "utf8");
  ok(cardSrc.includes("POLL_FAILURE_LIMIT"), "侧栏卡源码携带失败上限常量（有界重试）");
  const storeSrc = readFileSync(fileURLToPath(new URL("../store/planUsage.ts", import.meta.url)), "utf8");
  ok(storeSrc.includes("PLAN_USAGE_FAILURE_LIMIT"), "store 源码携带失败上限常量（有界重试）");
}

stopPlanUsagePollingForTest();
await act(async () => { await root?.unmount?.(); });
container.remove();
planContainer.remove();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
