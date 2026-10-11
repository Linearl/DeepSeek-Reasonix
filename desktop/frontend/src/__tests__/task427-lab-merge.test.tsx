// Run: npx tsx src/__tests__/task427-lab-merge.test.tsx
// Task 427 acceptance, updated by task 561: the lab's two "compaction"
// entries merged into ONE compress-opt card and the two "budget" entries
// into ONE budget-control card (task 318.5 precedent: one rail entry,
// independent switches); task 561 M3 then folds BOTH cards (plus
// compactionParallel and cacheTuning) into the contextGovernance family
// card. The invariants that must survive both rounds:
//  ① render table: the four old per-switch ids stay gone from the union,
//     the features array and the detail-pane branches; the compressOpt /
//     budgetControl family ids are gone too (superseded by
//     contextGovernance) (81/123 lost-save rule: union/table/branches move
//     together);
//  ② merged card: every member switch still writes through its OWN setter
//     (independent save, config keys unchanged — pure UI merge);
//  ③ the family entry light reads the member switches (discoverability);
//  ④ three locales carry the family label key.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
// 722 起 rail 条目（分组归属 + 灯读数 onKeys）改由布局数据驱动、灯谓词在
// labLightKeys、面板经 labEntryLightOn 派生——①③ 的锚点随机制迁移到同源数据。
import { labEntries, labEntryLightOn } from "../lab/labLayout";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const src = (name: string) => readFileSync(fileURLToPath(new URL(name, import.meta.url)), "utf8");
const panel = src("../components/SettingsPanel.tsx");
const zh = src("../locales/zh.ts");
const en = src("../locales/en.ts");
const zhTW = src("../locales/zh-TW.ts");

console.log("\ntask 427 lab merge (superseded by task 561 M3 contextGovernance)");

// ① render table + union + pane branches move together.
const familyEntry = labEntries(LAB_LAYOUT_DEFAULT_DATA).find(({ entry }) => entry.id === "contextGovernance");
ok(familyEntry?.group === "efficiency", "efficiency group carries the contextGovernance family entry");
ok(!panel.includes('{ id: "proactiveCompact", group:'), "no standalone proactiveCompact rail entry remains");
ok(!panel.includes('{ id: "coldCacheCompact", group:'), "no standalone coldCacheCompact rail entry remains");
ok(!panel.includes('{ id: "contextBudget", group:'), "no standalone contextBudget rail entry remains");
ok(!panel.includes('{ id: "researchBudget", group:'), "no standalone researchBudget rail entry remains");
ok(!panel.includes('{ id: "compressOpt", group:'), "no standalone compressOpt rail entry remains (task 561 M3 folded it)");
ok(!panel.includes('{ id: "budgetControl", group:'), "no standalone budgetControl rail entry remains (task 561 M3 folded it)");
ok(!panel.includes('{ id: "cacheTuning", group:'), "no standalone cacheTuning rail entry remains (task 561 M3 folded it)");
ok(!panel.includes('{ id: "compactionParallel", group:'), "no standalone compactionParallel rail entry remains (task 561 M3 folded it)");
ok(!panel.includes('| "proactiveCompact"'), "union drops proactiveCompact");
ok(!panel.includes('| "coldCacheCompact"'), "union drops coldCacheCompact");
ok(!panel.includes('| "contextBudget"'), "union drops contextBudget");
ok(!panel.includes('| "researchBudget"'), "union drops researchBudget");
ok(!panel.includes('| "compressOpt"'), "union drops compressOpt (task 561 M3)");
ok(!panel.includes('| "budgetControl"'), "union drops budgetControl (task 561 M3)");
ok(panel.includes('| "contextGovernance"'), "union gains contextGovernance");
ok(!panel.includes('{selected === "proactiveCompact" &&'), "no pane branch for proactiveCompact");
ok(!panel.includes('{selected === "coldCacheCompact" &&'), "no pane branch for coldCacheCompact");
ok(!panel.includes('{selected === "contextBudget" &&'), "no pane branch for contextBudget");
ok(!panel.includes('{selected === "researchBudget" &&'), "no pane branch for researchBudget");
ok(!panel.includes('{selected === "compressOpt" &&'), "no pane branch for compressOpt (task 561 M3)");
ok(!panel.includes('{selected === "budgetControl" &&'), "no pane branch for budgetControl (task 561 M3)");
ok(!panel.includes('{selected === "cacheTuning" &&'), "no pane branch for cacheTuning (task 561 M3)");
ok(panel.includes('{selected === "contextGovernance" && ('), "pane branch renders for contextGovernance");

// ② merged card: every switch writes through its own setter — independent
//    saves, no cross-wiring.
{
  const start = panel.indexOf('{selected === "contextGovernance" && (');
  const end = panel.indexOf('{selected === "devDebug" && (');
  ok(start >= 0 && end > start, "contextGovernance pane branch located");
  const card = panel.slice(start, end);
  ok(card.includes("app.SetExperimentalCompactionParallel(on)"), "card: compaction-parallel switch saves through its own setter");
  ok(card.includes("app.SetExperimentalContextBudget(on)"), "card: context-budget switch saves through its own setter");
  ok(card.includes("app.SetExperimentalResearchBudget(on)"), "card: research-budget switch saves through its own setter");
  ok(card.includes("app.SetExperimentalProactiveCompact(on)"), "card: proactive switch saves through its own setter");
  ok(card.includes("app.SetExperimentalColdCacheCompact(on)"), "card: cold-cache switch saves through its own setter");
  ok(card.includes("app.SetExperimentalCacheTuning(on)"), "card: cache-tuning switch saves through its own setter");
  ok(card.includes("app.SetProactiveCompactCooldownMinutes("), "card: cooldown knob intact");
  ok(card.includes("app.SetColdCacheCompactMinBytes("), "card: size knob intact");
  ok(card.includes("app.SetColdCacheCompactIdleMinutes("), "card: idle knob intact");
  ok(card.includes("app.SetTranscriptCacheTuning("), "card: cache-tuning knobs intact");
  ok(card.includes("app.SetDagGraphCacheCapacity(v)"), "card: dag-cache capacity knob intact");
  ok(card.includes("settings.contextBudgetCompress"), "card keeps the compress note field");
  ok(card.includes('t("settings.cacheTuning.dagCache")'), "card keeps the dag-cache label");
  ok(
    card.indexOf("app.SetExperimentalProactiveCompact(") < card.indexOf("app.SetExperimentalColdCacheCompact("),
    "card keeps the original mount order (proactive before cold cache)",
  );
  ok(
    card.indexOf("app.SetExperimentalContextBudget(") < card.indexOf("app.SetExperimentalResearchBudget("),
    "card keeps the original mount order (context budget before research budget)",
  );
}

// ③ family entry light reads the member switches (either on = discoverable).
{
  ok(Boolean(familyEntry), "contextGovernance entry light found");
  // 灯读数键 = 恰好六个成员开关（布局数据源，722 起 rail 灯读数即此表）。
  const expected = [
    "experimentalCompactionParallel",
    "experimentalContextBudget",
    "experimentalResearchBudget",
    "experimentalProactiveCompact",
    "experimentalColdCacheCompact",
    "experimentalCacheTuning",
  ];
  ok(JSON.stringify(familyEntry?.entry.onKeys ?? []) === JSON.stringify(expected),
    "family entry onKeys are exactly the six member switches");
  // lightKeys 谓词逐键读原配置键（④ 的「原名读取」在灯侧同样成立）。
  const lightKeysSrc = src("../lab/labLightKeys.ts");
  for (const key of expected) {
    ok(lightKeysSrc.includes(`${key}: (s: SettingsView) => Boolean(s.${key})`), `light reads s.${key}`);
  }
  // either on = discoverable：labEntryLightOn 的 some 语义功能验证（全关不亮，
  // 任一成员开即亮）。
  const allOff = labEntryLightOn(familyEntry!.entry, {} as never);
  const anyOn = labEntryLightOn(familyEntry!.entry, { experimentalColdCacheCompact: true } as never);
  ok(allOff === false && anyOn === true, "either member on = discoverable (labEntryLightOn some-semantics)");
}

// ④ config compatibility: the six persisted keys are untouched (UI merge
//    only) — the view still reads them by their original names.
ok(
  panel.includes("Boolean(s.experimentalProactiveCompact)") &&
    panel.includes("Boolean(s.experimentalColdCacheCompact)") &&
    panel.includes("Boolean(s.experimentalContextBudget)") &&
    panel.includes("Boolean(s.experimentalResearchBudget)") &&
    panel.includes("Boolean(s.experimentalCompactionParallel)") &&
    panel.includes("Boolean(s.experimentalCacheTuning)"),
  "all six original config keys still read under their original names",
);

// ⑤ three locales ship the family label key.
for (const [lang, text] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  ok(text.includes('"settings.contextGovernance"'), `${lang} ships settings.contextGovernance`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
