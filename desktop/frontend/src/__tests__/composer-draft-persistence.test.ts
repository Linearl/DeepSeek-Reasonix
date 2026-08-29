// Run: npx tsx src/__tests__/composer-draft-persistence.test.ts
import { JSDOM } from "jsdom";
import {
  deletePersistedComposerText,
  loadPersistedComposerText,
  persistComposerText,
} from "../lib/composerDraftPersistence";

// jsdom provides localStorage
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { localStorage: Storage }).localStorage = dom.window.localStorage;

let passed = 0;
let failed = 0;
function ok(condition: boolean, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function main() {
  dom.window.localStorage.clear();

  // round-trip per session key
  persistComposerText("session-topic\tproject\t/root\ttopic-1", "未发送的草稿", true);
  ok(
    loadPersistedComposerText("session-topic\tproject\t/root\ttopic-1") === "未发送的草稿",
    "persists and restores a draft text per session key",
  );
  ok(loadPersistedComposerText("session-topic\tproject\t/root\ttopic-2") === "", "other session keys stay empty");

  // sent/emptied drafts are removed
  persistComposerText("key-a", "draft", true);
  persistComposerText("key-a", "", true);
  ok(loadPersistedComposerText("key-a") === "", "clearing the input removes the persisted entry");

  // oversize drafts are not persisted
  persistComposerText("key-huge", "x".repeat(70_000), true);
  ok(loadPersistedComposerText("key-huge") === "", "oversize drafts are not persisted");

  // delete clears the stored entry
  persistComposerText("key-c", "keep me", true);
  deletePersistedComposerText("key-c");
  ok(loadPersistedComposerText("key-c") === "", "deletePersistedComposerText clears the entry");

  dom.window.localStorage.clear();
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
