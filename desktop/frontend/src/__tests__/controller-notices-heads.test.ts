// Run: tsx src/__tests__/controller-notices-heads.test.ts
//
// Head notices from the session log localize by their stable code so the
// backend text, which may carry a head id, never reaches the transcript.
// Task 658: the concurrent-writer notice also carries an inline view_versions
// action so the button opens the version dialog directly.

import assert from "node:assert/strict";
import { appendNoticeItem, historyNoticeItems, localizedNoticeText, quietTranscriptNoticeKey } from "../lib/controllerNotices";

const cases: Array<[string, string, string]> = [
  ["session_concurrent_writer", "another writer appended to this session log", "Another Reasonix window or process added to this conversation. Its content is kept as a separate version; select the conversation in the history panel and use View versions to switch."],
  ["session_head_switched", "opened head 01HEAD (newest activity)", "Opened the newest version of this conversation. Other saved versions are available in View versions."],
  ["session_head_selected", "selected head 01HEAD", "This version is now the current version of the conversation."],
  ["session_also_open", "This session is also open in another Reasonix instance. Both may save; conflicts will fork a recovery copy.", "This session is also open in another Reasonix instance. Both may save; conflicts will fork a recovery copy."],
];
for (const [code, raw, want] of cases) {
  assert.equal(localizedNoticeText(raw, code), want, `${code} localizes by code`);
  assert.equal(quietTranscriptNoticeKey(raw, code), "", `${code} is shown, not a quiet lifecycle notice`);
  assert.ok(!localizedNoticeText(raw, code).includes("01HEAD"), `${code} must not leak head ids`);
}
console.log("  PASS  session-log head notices localize by code and stay visible");

// Task 658: only the concurrent-writer notice gets the inline jump; the dual-tab
// notice asks the user to close a tab, not to inspect versions.
const withAction = appendNoticeItem([], 0, "n0", "warn", "another writer appended to this session log", undefined, "session_concurrent_writer").items[0];
assert.equal(withAction.kind, "notice");
assert.equal(withAction.kind === "notice" && withAction.action, "view_versions", "concurrent-writer notice carries the view_versions action");

const plain = appendNoticeItem([], 0, "n1", "warn", "another writer appended to this session log", undefined, "session_concurrent_dual_tab").items[0];
assert.equal(plain.kind === "notice" && plain.action, undefined, "dual-tab notice stays action-free");

const headSwitched = appendNoticeItem([], 0, "n2", "info", "opened head 01HEAD (newest activity)", undefined, "session_head_switched").items[0];
assert.equal(headSwitched.kind === "notice" && headSwitched.action, undefined, "head-switched notice stays action-free");
console.log("  PASS  concurrent-writer notice carries the inline view_versions action");

// The same action survives a reload: windowed history rebuilds the notice from
// the persisted log message.
const rebuilt = historyNoticeItems({ role: "notice", content: "another writer appended to this session log", code: "session_concurrent_writer", level: "warn" } as Parameters<typeof historyNoticeItems>[0], "h0")[0];
assert.equal(rebuilt.kind === "notice" && rebuilt.action, "view_versions", "rebuilt history notice keeps the view_versions action");
console.log("  PASS  history-rebuilt concurrent-writer notice keeps the inline action");
