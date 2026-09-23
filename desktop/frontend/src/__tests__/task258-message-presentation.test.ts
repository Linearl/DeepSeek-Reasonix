// Run: npx tsx src/__tests__/task258-message-presentation.test.ts
//
// Task 258 — 消息呈现层三相:
//   1. guidance shelf click → receipt-time transcript bubble + row retirement
//      (both steer_accepted and queued_followup; no snapshot resurrection)
//   2. steer/followup renders as a user-side bubble (receipt first, the
//      agent's consume-time steer event dedupes against it)
//   3. a drain-merged injection folds into "合并消息 ×N" + expands per original
// Also pins the shelf regressions: batch/reorder/merge-next stay untouched.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { initialState, reducer, STEER_NOTICE_PREFIX, isSteerNoticeText } from "../lib/useController";
import { retireSubmittedGuidance } from "../lib/composerInboxQueue";
import type { PendingGuidance } from "../components/ComposerGuidanceShelf";
import { parseMergedMessage } from "../components/Message";

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

function noticeItems(state: typeof initialState) {
  return state.items.filter((item) => item.kind === "notice");
}

// ── 相1+相2: guidance_bubble (receipt-time) ─────────────────────────────────
{
  // Append-only: the bubble rides an already-active turn.
  let state = { ...initialState, running: true, turnActive: true, seq: 5 };
  state = reducer(state, { type: "guidance_bubble", text: "do the thing", inboxItemId: "ib-1" });
  const bubbles = noticeItems(state).filter((item) => item.inboxItemId === "ib-1");
  ok(bubbles.length === 1, "guidance_bubble appends exactly one item");
  ok(bubbles[0]?.text === `${STEER_NOTICE_PREFIX}do the thing`, "bubble carries the steer notice prefix the shelf path already renders");
  ok(state.running === true && state.turnActive === true, "bubble append never touches running/turnActive (rides the active turn)");
  ok(state.seq === 6, "bubble bumps seq for a stable id");
  ok(isSteerNoticeText(bubbles[0]?.text ?? ""), "bubble text is recognised as a user-side steer notice");

  // Receipt first, event later: same inboxItemId must not double.
  const afterEvent = reducer(state, { type: "event", e: { kind: "steer", turnId: "t1", text: "do the thing", itemId: "ib-1" } });
  ok(noticeItems(afterEvent).filter((item) => item.inboxItemId === "ib-1").length === 1,
    "consume-time steer event dedupes against the receipt bubble (no double row)");

  // Same itemId retried must not double either.
  const retried = reducer(state, { type: "guidance_bubble", text: "do the thing", inboxItemId: "ib-1" });
  ok(noticeItems(retried).filter((item) => item.inboxItemId === "ib-1").length === 1,
    "a repeated guidance_bubble for the same inboxItemId keeps one bubble");

  // A DIFFERENT item still renders.
  const second = reducer(state, { type: "guidance_bubble", text: "another", inboxItemId: "ib-2" });
  ok(noticeItems(second).filter((item) => item.kind === "notice" && item.inboxItemId).length === 2,
    "a different inboxItemId renders its own bubble");

  // No itemId (local/unknown source): still renders, dedupe keyed by text is
  // NOT attempted — the prefix keeps legacy renderers working.
  const noId = reducer({ ...initialState, running: true }, { type: "guidance_bubble", text: "loose text" });
  ok(noticeItems(noId).some((item) => item.text === `${STEER_NOTICE_PREFIX}loose text`),
    "bubble without inboxItemId still renders");

  // ── case "steer" without a prior receipt still behaves as before ────────
  const fresh = reducer({ ...initialState, running: true }, { type: "event", e: { kind: "steer", turnId: "t1", text: "plain steer", itemId: "ib-9" } });
  ok(noticeItems(fresh).filter((item) => item.inboxItemId === "ib-9").length === 1,
    "a steer event with no receipt bubble still renders exactly once");
}

// ── 相1: submitted-row suppression across snapshot refreshes ────────────────
{
  const rows: PendingGuidance[] = [
    { id: "a", text: "queued one", intent: "followup", state: "queued", source: "desktop" },
    { id: "b", text: "steer accepted", intent: "steer", state: "steer_accepted", source: "desktop" },
    { id: "c", text: "still waiting", intent: "followup", state: "queued", source: "desktop" },
  ];
  const submitted = new Set(["a", "b"]);
  const filtered = retireSubmittedGuidance(rows, submitted);
  ok(filtered.length === 1 && filtered[0]?.id === "c", "submitted rows stay retired; untouched rows survive the refresh");
  ok(retireSubmittedGuidance(rows, new Set()).length === 3, "an empty submitted set is identity (zero regression when nothing was sent)");
  const sameRef = retireSubmittedGuidance(rows, new Set());
  ok(sameRef === rows, "empty set returns the SAME array reference (no needless rerender)");
}

// ── 相3: merged injection folds into 合并消息 ×N ────────────────────────────
{
  const merged = [
    "[合并消息 ×3]",
    "",
    "── 合并自 inbox 条目 i1（来源 collab:sc_1）──",
    "first body",
    "",
    "── 合并自 inbox 条目 i2（来源 collab:sc_2）──",
    "second body",
    "spanning two lines",
    "",
    "── 合并自 inbox 条目 i3 ──",
    "third body",
  ].join("\n");
  const parsed = parseMergedMessage(merged);
  assert.ok(parsed, "a drain-merged injection is recognised");
  ok(parsed.title === "[合并消息 ×3]" && parsed.count === 3, "title and count come from the header row");
  ok(parsed.segments.length === 3, "every ── 合并自 inbox 条目 segment is split out");
  ok(parsed.segments[0]?.body === "first body", "first segment body is verbatim");
  ok(parsed.segments[1]?.body === "second body\nspanning two lines", "segment bodies keep internal newlines");
  ok(parsed.segments[1]?.header.includes("i2"), "segment header keeps the original inbox item id");

  ok(parseMergedMessage("ordinary user text") === null, "an ordinary user message never folds");
  ok(parseMergedMessage("[合并消息 ×1]")?.segments.length === 0, "a header-only injection still folds with zero segments");
  ok(parseMergedMessage("[合并消息 ×2]\nbody without marker")?.segments.length === 0,
    "segments without a ── marker stay empty rather than being invented");
}

// ── source-level pins: the Composer wiring the reducer tests cannot reach ───
{
  const root = join(dirname(fileURLToPath(import.meta.url)), "..");
  const composer = readFileSync(join(root, "components/Composer.tsx"), "utf8").replace(/\n\s*/g, " ");
  const controller = readFileSync(join(root, "lib/useController.ts"), "utf8").replace(/\n\s*/g, " ");
  const shelf = readFileSync(join(root, "components/ComposerGuidanceShelf.tsx"), "utf8").replace(/\n\s*/g, " ");
  const message = readFileSync(join(root, "components/Message.tsx"), "utf8").replace(/\n\s*/g, " ");

  // 相1: both dispositions retire the row and render the bubble.
  ok(/submittedGuidanceIdsRef\.current\.add\(item\.id\); updatePendingGuidanceForDraft\(targetDraftKey, \(items\) => items\.filter\(\(queued\) => queued\.id !== item\.id\)\)/.test(composer)
    && /onQueueGuidanceBubble\?\.\(bubbleText, item\.id\)/.test(composer),
    "shelf send: accepted AND rejected steers retire the row and render the receipt bubble");
  ok(/retireSubmittedGuidance\(items, submittedGuidanceIdsRef\.current\)/.test(composer),
    "snapshot refresh filters through retireSubmittedGuidance (flash-and-return suppressed)");

  // 相2: Enter-queued follow-up renders a bubble instead of appending a row.
  ok(/if \(itemId && !rowStaysVisible\) { submittedGuidanceIdsRef\.current\.add\(itemId\); onQueueGuidanceBubble\?\.\(guidanceText, itemId\); }/.test(composer),
    "Enter follow-up: receipt renders the bubble and retires the row (paused/finishing keep theirs)");
  ok(/steerForTab receipt-time|dispatchTo\(tabId, \{ type: "guidance_bubble", text, inboxItemId: receipt\?\.itemId \}\)/.test(controller),
    "steerForTab dispatches the bubble at receipt time (Enter 补充同显)");
  ok(/onQueueGuidanceBubble: router\.handleQueueGuidanceBubble/.test(readFileSync(join(root, "app-shell/decisionFooterBuilders.ts"), "utf8"))
    && /queueGuidanceBubble: controller\.queueGuidanceBubble/.test(readFileSync(join(root, "app-runtime/useAppRuntimeAdapter.ts"), "utf8")),
    "the main composer surface receives queueGuidanceBubble through the router chain");

  // 相3: the fold is wired into the user bubble body.
  ok(/const mergedMessage = useMemo\(\(\) => \(imSource \? null : parseMergedMessage\(displayText\)\)/.test(message)
    && /<MergedMessageBody merged=\{mergedMessage\} \/>/.test(message),
    "a merged injection renders MergedMessageBody inside the user bubble");

  // Regressions: shelf buttons stay as they were (batch / reorder / merge-next).
  ok(/onClick=\{\(\) => onSend\(item\)\}/.test(shelf), "the per-row send button still calls onSend directly");
  ok(/onBatchSend\(batchSendable\)/.test(shelf) && /onMove\?\.\(item, index - 1\)/.test(shelf),
    "batch send and reorder arrows are untouched");
  ok(/onMergeNext\(item\)/.test(shelf), "merge-next button is untouched");

  // The steer event dedupe lives in the reducer, not the caller.
  ok(/case "steer": if \(isHostRecoveryGuidance\(e\.text \?\? ""\)\) return s;[\s\S]*?if \(e\.itemId && s\.items\.some/.test(controller),
    "the consume-time steer event checks the receipt bubble before appending");
}

assert.ok(passed >= 20, `expected at least 20 checks, got ${passed}`);
console.log(`\n${passed} checks passed, ${failed} failed`);
if (failed > 0) process.exit(1);
