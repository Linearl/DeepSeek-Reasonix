// Task 845 acceptance ③ (UI 三态可见) — the two new lane faces the user
// ratified, on top of the existing retry strip:
//  ① 429 QUEUE face: "服务繁忙，已排队等待重试…可点击停止" (zh/en/zh-TW);
//  ② quota LONG-WAIT face: "配额已用完，预计 N 秒后自动继续…可点击停止";
//  ③ face priority: quota_wait > queue > budget counts > generic waiting;
//  ④ the wire contract: the Go event surface serializes queue/quota_wait,
//     the retrying handler keeps cancellable true (Stop cancels the wait),
//     and the lane emission sites carry the new markers.
//
// Run: npx tsx src/__tests__/task845-queue-quota-visibility.test.tsx

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
const statusLib = read("../lib/recoveryStatus.ts");
const goRecovery = read("../../../../internal/event/recovery.go");
const goLane = read("../../../../internal/agent/sampling_recovery.go");

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

console.log("\ntask 845 queue/quota retry visibility");

const now = Date.now();
const inSeconds = (s: number) => now + s * 1000;

// ① 429 queue face, three languages, cancellable cue included.
{
  const retry: RecoveryRetry = { attempt: 5, max: 8, recovery: { queue: true, waiting: true, phase: "headers", next_attempt_at: inSeconds(45) } };
  const zhText = recoveryStatusText(makeT("zh"), retry, now);
  ok(zhText.includes("服务繁忙") && zhText.includes("排队") && zhText.includes("45") && zhText.includes("停止"), `zh queue copy: ${zhText}`);
  const enText = recoveryStatusText(makeT("en"), retry, now);
  ok(enText.includes("Service busy") && enText.includes("queued") && enText.includes("45"), `en queue copy: ${enText}`);
  const twText = recoveryStatusText(makeT("zhTW"), retry, now);
  ok(twText.includes("服務繁忙") && twText.includes("排隊"), `zh-TW queue copy: ${twText}`);
}

// ② quota long-wait face, three languages, resume expectation + cancellable.
{
  const retry: RecoveryRetry = { attempt: 2, max: 8, recovery: { quota_wait: true, waiting: true, phase: "quota", next_attempt_at: inSeconds(300) } };
  const zhText = recoveryStatusText(makeT("zh"), retry, now);
  ok(zhText.includes("配额已用完") && zhText.includes("300 秒后自动继续") && zhText.includes("停止"), `zh quota copy: ${zhText}`);
  const enText = recoveryStatusText(makeT("en"), retry, now);
  ok(enText.includes("Quota exhausted") && enText.includes("resuming automatically") && enText.includes("300"), `en quota copy: ${enText}`);
  const twText = recoveryStatusText(makeT("zhTW"), retry, now);
  ok(twText.includes("配額已用完") && twText.includes("自動繼續"), `zh-TW quota copy: ${twText}`);
}

// ③ face priority: quota_wait wins over queue; queue wins over budget counts;
//    an exhausted frame still wins over everything (the end state must show).
{
  const both: RecoveryRetry = { attempt: 2, max: 8, recovery: { quota_wait: true, queue: true, waiting: true, next_attempt_at: inSeconds(10) } };
  ok(recoveryStatusText(makeT("zh"), both, now).includes("配额已用完"), "quota_wait outranks queue when both flags ride one frame");
  const queueVsBudget: RecoveryRetry = { attempt: 3, max: 8, recovery: { queue: true, waiting: true, budget_used: 3, budget_limit: 8, next_attempt_at: inSeconds(10) } };
  ok(recoveryStatusText(makeT("zh"), queueVsBudget, now).includes("排队"), "queue outranks the generic budget counts");
  const exhausted: RecoveryRetry = { attempt: 9, max: 8, recovery: { queue: true, quota_wait: true, budget_exhausted: true, budget_used: 9, budget_limit: 8 } };
  ok(recoveryStatusText(makeT("zh"), exhausted, now).includes("已终止"), "the exhausted end state still outranks the lane faces");
}

// ④a wire contract: the Go RecoveryStatus tags the frontend reads.
ok(goRecovery.includes('Queue     bool `json:"queue,omitempty"`'), "wire tag queue stays stable");
ok(goRecovery.includes('QuotaWait bool `json:"quota_wait,omitempty"`'), "wire tag quota_wait stays stable");

// ④b lane emission sites: the queue marker rides the 429 waiting frame, the
// quota marker rides the long-wait frame, and both lanes emit the plain
// Retrying kind the controller already keeps cancellable.
{
  const waitIdx = goLane.indexOf("status := &event.RecoveryStatus{Phase: failure.Phase, Reason: failure.Code, NextAttemptAt:");
  ok(waitIdx > 0 && goLane.slice(waitIdx, waitIdx + 220).includes("Queue: queue"), "429 waiting frame carries the queue marker");
  const quotaIdx = goLane.indexOf("QuotaWait:     true");
  ok(quotaIdx > 0, "quota long-wait frame carries the quota_wait marker");
}
ok(/e\.kind === "retrying"/.test(controller) && /cancellable: true/.test(controller), "retrying handler keeps cancellable true (Stop cancels both lane waits)");

// ④c the strips share the task-372 recovery wiring — the interface carries
//    the new fields next to the 372 budget ones.
ok(statusLib.includes("queue?: boolean;") && statusLib.includes("quota_wait?: boolean;"), "RecoveryStatus interface carries the 845 fields");

// Locale keys across the three languages.
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  const keys = ["status.queuing", "status.quotaWaiting"];
  const missing = keys.filter((k) => !(k in dict));
  ok(missing.length === 0, `locale ${name} carries the 2 task-845 keys`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
