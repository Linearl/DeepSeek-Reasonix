// Run: LANG=en_US.UTF-8 node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/effort-tiers-fold.test.ts
//
// Task 331: per-provider isolation for the composer effort menu fold.
// Task 301 folded every vocabulary onto the MiMo four-tier presets, which
// silently dropped deepseek's honest "max" level and reordered "disabled" to
// the tail (the installed 1615 build). foldEffortMenu must fold only the MiMo
// compatibility set and pass every other provider's vocabulary through,
// preserving both contents and order.

import { EFFORT_PRESETS, foldEffortMenu, normalizeEffortForMenu } from "../lib/effortTiers";

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

// MiMo compatibility set (task 301 ruling still holds): folds to the four
// honest tiers in preset order, aliases never offered.
eq(foldEffortMenu(["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"]),
  EFFORT_PRESETS,
  "MiMo eight-level set still folds to the four honest tiers");

// Zhipu/LongCat binary vocabulary: honest levels, not aliases — untouched.
eq(foldEffortMenu(["enabled", "disabled"]),
  ["enabled", "disabled"],
  "GLM-style binary vocabulary passes through untouched");

// currentEffort uses the same per-family decision on a single value.
eq(foldEffortMenu(["max"]), ["max"], "deepseek current=max stays max");
eq(foldEffortMenu(["xhigh"]), ["high"], "MiMo current=xhigh folds to high");
eq(foldEffortMenu(["enabled"]), ["enabled"], "GLM current=enabled stays enabled");

// normalizeEffortForMenu itself is unchanged (task 254 contract).
eq(normalizeEffortForMenu("ultra"), "high", "ultra still normalizes to high");

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
process.exit(0);
