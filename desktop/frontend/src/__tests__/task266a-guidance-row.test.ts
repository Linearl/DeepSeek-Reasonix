// Run: LANG=en node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/task266a-guidance-row.test.ts
//
// Task 266-A — 引导货架排序按钮合行（A 段，B 拖拽排序已推批七）:
//   the reorder controls share the single control row with
//   edit / merge / guide / dismiss instead of trailing the expandable
//   preview onto a second grid line — each shelf row returns to single
//   height (the user-reported half-height squeeze).
// Task 441 (2026-10-02): the 266-A up/down ARROWS are removed — the six-dot
//   drag handle (⠿) is the reorder affordance and doubles as the row glyph.
//   The layout pin survives: handle + text + edit/merge/guide/dismiss still
//   fit ONE grid line, and the preview still spans the full row.
// Also pins: FIFO move semantics (pendingGuidance[0] first) untouched,
// and the 258 shelf pins still hold.

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

// ── 266-A + 441: DOM order puts the controls inside the control row ────────
{
  const dismissAt = shelf.indexOf("aria-label={inFlight || delivering ? actionLabel : t(\"composer.guidanceDismiss\")}");
  const handleAt = shelf.indexOf("composer-guidance-item__handle");
  const previewAt = shelf.indexOf('<div className="composer-guidance-item__preview"');
  ok(dismissAt > 0 && handleAt > 0 && previewAt > 0,
    "all three landmarks exist (dismiss control, drag handle, preview block)");
  ok(handleAt < dismissAt,
    "the drag handle leads the row (task 441 six-dot handle, not the old arrows)");
  ok(previewAt > dismissAt,
    "the expandable preview comes AFTER the controls, so opening it can never push them to a second line");
  ok(!/composer-guidance-item__reorder/.test(shelf) && !/guidanceMoveUp/.test(shelf) && !/guidanceMoveDown/.test(shelf),
    "the task 441 removal holds: no reorder span, no up/down arrow buttons");
}

// ── 266-A + 441: grid capacity keeps every control on ONE row ──────────────
{
  const itemRule = styles.slice(styles.indexOf(".composer-guidance-item {"), styles.indexOf(".composer-guidance-item__icon"));
  ok(/grid-template-columns:\s*auto minmax\(0, 1fr\) repeat\(4, auto\)/.test(itemRule),
    "the row grid holds 1 auto + text + 4 auto slots (handle/text/edit/merge/guide/dismiss all fit one line)");
  ok(/min-height: 32px/.test(itemRule),
    "the single-height baseline (32px) is retained");
  const handleRule = styles.slice(styles.indexOf(".composer-guidance-item__handle {"), styles.indexOf(".composer-guidance-item__handle:hover"));
  ok(/cursor:\s*grab/.test(handleRule),
    "the six-dot handle carries the grab cursor (drag affordance, task 441)");
  const previewRule = styles.slice(styles.indexOf(".composer-guidance-item__preview {"), styles.indexOf(".composer-guidance-item__badge"));
  ok(/grid-column:\s*1 \/ -1/.test(previewRule),
    "the preview spans the full grid row instead of squeezing into a control column");
  ok(!/flex-basis:\s*100%/.test(previewRule),
    "the flex leftover (ignored by grid) is gone");
}

// ── FIFO move semantics untouched (pendingGuidance[0] first) ───────────────
{
  // Task 441: moves ride the handle drag — the drop lands at the hovered
  // row's index through onMove (the same durable MoveInboxItem path the
  // arrows used), and the drag source is per-row so a drag can only start
  // from a handle, never the card body.
  ok(/const from = items\.findIndex\(\(queued\) => queued\.id === dragId\)/.test(shelf)
    && /if \(from >= 0 && onMove\) onMove\(items\[from\], index\)/.test(shelf),
    "drop still moves the dragged row to the hovered row's index behind the same gates");
  ok(/draggable/.test(shelf) && /onDragStart=/.test(shelf),
    "the handle (not the card) is the draggable element");
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
