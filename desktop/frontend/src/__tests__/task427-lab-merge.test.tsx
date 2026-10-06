// Run: npx tsx src/__tests__/task427-lab-merge.test.tsx
// Task 427 acceptance: the lab's two "compaction" entries merge into ONE
// storage card (compressOpt) and the two "budget" entries into ONE
// efficiency card (budgetControl), same precedent as the monitoring card
// (task 318.5: one rail entry, two independent switches):
//  ① render table: exactly the two new entries; the four old ids are gone
//     from the union, the features array and the detail-pane branches
//     (81/123 lost-save rule: union/table/branches must move together);
//  ② merged cards: each card renders BOTH switches, each switch still
//     writes through its OWN setter (independent save, config keys
//     unchanged — old four keys keep their values, pure UI merge);
//  ③ the entry light reads either switch (discoverability kept);
//  ④ all three locales carry the two new label keys.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

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

console.log("\ntask 427 lab merge (compressOpt + budgetControl)");

// ① render table + union + pane branches move together.
// Task 561: the audit table re-homes compressOpt from storage to efficiency.
ok(panel.includes('{ id: "compressOpt", group: "efficiency",'), "efficiency group carries the merged compressOpt entry");
ok(panel.includes('{ id: "budgetControl", group: "efficiency",'), "efficiency group carries the merged budgetControl entry");
ok(!panel.includes('{ id: "proactiveCompact", group:'), "no standalone proactiveCompact rail entry remains");
ok(!panel.includes('{ id: "coldCacheCompact", group:'), "no standalone coldCacheCompact rail entry remains");
ok(!panel.includes('{ id: "contextBudget", group:'), "no standalone contextBudget rail entry remains");
ok(!panel.includes('{ id: "researchBudget", group:'), "no standalone researchBudget rail entry remains");
ok(!panel.includes('| "proactiveCompact"'), "union drops proactiveCompact");
ok(!panel.includes('| "coldCacheCompact"'), "union drops coldCacheCompact");
ok(!panel.includes('| "contextBudget"'), "union drops contextBudget");
ok(!panel.includes('| "researchBudget"'), "union drops researchBudget");
ok(panel.includes('| "compressOpt"') && panel.includes('| "budgetControl"'), "union gains compressOpt + budgetControl");
ok(!panel.includes('{selected === "proactiveCompact" &&'), "no pane branch for proactiveCompact");
ok(!panel.includes('{selected === "coldCacheCompact" &&'), "no pane branch for coldCacheCompact");
ok(!panel.includes('{selected === "contextBudget" &&'), "no pane branch for contextBudget");
ok(!panel.includes('{selected === "researchBudget" &&'), "no pane branch for researchBudget");

// ② merged cards: both switches, each writing through its own setter —
//    independent saves, no cross-wiring.
{
  const start = panel.indexOf('{selected === "compressOpt" && (');
  const end = panel.indexOf('{selected === "compactionParallel" && (');
  ok(start >= 0 && end > start, "compressOpt pane branch located");
  const card = panel.slice(start, end);
  ok(card.includes("app.SetExperimentalProactiveCompact(on)"), "compressOpt card: proactive switch saves through its own setter");
  ok(card.includes("app.SetExperimentalColdCacheCompact(on)"), "compressOpt card: cold-cache switch saves through its own setter");
  ok(card.includes("app.SetProactiveCompactCooldownMinutes("), "compressOpt card: cooldown knob intact");
  ok(card.includes("app.SetColdCacheCompactMinBytes("), "compressOpt card: size knob intact");
  ok(card.includes("app.SetColdCacheCompactIdleMinutes("), "compressOpt card: idle knob intact");
  ok(
    card.indexOf("app.SetExperimentalProactiveCompact(") < card.indexOf("app.SetExperimentalColdCacheCompact("),
    "compressOpt card keeps the original mount order (proactive first)",
  );
}
{
  const start = panel.indexOf('{selected === "budgetControl" && (');
  const end = panel.indexOf('{selected === "opencodeGoUsage" && (');
  ok(start >= 0 && end > start, "budgetControl pane branch located");
  const card = panel.slice(start, end);
  ok(card.includes("app.SetExperimentalContextBudget(on)"), "budgetControl card: context-budget switch saves through its own setter");
  ok(card.includes("app.SetExperimentalResearchBudget(on)"), "budgetControl card: research-budget switch saves through its own setter");
  ok(card.includes("settings.contextBudgetCompress"), "budgetControl card keeps the compress note field");
}

// ③ entry light reads either switch (either feature on = entry discoverable).
ok(
  panel.includes("on: Boolean(s.experimentalProactiveCompact) || Boolean(s.experimentalColdCacheCompact)"),
  "compressOpt light reads either storage switch",
);
ok(
  panel.includes("on: Boolean(s.experimentalContextBudget) || Boolean(s.experimentalResearchBudget)"),
  "budgetControl light reads either budget switch",
);

// ④ config compatibility: the four persisted keys are untouched (UI merge
//    only) — the view still reads them by their original names.
ok(
  panel.includes("Boolean(s.experimentalProactiveCompact)") &&
    panel.includes("Boolean(s.experimentalColdCacheCompact)") &&
    panel.includes("Boolean(s.experimentalContextBudget)") &&
    panel.includes("Boolean(s.experimentalResearchBudget)"),
  "all four original config keys still read under their original names",
);

// ⑤ three locales ship the two new label keys.
for (const [lang, text] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  ok(text.includes('"settings.compressOpt"'), `${lang} ships settings.compressOpt`);
  ok(text.includes('"settings.budgetControl"'), `${lang} ships settings.budgetControl`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
