// Task 282 acceptance (fork features intro panel):
//  1. the rendered group count equals the design table (3) — pinned in tsc by
//     `_groupCountPinnedToDesignTable` and asserted here at runtime;
//  2. the first batch carries at least 5 entries;
//  3. locale full-path: every key used by the table exists in zh / en / zh-TW;
//  4. the section is pure display: rendering it emits no form controls
//     (switch/checkbox/input) — the acceptance rule "no variable settings".
//
// Run: npx tsx src/__tests__/task282-fork-features-intro.test.tsx

import { FORK_FEATURE_INTRO_GROUPS, FORK_FEATURE_INTRO_COUNT } from "../lib/forkFeaturesIntro";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";

let failed = 0;
function check(name: string, ok: boolean, detail = "") {
  if (ok) {
    console.log(`ok   ${name}`);
  } else {
    failed += 1;
    console.error(`FAIL ${name}${detail ? ` — ${detail}` : ""}`);
  }
}

// 1. group count matches the design table.
check("group count == design table (3)", FORK_FEATURE_INTRO_GROUPS.length === 3, `got ${FORK_FEATURE_INTRO_GROUPS.length}`);

// 2. first batch >= 5 entries, each with all three keys.
const allFeatures = FORK_FEATURE_INTRO_GROUPS.flatMap((g) => g.features);
check("entries >= 5", allFeatures.length >= 5, `got ${allFeatures.length}`);
check("every entry has title/desc/how keys", allFeatures.every((f) => f.titleKey && f.descKey && f.howKey));
check("ids unique", new Set(allFeatures.map((f) => f.id)).size === allFeatures.length);

// 3. locale full path: title/lead/group labels + every feature key in zh/en/zh-TW.
const requiredKeys = [
  "settings.forkFeaturesIntro.title",
  "settings.forkFeaturesIntro.lead",
  ...FORK_FEATURE_INTRO_GROUPS.flatMap((g) => [
    g.labelKey,
    ...g.features.flatMap((f) => [f.titleKey, f.descKey, f.howKey]),
  ]),
] as const;
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  const missing = requiredKeys.filter((k) => !(k in dict));
  check(`locale ${name} complete (${requiredKeys.length} keys)`, missing.length === 0, `missing: ${missing.join(", ")}`);
}

// 4. pure display — no setting controls in the component source.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
const componentSrc = readFileSync(fileURLToPath(new URL("../components/ForkFeaturesIntro.tsx", import.meta.url)), "utf8");
const controlTags = [/<input\b/, /<button\b/, /<select\b/, /type="checkbox"/, /type="radio"/];
const hit = controlTags.filter((re) => re.test(componentSrc)).map(String);
check("pure display: no form controls in component", hit.length === 0, `found ${hit.join(", ")}`);

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nall checks passed (${FORK_FEATURE_INTRO_COUNT} entries, ${FORK_FEATURE_INTRO_GROUPS.length} groups)`);
