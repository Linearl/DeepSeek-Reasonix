// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1670-caliber-note.test.ts
//
// tl1670 验收矩阵（两态同一把尺）：
//   常态态（投影有效）：canonical = 整段历史体积（压缩只写投影、历史永不缩减），
//     主读数必须保持「下一请求发送量」口径（切 canonical 等于把整段历史谎报成
//     窗口占用），但面板必须明示两个数——20261117 18:34 现场：面板 381.9K vs
//     日志 src=1050159，2.7 倍差却零口径标注。
//   降级态（投影失效）：tl1656 已交付面——偏差>30% 时主读数切 canonical，本件
//     语义逐字不变（回归钉）。
//   阈值 30% 的出处：任务书 tl1656 原文「估算偏差>30% 时显示 canonical 或标
//     『估算偏低』」，常量 CONTEXT_ESTIMATE_DEVIATION_RATIO=1.3 即该阈值。

import assert from "node:assert/strict";
import {
  CONTEXT_ESTIMATE_DEVIATION_RATIO,
  contextDisplayCaliber,
} from "../lib/contextPanelUtils";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
function ok(value: boolean, label: string) {
  if (!value) throw new Error(`FAIL ${label}`);
  passed += 1;
}

// ── 态一：常态（投影有效）+ 偏差>30% → 主读数不切，标注口径 ────────────────
// 现场形态：面板 381.9K（下一请求发送量）vs canonical 1050159（整段历史），
// 2.75 倍差。切 canonical 会把一个健康折叠后的会话报成 105% 超窗。
{
  const caliber = contextDisplayCaliber(false, 381_900, 1_050_159);
  assert.equal(caliber.tokens, 381_900, "the normal-state readout must stay on the payload caliber");
  assert.equal(caliber.canonicalOverride, false, "the normal state never switches the readout to canonical");
  assert.equal(caliber.note, "historyVolume", "the divergence must be stated on screen");
  ok(true, "normal state with >30% divergence keeps the payload readout and asks for a caliber note");
}

// 健康折叠形态（现场 20261117 18:14:57：source 771812 → 视图 261814，2.95 倍）
// ——与上面的失真现场同形：主读数保持视图口径，不误报超窗。
{
  const caliber = contextDisplayCaliber(false, 261_814, 771_812);
  assert.equal(caliber.tokens, 261_814, "a healthy folded session keeps the view-sized readout");
  assert.equal(caliber.canonicalOverride, false);
  assert.equal(caliber.note, "historyVolume", "the history volume is stated, not hidden");
  ok(true, "healthy fold shape keeps the payload readout and states the history volume");
}

// ── 态二：常态 + 偏差≤30% → 无标注（两数同尺，无需打扰） ──────────────────
{
  const under = contextDisplayCaliber(false, 800_000, 1_000_000);
  assert.equal(under.note, "none", "no divergence beyond 30% means no caliber note");
  assert.equal(under.tokens, 800_000);
  const at = contextDisplayCaliber(false, 1_000_000, 1_300_000);
  assert.equal(at.note, "none", "exactly 30% stays silent (strict >)");
  ok(true, "deviation at/below 30% stays silent in the normal state");
}

// ── 态三：降级（投影失效）+ 偏差>30% → 切 canonical（tl1656 面，语义不变）───
{
  const caliber = contextDisplayCaliber(true, 229_900, 941_485);
  assert.equal(caliber.tokens, 941_485, "the degraded readout still switches to the canonical figure");
  assert.equal(caliber.canonicalOverride, true, "the tl1656 override flag is unchanged");
  assert.equal(caliber.note, "degradedCanonical", "the degraded state names the canonical caliber");
  ok(true, "degraded state keeps the tl1656 switch-to-canonical behavior");
}

// 降级 + 偏差≤30% → 保持视图读数（tl1656 回归）。
{
  const caliber = contextDisplayCaliber(true, 800_000, 1_000_000);
  assert.equal(caliber.tokens, 800_000);
  assert.equal(caliber.canonicalOverride, false);
  assert.equal(caliber.note, "none");
  ok(true, "degraded state below the gate keeps the view readout");
}

// ── 旧侧报文缺 canonical（0）→ 全态安静 ────────────────────────────────────
{
  const normal = contextDisplayCaliber(false, 20_000, 0);
  assert.equal(normal.note, "none", "a missing canonical cannot raise a caliber note");
  assert.equal(normal.tokens, 20_000);
  const degraded = contextDisplayCaliber(true, 20_000, 0);
  assert.equal(degraded.note, "none");
  assert.equal(degraded.tokens, 20_000, "the missing canonical cannot switch the readout");
  ok(true, "a missing canonical figure stays quiet in both states");
}

// used=0 的空读数 + 巨大 canonical：降级切、常态标注（0 读数是最大背离）。
{
  const degraded = contextDisplayCaliber(true, 0, 941_485);
  assert.equal(degraded.tokens, 941_485);
  const normal = contextDisplayCaliber(false, 0, 1_050_159);
  assert.equal(normal.tokens, 0, "the normal state never invents a readout out of a canonical figure");
  assert.equal(normal.note, "historyVolume");
  ok(true, "a zero readout is handled in both states");
}

// ── 阈值契约：常量就是任务书要求的 30% ────────────────────────────────────
assert.equal(CONTEXT_ESTIMATE_DEVIATION_RATIO, 1.3, "the deviation gate stays at the mandated 30%");
ok(true, "the deviation gate constant stays at 1.3");

// ── 三语键存在钉：口径标注三语齐备，且都点名 canonical 与当前读数 ──────────
{
  const dicts: Array<[string, Record<string, string>]> = [
    ["en", en as unknown as Record<string, string>],
    ["zh", zh as unknown as Record<string, string>],
    ["zh-TW", zhTW as unknown as Record<string, string>],
  ];
  for (const [name, dict] of dicts) {
    const label = dict["context.historyVolumeLabel"];
    const title = dict["context.historyVolumeTitle"];
    assert.ok(label && label.includes("{canonical}"), `${name}: context.historyVolumeLabel must name the canonical figure`);
    assert.ok(title && title.includes("{canonical}") && title.includes("{used}"), `${name}: context.historyVolumeTitle must name both figures`);
    ok(true, `${name} carries the tl1670 caliber keys`);
  }
  // 三语不是同一条文案（直译不算三语）。
  assert.notEqual(en["context.historyVolumeTitle"], zh["context.historyVolumeTitle"]);
  assert.notEqual(zh["context.historyVolumeTitle"], zhTW["context.historyVolumeTitle"]);
  assert.notEqual(en["context.historyVolumeTitle"], zhTW["context.historyVolumeTitle"]);
  ok(true, "the three languages carry distinct caliber copy");
}

console.log(`tl1670 contextDisplayCaliber: ${passed} asserts passed`);
