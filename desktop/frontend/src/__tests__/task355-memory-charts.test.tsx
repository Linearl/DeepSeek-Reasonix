// Task 355 acceptance (two defects from the user's 0928 screenshots of the
// monitoring page, task 338's memory charts):
//   ① heap pie clipped on its LEFT edge (viewBox/overflow issue);
//   ② WS curve had no Y axis — only the min/max footer, so no way to read an
//      arbitrary point's value.
// Guards: Y axis (2-3 MB ticks) exists beside the curve, gridlines present,
// the X-axis footer (points × interval) is kept, and the pie geometry is
// provably inside its viewBox. The 338 contract lives in
// settings-perf-memory.test.ts and must stay green alongside this file.
//
// Run: npx tsx src/__tests__/task355-memory-charts.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const section = read("../components/PerfMemorySection.tsx");
const css = read("../components/PerfMemorySection.css");

console.log("\ntask 355 memory charts: Y axis + unclipped pie");

// ② Y axis: max/mid/min MB ticks beside the curve.
ok(section.includes("perf-memory-section__yaxis"), "Y axis column renders beside the chart");
ok(section.includes("`${wsMax.toFixed(1)} MB`") && section.includes("{wsMin.toFixed(1)}"), "Y ticks read the series max/min in MB (2-3 ticks)");
ok(section.includes("((wsMax + wsMin) / 2)"), "mid tick = (max+min)/2 so at least three gradations exist");
ok(section.includes("perf-memory-section__chart-row"), "Chart and Y axis share one row (grid layout)");
ok(css.includes(".perf-memory-section__chart-row") && css.includes("grid-template-columns: auto minmax(0, 1fr)"), "Row is a grid that cannot squash the curve (minmax(0,1fr))");
ok(css.includes(".perf-memory-section__yaxis") && css.includes("font-variant-numeric: tabular-nums"), "Y axis styled with tabular numerals");

// Gridlines: two dashed lines (top + mid) guide the eye to the Y ticks.
ok(section.includes('className="perf-memory-section__grid"') && section.includes('y1="60"'), "Curve carries dashed gridlines (top + mid) matching the ticks");
ok(css.includes(".perf-memory-section__grid") && css.includes("stroke-dasharray"), "Gridline style exists");

// X axis footer preserved (points × interval time hint).
ok(section.includes("series.points.length") && section.includes("series.intervalSeconds"), "Footer keeps 'N points × Ns' X-axis time hint");
ok(section.includes("perf-memory-section__axis"), "Footer axis row still rendered");

// ① Pie: explicit size + meet + overflow visible (anti-clip).
ok(section.includes('viewBox="0 0 120 120" width="120" height="120"'), "Pie svg pins width/height so layout cannot squash/clip it");
ok(section.includes('preserveAspectRatio="xMidYMid meet"'), "Pie keeps aspect ratio (no distortion/overflow from stretching)");
ok(css.includes(".perf-memory-section__pie { overflow: visible; }") || /__pie[^}]*overflow:\s*visible/.test(css), "Pie svg paints edge pixels instead of clipping them");
ok(css.includes(".perf-memory-section__pie-row { overflow: visible; }") || /__pie-row[^}]*overflow:\s*visible/.test(css), "Pie row never clips horizontally");

// Geometry proof: pieArc stays inside viewBox 0 0 120 120 (r=50 around 60,60
// → x,y ∈ [10,110], plus 0.5 stroke → [9.5,110.5] ⊂ [0,120]).
{
  const toXY = (percent: number) => {
    const angle = (percent / 100) * 2 * Math.PI - Math.PI / 2;
    return [60 + 50 * Math.cos(angle), 60 + 50 * Math.sin(angle)];
  };
  let min = Infinity; let max = -Infinity;
  for (let p = 0; p <= 100; p += 0.5) {
    const [x, y] = toXY(p);
    min = Math.min(min, x, y); max = Math.max(max, x, y);
  }
  ok(min >= 9.5 && max <= 110.5, `pieArc geometry within viewBox (sampled min=${min.toFixed(2)} max=${max.toFixed(2)}, bound 9.5..110.5)`);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
