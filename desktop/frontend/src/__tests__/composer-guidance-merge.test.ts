// Run: npx tsx src/__tests__/composer-guidance-merge.test.ts
//
// Task 153: the manual "merge next" affordance on the guidance shelf.
// Covers the pure queue/text helpers and the shelf's render contract for
// showing (and not showing) the merge button.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mergeGuidanceTexts, mergeGuidanceWithNext } from "../lib/composerInboxQueue";
import type { PendingGuidance } from "../components/ComposerGuidanceShelf";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const guidance = (id: string, text: string, extra: Partial<PendingGuidance> = {}): PendingGuidance => ({
  id,
  text,
  submitText: text,
  ...extra,
});

// ── text joining: verbatim, double-newline separator ────────────────────────
{
  const joined = mergeGuidanceTexts("first body", "second body");
  assert.equal(joined, "first body\n\nsecond body");
  ok(joined === "first body\n\nsecond body", "texts join verbatim with a blank line");
  ok(mergeGuidanceTexts("first  ", "  second") === "first\n\nsecond", "edge whitespace is trimmed, bodies untouched");
}

// ── queue merge: one pair per click, carrier keeps its id ───────────────────
{
  const items = [guidance("a", "one"), guidance("b", "two"), guidance("c", "three")];
  const once = mergeGuidanceWithNext(items, "a");
  ok(once.length === 2, "merging removes exactly one entry");
  ok(once[0].id === "a", "the first entry is the carrier and keeps its id");
  ok(once[0].text === "one\n\ntwo", "the carrier body holds both texts in order");
  ok(once[1].id === "c" && once[1].text === "three", "later entries shift up untouched");
  const twice = mergeGuidanceWithNext(once, "a");
  ok(twice.length === 1 && twice[0].text === "one\n\ntwo\n\nthree", "clicking again folds the third entry in");
}

{
  const items = [guidance("a", "only")];
  ok(mergeGuidanceWithNext(items, "a") === items, "merging the last entry is a no-op (identity)");
  ok(mergeGuidanceWithNext(items, "missing") === items, "merging an unknown id is a no-op (identity)");
}

// ── shelf contract: the button renders only under the experiment ────────────
{
  const root = join(dirname(fileURLToPath(import.meta.url)), "..");
  const shelf = readFileSync(join(root, "components/ComposerGuidanceShelf.tsx"), "utf8");
  ok(/onMergeNext\?/.test(shelf), "merge callback is optional: switch off means no button anywhere");
  ok(/onMergeNext && index < items\.length - 1/.test(shelf), "the button needs a following entry (merge-next, not merge-all)");
  ok(!/onMergeNext && index < items\.length - 1[^&]*&& \(/.test(shelf.replace(/\n\s*/g, " ")), "button render is guarded before any click handler");
  ok(/!editing && !inFlight && !delivering/.test(shelf.replace(/\n\s*/g, " ")), "in-flight, delivering and editing rows never offer the merge");
  const composer = readFileSync(join(root, "components/Composer.tsx"), "utf8");
  ok(/collabGuidanceMergeEnabled \? \(item\) => void mergeQueuedGuidance\(item\) : undefined/.test(composer), "switch off passes undefined: zero change to the shelf");
}

process.stdout.write(`\n${passed} checks passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
