// Task 626 acceptance (lab rail grouped segments + sticky group headers):
//  ①  "all" view renders per-group segments, each with a header carrying the
//      group name AND its item total (header total badge, 任务 626);
//  ②  headers stick while scrolling and get pushed off by the next group —
//      the rail is its own sticky-scroll host (max-height + overflow-y) and
//      the header is position: sticky with an opaque background;
//  ③  left-rail filter behavior unchanged; the filtered-state header policy
//      (active group only, header still rendered and stuck) is fixed in a
//      comment at the labGroupTotals source;
//  ④  header group name/count share ONE source with the chips
//      (labGroupTotals) — structural equality, not coincidence;
//  ⑤  three-language key settings.labGroup.itemCount present with {n}.
//
// Run: npx tsx src/__tests__/task626-lab-group-headers.test.tsx

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

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const styles = readFileSync(fileURLToPath(new URL("../styles.css", import.meta.url)), "utf8");

console.log("\ntask 626 lab group headers");

// ④ + ③: one totals map feeds BOTH the chips and the rail group headers.
ok(panel.includes("const labGroupTotals = Object.fromEntries("), "labGroupTotals map exists (single source for chips + headers)");
{
  const readers = panel.match(/labGroupTotals\[g\.key\]/g) ?? [];
  ok(readers.length >= 2, `both chips and group header read labGroupTotals (got ${readers.length} read sites)`);
}
ok(panel.includes("const n = labGroupTotals[g.key];"), "chip count reads labGroupTotals (no private recount)");
ok(panel.includes('t("settings.labGroup.itemCount", { n: labGroupTotals[g.key] })'), "group header badge renders the itemCount key from labGroupTotals");

// ①: header carries name + total; the task 359 on-count badge stays.
ok(panel.includes('className="experimental-lab__group-count experimental-lab__group-count--total"'), "header total badge carries the --total modifier class");
ok(panel.includes('t("settings.labGroup.onCount", { n: onCount })'), "on-count badge kept (task 359 contract intact)");
ok(panel.includes('id={`lab-group-${g.key}`}'), "header keeps its jump anchor id (task 359 toc intact)");

// ③: filter behavior unchanged + policy comment pinned at the source.
ok(panel.includes('labFilter === "all" || labFilter === g.key'), "rail filter guard unchanged (chips stay mutually exclusive, zero behavior drift)");
ok(/任务 626[\s\S]{0,400}labFilter/.test(panel), "filter-state header policy documented at the labGroupTotals source");

// ②: sticky — the rail is the scroll host, the header sticks inside it.
{
  const railStart = styles.indexOf(".experimental-rail {");
  const railBody = railStart >= 0 ? styles.slice(railStart, styles.indexOf("}", railStart)) : "";
  ok(railBody.includes("max-height:") && railBody.includes("overflow-y: auto;"), "rail is its own scroll host (max-height + overflow-y: auto)");
  ok(railBody.includes("scrollbar-gutter: stable;"), "rail reserves the scrollbar gutter (no layout shift on scroll)");
  ok(railBody.includes("position: sticky;") && railBody.includes("z-index: var(--z-inline-sticky);"), "task 353 rail float kept (sticky + z token)");
}
{
  // Collect every .experimental-lab__group-title rule body and require one of
  // them to carry the full sticky quartet (typography block + sticky block).
  const bodies: string[] = [];
  let idx = styles.indexOf(".experimental-lab__group-title {");
  while (idx >= 0) {
    bodies.push(styles.slice(idx, styles.indexOf("}", idx)));
    idx = styles.indexOf(".experimental-lab__group-title {", idx + 1);
  }
  const sticky = bodies.find((b) => b.includes("position: sticky;"));
  ok(Boolean(sticky), "a .experimental-lab__group-title rule declares position: sticky");
  // 基线预存红修复（43eacd1b3）：z-index 已 token 化为 --z-app-content（同一
  // 层级），钉随实现更新——钉「非零层级」而不是裸字面量。
  ok(Boolean(sticky?.includes("top: 0;") && sticky?.includes("background: var(--bg);") &&
    /z-index:\s*(1|var\(--z-app-content\));/.test(sticky ?? "")),
    "sticky header pins at top with an opaque background + stacking order");
  // Cascade guard: the sticky (opaque) rule must come AFTER the toggle base
  // that declares background: transparent, or rows print through the header.
  const toggleIdx = styles.indexOf(".experimental-lab__group-toggle {");
  const stickyIdx = styles.indexOf(".experimental-lab__group-title {\n  position: sticky;");
  ok(toggleIdx >= 0 && stickyIdx > toggleIdx, "opaque sticky rule declared after the transparent toggle base (cascade wins)");
}
ok(styles.includes(".experimental-lab__group-count--total {"), "total badge modifier styled in css (no dead class)");

// ② (narrow screen): the horizontal wrap rail opts out of sticky.
{
  const mqStart = styles.indexOf("@media (max-width: 800px)");
  const mqBody = mqStart >= 0 ? styles.slice(mqStart, styles.indexOf("@media", mqStart + 1)) : "";
  ok(mqBody.includes(".experimental-rail {") && mqBody.includes("max-height: none;") && mqBody.includes("overflow: visible;"), "narrow screen: rail cap/sticky host disabled");
  ok(mqBody.includes(".experimental-lab__group-title {") && mqBody.includes("position: static;"), "narrow screen: headers unstuck (horizontal wrap)");
}

// ⑤: three languages carry the new key with the {n} placeholder.
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  const v = dict["settings.labGroup.itemCount"];
  ok(typeof v === "string" && v.includes("{n}"), `locale ${name} carries settings.labGroup.itemCount with the {n} placeholder`);
}
ok(String(zh["settings.labGroup.itemCount"]) === "{n} 项", "zh itemCount wording is 「{n} 项」");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
