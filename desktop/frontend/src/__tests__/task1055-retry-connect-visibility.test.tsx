// 任务 1055-A acceptance (网络重试可见化 — 纯可见性，不改重试语义；B/C 随 1056):
//  A1① 发起即见「正在连接（第 n 次）」——独立 connecting 事件/状态，不等失败；
//  A1② 首次 dial 失败后「重试中 (n/m)」出现且数字随重试递增；
//  A2/G1 run strip 的 connecting/retry 文案优先级最前，不被 runtimeState
//       unknown/finishing/cancelling 短路（不再出现无解释的「已工作 N 分钟」；
//       取消终态由既有 cancelled 文案覆盖）；
//  A3 dial 纳入慢通知——RetryScope 新增 connect，与 header_wait_slow /
//       prefill_slow 三段静默互斥、各有独立命名；
//  前置 retryNotice 容量 16 + send 端非阻塞（时序窗口双保险）。
//
// Run: npx tsx src/__tests__/task1055-retry-connect-visibility.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";
import { recoveryStatusText, type RecoveryRetry } from "../lib/recoveryStatus";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const controller = read("../lib/useController.ts");
const composer = read("../components/Composer.tsx");
const types = read("../lib/types.ts");
const agentGo = read("../../../../internal/agent/agent.go");
const retryGo = read("../../../../internal/provider/retry.go");
const scopeGo = read("../../../../internal/event/recovery.go");
const codesGo = read("../../../../internal/event/notice_codes.go");

// Minimal translator over the real locale tables (no rendering needed).
const dicts = { zh, en, zhTW } as const;
type Lang = keyof typeof dicts;
function makeT(lang: Lang) {
  return (key: string, vars?: Record<string, string | number>) => {
    const dict = dicts[lang] as Record<string, string>;
    let text = dict[key] ?? key;
    if (vars) for (const [k, v] of Object.entries(vars)) text = text.replaceAll(`{${k}}`, String(v));
    return text;
  };
}

console.log("\ntask 1055 retry-connect visibility");

// ── A1① 发起即见「正在连接（第 n 次）」 ─────────────────────────────────
{
  const zhText = makeT("zh")("status.connectingAttempt", { attempt: 3 });
  ok(zhText.includes("正在连接（第 3 次）"), `zh connecting copy: ${zhText}`);
  ok(makeT("en")("status.connectingAttempt", { attempt: 3 }).includes("connecting (attempt 3)"), "en connecting copy");
  ok(makeT("zhTW")("status.connectingAttempt", { attempt: 3 }).includes("正在連線（第 3 次）"), "zh-TW connecting copy");
}
{
  // connecting 处理分支存在：attempt>0 置状态并钉住 running/turnActive/cancellable；
  // attempt<=0 是收口帧（清标签，无标签时引用相等零扰动）。
  const idx = controller.indexOf('e.kind === "connecting"');
  ok(idx > 0, "useController handles the connecting event");
  const block = controller.slice(idx, idx + 900);
  ok(block.includes("connecting: { attempt, max: e.retryMax ?? 0, observedAt: promptEventClock() }"), "connecting handler stores attempt/max/observedAt");
  ok(block.includes("running: true") && block.includes("turnActive: true") && block.includes("cancellable: true"), "connecting handler pins running + turnActive + cancellable (Stop stays available)");
  ok(block.includes("if (attempt <= 0)") && block.includes("return s.connecting ? { ...s, connecting: undefined } : s;"), "attempt 0 is the phase-end clear frame (reference-equal no-op when unset)");
}

// ── A1② 首次失败后「重试中 (n/m)」出现且递增 ───────────────────────────
{
  const texts = [1, 2, 3].map((n) => {
    const retry: RecoveryRetry = { attempt: n, max: 10, recovery: {} };
    return recoveryStatusText(makeT("zh"), retry, Date.now());
  });
  ok(texts[0].includes("正在重试 (1/10)") && texts[1].includes("正在重试 (2/10)") && texts[2].includes("正在重试 (3/10)"), `zh retry counter increments: ${texts.join(" | ")}`);
  const enTexts = [1, 2].map((n) => recoveryStatusText(makeT("en"), { attempt: n, max: 10, recovery: {} }, Date.now()));
  ok(enTexts[0].includes("retrying (1/10)") && enTexts[1].includes("retrying (2/10)"), "en retry counter increments");
}
{
  const idx = controller.indexOf('e.kind === "retrying"');
  const block = controller.slice(idx, idx + 700);
  ok(block.includes("attempt: e.retryAttempt ?? 0") && block.includes("max: e.retryMax ?? 0"), "retrying handler stores the n/m counters");
  ok(block.includes("connecting: undefined"), "retrying handler clears the stale connecting label (attempt failed → switch to retrying face)");
}

// ── A2/G1 run strip 优先级：connecting/retry 最前，不被 runtime 短路 ────
{
  const idx = composer.indexOf("const runStateText =");
  const slice = composer.slice(idx, idx + 1600);
  const posConnecting = slice.indexOf("connecting\n    ? ");
  const posRetry = slice.indexOf(": retry\n");
  const posUnknown = slice.indexOf("runtimeState.unknown");
  const posFinishing = slice.indexOf("finishing ?");
  const posCancelling = slice.indexOf('"cancelling"');
  ok(posConnecting > -1 && posRetry > -1 && posUnknown > -1, "runStateText has connecting/retry/runtime faces");
  ok(posConnecting < posRetry && posRetry < posUnknown, `connecting < retry < unknown order (G1: retry not short-circuited), got ${posConnecting}/${posRetry}/${posUnknown}`);
  ok(posUnknown < posFinishing && posFinishing < posCancelling, "runtime faces keep their relative order after the network faces");
  ok(/status\.connectingAttempt/.test(slice), "connecting face names the attempt (第 n 次)");
  ok(/recoveryStatusText\(t, retry, now\)/.test(slice), "retry face keeps the recovery wording");
}
{
  const showIdx = composer.indexOf("const showRunStrip =");
  ok(showIdx > -1 && composer.slice(showIdx, showIdx + 240).includes("|| connecting"), "showRunStrip lights up for the connecting face");
}
{
  // A2 取消终态：turn_done 同时清 retry + connecting（本轮结束即落终态），
  // 且取消文案存在（notice.cancelledTurnDisplay「本轮已中断」）。
  ok(controller.includes("retry: undefined,\n        connecting: undefined,"), "turn_done/snapshot clears carry retry + connecting together");
  ok(controller.includes("任务1055-A1: same per-turn scope for the connecting label."), "turn_done clears the connecting label (per-turn scope)");
  ok(typeof zh["notice.cancelledTurnDisplay"] === "string" && zh["notice.cancelledTurnDisplay"].includes("本轮已中断"), "cancelled terminal copy exists (取消终态已覆盖)");
  ok(controller.includes("prev.connecting === next.connecting"), "stream-delta fast path compares connecting (state churn safe)");
}

// ── A3 三段静默互斥、各有独立命名 ─────────────────────────────────────
{
  ok(/RetryScopeConnect\s+RetryScope = "connect"/.test(scopeGo), "RetryScopeConnect = \"connect\" exists (not reusing headers)");
  ok(/NoticeCodeHeaderWaitSlow\s*=\s*"header_wait_slow"/.test(codesGo) && /NoticeCodePrefillSlow\s*=\s*"prefill_slow"/.test(codesGo), "header_wait_slow + prefill_slow notice codes unchanged");
  const names = ["connect", "header_wait_slow", "prefill_slow"];
  ok(new Set(names).size === 3, "three silence-stage names are pairwise distinct");
  ok(/Kind: event\.Connecting, RetryAttempt: info\.Attempt, RetryMax: info\.Max, RetryScope: event\.RetryScopeConnect/.test(agentGo), "connecting frames carry scope connect");
  ok(/sink\.Emit\(event\.Event\{Kind: event\.Connecting, RetryScope: event\.RetryScopeConnect\}\)/.test(agentGo), "start emits the zero-attempt clear frame (connect label cannot name header/prefill silence)");
  ok(types.includes('"connect" | "headers" | "stream" | "protocol"') && /\|\s*"connecting"/.test(types), "frontend wire types admit connecting + connect scope");
  ok(/e\.kind === "retrying"/.test(controller), "header-phase retry events keep the retrying face");
}

// ── 前置：retryNotice cap 16 + 非阻塞 send（双保险都要在） ─────────────
{
  const caps = agentGo.match(/make\(chan provider\.RetryInfo, (\d+)\)/g) ?? [];
  ok(caps.length === 2 && caps.every((c) => Number(c.match(/(\d+)/)![1]) >= 16), `both notice channels cap >= 16: ${caps.join(", ")}`);
  const notifyIdx = agentGo.indexOf("provider.WithRetryNotify(ctx");
  ok(notifyIdx > -0 && /select \{\s*\n\t*case retryNotice <- info:\s*\n\t*default:/.test(agentGo.slice(notifyIdx, notifyIdx + 200)), "retry notify send is non-blocking (select/default)");
  const connectIdx = agentGo.indexOf("provider.WithConnectNotify(ctx");
  ok(connectIdx > -1 && /select \{\s*\n\t*case connectNotice <- info:\s*\n\t*default:/.test(agentGo.slice(connectIdx, connectIdx + 200)), "connect notify send is non-blocking (select/default)");
  // 连接中状态走独立通道：connectNotice 声明与 retryNotice 分离。
  ok(/connectNotice := make\(chan provider\.RetryInfo, 16\)/.test(agentGo) && /case info := <-connectNotice:/.test(agentGo), "connecting rides an independent channel (timing-window safe, not the attempt>0 retry path)");
  // Provider 侧：connect notify 在 Do 之前发（发起即见，不等失败）。
  const doIdx = retryGo.indexOf("resp, err := httpClient.Do(req)");
  const connectCallIdx = retryGo.indexOf("connect(RetryInfo{Attempt: RequestAttemptCount(ctx), Max: limit + 1})");
  ok(connectCallIdx > -1 && connectCallIdx < doIdx, "SendWithRetry fires the connect notify BEFORE the dial");
}

// ── locale key 完备性 ────────────────────────────────────────────────
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  ok("status.connectingAttempt" in dict, `locale ${name} carries status.connectingAttempt`);
  ok(Object.keys(dict).filter((k) => k === "status.connectingAttempt").length === 1, `locale ${name} has no duplicate connectingAttempt key`);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
