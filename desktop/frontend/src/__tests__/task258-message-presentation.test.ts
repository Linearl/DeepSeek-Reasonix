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

import { initialState, reducer, STEER_NOTICE_PREFIX, isSteerNoticeText, projectRunningState } from "../lib/useController";
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

  // 任务1106: a carrier that re-entered a merge group left NESTED merged bodies
  // (old title + segment headers verbatim inside a member's body). The parser
  // flattens them so each original renders once — no repeated title, no
  // repeated 块头 separator.
  const nested = [
    "[合并消息 ×2]",
    "",
    "── 合并自 inbox 条目 12ba92b9（来源 collab:sc_a）──",
    "[合并消息 ×2]",
    "",
    "── 合并自 inbox 条目 12ba92b9（来源 collab:sc_a）──",
    "原文A",
    "",
    "── 合并自 inbox 条目 id-b ──",
    "原文B",
    "",
    "── 合并自 inbox 条目 id-c ──",
    "原文C",
  ].join("\n");
  const flattened = parseMergedMessage(nested);
  assert.ok(flattened, "a nested merged body still folds");
  ok(flattened.count === 3, "count reports the flattened leaf segments, not the stale title number");
  ok(flattened.segments.length === 3, "the carrier shell segment is replaced by the originals it carried");
  ok(flattened.segments.every((s) => !s.body.includes("[合并消息 ×")), "no nested title line survives inside any segment body");
  ok(flattened.segments.map((s) => s.header).filter((h) => h.includes("12ba92b9")).length === 1,
    "12ba92b9 renders exactly one 块头");
  ok(flattened.segments[0]?.header.includes("12ba92b9") && flattened.segments[0]?.body === "原文A", "first leaf keeps id + verbatim body");
  ok(flattened.segments[1]?.header.includes("id-b") && flattened.segments[1]?.body === "原文B", "second leaf keeps id + verbatim body");
  ok(flattened.segments[2]?.header.includes("id-c") && flattened.segments[2]?.body === "原文C", "third leaf keeps id + verbatim body");
}

// ── source-level pins: the Composer wiring the reducer tests cannot reach ───
{
  const root = join(dirname(fileURLToPath(import.meta.url)), "..");
  const composer = readFileSync(join(root, "components/Composer.tsx"), "utf8").replace(/\n\s*/g, " ");
  const controller = readFileSync(join(root, "lib/useController.ts"), "utf8").replace(/\n\s*/g, " ");
  const shelf = readFileSync(join(root, "components/ComposerGuidanceShelf.tsx"), "utf8").replace(/\n\s*/g, " ");
  const message = readFileSync(join(root, "components/Message.tsx"), "utf8").replace(/\n\s*/g, " ");

  // 相1: both dispositions retire the row and render the bubble. 任务998: 第三
  // 参 createdAt（入队时刻）透传给气泡——行为契约（退行 + 渲染气泡）不变。
  ok(/submittedGuidanceIdsRef\.current\.add\(item\.id\); updatePendingGuidanceForDraft\(targetDraftKey, \(items\) => items\.filter\(\(queued\) => queued\.id !== item\.id\)\)/.test(composer)
    && /onQueueGuidanceBubble\?\.\(bubbleText, item\.id, item\.createdAt\)/.test(composer),
    "shelf send: accepted AND rejected steers retire the row and render the receipt bubble");
  ok(/retireSubmittedGuidance\(items, submittedGuidanceIdsRef\.current\)/.test(composer),
    "snapshot refresh filters through retireSubmittedGuidance (flash-and-return suppressed)");

  // 相2: Enter-queued follow-up renders a bubble instead of appending a row.
  // 任务998: 第三参 Date.now()（receipt 时刻）透传给气泡——块内可能夹注释，
  // 用 [\s\S]*? 跨注释匹配。
  ok(/if \(itemId && !rowStaysVisible\) { submittedGuidanceIdsRef\.current\.add\(itemId\);[\s\S]*?onQueueGuidanceBubble\?\.\(guidanceText, itemId, Date\.now\(\)\); }/.test(composer),
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

  // Regressions: shelf capabilities stay as they were (batch send / reorder /
  // merge-next). Task 441 (bc055ba45, 2026-10-01) intentionally replaced the
  // ↑/↓ arrow buttons with the six-dot drag handle (task 729: the pre-existing
  // red here), so the reorder pin follows the drag wiring — the shared movable
  // gate plus the onDrop → onMove call — not the removed arrow shape.
  ok(/onClick=\{\(\) => onSend\(item\)\}/.test(shelf), "the per-row send button still calls onSend directly");
  ok(/onBatchSend\(batchSendable\)/.test(shelf) && /const movable = Boolean\(onMove\)/.test(shelf)
    && /if \(from >= 0 && onMove\) onMove\(items\[from\], index\)/.test(shelf),
    "batch send and drag reorder are untouched (task 441 arrows→drag handle)");
  ok(/onMergeNext\(item\)/.test(shelf), "merge-next button is untouched");

  // The steer event dedupe lives in the reducer, not the caller.
  ok(/case "steer": if \(isHostRecoveryGuidance\(e\.text \?\? ""\)\) return s;[\s\S]*?if \(e\.itemId && s\.items\.some/.test(controller),
    "the consume-time steer event checks the receipt bubble before appending");
}

// ── 相4: fail turn → TurnDone → composer running 复位对冲 ───────────────────
{
  // The fail-turn lifecycle: turn_started admits, then a FAILED turn_done
  // (the 273 recovery-exhausted path) must finalize the local running state
  // and remember which turn it was.
  let s = reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "t-1", status: "in_progress" } });
  ok(s.running === true && s.activeTurnId === "t-1", "turn_started admits the running turn");
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t-1", status: "failed", err: "stream ID 3; INTERNAL_ERROR; received from peer" } });
  ok(s.running === false && s.turnActive === false, "a failed turn_done finalizes the local running state");
  ok(s.lastTurnIdAtDone === "t-1", "turn_done remembers the finalized turn id");

  // The projected composer flag (任务1015 SSOT): the backend projection is the
  // authority. A snapshot reporting THIS turn as running wins over the local
  // finalize — the 30s same-turn rejection (task 796) is gone because it was
  // the "local finalize overrides the backend" path behind the four-signal
  // contradiction; a wedged fan-out is healed by the backend idle-turn bridge.
  ok(projectRunningState(s, { known: true, running: true, state: { turnId: "t-1" } }) === true,
    "验收2 权威性: a snapshot still reporting the finalized turn as running wins — the UI stays running");
  ok(projectRunningState(s, { known: true, running: true, state: { turnId: "" } }) === true,
    "a running flag with no turn id still wins (backend authority)");
  ok(projectRunningState(s, { known: true, running: true, state: undefined }) === true,
    "a running projection with no snapshot state wins (backend authority)");

  // A genuinely NEW turn whose turn_started has not arrived yet obviously wins.
  ok(projectRunningState(s, { known: true, running: true, state: { turnId: "t-2" } }) === true,
    "a new turn's snapshot still wins before its turn_started arrives");

  // Local running beats a pre-start idle snapshot (no mid-turn flicker).
  const runningLocal = { ...initialState, running: true, turnActive: true, activeTurnId: "t-9" };
  ok(projectRunningState(runningLocal, { known: true, running: false, state: { turnId: "" } }) === true,
    "a pre-start idle snapshot never interrupts a locally running turn");

  // Unknown snapshot → local event flow decides.
  ok(projectRunningState(s, { known: false }) === false, "an unknown runtime snapshot falls back to local state");

  // 任务1015: settle requires BOTH sources idle — no local-only finalize path.
  ok(projectRunningState(s, { known: true, running: false, state: { turnId: "" } }) === false,
    "both sources idle settles the turn");

  // ── 任务1015: queued_followup 生命周期（验收3/5）─────────────────────────
  {
    // turn 在跑时提交被拒 → 降级 durable 队列（queued=true）→ 五态事实源。
    let q = reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "t-q", status: "in_progress" } });
    q = reducer(q, { type: "user", text: "hi", seq: 1, submissionId: "sub-1" });
    q = reducer(q, { type: "submit_degraded", submissionId: "sub-1", text: "queued", queued: true });
    ok(q.queuedFollowup === true, "降级回执（非 steer_accepted）置 queued_followup 事实源");
    ok(q.running === true, "降级后 turn face 保持运行中（461-P6 ruling ② 不回退）");

    // steer_accepted（本轮注入成功）不算排队。
    let q2 = reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "t-q2", status: "in_progress" } });
    q2 = reducer(q2, { type: "user", text: "hi", seq: 1, submissionId: "sub-2" });
    q2 = reducer(q2, { type: "submit_degraded", submissionId: "sub-2", text: "steered", queued: false });
    ok(q2.queuedFollowup === false, "steer_accepted（本轮注入成功）不置排队态");

    // 验收3：新轮开始 = 排队消息被消费发出 → 排队态解除、转 running。
    const q3 = reducer(q, { type: "event", e: { kind: "turn_started", turnId: "t-q-next", status: "in_progress" } });
    ok(q3.queuedFollowup === false && q3.running === true, "新轮开始（排队消息已发出）→ queued_followup 转 running");

    // reset / unsend 清排队态。
    ok(reducer(q, { type: "reset" }).queuedFollowup === false, "reset 清排队态");
    ok(reducer(q, { type: "unsend" }).queuedFollowup === false, "unsend（用户撤回）清排队态");

    // 心跳：turn 活动事件刷新 lastTurnEventAt（长工具链可见性）。
    const before = reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "t-h", status: "in_progress" } });
    ok(before.lastTurnEventAt !== undefined, "turn_started 记心跳时间");
    const after = reducer(before, { type: "event", e: { kind: "tool_dispatch", turnId: "t-h", name: "bash" } as never });
    ok(after.lastTurnEventAt !== undefined, "tool_dispatch 刷新心跳时间（长工具链无输出期亦可见）");
  }
}

// ── 258b 追加件: MiMo 相1 吸收——4 态入口拒绝 + latch 自愈 ──────────────────
{
  const root = join(dirname(fileURLToPath(import.meta.url)), "..");
  const composer = readFileSync(join(root, "components/Composer.tsx"), "utf8").replace(/\n\s*/g, " ");
  const dicts = [
    ["zh", readFileSync(join(root, "locales/zh.ts"), "utf8")],
    ["en", readFileSync(join(root, "locales/en.ts"), "utf8")],
    ["zh-TW", readFileSync(join(root, "locales/zh-TW.ts"), "utf8")],
  ] as const;

  // The four entry refusals each say why (the old one-line silent return is gone).
  ok(/if \(targetDraftKey !== activeDraftKeyRef\.current\) { showToast\(t\("composer\.guidanceWrongDraft"\), "warn"\); return; }/.test(composer),
    "draft switch refuses with the guidanceWrongDraft toast instead of a silent return");
  ok(/if \(disabled \|\| readOnly\) { showToast\(t\("composer\.guidanceNotWritable"\), "warn"\); return; }/.test(composer),
    "disabled/read-only refuses with the guidanceNotWritable toast");
  ok(/if \(running && item\.structured\) { showToast\(t\("composer\.guidanceStructuredBusy"\), "warn"\); return; }/.test(composer),
    "structured guidance on a running turn refuses with the guidanceStructuredBusy toast");
  ok(!/guidanceSendingIdRef\.current !== null\) return;/.test(composer),
    "the original combined one-line silent latch return is gone");

  // Stuck-latch self-heal: clear the ref AND the UI latch, then only a
  // DIFFERENT row is told to retry (same row proceeds — MiMo semantics kept).
  ok(/if \(guidanceSendingIdRef\.current !== null\) {[\s\S]*?const staleId = guidanceSendingIdRef\.current; guidanceSendingIdRef\.current = null; updateGuidanceSendingIdForDraft\(targetDraftKey, null\);[\s\S]*?if \(staleId !== item\.id\) { showToast\(t\("composer\.guidanceSendBusyRetry"\), "warn"\); } }/.test(composer),
    "a leaked sending-latch self-heals (ref + UI latch cleared) with the actionable retry toast for a different row");

  // Absorption boundary: 258's receipt-side bubble flow stays toast-free —
  // no double messaging when a steer is rejected into a follow-up.
  ok(!/onQueueGuidanceBubble[\s\S]{0,260}showToast/.test(composer),
    "the receipt-side bubble path never toasts (input refusals only — no overlap with 258's rejected-steer receipt)");

  // Three dialects, each key exactly ONCE — the MiMo source diff inserted
  // every key twice per dialect; absorption dedupes.
  for (const [name, dict] of dicts) {
    for (const key of ["guidanceWrongDraft", "guidanceNotWritable", "guidanceSendBusyRetry", "guidanceStructuredBusy"]) {
      const hits = dict.split(`"composer.${key}"`).length - 1;
      ok(hits === 1, `${name} defines composer.${key} exactly once (deduped vs MiMo's doubled insert)`);
    }
  }
}

assert.ok(passed >= 44, `expected at least 44 checks, got ${passed}`);
console.log(`\n${passed} checks passed, ${failed} failed`);
if (failed > 0) process.exit(1);
