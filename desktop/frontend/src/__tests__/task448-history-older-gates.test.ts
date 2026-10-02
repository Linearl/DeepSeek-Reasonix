// 任务 448 验收（384 组件层收尾 + zcode 两项借鉴 B1/B3）：
//  B2  组件层 4 处 `running` 闸清零 —— 与 384 已交付的 controller 层同口径：
//      只有 hasOlder / loading 两态闸请求，running 只是展示态。
//  B3  失败不进 error 态 —— `olderHistoryError` 不再隐藏"加载更早"按钮、
//      不再永久停掉自动填充；失败行照旧带原因和"重试"，下次触发直接重试。
//  B1  2 视口预取 —— 触发半径 64px → max(64, 2 × 视口高)。
//  B4  四个入口共用 `canRequestOlderHistory` 一份判定（本文件测一次）。
//  收尾（445 §1.5-2）闸拒绝留痕 —— `explainOlderHistoryGate` 回答"为什么拒"，
//      `createOlderHistoryGateLogger` 转换式去重（同因连续拒绝只记首条），
//      Transcript.requestOlder 拒绝分支接入 `history-paging` 域日志。
//
// 语义测试（running 中点击按钮确实发出请求 / 失败后按钮仍在且可点）落在
// transcript-load-older-button.test.tsx 的 DOM 断言里；本文件锁源码形状与谓词本身。
//
// Run: npx tsx src/__tests__/task448-history-older-gates.test.ts

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { canRequestOlderHistory, createOlderHistoryGateLogger, explainOlderHistoryGate, olderHistoryTriggerPx } from "../lib/historyOlderGates";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const transcript = read("../components/Transcript.tsx");
const viewport = read("../components/TranscriptViewport.tsx");
const navigation = read("../lib/useTranscriptHistoryNavigation.ts");
const kernel = read("../lib/useTranscriptKernel.ts");
// 闸的否决词只能出现在代码里，不能出现在解释历史的注释里 —— 断言一律看去注释后的形状。
const code = (text: string) => text.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/[^\n]*/g, "");

console.log("\ntask 448 component-layer history gates");

// ── 谓词本身（四入口共用，测一次）────────────────────────────────────────
{
  // loading 是两态之一：在途时一切入口让位（单飞由 controller 的 in-flight 复用承接）。
  ok(canRequestOlderHistory({ hasOlderHistory: true, loadingOlderHistory: true }) === false, "loading in flight refuses the request");
  ok(canRequestOlderHistory({ hasOlderHistory: true, loadingOlderHistory: false }) === true, "idle with older history allows the request");
  // hasOlder 是另一态：controller 的唯一硬闸（384 A′）。
  ok(canRequestOlderHistory({ hasOlderHistory: false, loadingOlderHistory: false }) === false, "exhausted history refuses the request");
  // 省略 hasOlder = 调用方看不见它（问题跳转）：按"还有"处理，由 controller 回答。
  ok(canRequestOlderHistory({ loadingOlderHistory: false }) === true, "unknown hasOlder assumes more history (question-jump path)");
  // ★ B2：谓词签名根本没有 running/error 这两个字段 —— 多传也不改变结果，
  //   这正是"running/error 只是展示态、不是策略"的可执行表达。
  ok(canRequestOlderHistory({ hasOlderHistory: true, loadingOlderHistory: false, running: true, olderHistoryError: "x" } as never) === true,
    "running / olderHistoryError are not part of the gate (B2 + B3)");
  ok(canRequestOlderHistory({ hasOlderHistory: true, loadingOlderHistory: true, running: false } as never) === false,
    "loading still refuses even when running is false");
}

// ── B1 预取半径 ──────────────────────────────────────────────────────────
{
  ok(olderHistoryTriggerPx(800) === 1600, "a 800px viewport prefetches at 1600px (two viewports)");
  ok(olderHistoryTriggerPx(300) === 600, "a short viewport still prefetches at 2x, above the 64px floor");
  ok(olderHistoryTriggerPx(10) === 64, "a tiny viewport keeps the 64px floor");
  ok(olderHistoryTriggerPx(0) === 64, "an unmeasured viewport degrades to the legacy 64px radius, never to 0");
  ok(olderHistoryTriggerPx(Number.NaN) === 64, "a non-finite viewport degrades to the legacy 64px radius");
}

// ── B2/B3：四处入口逐处锁源码形状 ────────────────────────────────────────
{
  // ① Transcript.requestOlder —— 滚动到顶 / 按钮 / 横条跳转共用的那道门。
  const requestIdx = transcript.indexOf("const requestOlder = useTranscriptCommand");
  ok(requestIdx > 0, "Transcript requestOlder gate located");
  const requestGate = code(transcript.slice(requestIdx, requestIdx + 600));
  ok(requestGate.includes("canRequestOlderHistory({ hasOlderHistory, loadingOlderHistory })"),
    "gate 1 (scroll/button/requestOlder) uses the shared predicate");
  ok(!/\brunning\b/.test(requestGate),
    "gate 1 carries no `running` refusal (384 component-layer 收尾)");

  // ② autoFill 效应 —— 既不停在 running，也不死锁在 error。
  const autoIdx = transcript.indexOf("autoFillRef.current.surface !== surfaceKey");
  ok(autoIdx > 0, "Transcript autoFill effect located");
  const autoGuard = code(transcript.slice(autoIdx, autoIdx + 900));
  ok(autoGuard.includes("canRequestOlderHistory({ hasOlderHistory, loadingOlderHistory })"),
    "gate 2 (auto-fill) uses the shared predicate");
  ok(!/\brunning\b/.test(autoGuard) && !/\bolderHistoryError\b/.test(autoGuard),
    "gate 2 neither stops on running nor locks on olderHistoryError (B2 + B3)");
  ok(autoGuard.includes("pages >= 3"), "gate 2 keeps the 3-page budget (failure retry stays bounded)");

  // ③ TranscriptViewport.showLoadOlder —— 按钮可见性。
  const showIdx = viewport.indexOf("const showLoadOlder =");
  ok(showIdx > 0, "TranscriptViewport showLoadOlder located");
  const showLine = viewport.slice(showIdx, viewport.indexOf("\n", showIdx));
  ok(showLine.includes("canRequestOlderHistory("), "gate 3 (button visibility) uses the shared predicate");
  ok(!showLine.includes("running") && !showLine.includes("olderHistoryError"),
    "gate 3 shows the button while running and after a failure (B2 + B3)");
  // 失败行仍然在（诊断面，任务 400 家族）：B3 只是不锁 UI，不是删诊断。
  ok(viewport.includes("olderHistoryError ? ` (${olderHistoryError})`") && viewport.includes("onRetryOlderHistory"),
    "the failure row still reports the reason and offers retry (B3 keeps diagnostics)");

  // ④ useTranscriptHistoryNavigation —— 问题跳转。
  ok(navigation.includes("canRequestOlderHistory({ loadingOlderHistory })"),
    "gate 4 (question jump) uses the shared predicate");
  ok(!/\brunning\b/.test(navigation.replace(/\/\/[^\n]*/g, "")),
    "gate 4's code and signature carry no `running` (the overlay used to hang forever)");
  ok(!navigation.includes("loadingOlderHistory, running"), "the hook no longer takes a running prop");
}

// ── 调用点：running 未从 props 面残留 ─────────────────────────────────────
{
  ok(!transcript.includes("loadingOlderHistory={loadingOlderHistory} running={running}"),
    "Transcript no longer passes running to the question navigator");
  ok(transcript.includes("running={running}"), "running still reaches TranscriptViewport (live-status display, unrelated)");
}

// ── B1 接线：两处触发点共用同一半径 ───────────────────────────────────────
{
  const scrollIdx = transcript.indexOf("const handleScroll = useTranscriptCommand");
  ok(scrollIdx > 0, "Transcript handleScroll located");
  const scrollBody = transcript.slice(scrollIdx, transcript.indexOf("\n", scrollIdx + 40) + 400);
  ok(scrollBody.includes("element.scrollTop <= olderHistoryTriggerPx(element.clientHeight)"),
    "the scroll trigger prefetches at max(64, 2x viewport) instead of a flat 64px");
  ok(kernel.includes("element.scrollTop > olderHistoryTriggerPx(element.clientHeight)"),
    "the wheel/key trigger shares the same radius");
  ok(!kernel.includes("HISTORY_TOP_GUARD_PX") && !/scrollTop <= 64/.test(transcript),
    "the legacy flat-64px constants are gone from both entries");
}

// ── 收尾（445 §1.5-2）：闸拒绝留痕 ────────────────────────────────────────
// 445 取证缺口：组件层拒绝此前零日志，desktop.log 里只能看到
// `history.older-request` 缺席，说不出是哪道闸、什么原因。本节锁三件事：
// explain 回答"为什么拒"、发射器只记"转换"不记每帧、Transcript 接了线。
{
  ok(explainOlderHistoryGate({ hasOlderHistory: true, loadingOlderHistory: true })
    .reason === "loading-in-progress", "explain names loading as the refusal reason");
  ok(explainOlderHistoryGate({ hasOlderHistory: false, loadingOlderHistory: false })
    .reason === "no-older-page", "explain names exhaustion as the refusal reason");
  ok(explainOlderHistoryGate({ hasOlderHistory: true, loadingOlderHistory: false }).allowed === true,
    "explain agrees with the boolean predicate on the allow side");
  ok(explainOlderHistoryGate({ loadingOlderHistory: false }).allowed === true,
    "explain treats unknown hasOlder the same way the predicate does");
  // 两份判定互为对照：任何一方漂移，这里当场红。
  const pairs: Array<{ hasOlderHistory?: boolean; loadingOlderHistory: boolean }> = [
    { hasOlderHistory: true, loadingOlderHistory: true },
    { hasOlderHistory: true, loadingOlderHistory: false },
    { hasOlderHistory: false, loadingOlderHistory: true },
    { hasOlderHistory: false, loadingOlderHistory: false },
    { loadingOlderHistory: false },
  ];
  ok(pairs.every((p) => explainOlderHistoryGate(p).allowed === canRequestOlderHistory(p)),
    "explain and the boolean predicate never disagree");

  // 发射器：同一原因连续拒绝只记首条；翻回允许后重置；原因切换各记一条。
  const lines: string[] = [];
  const log = createOlderHistoryGateLogger((message, detail) => lines.push(`${message} ${detail}`));
  log({ allowed: false, reason: "loading-in-progress" }, "viewport-user");
  log({ allowed: false, reason: "loading-in-progress" }, "load-older-button");
  log({ allowed: false, reason: "loading-in-progress" }, "auto-fill");
  ok(lines.length === 1 && lines[0].includes("reason=loading-in-progress") && lines[0].includes("trigger=viewport-user"),
    "consecutive refusals with the same reason log exactly one line");
  log({ allowed: true }, "viewport-user");
  log({ allowed: false, reason: "loading-in-progress" }, "viewport-user");
  ok(lines.length === 2, "a refusal after recovery logs a fresh line");
  log({ allowed: false, reason: "no-older-page" }, "viewport-user");
  log({ allowed: false, reason: "no-older-page" }, "viewport-user");
  ok(lines.length === 3 && lines[2].includes("reason=no-older-page"),
    "a reason switch logs its own line and stays deduplicated");
  ok(!lines.some((l) => l.includes("allowed")), "recovery itself stays silent (only refusals are logged)");
}

// ── 收尾接线：requestOlder 的拒绝分支接了发射器 ───────────────────────────
{
  const requestIdx = transcript.indexOf("const requestOlder = useTranscriptCommand");
  const requestGate = code(transcript.slice(requestIdx, requestIdx + 900));
  ok(requestGate.includes("reportOlderGateBlock.current?.(") && requestGate.includes("explainOlderHistoryGate("),
    "the refusal branch reports through the transition logger with a reason");
  ok(transcript.includes('reportFrontendLog("history-paging"'),
    "the log lands in the history-paging domain (same channel the controller layer uses)");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
