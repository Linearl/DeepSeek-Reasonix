// Run: tsx src/__tests__/session-metrics-snapshot.test.tsx
//
// tl1500 验收钉（子①取数纪律）：底部信息栏「请求数/运行时间」芯片的快照 hook
// ——门控关 = 零请求；门控开 = 一次取数、tab 切换清屏重取、事件流按 1s 节流、
// 失败静默保旧值。fetcher 注入，不碰真 bridge。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import type { ContextPanelInfo } from "../lib/types";
import { sessionMetricsFrom, useSessionMetricsSnapshot } from "../lib/sessionMetricsSnapshot";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

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

// 可拨时钟：节流窗口判定吃 Date.now。
let clockMs = 1_000_000;
const realDateNow = Date.now;
Date.now = () => clockMs;

function info(partial: Partial<ContextPanelInfo>): ContextPanelInfo {
  return {
    usedTokens: 0, windowTokens: 0, promptTokens: 0, completionTokens: 0,
    totalTokens: 0, reasoningTokens: 0, cacheHitTokens: 0, cacheMissTokens: 0,
    sessionCacheHitTokens: 0, sessionCacheMissTokens: 0, sessionCompletionTokens: 0,
    readFiles: [], changedFiles: [],
    ...partial,
  };
}

// fetcher fixture：按 tab 记录调用并返回编排好的响应；可切换失败态/挂起态
// （挂起用于钉「tab 切换清屏先于新响应落地」——act 会把微任务全 flush，
// 不挂起就观察不到清屏窗口）。
let calls: string[] = [];
let failNext = false;
let nextInfo: ContextPanelInfo | null = info({ requestCount: 3, elapsedMs: 61_000 });
let hangNext = false;
let release: (() => void) | null = null;
const fetcher = async (tabId: string): Promise<ContextPanelInfo> => {
  calls.push(tabId);
  if (failNext) { failNext = false; throw new Error("bridge unavailable"); }
  if (hangNext) {
    hangNext = false;
    return new Promise<ContextPanelInfo>((resolve) => {
      release = () => resolve(nextInfo ?? info({}));
    });
  }
  return nextInfo ?? info({});
};

let latestSnapshot: ReturnType<typeof useSessionMetricsSnapshot> = null;
let snapshotSeq = 0;
function Probe({ tabId, enabled, activity }: { tabId?: string; enabled: boolean; activity: unknown }) {
  latestSnapshot = useSessionMetricsSnapshot({ tabId, enabled, activity, fetcher });
  snapshotSeq += 1;
  return null;
}

const root = createRoot(document.getElementById("root")!);
const paint = (tabId?: string, enabled = true, activity: unknown = { n: 0 }) =>
  act(async () => root.render(<Probe tabId={tabId} enabled={enabled} activity={activity} />));
const flush = () => act(async () => { await Promise.resolve(); });

try {
  // ── 纯函数：ContextPanelInfo → 快照 ──
  ok(sessionMetricsFrom(null) === null, "a missing ContextPanel read maps to no snapshot");
  const mapped = sessionMetricsFrom(info({ requestCount: 12, elapsedMs: 3_600_000 }));
  ok(mapped?.requestCount === 12 && mapped?.elapsedMs === 3_600_000, "the snapshot carries the card's exact fields (requestCount/elapsedMs)");

  // ── 门控关：零请求 ──
  calls = [];
  await paint("t1", false);
  await flush();
  ok(calls.length === 0, "disabled gate never touches the bridge");
  ok(latestSnapshot === null, "disabled gate exposes no snapshot");

  // ── 门控开：一次取数；无 tab 时不取 ──
  await paint(undefined, true);
  await flush();
  ok(calls.length === 0, "no active tab means no fetch even when enabled");
  await paint("t1", true);
  await flush();
  ok(calls.length === 1 && calls[0] === "t1", "enabling fetches the visible tab once");
  ok(latestSnapshot?.requestCount === 3 && latestSnapshot?.elapsedMs === 61_000, "the fetched snapshot reaches the chip layer");

  // ── 事件流（activity 变化）：节流窗口内不重取 ──
  const activityA = { n: 1 };
  await paint("t1", true, activityA);
  await flush();
  ok(calls.length === 1, "activity inside the throttle window does not refetch");

  // ── 节流窗口外：恰好一次重取，读数随之更新 ──
  clockMs += 1_000;
  nextInfo = info({ requestCount: 5, elapsedMs: 122_000 });
  await paint("t1", true, { n: 2 });
  await flush();
  ok(calls.length === 2, "activity past the throttle window refetches exactly once");
  ok(latestSnapshot?.requestCount === 5 && latestSnapshot?.elapsedMs === 122_000, "the refreshed snapshot replaces the old readout");

  // ── tab 切换：清屏 + 换 tab 取数（上一 tab 的读数不上屏）──
  // 清屏窗口用挂起响应钉住：t2 的取数在途时，旧 tab 读数必须已经下屏。
  nextInfo = info({ requestCount: 9, elapsedMs: 5_000 });
  hangNext = true;
  await paint("t2", true, { n: 2 });
  ok(calls.length === 3 && calls[2] === "t2" && latestSnapshot === null,
    "a tab switch clears the previous tab's numbers while the new fetch is in flight");
  release?.();
  await flush();
  ok(calls.length === 3 && calls[2] === "t2", "the new tab is fetched after the switch");
  ok(latestSnapshot?.requestCount === 9, "the new tab's snapshot lands");

  // ── 失败静默：保旧值、不上抛 ──
  clockMs += 1_000;
  failNext = true;
  await paint("t2", true, { n: 3 });
  await flush();
  ok(calls.length === 4, "the failed refresh was attempted once");
  ok(latestSnapshot?.requestCount === 9, "a failed refresh keeps the last good readout");

  // ── 门控关断：清屏且不再请求 ──
  clockMs += 1_000;
  await paint("t2", false, { n: 4 });
  await flush();
  ok(latestSnapshot === null, "shutting the gate clears the snapshot");
  const callsAtDisable = calls.length;
  await paint("t2", false, { n: 5 });
  await flush();
  ok(calls.length === callsAtDisable, "a disabled gate stays inert on further activity");

  console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
} finally {
  Date.now = realDateNow;
  await act(async () => root.unmount());
  dom.window.close();
}
if (failed > 0) process.exit(1);
