// Task 51 UI-2: short-window inbox enqueue dedup.
import assert from "node:assert/strict";
import {
  attachInboxDedupItemId,
  noteInboxEnqueue,
  resetInboxDedup,
} from "../lib/inboxDedup";

let passed = 0;
function check(cond: boolean, label: string): void {
  if (!cond) {
    console.error(`  FAIL  ${label}`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  console.log(`  PASS  ${label}`);
}

resetInboxDedup();

// First enqueue is recorded, not a hit.
check(noteInboxEnqueue("tab-a", "fix the bug", "", { now: 1000 }) === null, "first enqueue proceeds");
// Same text inside the window is a duplicate.
const hit = noteInboxEnqueue("tab-a", "fix the bug", "", { now: 2000 });
check(hit !== null && hit.at === 1000, "same text inside window is a duplicate");
// Different tab is independent.
check(noteInboxEnqueue("tab-b", "fix the bug", "", { now: 2000 }) === null, "different tab is not a duplicate");
// Different text on the same tab is independent.
check(noteInboxEnqueue("tab-a", "other request", "", { now: 2000 }) === null, "different text is not a duplicate");
// Outside the window the same text proceeds again.
check(noteInboxEnqueue("tab-a", "fix the bug", "", { now: 1000 + 6000 }) === null, "outside window proceeds");

// Attach item id after a durable receipt.
resetInboxDedup();
check(noteInboxEnqueue("tab-a", "hello", "fp1", { now: 5000 }) === null, "attach path first proceeds");
attachInboxDedupItemId("tab-a", "hello", "fp1", "item-42");
const withId = noteInboxEnqueue("tab-a", "hello", "fp1", { now: 5500 });
check(withId !== null && withId.itemId === "item-42", "duplicate carries the durable item id");

// Structured fingerprint separates otherwise-same display text.
resetInboxDedup();
check(noteInboxEnqueue("tab-a", "run", "fpA", { now: 1 }) === null, "fpA proceeds");
check(noteInboxEnqueue("tab-a", "run", "fpB", { now: 2 }) === null, "different fingerprint proceeds");

console.log(`\n${passed} passed${process.exitCode ? ", with failures" : ""}`);
if (process.exitCode) process.exit(1);
