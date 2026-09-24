// Run: LANG=en node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/task266a-guidance-row.test.ts
//
// Task 266-A — 引导货架排序按钮合行（A 段，B 拖拽排序已推批七）:
//   the up/down reorder arrows share the single control row with
//   edit / merge / guide / dismiss instead of trailing the expandable
//   preview onto a second grid line — each shelf row returns to single
//   height (the user-reported half-height squeeze).
// Also pins: FIFO move semantics (pendingGuidance[0] first) untouched,
// preview spans the whole row, and the 258 shelf pins still hold.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const shelf = readFileSync(join(root, "components/ComposerGuidanceShelf.tsx"), "utf8").replace(/\n\s*/g, " ");
const styles = readFileSync(join(root, "styles.css"), "utf8");

// ── 266-A: DOM order puts the arrows inside the control row ────────────────
{
  const dismissAt = shelf.indexOf("aria-label={inFlight || delivering ? actionLabel : t(\"composer.guidanceDismiss\")}");
  const reorderAt = shelf.indexOf('<span className="composer-guidance-item__reorder">');
  const previewAt = shelf.indexOf('<div className="composer-guidance-item__preview"');
  ok(dismissAt > 0 && reorderAt > 0 && previewAt > 0,
    "all three landmarks exist (dismiss control, reorder span, preview block)");
  ok(reorderAt > dismissAt,
    "the reorder arrows follow the dismiss control — same grid run of buttons");
  ok(previewAt > reorderAt,
    "the expandable preview comes AFTER the arrows, so opening it can never push the controls to a second line");
}

// ── 266-A: grid capacity keeps every control on ONE row ────────────────────
{
  const itemRule = styles.slice(styles.indexOf(".composer-guidance-item {"), styles.indexOf(".composer-guidance-item__icon"));
  ok(/grid-template-columns:\s*auto minmax\(0, 1fr\) repeat\(6, auto\)/.test(itemRule),
    "the row grid holds 1 auto + text + 6 auto slots (icon/text/edit/merge/guide/dismiss/up/down all fit one line)");
  ok(/min-height: 32px/.test(itemRule),
    "the single-height baseline (32px) is retained");
  const previewRule = styles.slice(styles.indexOf(".composer-guidance-item__preview {"), styles.indexOf(".composer-guidance-item__badge"));
  ok(/grid-column:\s*1 \/ -1/.test(previewRule),
    "the preview spans the full grid row instead of squeezing into a control column");
  ok(!/flex-basis:\s*100%/.test(previewRule),
    "the flex leftover (ignored by grid) is gone");
}

// ── FIFO move semantics untouched (pendingGuidance[0] first) ───────────────
{
  const upDisabled = /disabled=\{index === 0 \|\| disabled \|\| readOnly \|\| sendingId !== null\} onClick=\{\(\) => onMove\?\.\(item, index - 1\)\}/.test(shelf);
  const downDisabled = /disabled=\{index >= items\.length - 1 \|\| disabled \|\| readOnly \|\| sendingId !== null\} onClick=\{\(\) => onMove\?\.\(item, index \+ 1\)\}/.test(shelf);
  ok(upDisabled, "move-up still swaps with the previous row (index-1) behind the same gates");
  ok(downDisabled, "move-down still swaps with the next row (index+1) behind the same gates");

  // 258 shelf pins must survive the layout move.
  ok(/onClick=\{\(\) => onSend\(item\)\}/.test(shelf), "258 pin: per-row send button still calls onSend directly");
  ok(/onBatchSend\(batchSendable\)/.test(shelf) && /onMergeNext\(item\)/.test(shelf),
    "258 pins: batch send and merge-next buttons untouched");
}

assertNoFailure();
function assertNoFailure() {
  ok(passed >= 9, `expected at least 9 checks, got ${passed}`);
  process.stdout.write(`\n${passed} checks passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}
