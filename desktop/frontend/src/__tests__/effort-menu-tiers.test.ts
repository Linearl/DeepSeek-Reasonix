// Run: tsx src/__tests__/effort-menu-tiers.test.ts
//
// tl1538 (superseding the task 254 four-tier ruling): the settings UI offers
// the SEVEN honest union tiers (none/minimal/low/medium/high/xhigh/max) of the
// OpenAI/Anthropic documented vocabularies. The MiMo 8-level wire set stays a
// kernel-side concern (normalizeMimoEffort untouched); its alias spellings
// still fold for display behind the backend's MiMo identity mark, and a
// hand-edited unknown level must never be silently rewritten by the picker.

import { EFFORT_PRESETS, normalizeEffortForMenu } from "../lib/effortTiers";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

console.log("\neffort menu tiers (tl1538)");

// 1. The menu is the seven honest union tiers.
eq(EFFORT_PRESETS, ["none", "minimal", "low", "medium", "high", "xhigh", "max"], "menu offers the seven union tiers (tl1538)");

// 2. The four tiers pass through unchanged (and stay selectable).
eq(normalizeEffortForMenu("none"), "none", "none passes through");
eq(normalizeEffortForMenu("low"), "low", "low passes through");
eq(normalizeEffortForMenu("medium"), "medium", "medium passes through");
eq(normalizeEffortForMenu("high"), "high", "high passes through");

// 3. Wire aliases fold onto the tier they mean, matching the backend map.
eq(normalizeEffortForMenu("minimal"), "low", "minimal displays as low");
eq(normalizeEffortForMenu("xhigh"), "high", "xhigh displays as high");
eq(normalizeEffortForMenu("max"), "high", "max displays as high");
eq(normalizeEffortForMenu("ultra"), "high", "ultra displays as high");

// 4. The inherit option (empty) stays empty.
eq(normalizeEffortForMenu(""), "", "empty stays inherit");

// 5. Unknown hand-edited values are returned as-is so the picker never
// silently rewrites a level it does not know.
eq(normalizeEffortForMenu("turbo"), "turbo", "unknown level passes through untouched");
eq(normalizeEffortForMenu(undefined), "", "undefined reads as empty");

// 6. The alias fold is case-sensitive, exactly like the wire: Go's
// normalizeMimoEffort switches on exact lowercase literals, so "Ultra" is a
// hand-edited unknown value that must pass through untouched.
eq(normalizeEffortForMenu("Ultra"), "Ultra", "alias fold stays case-sensitive, matching the wire");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
