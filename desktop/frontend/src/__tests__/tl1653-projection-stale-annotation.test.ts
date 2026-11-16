// Run: tsx src/__tests__/tl1653-projection-stale-annotation.test.ts
//
// tl1653：投影失效标注的第二形态。任务549 的原门（projectionValid===false
// 且 usedTokens > triggerTokens）只覆盖「降级视图大于触发线」——20261116
// 21:24 现场（95 轮会话投影拒收 → 尾部梯队 2%，canonical 480K）永不命中，
// 容量卡零降级标注，2% 读数纯误导。门提纯为 projectionStaleAnnotation 并
// 扩出「canonical 超过显示读数」形态。

import assert from "node:assert/strict";
import { projectionStaleAnnotation } from "../lib/contextPanelUtils";

// 形态一（任务549 原样保留）：全量回退，读数超触发线。
assert.equal(projectionStaleAnnotation(false, 900_000, 800_000, 900_000), true, "over-limit fallback still annotates");
// 形态二（tl1653 现场）：降级尾部梯队，读数远小于 canonical。
assert.equal(projectionStaleAnnotation(false, 20_000, 800_000, 480_000), true, "tail-ladder under-report annotates (the 20261116 shape)");
// 投影失效但 canonical≈读数（诚实数字、未超线）：不标注。
assert.equal(projectionStaleAnnotation(false, 20_000, 800_000, 20_000), false, "honest small readout stays quiet");
// 投影有效：两形态都不标注。
assert.equal(projectionStaleAnnotation(true, 900_000, 800_000, 900_000), false, "valid projection never annotates");
// 旧后端不带字段（undefined）：保持无标注旧行为。
assert.equal(projectionStaleAnnotation(undefined, 900_000, 800_000, 900_000), false, "legacy backend (undefined) keeps the old no-badge behavior");
// canonical 缺数（旧侧报文）：形态二不误报。
assert.equal(projectionStaleAnnotation(false, 20_000, 800_000, 0), false, "missing canonical number cannot claim under-report");

console.log("tl1653 projectionStaleAnnotation: all asserts passed");
