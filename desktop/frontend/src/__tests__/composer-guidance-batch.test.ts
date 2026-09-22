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
  ok(/const selectable = !editing && !inFlight && !delivering && !unknownState && !item\.paused/.test(shelf),
    "batch-selectable excludes editing, in-flight, delivering, unknown and paused rows (one shared gate)");
  ok(/batchSendable = batchSelected\.filter\( \(item\) => !\(running && !guidanceNeedsRetry\(item\.state\) && Boolean\(item\.structured\)\)/.test(shelf),
    "batch send skips structured entries on an active turn instead of counting a silent no-op");
  ok(/onBatchSend\(batchSendable\)/.test(shelf) && /onBatchDismiss\(batchSelected\)/.test(shelf),
    "batch bar dispatches the filtered send set and the full dismiss set");
  ok(/disabled=\{batchSendable\.length === 0 \|\| sendingId !== null\}/.test(shelf),
    "batch send disables while nothing is sendable or a single send is in flight");
  ok(/selectMode && selectable && onToggleSelect/.test(shelf),
    "checkbox renders only in select mode on selectable rows");
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

// ── 181 reorder: MoveInboxItem is the UI's only ordering write ─────────────
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
    "the same gate hides the arrows and the drag handle on rows that cannot move");
  ok(/onClick=\{\(\) => onMove\?\.\(item, index - 1\)\}/.test(shelf) && /onClick=\{\(\) => onMove\?\.\(item, index \+ 1\)\}/.test(shelf),
    "up/down arrows move by exactly one position");
  ok(/onDrop=/.test(shelf) && /onDragStart=/.test(shelf) && /setDragId/.test(shelf),
    "drag reorder tracks its source id and drops at the hovered row's index");
  ok(/draggable=\{movable && !selectMode\}/.test(shelf),
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
    "composer.guidanceBatchBar",
    "composer.guidanceBatchSelected",
    "composer.guidanceBatchSend",
    "composer.guidanceBatchDismiss",
    "composer.guidanceMoveUp",
    "composer.guidanceMoveDown",
  ];
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    const src = readFileSync(join(root, "locales", locale), "utf8");
    const missing = keys.filter((key) => !src.includes(`"${key}"`));
    ok(missing.length === 0, `${locale} carries all ${keys.length} new keys${missing.length ? ` (missing: ${missing.join(", ")})` : ""}`);
  }
}

process.stdout.write(`\n${passed} checks passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
