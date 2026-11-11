// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/notice-write-authority-1296.test.ts
//
// tl1296 增强②：write authority stale 拒绝族 + 自动恢复纠正行的「应用崩溃后
// 正在恢复」状态说明（三语）。此前两个发射点（control 层拒绝 / desktop 自愈
// 纠正）不带 code，Go 英文长句在桌面端原样直出；本钉保证：
//   1. 两个 code 都映射到 locale 文案（localizedNoticeText 生效，不再直出）；
//   2. 三语词典键齐全且非空（en 为 DictKey 源，zh/zh-TW 由 tsc 强制同键集，
//      这里再实测值非空防退化）；
//   3. 文案确实携带崩溃恢复说明（关键词断言），并保留 reopen 补救语义；
//   4. appendNoticeItem 的 code 通路把文案落到条目上。

import assert from "node:assert/strict";
import { appendNoticeItem, localizedNoticeText } from "../lib/controllerNotices";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

const staleKey = "notice.writeAuthorityStale";
const recoveredKey = "notice.writeAuthorityRecovered";

// 三语键齐全且非空。
for (const [dict, label] of [[en, "en"], [zh, "zh"], [zhTW, "zh-TW"]] as Array<[Record<string, string>, string]>) {
  assert.ok((dict[staleKey] ?? "").length > 0, `${label} dict must carry a non-empty ${staleKey}`);
  assert.ok((dict[recoveredKey] ?? "").length > 0, `${label} dict must carry a non-empty ${recoveredKey}`);
}

// 崩溃恢复状态说明必须真的在文案里（三语各自的措辞锚点），拒绝文案同时
// 保留 reopen 补救。
assert.ok(/crash/i.test(en[staleKey]) && /reopen/i.test(en[staleKey]), "en stale copy must explain the crash recovery state and keep the reopen remedy");
assert.ok(zh[staleKey].includes("崩溃") && zh[staleKey].includes("恢复") && zh[staleKey].includes("重新打开"), "zh stale copy must explain 崩溃恢复 and keep the reopen remedy");
assert.ok(zhTW[staleKey].includes("當機") && zhTW[staleKey].includes("恢復") && zhTW[staleKey].includes("重新開啟"), "zh-TW stale copy must explain 當機恢復 and keep the reopen remedy");
assert.ok(/crash/i.test(en[recoveredKey]) && /restored automatically/i.test(en[recoveredKey]), "en recovered copy must frame the auto-restore in the crash recovery context");
assert.ok(zh[recoveredKey].includes("崩溃") && zh[recoveredKey].includes("自动恢复"), "zh recovered copy must frame 自动恢复 in the 崩溃恢复 context");
assert.ok(zhTW[recoveredKey].includes("當機") && zhTW[recoveredKey].includes("自動恢復"), "zh-TW recovered copy must frame 自動恢復 in the 當機恢復 context");

// code → 文案映射生效（不再英文直出）。
const staleText = localizedNoticeText("input was not accepted: this session is no longer writable", "session_write_authority_stale");
assert.equal(staleText, en[staleKey]);
const recoveredText = localizedNoticeText("the session's write access had gone stale", "write_authority_auto_recovered");
assert.equal(recoveredText, en[recoveredKey]);

// 无 code 的其他 notice 不受影响（改动前逐字直出）。
assert.equal(localizedNoticeText("some plain backend line"), "some plain backend line");

// appendNoticeItem 通路：code 进条目、文案用映射结果。
const item = appendNoticeItem([], 0, "n0", "warn", "input was not accepted", undefined, "session_write_authority_stale").items[0];
assert.equal(item.kind === "notice" && item.code, "session_write_authority_stale");
assert.equal(item.kind === "notice" && item.text, en[staleKey]);

console.log("  PASS  write authority stale/recovered notices localize with the crash-recovery explanation (2 codes x 3 locales)");
