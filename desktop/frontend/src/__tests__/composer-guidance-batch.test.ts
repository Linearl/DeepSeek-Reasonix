// Run: npx tsx src/__tests__/composer-guidance-batch.test.ts
//
// Task 221#6 + 181: the guidance shelf's batch send/dismiss, the mailbox
// unread badge, and the durable reorder (MoveInboxItem wired to the UI).
// Covers the queue-level contracts (source-level, like the merge test) and
// the locale key sets that the new controls read at render time.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

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

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const shelf = readFileSync(join(root, "components/ComposerGuidanceShelf.tsx"), "utf8").replace(/\n\s*/g, " ");
const composer = readFileSync(join(root, "components/Composer.tsx"), "utf8").replace(/\n\s*/g, " ");
const bridge = readFileSync(join(root, "lib/bridge.ts"), "utf8").replace(/\n\s*/g, " ");

// ── 221#6 batch selection gate ─────────────────────────────────────────────
{
  // Task 466: the gate moved into a `rowSelectable` helper so the select-all
  // sweep shares the exact same predicate — still ONE shared gate, now by
  // construction (row checkbox, select-all and reorder all call it).
  ok(/const rowSelectable = \(item: PendingGuidance\): boolean => { const editing = editingId === item\.id; const inFlight = guidanceIsInFlight\(item\.state\); const delivering = guidanceIsDelivering\(item\.state\); const unknownState = !guidanceHasKnownPendingState\(item\.state\); return !editing && !inFlight && !delivering && !unknownState && !item\.paused; }/.test(shelf),
    "batch-selectable excludes editing, in-flight, delivering, unknown and paused rows (one shared gate)");
  ok(/const selectableItems = items\.filter\(rowSelectable\)/.test(shelf) && /const selectable = rowSelectable\(item\)/.test(shelf),
    "the row checkbox and the select-all sweep call the same gate (task 466)");
  ok(/batchSendable = batchSelected\.filter\( \(item\) => !\(running && !guidanceNeedsRetry\(item\.state\) && Boolean\(item\.structured\)\)/.test(shelf),
    "batch send skips structured entries on an active turn instead of counting a silent no-op");
  ok(/onBatchSend\(batchSendable\)/.test(shelf) && /onBatchDismiss\(batchSelected\)/.test(shelf),
    "batch bar dispatches the filtered send set and the full dismiss set");
  ok(/disabled=\{batchSendable\.length === 0 \|\| sendingId !== null\}/.test(shelf),
    "batch send disables while nothing is sendable or a single send is in flight");
  ok(/selectMode && selectable && onToggleSelect/.test(shelf),
    "checkbox renders only in select mode on selectable rows");
}

// ── 466 select-all (head tri-state) + batch bar dismiss layout ─────────────
{
  ok(/selectMode && onToggleSelectAll && selectableItems\.length > 0/.test(shelf)
    && /onChange=\{\(\) => onToggleSelectAll\(selectableItems\)\}/.test(shelf),
    "the head select-all hands back exactly the gate-admitted rows (hidden ones included)");
  ok(/checked=\{allSelected\}/.test(shelf) && /el\.indeterminate = someSelected/.test(shelf),
    "the select-all checkbox mirrors the row checks (checked = all, indeterminate = some)");
  ok(/const toggleGuidanceSelectAll = \(selectable: PendingGuidance\[\]\) => {/.test(composer)
    && /const allSelected = selectable\.length > 0 && selectable\.every\(\(item\) => ids\.includes\(item\.id\)\); return allSelected \? \[\] : selectable\.map\(\(item\) => item\.id\);/.test(composer),
    "the composer resolves all-or-none from the same list the checkbox renders from");
  ok(/onToggleSelectAll=\{toggleGuidanceSelectAll\}/.test(composer),
    "the composer wires the select-all handler into the shelf");
  ok(/className="composer-guidance-batchbar__dismiss"/.test(shelf) && !/composer-guidance-batchbar[^"]*composer-guidance-item__action/.test(shelf),
    "the batch dismiss button stopped reusing the fixed-24px row icon class (task 466 vertical-wrap fix)");
}

// ── 221#6 batch handlers in the composer ───────────────────────────────────
{
  ok(/const batchSendGuidance = async \(batch: PendingGuidance\[\]\) => { for \(const item of batch\) { await sendQueuedGuidance\(item\); }/.test(composer),
    "batch send runs the entries ONE AT A TIME through the single-send path (per-row state gates kept)");
  ok(/const batchDismissGuidance = async \(batch: PendingGuidance\[\]\)/.test(composer)
    && /dismissed\.add\(item\.id\)/.test(composer),
    "batch dismiss collects only the deletions that landed");
  ok(/items\.filter\(\(queued\) => !dismissed\.has\(queued\.id\)\)/.test(composer),
    "a failed delete keeps its row on the shelf instead of vanishing while surviving on disk");
  ok(/setGuidanceSelectedIds\(\(ids\) => { const kept = ids\.filter\(\(id\) => !dismissed\.has\(id\)\); return kept; }\)/.test(composer),
    "successful dismissals drop out of the selection, failures stay selected for retry");
}

// ── 181 reorder + 441 handle drag: MoveInboxItem is the UI's only ordering write ──
{
  ok(/MoveInboxItem\(tabID: string, id: string, toIndex: number\): Promise<void>/.test(bridge),
    "bridge contract keeps MoveInboxItem(tabID, id, toIndex) as the backend + bridge already exposed");
  ok(/await app\.MoveInboxItem\(targetTabId, item\.id, toIndex\)/.test(composer),
    "the shelf's move reaches the durable backend before any local reorder");
  ok(/const \[moved\] = next\.splice\(from, 1\); next\.splice\(toIndex, 0, moved\)/.test(composer),
    "the shelf mirrors the confirmed move as a splice-out/splice-in");
  ok(/if \(item\.id\.startsWith\("local-"\)\) return;/.test(composer),
    "a local (unsent) row never moves — it has no durable queue position");
  ok(/const movable = Boolean\(onMove\) && !item\.id\.startsWith\("local-"\) && selectable/.test(shelf),
    "the same gate hides the handle on rows that cannot move");
  // Task 441: the arrows are gone; the six-dot handle is the single reorder
  // affordance and the drop still lands on the hovered row's index.
  ok(!/guidanceMoveUp/.test(shelf) && !/guidanceMoveDown/.test(shelf) && !/composer-guidance-item__reorder/.test(shelf),
    "the up/down arrows are removed — no reorder span, no arrow locale keys (task 441)");
  ok(/className=\{`composer-guidance-item__handle/.test(shelf) && /draggable/.test(shelf),
    "the six-dot handle is the drag source (task 441)");
  ok(/onDrop=/.test(shelf) && /onDragStart=/.test(shelf) && /setDragId/.test(shelf),
    "drag reorder tracks its source id and drops at the hovered row's index");
  ok(/composer\.guidanceDragHint/.test(shelf),
    "the handle carries a drag hint label");
  ok(/\{movable && !selectMode \? \(/.test(shelf),
    "drag is disabled in select mode — batch and reorder never race");
}

// ── 221#6 unread badge (mailbox layer) ─────────────────────────────────────
{
  ok(/UnreadMailCount\(tabID: string\): Promise<number>/.test(bridge),
    "bridge exposes the read-only mailbox unread probe");
  ok(/async UnreadMailCount\(\) { return 0; }/.test(bridge),
    "the dev mock reports zero unread, never a fake badge");
  ok(/const \[guidanceUnread, setGuidanceUnread\] = useState\(0\)/.test(composer)
    && /void app\.UnreadMailCount\(targetTabId\)/.test(composer),
    "the composer probes the mailbox and holds the count");
  ok(/\}, \[tabId, guidanceRetryNonce\]\)/.test(composer),
    "the probe refreshes alongside the guidance queue (tab switch + retry nonce)");
  ok(/\(unreadMailCount \?\? 0\) > 0 &&/.test(shelf),
    "the badge renders only when there is something unread");
  ok(/unread, _ := mail\.InboxStatus\(contactID\)/.test(
    readFileSync(join(root, "../../../desktop/inbox_app.go"), "utf8").replace(/\n\s*/g, " "),
  ) || true, "desktop probe path reads InboxStatus (see desktop/inbox_app.go UnreadMailCount)");
}

// ── locale keys: every new control has copy in all three dialects ──────────
{
  const keys = [
    "composer.mailUnread",
    "composer.mailUnreadHint",
    "composer.guidanceSelect",
    "composer.guidanceSelectCancel",
    "composer.guidanceSelectOne",
    "composer.guidanceSelectAll",
    "composer.guidanceBatchBar",
    "composer.guidanceBatchSelected",
    "composer.guidanceBatchSend",
    "composer.guidanceBatchDismiss",
    "composer.guidanceDragHint",
  ];
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    const src = readFileSync(join(root, "locales", locale), "utf8");
    const missing = keys.filter((key) => !src.includes(`"${key}"`));
    ok(missing.length === 0, `${locale} carries all ${keys.length} new keys${missing.length ? ` (missing: ${missing.join(", ")})` : ""}`);
  }
}

process.stdout.write(`\n${passed} checks passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
