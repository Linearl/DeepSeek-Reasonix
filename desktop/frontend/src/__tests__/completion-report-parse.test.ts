// Run: tsx src/__tests__/completion-report-parse.test.ts
// Task 112: structured completion report field extraction.

import assert from "node:assert/strict";
import { completionReportHasContent, parseCompletionReport } from "../lib/completionReport";

const sample = [
  "已经把 A1 做完了，下面是完成汇报。",
  "",
  "- 交付物: desktop/frontend/src/lib/layoutPreferences.ts",
  "- 变更: 活跃标签页镜像到 localStorage，重启后恢复",
  "- 验证: npx tsc --noEmit 通过；pnpm build 全绿",
  "- 未做 / 风险: 未出包",
].join("\n");

const parsed = parseCompletionReport(sample);
assert.ok(parsed, "policy-shaped report is recognized");
assert.equal(parsed.fields.deliverables, "desktop/frontend/src/lib/layoutPreferences.ts");
assert.equal(parsed.fields.changes, "活跃标签页镜像到 localStorage，重启后恢复");
assert.equal(parsed.fields.verification, "npx tsc --noEmit 通过；pnpm build 全绿");
assert.equal(parsed.fields.remaining, "未出包");
assert.ok(parsed.body.includes("完成汇报"));
assert.ok(!parsed.body.includes("交付物"), "report block is peeled from the body");
assert.ok(completionReportHasContent(parsed.fields));

const noneFields = parseCompletionReport([
  "Done.",
  "",
  "**交付物**: 无",
  "**变更**: 修了一个按钮",
  "**验证**: 无",
  "**未做 / 风险**: 无",
].join("\n"));
assert.ok(noneFields, "bold labels and 无 are accepted");
assert.equal(noneFields.fields.deliverables, "");
assert.equal(noneFields.fields.changes, "修了一个按钮");
assert.equal(noneFields.fields.remaining, "");
assert.ok(completionReportHasContent(noneFields.fields));

const english = parseCompletionReport([
  "Shipped.",
  "",
  "- Deliverables: feat/wave2-a-ui-112",
  "- Changes: UI card for the report",
  "- Verification: pnpm build passed",
  "- Remaining / Risks: none",
].join("\n"));
assert.ok(english, "English field labels are accepted");
assert.equal(english.fields.deliverables, "feat/wave2-a-ui-112");
assert.equal(english.fields.remaining, "none");

const prose = "交付物很重要，但这段话并不是结构化汇报，只是提到了标签。";
assert.equal(parseCompletionReport(prose), null, "a single mention is not a report");

const twoOnly = parseCompletionReport([
  "Short turn.",
  "",
  "- 交付物: a.ts",
  "- 变更: 新增 a",
].join("\n"));
assert.ok(twoOnly, "two fields are enough");
assert.equal(twoOnly.fields.deliverables, "a.ts");
assert.equal(twoOnly.fields.verification, "");

console.log("completion report parse: ok");
