// 任务 986 harness：stackingOrder 层内 LIFO 单调序——跑法：
//   npx tsx src/__tests__/task986-stacking-order.ts
// 验收：①连续取号严格递增（打开=后弹在前）；②重置钩子只用于测试隔离；
// ③bump（再次 acquire）返回值恒大于此前任何号（点击置前恒抬升）。
import { acquireOverlayOrder, resetOverlayOrderForTest } from "../lib/stackingOrder";

let pass = 0;
let fail = 0;
function check(name: string, cond: boolean): void {
  if (cond) {
    pass += 1;
    console.log(`  ok ${name}`);
  } else {
    fail += 1;
    console.log(`  FAIL ${name}`);
  }
}

resetOverlayOrderForTest();
const a = acquireOverlayOrder();
const b = acquireOverlayOrder();
const c = acquireOverlayOrder();
check("单调递增 (a<b<c): 后弹在前", a < b && b < c);
check("从 1 起号", a === 1);
const bumped = acquireOverlayOrder();
check("点击置前再取号 > 此前全部 (bumped>c)", bumped > c);
resetOverlayOrderForTest();
check("测试重置钩子生效（重取从 1 开始）", acquireOverlayOrder() === 1);

console.log(`\n${fail === 0 ? "PASS" : "FAIL"}: ${pass} ok, ${fail} failed`);
process.exit(fail === 0 ? 0 : 1);
