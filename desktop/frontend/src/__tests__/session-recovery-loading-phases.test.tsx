// 任务 560：会话加载分阶段文案。验收对照：
// ① 分阶段主标题两态可区分 + 兜底正确；
// ② loading 副文案是预期时长提示，不再是失败态的「未完成，请重试」；
// ③ ≥10 秒出现「已等待 n 秒」且随秒数递增；
// ④ 失败态文案零回归；
// ⑤ 远程连接类加载不受影响（不轮询阶段名）。
import assert from "node:assert/strict";
import { register } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { SessionRecoveryBanner } from "../components/SessionRecoveryBanner";
import { LocaleProvider } from "../lib/i18n";
import { __setSessionLoadProbeForTest, historyLoadPhaseKey } from "../lib/sessionLoadPhase";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

register(new URL("../../scripts/svg-loader.mjs", import.meta.url));

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true });
// detectLocale 在无显式 pref 时读裸 navigator：Node 24 的全局 navigator 跟随
// 操作系统语言（本机 zh-CN），会让断言随机器漂移。钉成 JSDOM 的 en-US。
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const root = createRoot(document.getElementById("root")!);

// 可控时钟与可控阶段源：now 全部走注入时钟，interval 用真实 1s 节拍推进断言。
let clockMs = 1_000_000;
let phaseName = "";
let phaseCalls = 0;
let lastPhaseTabId = "";
__setSessionLoadProbeForTest({
  phaseLoader: async (tabId) => { phaseCalls++; lastPhaseTabId = tabId; return phaseName; },
  now: () => clockMs,
});
const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const paint = (availability: Parameters<typeof SessionRecoveryBanner>[0]["availability"], tabId?: string) =>
  act(async () => root.render(<LocaleProvider><SessionRecoveryBanner availability={availability} tabId={tabId} /></LocaleProvider>));
const headline = () => document.querySelector(".session-recovery__copy strong")?.textContent;
const hint = () => document.querySelector(".session-recovery__copy > span")?.textContent;
const waitedRow = () => document.querySelector(".session-recovery__wait")?.textContent;

try {
  // 三语键齐备（zh/zh-TW 由 Record<DictKey,string> 在编译期强制镜像，这里再锚一遍文案本体）。
  for (const [dict, copy] of [
    [en, "Reading session index…"], [zh, "正在读取会话索引…"], [zhTW, "正在讀取會話索引…"],
  ] as const) assert.equal(dict["sessionRecovery.loadingIndex"], copy);
  for (const [dict, copy] of [
    [en, "Restoring message history…"], [zh, "正在还原消息历史…"], [zhTW, "正在還原訊息歷史…"],
  ] as const) assert.equal(dict["sessionRecovery.loadingEvents"], copy);
  assert.equal(zh["sessionRecovery.loadingHint"], "大会话首次加载约需 20–30 秒，之后会更快");
  assert.equal(zhTW["sessionRecovery.loadingHint"], "大會話首次載入約需 20–30 秒，之後會更快");
  assert.equal(zh["sessionRecovery.waitedSeconds"], "已等待 {n} 秒");
  // 阶段名→文案键映射（含未知兜底）。
  assert.equal(historyLoadPhaseKey("live-index-load"), "sessionRecovery.loadingIndex");
  assert.equal(historyLoadPhaseKey("cold-eventlog"), "sessionRecovery.loadingEvents");
  assert.equal(historyLoadPhaseKey(""), "sessionRecovery.loadingHistory");
  assert.equal(historyLoadPhaseKey("planner-turns"), "sessionRecovery.loadingHistory");

  // ① 阶段=索引：主标题分阶段 + ② 副文案为预期时长提示（不再是失败文案）。
  phaseName = "live-index-load";
  await paint({ kind: "loading", source: "history" }, "tab-1");
  assert.equal(phaseCalls >= 1, true, "history loading polls the backend phase at least once");
  assert.equal(lastPhaseTabId, "tab-1", "phase poll carries the loading tab id");
  assert.equal(headline(), "Reading session index…");
  assert.equal(hint(), en["sessionRecovery.loadingHint"]);
  assert.notEqual(hint(), en["sessionRecovery.historyHint"], "loading hint must not reuse the failure copy");
  assert.equal(waitedRow(), undefined, "no waited row before 10s");

  // ① 阶段=事件日志：两态可区分。
  phaseName = "cold-eventlog";
  await act(async () => { await sleep(1100); });
  assert.equal(headline(), "Restoring message history…");

  // ① 阶段未知：兜底通用文案。
  phaseName = "";
  await act(async () => { await sleep(1100); });
  assert.equal(headline(), "Loading session history…");

  // ③ ≥10 秒出现等待行，且随秒数递增。
  clockMs += 10_400;
  await act(async () => { await sleep(1100); });
  assert.equal(waitedRow(), "Waited 10s");
  clockMs += 1_300;
  await act(async () => { await sleep(1100); });
  assert.equal(waitedRow(), "Waited 11s", "waited row keeps counting up");

  // ④ 失败态文案零回归（标题与副文案逐字同基线）。
  await paint({ kind: "error", source: "history", detail: "disk error" }, "tab-1");
  assert.equal(headline(), en["sessionRecovery.historyFailed"]);
  assert.equal(hint(), en["sessionRecovery.historyHint"]);
  assert.equal(waitedRow(), undefined, "failure state shows no waited row");

  // ⑤ 远程连接类：文案不变，且不轮询后端阶段名。
  const connectionCalls = phaseCalls;
  phaseName = "cold-eventlog";
  await paint({ kind: "loading", source: "connection" });
  assert.equal(headline(), en["remoteSurface.connecting"]);
  assert.equal(hint(), en["sessionRecovery.connectionHint"]);
  assert.equal(phaseCalls, connectionCalls, "connection loading must not poll history phases");
  assert.equal(waitedRow(), undefined, "connection loading shows no waited row");
  await act(async () => { await sleep(1100); });
  assert.equal(headline(), en["remoteSurface.connecting"], "connection copy stays stable while polling would have flipped it");

  await act(async () => root.unmount());
  console.log("PASS session recovery loading phases: staged headlines, loading hint, waited row, failure-copy zero regression, connection isolation");
} finally {
  __setSessionLoadProbeForTest();
  try { await act(async () => { root.unmount(); }); } catch { /* already unmounted */ }
  dom.window.close();
}
