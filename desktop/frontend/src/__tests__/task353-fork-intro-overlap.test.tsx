// Task 353 acceptance (Fork Features intro no longer prints over the lab list):
//  the lab rail is a STICKY floating surface (task 282 put the intro section
//  right below it). Before the fix the rail had no backdrop and no stacking
//  order, so scrolling to the bottom let the intro text scroll *through* the
//  floating rail and print over the switch rows (user screenshots 0928).
//
//  Guards: rail carries an opaque backdrop + z-index; intro owns a lower
//  stacking context; the intro is still rendered in the lab column (after the
//  nav, before the pane) and stays pure-display (task 282 invariants).
//
// Run: npx tsx src/__tests__/task353-fork-intro-overlap.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");

// ── layout guards (the actual fix) ───────────────────────────────────────────
{
  const css = read("../styles.css");

  // Rail: sticky + opaque backdrop + above the intro in stacking order.
  const railMatch = css.match(/\.experimental-rail \{[^}]+\}/);
  ok(Boolean(railMatch), "rail rule exists");
  const rail = railMatch?.[0] ?? "";
  ok(/position:\s*sticky/.test(rail), "rail keeps sticky behaviour (task 132 contract untouched)");
  ok(/background:\s*var\(--bg\)/.test(rail), "rail has an opaque backdrop (theme token, no transparent pass-through)");
  ok(/z-index:\s*var\(--z-inline-sticky\)/.test(rail), "rail floats above the intro section via the --z-* token (check:z-index contract)");

  // Intro: own stacking context strictly below the rail.
  const introMatch = css.match(/\.experimental-intro \{[^}]+\}/);
  ok(Boolean(introMatch), "intro rule exists");
  const intro = introMatch?.[0] ?? "";
  ok(/position:\s*relative/.test(intro), "intro owns a positioning context");
  ok(/z-index:\s*var\(--z-theme-bg\)/.test(intro), "intro stacks below the rail via the --z-* token (no interleaving)");
}

// ── structure guards (task 282 invariants kept) ─────────────────────────────
{
  const panel = read("../components/SettingsPanel.tsx");
  const navIdx = panel.indexOf("</nav>");
  const introIdx = panel.indexOf("<ForkFeaturesIntro");
  const paneIdx = panel.indexOf('className="experimental-pane"');
  ok(navIdx > 0 && introIdx > 0 && introIdx < navIdx && paneIdx > introIdx, "intro renders in the lab column above the rail and before the pane (task 359 moved it to the top banner)");
  ok(panel.includes("selected === \"forkFeaturesIntro\"") === false, "no phantom detail branch — intro is a plain in-flow section");

  const lib = read("../lib/forkFeaturesIntro.ts");
  ok(lib.includes("FORK_FEATURE_INTRO_GROUPS"), "task 282 design table still present");
  ok(lib.includes("_groupCountPinnedToDesignTable"), "task 282 tsc group-count pin intact");
}

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nall checks passed (${passed} assertions)`);
