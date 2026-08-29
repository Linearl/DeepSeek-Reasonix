// Run: npx tsx src/__tests__/composer-draft-persistence.test.ts
import { JSDOM } from "jsdom";
import {
  deletePersistedComposerDraft,
  loadPersistedComposerDraft,
  persistComposerDraft,
  type PersistedComposerDraft,
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

function sample(overrides?: Partial<PersistedComposerDraft>): PersistedComposerDraft {
  return {
    text: "",
    pastedBlocks: [],
    attachments: [],
    attachmentDedupKeys: {},
    workspaceRefs: [],
    ...overrides,
  };
}

function main() {
  dom.window.localStorage.clear();

  // full round-trip: text + pasted blocks + attachments + dedup + refs
  const rich = sample({
    text: "看这个 [已粘贴文本 #1 · 99 行] 和附件",
    pastedBlocks: [{ label: "[已粘贴文本 #1 · 99 行]", text: "99 行的长文本……" }],
    attachments: [{ path: "C:/tmp/report.pdf", displayName: "report.pdf" }],
    attachmentDedupKeys: { k1: { hash: "abc", source: "native-clipboard:/tmp/report.pdf" } },
    workspaceRefs: [{ path: "D:/1.workspace/demo", isDir: true }],
  });
  persistComposerDraft("session-topic\tproject\t/root\ttopic-1", rich, true);
  const restored = loadPersistedComposerDraft("session-topic\tproject\t/root\ttopic-1");
  ok(restored !== null, "persists and restores a rich draft");
  ok(restored?.text === rich.text, "text round-trips");
  ok(
    JSON.stringify(restored?.pastedBlocks) === JSON.stringify(rich.pastedBlocks),
    "pasted blocks round-trip (label + full text)",
  );
  ok(
    restored?.attachments.length === 1 && restored?.attachments[0].path === "C:/tmp/report.pdf"
      && restored?.attachments[0].displayName === "report.pdf"
      && restored?.attachments[0].previewUrl === undefined,
    "attachments round-trip path/displayName and drop previewUrl",
  );
  ok(
    JSON.stringify(restored?.attachmentDedupKeys) === JSON.stringify(rich.attachmentDedupKeys),
    "dedup keys round-trip",
  );
  ok(
    JSON.stringify(restored?.workspaceRefs) === JSON.stringify(rich.workspaceRefs),
    "workspace refs round-trip",
  );

  // per-session isolation
  ok(loadPersistedComposerDraft("session-topic\tproject\t/root\ttopic-2") === null, "other keys stay empty");

  // sent/emptied drafts are removed
  persistComposerDraft("key-a", sample({ text: "draft" }), true);
  persistComposerDraft("key-a", sample(), true);
  ok(loadPersistedComposerDraft("key-a") === null, "emptying the composer removes the persisted entry");

  // oversize drafts are not persisted
  persistComposerDraft("key-huge", sample({ text: "x".repeat(300_000) }), true);
  ok(loadPersistedComposerDraft("key-huge") === null, "oversize drafts are not persisted");

  // delete clears the stored entry
  persistComposerDraft("key-c", sample({ text: "keep me" }), true);
  deletePersistedComposerDraft("key-c");
  ok(loadPersistedComposerDraft("key-c") === null, "deletePersistedComposerDraft clears the entry");

  dom.window.localStorage.clear();
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
