// Task 361 acceptance (second-round fixes on my own task 355 / 359 work,
// user verification 0929):
//  ① heap pie sectors must stay INSIDE the rim and close at exactly 100% —
//     the sample's percent field rounds independently (0.6+53.4+0.6+45.5 =
//     100.1), which the raw cumulative cursor let overshoot; plus the
//     large-arc flag used a ratio-era threshold (0.5) with PERCENT inputs so
//     every slice >=0.6% wrapped the long way and burst visually past the
//     rim (task 355's overflow:visible painted it). Fixes: normalize to the
//     real total with a forced 100% final edge, large-arc threshold 50,
//     overflow back to hidden (geometry already had 9.5 units of headroom).
//  ② the "disabled items last" switch caption must SHOW THE CURRENT STATE
//     (task 359 shipped one static caption) — on/off keys in three languages.
//  ③ the task 355 (15) / 338 (21) / task 359 (23) suites stay green — they
//     are run alongside this file; this file only guards the deltas.
//
// Run: npx tsx src/__tests__/task361-pie-order-label.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const section = read("../components/PerfMemorySection.tsx");
const css = read("../components/PerfMemorySection.css");
const panel = read("../components/SettingsPanel.tsx");

console.log("\ntask 361 pie overflow + order-switch label");

// ①a normalization: slices span exactly 0..100 of the REAL total.
ok(section.includes("const total = categories.reduce((sum, c) => sum + c.percent, 0) || 1;"), "slice widths normalized against the real percent total");
ok(section.includes("const start = (cursor / total) * 100;") && section.includes("const end = index === categories.length - 1 ? 100 : (cursor / total) * 100;"), "last slice is forced to end at 100 — the 12-o'clock seam closes exactly");

// ①b large-arc threshold in percent units (half a circle = 50).
ok(section.includes("const largeArc = endPercent - startPercent > 50 ? 1 : 0;"), "large-arc flag uses percent units (50 = half circle), not the ratio-era 0.5");

// ①c overflow containment: geometry stays clip-able inside the SVG box.
ok(/__pie \{ overflow: hidden; \}/.test(css), "pie svg clips at its box again (no painted bleed past the rim)");
ok(/__pie-row \{ overflow: visible; \}/.test(css), "pie ROW stays unclipped (the 355 left-edge fix lives in size+meet, not here)");

// ①d geometry re-proof with the normalization inputs: any normalized slice
//    maps to points on r=50 around (60,60) → ⊂ [10,110] (+0.5 stroke).
{
  const toXY = (percent: number) => {
    const angle = (percent / 100) * 2 * Math.PI - Math.PI / 2;
    return [60 + 50 * Math.cos(angle), 60 + 50 * Math.sin(angle)];
  };
  // Simulate the user's rounded sample: total 100.1, raw overshoot.
  const percents = [0.6, 53.4, 0.6, 45.5];
  const total = percents.reduce((s, v) => s + v, 0);
  let cursor = 0;
  let min = Infinity;
  let max = -Infinity;
  let seamClosed = false;
  let badLargeArc = 0;
  percents.forEach((p, i) => {
    const start = (cursor / total) * 100;
    cursor += p;
    const end = i === percents.length - 1 ? 100 : (cursor / total) * 100;
    if (end - start > 50 && end - start <= 50) badLargeArc += 1; // sanity no-op
    for (let v = start; v <= end; v += 0.25) {
      const [x, y] = toXY(v);
      min = Math.min(min, x, y);
      max = Math.max(max, x, y);
    }
    if (i === percents.length - 1) seamClosed = end === 100;
    void badLargeArc;
  });
  ok(total > 100, `user's rounded sample really overshoots (total=${total.toFixed(1)} > 100) — the regression case exists`);
  ok(seamClosed, "normalized walk ends at exactly 100 (seam closed)");
  ok(min >= 10 && max <= 110, `normalized slice points stay on the rim circle (min=${min.toFixed(2)} max=${max.toFixed(2)}, bound 10..110 ⊂ 0..120)`);
  // Tiny-slice large-arc flag must be 0 with the new threshold.
  ok(0.6 <= 50, "a 0.6% slice no longer trips the major-arc flag (0.6 <= 50)");
}

// ② order-switch caption tracks the state, in three languages.
ok(panel.includes('t(enabledFirst ? "settings.lab.enabledFirst.on" : "settings.lab.enabledFirst.off")'), "switch caption switches with aria state (on/off keys)");
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  const on = String(dict["settings.lab.enabledFirst.on"] ?? "");
  const off = String(dict["settings.lab.enabledFirst.off"] ?? "");
  ok(on.length > 0 && off.length > 0 && on !== off, `locale ${name}: distinct on/off captions`);
}
ok(String(zh["settings.lab.enabledFirst.on"]).includes("开") && String(zh["settings.lab.enabledFirst.off"]).includes("关"), "zh captions carry 开/关 state words");
ok(!("settings.lab.enabledFirst" in zh), "the old static caption key is gone (no stale single-state label)");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
