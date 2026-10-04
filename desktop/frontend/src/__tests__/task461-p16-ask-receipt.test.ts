// Run: tsx src/__tests__/task461-p16-ask-receipt.test.ts
//
// 任务461-P16：ask 投递收据打点（纯判定）。describeAskReceipt 把 wire 的
// emittedAt 折算成前端收据行的 delivery_ms——与后端 `[ask-panel] ask request
// emitted`、reducer 的 `ask received`/`ask arrival judged`、AskCard 的
// `ask card mounted` 四点连成投递链，「弹窗延迟大 / 完全不弹」可从 desktop.log
// 直接二分定位断点在哪一段。

import { describeAskReceipt } from "../lib/askPanelGate";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

// 有锚点：延迟 = now - emittedAt，非负。
const stamped = describeAskReceipt(1000, "ask-1", "turn-1", 1250);
eq(stamped.latencyMs, 250, "有 emittedAt → 延迟=now-emittedAt");
eq(stamped.detail.includes("ask=ask-1"), true, "收据行带 ask id");
eq(stamped.detail.includes("turn=turn-1"), true, "收据行带 turn id");
eq(stamped.detail.includes("delivery_ms=250"), true, "收据行带 delivery_ms");

// 时钟倒挂（后端快于前端）：夹到 0，不出负数。
eq(describeAskReceipt(2000, "a", "t", 1500).latencyMs, 0, "时钟倒挂夹到 0");

// 无锚点（旧后端/历史重放）：延迟缺席但收据仍落。
const absent = describeAskReceipt(undefined, "ask-2", undefined, 5000);
eq(absent.latencyMs, undefined, "无 emittedAt → 延迟缺席");
eq(absent.detail.includes("delivery_ms=absent"), true, "缺席显式标注（不断链）");
eq(absent.detail.includes("turn=-"), true, "缺 turn id 显式占位");

// emittedAt=0 视为缺席（omitempty 字段不会真发 0，防旧数据陷阱）。
eq(describeAskReceipt(0, "a", "t", 5000).latencyMs, undefined, "emittedAt=0 按缺席处理");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
