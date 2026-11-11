// Run: tsx src/__tests__/money-bucketed-display.test.ts
//
// tl1556 计费多币种显示修复钉：
//  - 验收② 多币种混合（bucketed / currency_buckets）金额不再被静态文案吞掉，
//    分桶金额逐币种上屏（"$0.0123 + ¥0.8500"）；
//  - 验收① 存量 USD 会话切 CNY 后金额仍正常显示（标量兜底 + fallback_original
//    双路径数字都在、币种如实标注）；
//  - 验收③ 单币种正常路径零回退。
//
// 注：Intl 在 zh 运行环境下 USD 可能格式化为 "US$0.0123"（locale-aware），
// 因此 USD 断言用 /US\$|\$/ 双形态，金额数字严格断言。

import { contextCostDisplay } from "../components/ContextPanel";
import { currencySymbol, formatMoney, formatMoneyBuckets, formatMoneyLocalized } from "../lib/money";
import type { CostQuote } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function ok(condition: boolean, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nmoney bucketed display (tl1556)");

// ---- 验收②：多币种混合数据格式化不吞数字 ----

const mixedQuote: CostQuote = {
  original: { amount: "0", currency: "" },
  originalTotals: [
    { amount: "0.0123", currency: "USD" },
    { amount: "0.85", currency: "CNY" },
  ],
  estimated: true,
  costComplete: true,
  displayComplete: false,
  complete: false,
  displayStatus: "bucketed",
  aggregateMode: "currency_buckets",
  incompleteReason: "currency_buckets",
};

const mixedDisplay = contextCostDisplay({
  info: { sessionCost: 0, sessionCostComplete: true, sessionCostQuote: mixedQuote },
});

eq(mixedDisplay.labelKind, "bucketed", "mixed-currency quote keeps the bucketed labelKind");
eq(mixedDisplay.buckets, mixedQuote.originalTotals, "bucketed branch passes originalTotals through instead of dropping them");

const joined = formatMoneyBuckets(mixedQuote.originalTotals, {});
ok(joined.includes("0.0123"), `mixed buckets keep the USD digits visible (${joined})`);
ok(joined.includes("0.8500"), `mixed buckets keep the CNY digits visible (${joined})`);
ok(joined.includes("+"), `mixed buckets join with a visible separator (${joined})`);
ok(/US\$|\$/.test(joined), `mixed buckets label the USD amount with a currency marker (${joined})`);
ok(joined.includes("¥"), `mixed buckets label the CNY amount with ¥ (${joined})`);

// 老宿主无 originalTotals：回退静态文案路径（不造数字、也不崩）。
eq(formatMoneyBuckets(undefined, {}), "-", "missing originalTotals degrades to the dash marker");
eq(formatMoneyBuckets([], {}), "-", "empty originalTotals degrades to the dash marker");
eq(formatMoneyBuckets([{ amount: "0", currency: "USD" }, { amount: "-1", currency: "CNY" }, { amount: "NaN", currency: "JPY" }], {}), "-", "zero/negative/invalid buckets carry no display value");

// ---- 验收①：存量 USD 会话切 CNY 后金额正常显示 ----

// 路径 A：legacy 标量（无 quote）——旧 USD 会话在侧边车里的形态。
const legacyUsd = contextCostDisplay({
  info: { sessionCost: 0.12, sessionCurrency: "USD", sessionCostComplete: true, sessionCostQuote: undefined },
  sessionCost: 0.12,
  sessionCurrency: "CNY",
});
eq(legacyUsd.labelKind, "estimated", "legacy USD scalar keeps the estimated labelKind");
eq(legacyUsd.amount, 0.12, "legacy USD scalar keeps the amount visible after a CNY switch");
eq(legacyUsd.currency, "USD", "legacy USD scalar keeps its original currency label");

// 路径 B：切币后活会话重绑（fallback_original）——Selected=原币种权威金额。
const fallbackQuote: CostQuote = {
  original: { amount: "0.12", currency: "USD" },
  valuations: {},
  selected: { amount: "0.12", currency: "USD" },
  estimated: true,
  costComplete: true,
  displayComplete: false,
  complete: false,
  displayStatus: "fallback_original",
  aggregateMode: "single_currency",
  incompleteReason: "display_unavailable",
};
const fallbackDisplay = contextCostDisplay({
  info: { sessionCost: 0.12, sessionCurrency: "USD", sessionCostComplete: true, sessionCostQuote: fallbackQuote },
});
// 真实 Go fallback_original 报文 displayComplete=false → quoteAuthoritative=false，
// 前端按标量兜底渲染（labelKind=estimated）：数字在、币种标注 USD，正是验收①。
eq(fallbackDisplay.labelKind, "estimated", "fallback_original (displayComplete=false) renders via the scalar fallback");
eq(fallbackDisplay.amount, 0.12, "fallback_original keeps the USD amount visible");
eq(fallbackDisplay.currency, "USD", "fallback_original keeps the USD currency label");
ok(formatMoneyLocalized(0.12, "USD", {}).includes("0.1200"), `fallback amount formats with digits (${formatMoneyLocalized(0.12, "USD", {})})`);

// ---- 验收③：正常单币种零回退 ----

const matchedQuote: CostQuote = {
  original: { amount: "0.0064", currency: "CNY" },
  valuations: {},
  selected: { amount: "0.0064", currency: "CNY" },
  estimated: true,
  costComplete: true,
  displayComplete: true,
  complete: true,
  displayStatus: "matched",
  aggregateMode: "single_currency",
};
const matchedDisplay = contextCostDisplay({
  info: { sessionCost: 0.0064, sessionCurrency: "CNY", sessionCostComplete: true, sessionCostQuote: matchedQuote },
});
eq(matchedDisplay.labelKind, "estimated", "single-currency matched quote keeps the estimated labelKind");
eq(matchedDisplay.amount, 0.0064, "single-currency matched quote keeps the amount");
eq(matchedDisplay.currency, "CNY", "single-currency matched quote keeps CNY");
eq(formatMoneyLocalized(0.0064, "CNY", {}), "¥0.0064", "CNY formatting is byte-stable");
eq(formatMoney(0.12, "USD"), "$0.1200", "plain USD formatting is unchanged");
eq(currencySymbol("USD"), "$", "USD symbol mapping is unchanged");
eq(currencySymbol(""), "¥", "empty currency falls back to ¥ as before");

// bucketed 纯文案回退（老宿主形态）不吞数字的前置=无数字可显，仍给标签。
const bucketedNoTotals = contextCostDisplay({
  info: {
    sessionCost: 0,
    sessionCostComplete: true,
    sessionCostQuote: { ...mixedQuote, originalTotals: undefined },
  },
});
eq(bucketedNoTotals.buckets, undefined, "bucketed quote without originalTotals keeps buckets undefined (old-host safe)");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
