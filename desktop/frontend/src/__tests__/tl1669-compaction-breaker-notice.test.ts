// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1669-compaction-breaker-notice.test.ts
//
// tl1669 压缩执行模型改同步 await（前端半）：连续失败 3 次熔断的用户可见提示。
//
// 后端语义（internal/agent context_receipt.go emitCompactionFailed）：
//   连续 compactionFailBreakerLimit(=3) 次失败 → 熔断本代内的自动压缩，
//   当前回合不中断（用现有上下文继续），下一次上下文检查恢复自动压缩。
//   事件形态 = Notice，code=compaction_breaker，detail=streak=N limit=3 trigger=…
//
// 本文件钉：
//  ① 三语词典都有 recovery.noticeCompactionBreaker（缺一即编译/取值失败）；
//  ② compaction_breaker 的 notice 正文按 code 本地化，不再回落到后端英文原文；
//  ③ streak 参数真的插进正文（用户看到「第 N 次」）。

import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import { localizedNoticeText } from "../lib/controllerNotices";

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

const dicts: Array<[string, Record<string, string>]> = [
  ["en", en as unknown as Record<string, string>],
  ["zh", zh as unknown as Record<string, string>],
  ["zh-TW", zhTW as unknown as Record<string, string>],
];

const backendText =
  "context compaction has failed 3 times in a row; automatic compaction is paused for this generation — the turn keeps running on the current context, and the next context check resumes the attempts";
const detail = "streak=3 limit=3 trigger=pressure";

// ① the key exists in every locale.
for (const [name, dict] of dicts) {
  ok(
    typeof dict["recovery.noticeCompactionBreaker"] === "string" &&
      dict["recovery.noticeCompactionBreaker"].length > 0,
    `${name} carries recovery.noticeCompactionBreaker`,
  );
}

// ② the notice text localizes by code (no raw backend English fallback).
// The active locale in this runner is the default (en); the per-locale parity
// is pinned by ① (every dictionary carries the key).
{
  const text = localizedNoticeText(backendText, "compaction_breaker", detail);
  ok(text !== backendText, "the compaction_breaker notice localizes instead of the raw backend text");
  ok(!text.includes("{streak}"), "the rendered breaker notice has its placeholders filled");
  ok(text.startsWith((en as unknown as Record<string, string>)["recovery.noticeCompactionBreaker"].split("{streak}")[0]), "the rendered breaker notice comes from the dictionary");
  ok(text.includes("3"), "the rendered breaker notice shows the attempt count");
}

// ③ the streak reaches the rendered text.
for (const [name, dict] of dicts) {
  const text = dict["recovery.noticeCompactionBreaker"];
  ok(text.includes("{streak}"), `${name} interpolates the streak counter`);
  const rendered = text.replace("{streak}", "3");
  ok(rendered.includes("3"), `${name} shows the attempt count in the rendered notice`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
