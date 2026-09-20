// Run: tsx src/__tests__/composer-guidance-compose.test.ts
//
// Task 181: the guidance pencil now loads the entry into the main composer
// (a textarea) instead of editing it in a one-line shelf input. That path has
// to accept two rows the old inline editor refused — local-* optimistic entries
// (their body lives in item.text) and blocked/uncertain entries (edited here,
// then re-queued through RetryInboxItem) — while keeping in-flight and
// delivering entries closed, and it must not silently widen the older
// guidanceIsEditable contract that the shelf still asserts.

import { guidanceEditableInComposer, guidanceIsEditable } from "../lib/composerGuidance";

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

// ── what the composer editor accepts ─────────────────────────────────────────
check(guidanceEditableInComposer({ id: "i1", state: "queued" }), "a queued entry opens in the composer");
check(guidanceEditableInComposer({ id: "i1" }), "an entry without a state yet opens in the composer");
check(guidanceEditableInComposer({ id: "local-3", state: "queued" }), "a local optimistic entry opens in the composer");
check(guidanceEditableInComposer({ id: "i2", state: "blocked" }), "a blocked entry opens in the composer (re-queued on save)");
check(guidanceEditableInComposer({ id: "i3", state: "uncertain" }), "an uncertain entry opens in the composer (re-queued on save)");

// ── what it must still refuse ────────────────────────────────────────────────
check(!guidanceEditableInComposer({ id: "i4", state: "steer_accepted" }), "an in-flight entry stays closed");
check(!guidanceEditableInComposer({ id: "i5", state: "running" }), "a running entry stays closed");
check(!guidanceEditableInComposer({ id: "i6", state: "steer_consumed" }), "a consumed entry stays closed");
check(!guidanceEditableInComposer({ id: "i7", state: "queued", paused: true }), "a paused row stays closed");
check(!guidanceEditableInComposer({ id: "", state: "queued" }), "an entry without an id cannot be edited");
check(!guidanceEditableInComposer({ id: "i8", state: "mystery" }), "an unknown state stays closed");

// ── the older contract is unchanged (regression guard) ───────────────────────
check(guidanceIsEditable({ id: "i1", state: "queued" }), "guidanceIsEditable still accepts queued rows");
check(!guidanceIsEditable({ id: "local-2", state: "queued" }), "guidanceIsEditable still refuses local ids");
check(!guidanceIsEditable({ id: "i2", state: "blocked" }), "guidanceIsEditable still refuses retry rows");
check(!guidanceIsEditable({ id: "i4", state: "steer_accepted" }), "guidanceIsEditable still refuses in-flight rows");

console.log(`\n${passed} checks passed`);
