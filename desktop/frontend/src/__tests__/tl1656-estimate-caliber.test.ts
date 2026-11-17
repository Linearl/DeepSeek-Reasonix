// Run: tsx src/__tests__/tl1656-estimate-caliber.test.ts
//
// tl1656 验收矩阵（读数口径三态）：重启/resume 后投影降级，容量卡估算读数
// 与压缩判据的 canonical 口径脱钩——20261117 09:40 现场：读数 229.9K（23%），
// 压缩按 canonical 941485（94%）触发，同屏互相矛盾。门 = projectionStale
// （tl1653 标注已点亮）且 canonical 超显示读数 30% 以上 → 主读数切 canonical。

import assert from "node:assert/strict";
import {
  CONTEXT_ESTIMATE_DEVIATION_RATIO,
  contextDisplayCaliber,
} from "../lib/contextPanelUtils";

// 态 B（现场形态）：投影失效 + 尾部梯队读数 229.9K，canonical 941485（4 倍差）
// → 主读数切换到 canonical，标注 canonicalOverride 点亮。
{
  const caliber = contextDisplayCaliber(true, 229_900, 941_485);
  assert.equal(caliber.tokens, 941_485, "the 20261117 incident shape must display the canonical figure");
  assert.equal(caliber.canonicalOverride, true, "override flag lights the estimate-underreads annotation");
}

// 态 A（健康投影）：投影有效时 used<canonical 是压缩的目的本身，绝不切换——
// 压缩后 23 万视图 / 94 万 canonical 是正确读数。
{
  const caliber = contextDisplayCaliber(false, 229_900, 941_485);
  assert.equal(caliber.tokens, 229_900, "a valid projection keeps the view-sized readout");
  assert.equal(caliber.canonicalOverride, false, "no override on a healthy projection");
}

// 态 C（压缩失败后）：投影仍未落地、canonical 继续增长——同一门兜住，读数
// 跟随 canonical（tl1648 d 缺口同族：失败后读数不降的反面——读数本就不该降）。
{
  const caliber = contextDisplayCaliber(true, 240_000, 960_000);
  assert.equal(caliber.tokens, 960_000, "post-failure growth keeps riding the canonical figure");
  assert.equal(caliber.canonicalOverride, true);
}

// 偏差在 30% 以内（含等号边界）：读数不算失真，保持视图口径。
{
  const under = contextDisplayCaliber(true, 800_000, 1_000_000);
  assert.equal(under.tokens, 800_000, "deviation at/below 30% keeps the view readout");
  assert.equal(under.canonicalOverride, false);
  const at = contextDisplayCaliber(true, 1_000_000, 1_300_000);
  assert.equal(at.tokens, 1_000_000, "exactly 30% stays on the view readout (strict >)");
}

// 形态一（全量回退，读数已超触发线）：used 本身就是 canonical 级，门不切——
// 否则会把一个诚实的大读数误判为「偏低」。
{
  const caliber = contextDisplayCaliber(true, 900_000, 900_000);
  assert.equal(caliber.tokens, 900_000, "an honest canonical-level readout is never switched");
  assert.equal(caliber.canonicalOverride, false);
}

// 旧侧报文 canonical 缺数（0）：门安静，保持旧行为。
{
  const caliber = contextDisplayCaliber(true, 20_000, 0);
  assert.equal(caliber.tokens, 20_000, "missing canonical number cannot switch the readout");
  assert.equal(caliber.canonicalOverride, false);
}

// used=0 的降级空读数 + canonical 巨大：切（0 读数对 94 万是最大失真）。
{
  const caliber = contextDisplayCaliber(true, 0, 941_485);
  assert.equal(caliber.tokens, 941_485, "a zero degraded readout must not mask the canonical size");
}

// 阈值契约：门常量就是任务书的 30%。
assert.equal(CONTEXT_ESTIMATE_DEVIATION_RATIO, 1.3, "the deviation gate stays at the mandated 30%");

console.log("tl1656 contextDisplayCaliber: all asserts passed");
