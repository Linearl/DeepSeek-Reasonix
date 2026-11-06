// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1459-goal-notice-localization.test.ts
//
// tl1459 stage 2, frontend half:
// 1. every goal_* backend code localizes by code in en/zh/zh-TW, with the
//    backend cause id from Detail interpolated ({cause});
// 2. an unknown cause id stays verbatim (a newer backend never shows a blank);
// 3. the notice keeps the backend technical line as its detail (dual track);
// 4. the Go notice-code set and the frontend key set agree (a code without a
//    key — or the reverse — is red), matching the tl1446 dual-track contract;
// 5. isGoalLifecycleNotice gates exactly the goal_* codes.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import assert from "node:assert/strict";
import { preloadLocale, tFor } from "../lib/i18n";
import { goalCauseLabel, isGoalLifecycleNotice, localizedNoticeText } from "../lib/controllerNotices";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string): void {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const codesGo = readFileSync(join(import.meta.dirname, "../../../../internal/event/notice_codes.go"), "utf-8");
const goGoalCodes = [...codesGo.matchAll(/NoticeCode(Goal\w+)\s*=\s*"(goal_\w+)"/g)].map((m) => m[2]);
ok(goGoalCodes.length >= 6, `Go defines the goal_* codes (${goGoalCodes.join(", ")})`);

const incident = {
  code: "goal_stopped",
  text: "goal stopped: the turn was cancelled because a durable save failed: checkpoint conflict — turns 4",
  detail: "cause=durability_failure status=stopped reason=checkpoint conflict",
};
const halted = {
  code: "goal_auto_continue_halted",
  text: "goal auto-continue stopped after a turn error; the goal is still active and the next message continues it",
  detail: "cause=turn_error status=running reason=provider exploded",
};

// en: the full localizedNoticeText path (code → key, Detail cause → label).
{
  const stopped = localizedNoticeText(incident.text, incident.code, incident.detail);
  ok(stopped === "Goal stopped: saving the conversation failed, so the turn was interrupted. It will not continue on its own — turn the goal on again to restart it.", `en: goal_stopped localizes with the cause interpolated → ${stopped}`);
  ok(!stopped.includes("checkpoint conflict"), "en: the technical reason stays out of the human line");
  const haltedText = localizedNoticeText(halted.text, halted.code, halted.detail);
  ok(haltedText === "Goal auto-continue stopped after an error. The goal is still on — send a message to continue.", `en: goal_auto_continue_halted localizes → ${haltedText}`);
  ok(!/\{\w+\}/.test(stopped) && !/\{\w+\}/.test(haltedText), "en: no raw {placeholder} survives");
}

// zh / zh-TW: same composition through the locale dictionaries.
for (const [locale, stoppedWant] of [
  ["zh", "目标已停止：会话保存失败，本轮被中断。它不会再自动继续——重新开启目标即可重新开始。"],
  ["zh-TW", "目標已停止：會話保存失敗，本輪被中斷。它不會再自動繼續——重新開啟目標即可重新開始。"],
] as const) {
  await preloadLocale(locale);
  const tl = tFor(locale);
  const stopped = tl("notice.goalStopped", { cause: tl("notice.goalCause.durabilityFailure") });
  ok(stopped === stoppedWant, `${locale}: goal_stopped template + cause → ${stopped}`);
  for (const key of ["notice.goalComplete", "notice.goalBlocked", "notice.goalPaused", "notice.goalCleared", "notice.goalAutoContinueHalted"] as const) {
    const text = tl(key, { cause: "x" });
    ok(text !== key && !/\{\w+\}/.test(text) && !/[A-Za-z]{4,}/.test(text.replace("x", "")), `${locale}: ${key} is translated → ${text}`);
  }
}

// Dual track: the notice item keeps the backend technical line as its detail.
const item = (await import("../lib/controllerNotices")).appendNoticeItem(
  [], 0, "n0", "warn", incident.text, incident.detail, incident.code,
).items[0];
ok(item.kind === "notice" && item.detail === incident.detail, "the backend technical line stays visible as the notice detail");
ok(item.kind === "notice" && !item.text.includes("checkpoint conflict"), "the notice text is the localized human half");

// Unknown cause stays verbatim, not blank.
ok(goalCauseLabel("brand_new_cause") === "brand_new_cause", "an unknown cause id renders verbatim");

// Unknown code falls through to the backend text unchanged.
ok(localizedNoticeText("some future goal text", "goal_future_kind", "cause=x") === "some future goal text", "an unmapped goal code keeps the backend text");

// The gate covers exactly the Go codes (bidirectional with the parsed set).
for (const code of goGoalCodes) ok(isGoalLifecycleNotice(code), `${code} triggers a Goal state refresh`);
ok(!isGoalLifecycleNotice("session_recovery_adopted"), "non-goal notices do not trigger a refresh");
ok(!isGoalLifecycleNotice(undefined), "a code-less notice does not trigger a refresh");

// Frontend key set == Go code set (a key without a backend code is dead copy).
const enSrc = readFileSync(join(import.meta.dirname, "../locales/en.ts"), "utf-8");
const zhSrc = readFileSync(join(import.meta.dirname, "../locales/zh.ts"), "utf-8");
const twSrc = readFileSync(join(import.meta.dirname, "../locales/zh-TW.ts"), "utf-8");
const keyRe = /"(notice\.goal(?:Cause\.\w+|Complete|Blocked|Paused|Stopped|Cleared|AutoContinueHalted))"/g;
const collect = (src: string) => new Set([...src.matchAll(keyRe)].map((m) => m[1]));
const enKeys = collect(enSrc);
ok(enKeys.size === 17, `en defines the 6 goal notice + 11 cause keys (${enKeys.size})`);
for (const src of [zhSrc, twSrc]) {
  const keys = collect(src);
  assert.deepEqual([...keys].sort(), [...enKeys].sort(), "zh/zh-TW mirror the en goal notice key set");
}
const camel = (code: string) => code.replace(/_([a-z])/g, (_, c: string) => c.toUpperCase());
for (const code of goGoalCodes) ok(enKeys.has(`notice.${camel(code)}`), `backend code ${code} has a localization key`);

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
