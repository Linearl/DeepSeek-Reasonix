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

// 六条高频失败提示 + 1081 耗尽建议——7 个 code 各命中一份
// locale 文案，且三语词典都有对应键（值非空）。
// tl1645：header_wait_light 从表内摘除——1080 后端正文与 1079 suggestion
// 文案逐字相同（两行连排同句），等待文案由后端正文单点承载（见下方钉）。
const codes: Array<[string, string]> = [
  ["surface_timeout", "notice.suggestion.surfaceTimeout"],
  ["final_readiness", "notice.suggestion.finalReadiness"],
  ["recovery_paused", "notice.suggestion.recoveryPaused"],
  ["provider_rejection", "notice.suggestion.providerRejection"],
  ["session_also_open", "notice.suggestion.leaseBusy"],
  ["session_compact_fail_streak", "notice.suggestion.oversizedEventLog"],
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

// tl1645 钉：header_wait_light 不再回填 suggestion——后端正文
// （i18n HeaderWaitLightNotice）已是完整人话文案（含 Esc 取消语义），
// 1079 suggestion 行与之逐字重复（用户 20261116 15:33「出现两次」现场）。
// 文案键保留在词典（不动文案内容），仅摘除映射，重复渲染不再发生。
assert.equal(noticeSuggestion("header_wait_light"), undefined, "header_wait_light must NOT resolve a suggestion line (wait copy lives in the notice text alone)");
const waitItem = appendNoticeItem([], 0, "nw0", "info", "正在等待模型响应…（通常几十秒内会开始输出；若长时间无响应可按 Esc 取消）", "awaiting response headers (light tier), elapsed 4s", "header_wait_light").items[0];
assert.ok(waitItem.kind === "notice" && !("suggestion" in waitItem), "header_wait_light notice carries no duplicate suggestion field");

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

console.log("  PASS  notice suggestions resolve by code from the locale dictionaries (7 codes × 3 locales; header_wait_light deduped per tl1645)");
