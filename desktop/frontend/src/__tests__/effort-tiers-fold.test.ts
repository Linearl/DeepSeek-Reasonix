// Run: LANG=en_US.UTF-8 node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/effort-tiers-fold.test.ts
//
// Task 331: per-provider isolation for the composer effort menu fold.
// Task 301 folded every vocabulary onto the MiMo four-tier presets, which
// silently dropped deepseek's honest "max" level and reordered "disabled" to
// the tail (the installed 1615 build).
// Task effortfix2 (A-line P2): the trigger is the backend's IDENTITY mark
// (EffortInfo.aliasFold, set only by mimoEffortCapability) — never a
// vocabulary shape sniff. Unmarked families pass through byte-for-byte even
// if their words look MiMo-ish; a marked MiMo entry still needs the shape as
// a second line of defense before folding.

import { EFFORT_PRESETS, foldEffortCurrent, foldEffortMenu, normalizeEffortForMenu, isMiMoEffortVocabulary } from "../lib/effortTiers";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

// DeepSeek (both deepseek-v4-flash models): config override vocabulary —
// must come back untouched, same order, max preserved.
eq(foldEffortMenu(["disabled", "low", "high", "max"]),
  ["disabled", "low", "high", "max"],
  "deepseek config vocabulary passes through untouched");

eq(foldEffortMenu(["auto", "disabled", "low", "high", "max"]),
  ["auto", "disabled", "low", "high", "max"],
  "deepseek levels (with auto) pass through untouched");

// DeepSeek built-in table: same family, same rule.
eq(foldEffortMenu(["auto", "disabled", "high", "max"]),
  ["auto", "disabled", "high", "max"],
  "deepseek built-in vocabulary passes through untouched");

// MiMo compatibility set WITH the identity mark (task 301 ruling still holds):
// folds to the four honest tiers in preset order, aliases never offered.
const MIMO_EIGHT = ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"];
eq(foldEffortMenu(MIMO_EIGHT, true),
  EFFORT_PRESETS,
  "identity-marked MiMo eight-level set folds to the four honest tiers");

// The identity mark is the necessary trigger: same MiMo words, unmarked
// (e.g. a config-declared vocabulary) stay verbatim.
eq(foldEffortMenu(MIMO_EIGHT, false),
  MIMO_EIGHT,
  "unmarked vocabulary never folds, even the MiMo-shaped one");

// Zhipu/LongCat binary vocabulary: honest levels, not aliases — untouched.
eq(foldEffortMenu(["enabled", "disabled"]),
  ["enabled", "disabled"],
  "GLM-style binary vocabulary passes through untouched");

// Honest xhigh/max sets (anthropic-family behind opencode-go, luna) pass
// through whether or not anything marked them — the shape alone can never
// trigger a fold (331's first判据 dropped their max).
eq(foldEffortMenu(["low", "medium", "high", "xhigh", "max"]),
  ["low", "medium", "high", "xhigh", "max"],
  "anthropic-family vocabulary (xhigh/max honest) passes through untouched");
eq(foldEffortMenu(["none", "low", "medium", "high", "xhigh", "max"]),
  ["none", "low", "medium", "high", "xhigh", "max"],
  "luna vocabulary (xhigh honest) passes through untouched");
eq(foldEffortMenu(["low", "medium", "high", "xhigh", "max"], true),
  ["low", "medium", "high", "xhigh", "max"],
  "second line of defense: even a mark cannot fold a non-MiMo-shaped set");

// Single values pass through unmarked.
eq(foldEffortMenu(["max"]), ["max"], "single max passes through");
eq(foldEffortMenu(["xhigh"]), ["xhigh"], "single xhigh passes through");
eq(foldEffortMenu(["enabled"]), ["enabled"], "single enabled passes through");

// foldEffortCurrent: same identity rule as the menu.
eq(foldEffortCurrent(["low", "medium", "high", "xhigh", "max"], "max"), "max",
  "anthropic-family current=max stays max");
eq(foldEffortCurrent(["disabled", "low", "high", "max"], "max"), "max",
  "deepseek current=max stays max");
eq(foldEffortCurrent(MIMO_EIGHT, "xhigh", true), "high",
  "identity-marked MiMo current=xhigh folds to high");
eq(foldEffortCurrent(MIMO_EIGHT, "xhigh"), "xhigh",
  "unmarked current never folds");
eq(foldEffortCurrent(["enabled", "disabled"], "enabled"), "enabled",
  "GLM current=enabled stays enabled");

// The shape helper itself (defensive condition).
eq(isMiMoEffortVocabulary(MIMO_EIGHT), true, "MiMo eight-level shape recognized");
eq(isMiMoEffortVocabulary(["low", "medium", "high", "xhigh", "max"]), false,
  "anthropic shape not MiMo");

// normalizeEffortForMenu itself is unchanged (task 254 contract).
eq(normalizeEffortForMenu("ultra"), "high", "ultra still normalizes to high");

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
process.exit(0);
