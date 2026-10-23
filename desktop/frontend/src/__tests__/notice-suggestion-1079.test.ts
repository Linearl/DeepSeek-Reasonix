// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/notice-suggestion-1079.test.ts
//
// tl1079: 高频失败提示的「怎么办」行——按 notice code 从 locale 词典
// (notice.suggestion.*) 取文案；后端显式携带的 Suggestion 优先；code 不在
// 表内 → 不渲染该行（与改动前逐字一致）。三语键齐全由 tsc 的
// Record<DictKey, string> 注解保证，这里断言三份词典的实际文案非空、且
// appendNoticeItem 的回填/优先级/缺省行为正确。
//
// 语言切换语义（locale 词典随语言生效）由 i18n.tFor 契约保证，本测试在
// 词典层断言——headless 环境不挂 LocaleProvider，t() 落在 en（设计默认）。

import assert from "node:assert/strict";
import { appendNoticeItem, noticeSuggestion } from "../lib/controllerNotices";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

// 六条高频失败提示 + 1080 轻提示 + 1081 耗尽建议——8 个 code 各命中一份
// locale 文案，且三语词典都有对应键（值非空）。
const codes: Array<[string, string]> = [
  ["surface_timeout", "notice.suggestion.surfaceTimeout"],
  ["final_readiness", "notice.suggestion.finalReadiness"],
  ["recovery_paused", "notice.suggestion.recoveryPaused"],
  ["provider_rejection", "notice.suggestion.providerRejection"],
  ["session_also_open", "notice.suggestion.leaseBusy"],
  ["session_compact_fail_streak", "notice.suggestion.oversizedEventLog"],
  ["header_wait_light", "notice.suggestion.headerWaitLight"],
  ["autopilot_resume_exhausted", "notice.suggestion.autopilotResumeExhausted"],
];
for (const [code, key] of codes) {
  const resolved = noticeSuggestion(code);
  assert.ok(resolved && resolved.length > 0, `${code} must resolve a non-empty suggestion`);
  // 三语键齐全（en 是 DictKey 源，zh/zh-TW 由 tsc 强制同键集；这里再实测一次
  // 值非空，防"键在但值为空串"的退化）。
  assert.ok((en as Record<string, string>)[key], `en dict must carry ${key}`);
  assert.ok(zh[key as keyof typeof zh], `zh dict must carry ${key}`);
  assert.ok(zhTW[key as keyof typeof zhTW], `zh-TW dict must carry ${key}`);
}

// code 不在表内 → undefined（不渲染该行，与改动前逐字一致）。
assert.equal(noticeSuggestion("loop_guard"), undefined);
assert.equal(noticeSuggestion(undefined), undefined);
assert.equal(noticeSuggestion(""), undefined);

// appendNoticeItem：无后端 suggestion 时按 code 回填（headless t() 落 en，
// 文案非空且含 code 对应 locale 键的关键词）；两者皆空 → item 无 suggestion
// 字段（渲染与改动前逐字一致）。
const withCode = appendNoticeItem([], 0, "n0", "warn", "加载超时", undefined, "surface_timeout").items[0];
assert.equal(withCode.kind === "notice" && withCode.suggestion, noticeSuggestion("surface_timeout"));
assert.ok(withCode.kind === "notice" && withCode.suggestion?.includes("tab"), "surface_timeout suggestion must carry actionable copy");

const noCode = appendNoticeItem([], 0, "n1", "warn", "some plain notice").items[0];
assert.equal(noCode.kind === "notice" && "suggestion" in noCode, false);

// 后端显式携带的 Suggestion 优先于 code 查表。
const backend = appendNoticeItem([], 0, "n2", "warn", "加载超时", undefined, "surface_timeout", undefined, undefined, "backend hard-coded line").items[0];
assert.equal(backend.kind === "notice" && backend.suggestion, "backend hard-coded line");

console.log("  PASS  notice suggestions resolve by code from the locale dictionaries (8 codes × 3 locales)");
