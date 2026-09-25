// Task 316: stable-key merge dedup for the two history merge reducers.
//
// Reproduction first (dispatch order): the assertions below pin the two
// forensically proven holes in the OLD defence before asserting the fix —
// duplicateLiveItemIds (signature tail-head match) returns [] on both, which
// is exactly how one entryId ended up twice in `items`:
//   L1 — ref-resolved patch landed between the two reads: same stable id,
//        different text ⇒ signature mismatch ⇒ no dedup ⇒ double render.
//   L2 — reset window / off-head alignment: the overlap is not a contiguous
//        suffix-head run ⇒ longest match 0 ⇒ removeIds empty ⇒ double render.
// The fix is the union of stable ids (the steer inboxItemId precedent): same
// id in page and live rows means one copy, whatever the text says. Signature
// matching stays inside as the fallback for rows without ids.
// transient injections (session-context etc.) carry DIFFERENT ids by design —
// the union never touches them (acceptance 3).
import assert from "node:assert/strict";
import {
  duplicateLiveItemIds,
  prependRestAfterMerge,
  retainedLiveTail,
} from "../lib/hydrateHistoryApply";

let passed = 0;
function check(condition: boolean, label: string): void {
  if (!condition) {
    console.error(`  FAIL  ${label}`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  console.log(`  PASS  ${label}`);
}

type Row = { kind: string; id: string; text?: string; level?: string };

// ── L1: same stable id, patch changed the text between reads ─────────────────
const l1Page: Row[] = [{ kind: "user", id: "e1", text: "question (truncated)" }];
const l1Live: Row[] = [{ kind: "user", id: "e1", text: "question (full resolved content)" }];
const l1Old = duplicateLiveItemIds(l1Page, l1Live);
check(l1Old.length === 0, "REPRO L1: old signature matcher misses the same-id pair (patch window)");
const l1Merged = retainedLiveTail(l1Live, l1Page);
check(l1Merged.length === 0, "FIX L1: stable-id union drops the live copy of a page row");
check(new Set([...l1Page, ...l1Merged].map((r) => r.id)).size === 1 + l1Merged.length, "FIX L1: merged result carries e1 exactly once");

// ── L2: overlap sits mid-array (reset window / head not the overlap start) ──
const l2Page: Row[] = [
  { kind: "user", id: "p0", text: "older question" },
  { kind: "assistant", id: "p1", text: "older answer" },
];
const l2Live: Row[] = [
  { kind: "user", id: "noise", text: "placeholder row" },
  { kind: "user", id: "p1", text: "older answer" }, // same id as page row 1, NOT at the head
  { kind: "assistant", id: "live2", text: "streaming" },
];
const l2Old = duplicateLiveItemIds(l2Page, l2Live);
check(l2Old.length === 0, "REPRO L2: old signature matcher needs a contiguous suffix-head run (off-head overlap)");
const l2Merged = retainedLiveTail(l2Live, l2Page);
check(l2Merged.every((r) => r.id !== "p1"), "FIX L2: union removes the off-head duplicate by id");
check(l2Merged.some((r) => r.id === "noise") && l2Merged.some((r) => r.id === "live2"), "FIX L2: non-overlapping live rows survive");

// ── acceptance 3: transient injections are id-distinct and must not be touched
const trPage: Row[] = [{ kind: "user", id: "a1", text: "<session-context>ABC</session-context>" }];
const trLive: Row[] = [{ kind: "user", id: "b2", text: "<session-context>ABC</session-context>" }];
check(retainedLiveTail(trLive, trPage).length === 1, "transient rows with different ids are never deduped (design behaviour)");
check(prependRestAfterMerge(trLive, trPage, []).length === 1, "transient rows survive prepend merge too");

// ── acceptance 2 (second reducer): prepend drops same-id existing rows ──────
const prePage: Row[] = [{ kind: "user", id: "x1", text: "older page row" }];
const preExisting: Row[] = [
  { kind: "user", id: "x1", text: "x1 after ref-resolved patch" },
  { kind: "assistant", id: "y1", text: "live row" },
];
const preRest = prependRestAfterMerge(preExisting, prePage, []);
check(preRest.length === 1 && preRest[0]?.id === "y1", "prepend union drops the existing copy of a page row by id");
const preRestWithRemove = prependRestAfterMerge(preExisting, prePage, ["y1"]);
check(preRestWithRemove.length === 0, "prepend keeps honouring caller-provided removeIds on top of the union");

// ── rows without ids: union has nothing to key on, signature fallback stays ──
const noIdPage: Row[] = [{ kind: "user", id: "", text: "same text" }];
const noIdLive: Row[] = [{ kind: "user", id: "", text: "same text" }, { kind: "user", id: "", text: "other" }];
const noIdMerged = retainedLiveTail(noIdLive, noIdPage);
check(noIdMerged.length === 1, "id-less rows fall back to the signature suffix match (second row kept)");

console.log(`\n${passed} passed${process.exitCode ? ", with failures" : ""}`);
if (process.exitCode) process.exit(1);
