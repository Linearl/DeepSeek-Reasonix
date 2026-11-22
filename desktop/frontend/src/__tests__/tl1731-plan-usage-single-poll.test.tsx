// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1731-plan-usage-single-poll.test.tsx
//
// tl1731 验收钉：plan 配额单 poll——状态栏 plan 芯片与右栏 PlanUsageCard 同屏
// 读数必须恒一致，且全应用只有一个轮询源（全局 planUsage store 60s）。
//
// 现状核实（20261122，基点 baeb97db2f）：tl1720 盘点锚点 StatusBar.tsx:267
// 指向的「独立 poll」实为 Task 666 注释行（"the plan usage poll targets its
// current model"）——扫描误读；StatusBar 自 Task 287 起就订阅全局 store。
// 本文件把已收敛态钉死，防回漂：
//   ① 源码契约：StatusBar/PlanUsageCard 均不得直连 GetProviderPlanUsage，
//     必须订阅 usePlanUsageStore；
//   ② 同屏一致：同一 store view → 两面同值；view 原位更新 → 两面同帧换值；
//   ③ 单 poll 行为：两面挂载 + 同 tabId reportTab → 恰好一次桥查询
//     （若状态栏私起 poll，此处计数即 >1 变红）。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { stopPlanUsagePollingForTest, usePlanUsageStore } from "../store/planUsage";
import type { PlanUsageResult } from "../lib/planUsage";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  IS_REACT_ACT_ENVIRONMENT: true,
});
// userAgent 必须在位：react-dom 模块级读 navigator.userAgent（本测试晚于
// pin 才加载 react-dom，缺 userAgent 会 TypeError）。
Object.defineProperty(globalThis, "navigator", { configurable: true, value: { language: "en-US", languages: ["en-US"], userAgent: "Mozilla/5.0 (X11; Linux x86_64) test-runner" } });
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({ matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }),
});

let queries = 0;
let seenTabs: (string | undefined)[] = [];
// tl1731：桥应答可参数化——场景②经真实链路注入配额（reportTab→refresh→桥
// 应答落 store），手工 setState 注入会被挂载轮询的桥应答覆盖（那是测试伪影，
// 生产上桥应答本身就是唯一写入口）。
let bridgeAnswer: PlanUsageResult = { supported: false, windows: [], note: "unsupported", queriedAt: 0 };
Object.assign(window, {
  go: {
    main: {
      App: {
        GetProviderPlanUsage: async (tabID?: string) => {
          queries += 1;
          seenTabs.push(tabID);
          return bridgeAnswer;
        },
      },
    },
  },
});

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const testDir = dirname(fileURLToPath(import.meta.url));
const statusBarSource = readFileSync(resolve(testDir, "../components/StatusBar.tsx"), "utf8");
const cardSource = readFileSync(resolve(testDir, "../components/PlanUsageCard.tsx"), "utf8");

// ── ① 源码契约：单 poll 架构钉（防回漂到双 poll）──
assert.doesNotMatch(statusBarSource, /GetProviderPlanUsage/, "the status bar never issues plan-usage bridge calls of its own");
assert.match(statusBarSource, /usePlanUsageStore\(\(s\) => s\.view\)/, "the status bar reads the shared plan-usage store");
assert.doesNotMatch(cardSource, /app\.GetProviderPlanUsage/, "the overview card never issues plan-usage bridge calls of its own");
assert.match(cardSource, /usePlanUsageStore\(\(s\) => s\.view\)/, "the overview card reads the shared plan-usage store");
ok(true, "source contract: both surfaces subscribe the store, neither polls the bridge directly");

const { StatusBar } = await import("../components/StatusBar");
const { PlanUsageCard } = await import("../components/PlanUsageCard");

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function mountBoth(tabId = "tab-a") {
  usePlanUsageStore.setState({ view: null, loading: false, switching: false, tabId: undefined, paused: false, failStreak: 0, lastAnswerSupported: undefined });
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <StatusBar context={{ used: 100, window: 1000, sessionTokens: 0 }} running={false} tabId={tabId} items={["plan"]} />
        <PlanUsageCard tabId={tabId} />
      </LocaleProvider>,
    );
    await sleep(30);
  });
  return {
    chip: () => container.querySelector<HTMLElement>("[data-statusbar-item='plan'] b")?.textContent ?? "",
    card: () => container.querySelector<HTMLElement>('[data-testid="plan-usage-card"]')?.textContent ?? "",
    dispose: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

const view = (percent: number): PlanUsageResult => ({
  supported: true,
  provider: "GLM",
  region: "cn",
  windows: [{ window: "five_hour", percent, resetsAt: "" }],
  note: "",
  queriedAt: Date.now(),
});

// ── ② 同屏一致：同一 store view → 两面同值；view 原位更新 → 同帧同换 ──
{
  bridgeAnswer = view(42.5);
  const r = await mountBoth();
  ok(r.chip() === "43%", `status bar chip renders the store percent (${r.chip()})`);
  ok(r.card().includes("43%"), "the overview card renders the same store percent");
  ok(r.chip() === "43%" && r.card().includes("43%"), "same screen, same quota: one view feeds both surfaces");
  await act(async () => {
    usePlanUsageStore.setState({ view: view(73.4) });
    await sleep(10);
  });
  ok(r.chip() === "73%", `chip follows the store update (${r.chip()})`);
  ok(r.card().includes("73%"), "card follows the same update in the same commit");
  await r.dispose();
}

// ── ③ 单 poll 行为锁：两面挂载 + 同 tabId → 恰好一次桥查询 ──
{
  bridgeAnswer = { supported: false, windows: [], note: "unsupported", queriedAt: 0 };
  queries = 0;
  seenTabs = [];
  const r = await mountBoth("tab-a");
  // 两面各跑一次 reportTab("tab-a")：第一次触发 store 查询，第二次幂等跳过。
  ok(queries === 1, `two surfaces, same tab → exactly one bridge query (got ${queries})`);
  ok(seenTabs.every((tab) => tab === "tab-a"), "the single query targets the reported tab");
  await r.dispose();
}

// ── ④ 反例锚（tl1731 任务书描述的退化形态）：状态栏不再有独立轮询入口 ──
{
  queries = 0;
  const r = await mountBoth("tab-a");
  const before = queries;
  // 稳定态下不再有任何额外查询（无第二 timer / 第二 poll 入口）。
  await act(async () => { await sleep(40); });
  ok(queries === before, "no surface issues a second, independent poll");
  await r.dispose();
}

stopPlanUsagePollingForTest();
dom.window.close();
process.stdout.write(`\nplan usage single poll (tl1731): ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
