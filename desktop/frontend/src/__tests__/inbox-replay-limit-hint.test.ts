// Run: tsx src/__tests__/inbox-replay-limit-hint.test.ts

import { formatInboxError, formatReplayLimitHint } from "../lib/inboxError";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: got ${JSON.stringify(actual)}, want ${JSON.stringify(expected)}\n`);
    failed += 1;
  }
}

console.log("\ninbox replay-limit hint (task 51 UI-3)");

const hostErr = new Error(
  "session history exceeds safe replay limits: encoded_bytes=134320633, limit=134217728; session files were left unchanged",
);
const zh = formatInboxError(hostErr, "zh");
eq(zh.includes("会话历史超出安全回放上限"), true, "zh names replay limit");
eq(zh.includes("encoded_bytes=134320633"), true, "zh keeps host diagnostic");
eq(zh.includes("请先压缩或拆分"), true, "zh gives an action");
eq(zh === "当前状态下无法操作这条收件箱指令", false, "zh is not generic invalid-state");

const en = formatInboxError(hostErr, "en");
eq(en.includes("Session history exceeds the safe replay limit"), true, "en names replay limit");

eq(
  formatInboxError(new Error("reasonix_error:inbox_invalid_state"), "zh"),
  "当前状态下无法操作这条收件箱指令",
  "unrelated inbox errors keep existing copy",
);
eq(
  formatReplayLimitHint("session history exceeds safe replay limits", "zh-TW").includes("安全重放上限"),
  true,
  "zh-TW hint",
);

if (failed) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  process.exit(1);
}
process.stdout.write(`\n${passed} passed\n`);
