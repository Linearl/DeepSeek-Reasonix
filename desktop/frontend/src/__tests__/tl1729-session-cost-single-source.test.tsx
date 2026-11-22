// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1729-session-cost-single-source.test.tsx
//
// tl1729 验收钉：会话成本双源统一——右侧栏概览卡（ContextPanel）的会话费用
// 读数只消费 runtime props（context/sessionCost/usage，与状态栏费用芯片同一
// 分发链），不再读自取的 ContextPanelInfo 快照。
//
// 反例（改前）：Panel 自 fetch info.sessionCost 与 StatusBar 读 props 的刷新
// 时机不同步，同一屏可现 $9.99（Panel）vs $1.25（状态栏）矛盾读数。
// 正例（改后）：同 props 同渲染帧 → 两个面读数恒一致；RPC 快照里的过期
// 费用值不再泄漏到费用格。

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as typeof globalThis.window;
globalThis.document = dom.window.document;
globalThis.localStorage = dom.window.localStorage;
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
// 英文环境（值断言 "$1.25"，避开 zh 字典 preload 时序）。userAgent 必须在
// 位：react-dom 模块级读 navigator.userAgent（本测试晚于 pin 才加载
// react-dom，缺 userAgent 会 TypeError）。
Object.defineProperty(globalThis, "navigator", { configurable: true, value: { language: "en-US", languages: ["en-US"], userAgent: "Mozilla/5.0 (X11; Linux x86_64) test-runner" } });
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
});
class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = TestResizeObserver;

const fetches: { contextPanel: number; planUsage: number } = { contextPanel: 0, planUsage: 0 };

// RPC 快照带「过期」费用 $9.99——改后费用格不得读它（反例锚）。
const staleInfo = {
  usedTokens: 1200, windowTokens: 8000, promptTokens: 900, completionTokens: 300,
  totalTokens: 1200, reasoningTokens: 0, cacheHitTokens: 0, cacheMissTokens: 0,
  sessionCacheHitTokens: 0, sessionCacheMissTokens: 0, sessionCompletionTokens: 0,
  requestCount: 3, elapsedMs: 1000,
  readFiles: [], changedFiles: [],
  sessionCost: 9.99,
  sessionCurrency: "USD",
  sessionCostUsd: 9.99,
  sessionCostComplete: true,
  sources: {},
};

(dom.window as unknown as { go?: unknown }).go = {
  main: {
    App: new Proxy({}, {
      get: (_target, prop) => () => {
        if (prop === "ContextPanel") {
          fetches.contextPanel += 1;
          return Promise.resolve(staleInfo);
        }
        if (prop === "GetProviderPlanUsage") {
          fetches.planUsage += 1;
          // 完整 PlanUsageResult 形（windows 必在）：ContextPanel 挂载
          // PlanUsageCard 读 view.windows，残形会让组件崩。
          return Promise.resolve({ supported: false, windows: [], note: "unsupported", queriedAt: 0 });
        }
        return Promise.resolve(null);
      },
    }),
  },
};

const unhandled: unknown[] = [];
process.on("unhandledRejection", (reason) => { unhandled.push(reason); });

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ContextPanel } = await import("../components/ContextPanel");
const { StatusBar } = await import("../components/StatusBar");
const { stopPlanUsagePollingForTest } = await import("../store/planUsage");
type ContextInfo = import("../lib/types").ContextInfo;

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

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

const hosts: Array<{ root: ReturnType<typeof createRoot>; dom: JSDOM }> = [];

async function mountBoth(props: {
  context: Record<string, unknown>;
  sessionCost?: number;
  sessionCurrency?: string;
  statusItems?: string[];
}) {
  document.body.innerHTML = "<div id=\"root\"></div>";
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ContextPanel tabId="tab-1" context={props.context as ContextInfo} sessionCost={props.sessionCost} sessionCurrency={props.sessionCurrency} />
        <StatusBar
          context={props.context as ContextInfo}
          cost={props.sessionCost}
          currency={props.sessionCurrency}
          running={false}
          items={props.statusItems ?? ["cost"]}
        />
      </LocaleProvider>,
    );
    await sleep(60); // ContextPanel 挂载自取 info（$9.99 注入）；字典 preload 落定。
  });
  hosts.push({ root, dom });
  return root;
}

async function unmountAll() {
  for (const { root } of hosts) {
    await act(async () => root.unmount());
  }
  hosts.length = 0;
}

const panelCostValue = () =>
  document.querySelector<HTMLElement>(".context-panel__summary-rows .context-panel__mini-stat--interactive strong")?.textContent ?? "";
const statusCostValue = () =>
  document.querySelector<HTMLElement>("[data-statusbar-item='cost'] b")?.textContent ?? "";

// ── ① quote 权威路径：同 props → 两面同值；RPC 过期 $9.99 不泄漏 ──
const quote125 = {
  original: { amount: "1.25", currency: "USD" },
  selected: { amount: "1.25", currency: "USD" },
  estimated: false,
  costComplete: true,
  displayComplete: true,
  complete: true,
  displayStatus: "matched",
  aggregateMode: "single_currency",
};
await mountBoth({ context: { used: 100, window: 1000, sessionTokens: 0, sessionCostQuote: quote125, sessionCostComplete: true } });
const panel1 = panelCostValue();
const status1 = statusCostValue();
ok(panel1 === "$1.25", `panel session cost reads the props quote (${panel1})`);
ok(status1 === "$1.25", `status bar chip reads the same props quote (${status1})`);
ok(panel1 === status1, "same screen, same readout: panel and status bar agree");
ok(!panel1.includes("9.99") && !status1.includes("9.99"), "the stale self-fetched snapshot cost ($9.99) no longer leaks to either surface");
ok(fetches.contextPanel > 0, "the panel still self-fetches info for the non-cost faces (sources/elapsed)");
await unmountAll();

// ── ② scalar 兜底路径：quote 缺席时两面同走 props 标量 ──
await mountBoth({ context: { used: 100, window: 1000, sessionTokens: 0 }, sessionCost: 1.25, sessionCurrency: "USD" });
const panel2 = panelCostValue();
const status2 = statusCostValue();
ok(panel2 === "$1.25", `panel scalar fallback reads the sessionCost prop (${panel2})`);
ok(status2 === "$1.25", `status bar scalar fallback reads the same prop (${status2})`);
ok(panel2 === status2, "scalar path keeps the two surfaces on one value");
ok(!panel2.includes("9.99"), "stale snapshot cost stays out of the scalar path too");
await unmountAll();

// ── ③ ≈ 标记同源：context.estimated=true → 两面同挂 ≈ ──
await mountBoth({ context: { used: 100, window: 1000, sessionTokens: 0, estimated: true }, sessionCost: 1.25, sessionCurrency: "USD" });
const panel3 = panelCostValue();
const status3 = statusCostValue();
ok(panel3 === "≈$1.25", `panel estimated mark follows the props flag (${panel3})`);
ok(status3 === "≈$1.25", `status bar estimated mark follows the same flag (${status3})`);
ok(panel3 === status3, "the ≈ decoration cannot disagree between the surfaces");
await unmountAll();

// ── ④ props 空费用：两面同现空态（不是快照残值）──
await mountBoth({ context: { used: 100, window: 1000, sessionTokens: 0 } });
const panel4 = panelCostValue();
const status4 = statusCostValue();
ok(!panel4.includes("9.99") && !status4.includes("9.99"), "no props cost renders no snapshot value on either surface");
ok(!panel4.includes("$") || panel4 === status4, `the empty-cost faces stay mutually consistent (${panel4} vs ${status4})`);
await unmountAll();

await act(async () => { stopPlanUsagePollingForTest(); });
dom.window.close();

const noise = unhandled.filter((reason) => !(reason instanceof Error && /ContextPanel|PlanUsage|OpenCodeGo|bridge/i.test(String((reason as Error).message))));
console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total${noise.length ? `, ${noise.length} unhandled bridge rejections` : ""}`);
if (failed > 0) process.exit(1);
if (noise.length > 0) {
  for (const reason of noise) console.error("  unhandled rejection:", reason);
  process.exit(1);
}
