// Run: tsx src/__tests__/transcript-find.test.ts
// Task 399: pure search logic for the in-session Ctrl+F find bar.

import { JSDOM } from "jsdom";

import {
  EMPTY_FIND_INDEX,
  TRANSCRIPT_FIND_HIT_CAP,
  buildTranscriptFindIndex,
  extractRowFindText,
  findAttributeSelector,
  searchTranscriptFind,
  shouldIgnoreFindShortcutTarget,
  stepFindHit,
} from "../lib/transcriptFind";
import {
  SHORTCUT_DEFINITIONS,
  defaultShortcutCombo,
  matchesShortcut,
  shortcutConflict,
} from "../lib/keyboardShortcuts";
import type { TranscriptRow } from "../lib/transcriptRows";
import type { TimelineBlock } from "../lib/transcriptTimeline";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

function userRow(text: string, key = "u:1"): TranscriptRow {
  return { kind: "user", key, item: { kind: "user", id: key.slice(2), text }, turn: 0, layoutVariant: "text-flow" };
}

function answerRow(text: string, key = "a:1"): TranscriptRow {
  return { kind: "answer", key, item: { kind: "assistant", id: key.slice(2), text, reasoning: "", streaming: false }, layoutVariant: "text-flow" };
}

function toolRow(name: string, output: string, key = "t:1"): TranscriptRow {
  return {
    kind: "tool",
    key,
    item: { kind: "tool", id: key.slice(2), name, args: "{}", readOnly: false, status: "done", output },
  };
}

function closedProcessHeader(segmentText: string): TranscriptRow {
  return {
    kind: "process-header",
    key: "ph:s1",
    open: false,
    segment: {
      key: "s1",
      processItems: [],
      outsideItems: [],
      displayItems: [{ kind: "tool", id: "tool-in-fold", name: "grep", args: "needle", readOnly: true, status: "done", output: segmentText }],
      hasOutsideContent: false,
      foldActive: false,
      hasRunningWork: false,
      durationMs: 1200,
      labelStyle: "counts",
      turnActive: false,
    },
    layoutVariant: "static",
  };
}

function blockOf(key: string, rows: TranscriptRow[]): TimelineBlock {
  return { key, phase: "completed", rows: rows as TimelineBlock["rows"], contentRevision: 1, measurementRevision: "1" };
}

console.log("\ntranscript find (task 399)");

// ── row text extraction ────────────────────────────────────────────────
eq(extractRowFindText(userRow("Hello World")), "Hello World", "user row extracts prompt text");
eq(extractRowFindText(answerRow("The answer is 42")), "The answer is 42", "answer row extracts answer text");
eq(extractRowFindText(toolRow("bash", "ls output")), "bash\n{}\nls output", "tool row includes name, args and output");
eq(extractRowFindText(closedProcessHeader("folded content")), "grep\nneedle\nfolded content", "closed fold header carries its segment text (name + args + output)");
eq(
  extractRowFindText({ kind: "process-header", key: "ph:s1", open: true, segment: closedProcessHeader("").segment, layoutVariant: "static" } as TranscriptRow),
  "",
  "open fold header contributes nothing (body rows search themselves)",
);
eq(
  extractRowFindText({ kind: "older-history", key: "older-history", layoutVariant: "static" } as TranscriptRow),
  "",
  "older-history sentinel row is not searchable",
);
eq(
  extractRowFindText({
    kind: "notice",
    key: "n:1",
    item: { kind: "notice", id: "1", level: "warn", text: "careful now", title: "Heads up" },
    layoutVariant: "text-flow",
  } as TranscriptRow),
  "Heads up\ncareful now",
  "notice row includes title and text",
);
eq(
  extractRowFindText({
    kind: "reasoning",
    key: "r:1",
    item: { kind: "assistant", id: "r1", text: "", reasoning: "thinking hard", streaming: false },
    segmentKey: "s1",
    layoutVariant: "reasoning-summary",
  } as TranscriptRow),
  "thinking hard",
  "reasoning row extracts reasoning, not answer text",
);

// ── index build + search ───────────────────────────────────────────────
const blocks: TimelineBlock[] = [
  blockOf("b1", [userRow("How do I bind Ctrl+F in Wails?", "u:1"), answerRow("Use a global shortcut handler.", "a:1")]),
  blockOf("b2", [toolRow("read", "the shortcut lives in keyboardShortcuts.ts", "t:1")]),
  blockOf("b3", [closedProcessHeader("folded grep output with KEYWORD inside")]),
];

const index = buildTranscriptFindIndex(blocks);
eq(index.length, 4, "index covers all searchable rows across blocks");
eq(index[0].blockKey, "b1", "index entries carry their block key");
eq(index.every((entry) => entry.text === entry.text.toLowerCase()), true, "index text is lowercased once");

const ctrlHits = searchTranscriptFind(index, "ctrl+f");
eq(ctrlHits.hits.length, 1, "literal substring matches (special chars are not regex)");
eq(ctrlHits.hits[0].rowKey, "u:1", "match lands on the right row");
eq(ctrlHits.hits[0].blockKey, "b1", "match carries the block key for jumping");
eq(ctrlHits.capped, false, "small result sets are not capped");

const caseHits = searchTranscriptFind(index, "GLOBAL SHORTCUT");
eq(caseHits.hits.length, 1, "search is case-insensitive");

const foldedHits = searchTranscriptFind(index, "keyword");
eq(foldedHits.hits.length, 1, "folded content is reachable through the header row");
eq(foldedHits.hits[0].rowKey, "ph:s1", "folded match lands on the header row");

eq(searchTranscriptFind(index, "   ").hits.length, 0, "whitespace-only query returns no hits");
eq(searchTranscriptFind(index, "no-such-token-anywhere").hits.length, 0, "miss returns empty hits");
eq(searchTranscriptFind(EMPTY_FIND_INDEX, "anything").hits.length, 0, "empty index is safe");
eq(searchTranscriptFind(index, "ctrl+f").hits[0].occurrences, 1, "occurrences counted per row");

// Multi-occurrence counting inside one row.
const dupIndex = buildTranscriptFindIndex([blockOf("b1", [answerRow("dup dup dup", "a:9")])]);
const multi = searchTranscriptFind(dupIndex, "dup");
eq(multi.hits.length, 1, "multi-occurrence row is one hit");
eq(multi.hits[0].occurrences, 3, "occurrences counted within a row");

// ── hit cap ────────────────────────────────────────────────────────────
const manyBlocks: TimelineBlock[] = [];
for (let i = 0; i < TRANSCRIPT_FIND_HIT_CAP + 25; i += 1) {
  manyBlocks.push(blockOf(`bx${i}`, [answerRow(`needle ${i}`, `a:x${i}`)]));
}
const capped = searchTranscriptFind(buildTranscriptFindIndex(manyBlocks), "needle");
eq(capped.hits.length, TRANSCRIPT_FIND_HIT_CAP, "hits stop at the cap for 万行-scale queries");
eq(capped.capped, true, "cap sets the truncated flag when more rows matched");

const exactCap = searchTranscriptFind(buildTranscriptFindIndex(manyBlocks.slice(0, TRANSCRIPT_FIND_HIT_CAP)), "needle");
eq(exactCap.capped, false, "exactly-cap results are not marked truncated");

// ── prev/next stepping ────────────────────────────────────────────────
eq(stepFindHit(0, 3, 1), 1, "next advances");
eq(stepFindHit(2, 3, 1), 0, "next wraps to the first hit");
eq(stepFindHit(0, 3, -1), 2, "prev wraps to the last hit");
eq(stepFindHit(1, 3, -1), 0, "prev retreats");
eq(stepFindHit(-1, 3, 1), 0, "no active hit → next starts at the first");
eq(stepFindHit(-1, 3, -1), 2, "no active hit → prev starts at the last");
eq(stepFindHit(0, 0, 1), -1, "empty hit list steps to -1 (no-op for callers)");
eq(stepFindHit(99, 3, 1), 0, "out-of-range cursor recovers to the first hit");

// ── attribute selector escaping ────────────────────────────────────────
eq(findAttributeSelector("data-row-key", "u:1"), '[data-row-key="u:1"]', "plain key builds a quoted selector");
eq(findAttributeSelector("data-row-key", 'a"b\\c'), '[data-row-key="a\\"b\\\\c"]', "quotes and backslashes are escaped");

// ── Ctrl+F domain isolation ────────────────────────────────────────────
const dom = new JSDOM("<!doctype html><html><body><div class='code-block__wrap'><textarea id='code'></textarea></div><input id='plain'/></body></html>");
const codeTarget = dom.window.document.getElementById("code");
const plainTarget = dom.window.document.getElementById("plain");
eq(shouldIgnoreFindShortcutTarget(codeTarget), true, "Ctrl+F inside a code block yields to the editor search");
eq(shouldIgnoreFindShortcutTarget(plainTarget), false, "Ctrl+F elsewhere opens the transcript find");
eq(shouldIgnoreFindShortcutTarget(null), false, "null target is safe");
eq(shouldIgnoreFindShortcutTarget(dom.window), false, "window target is safe");

// ── Ctrl+F shortcut registration ───────────────────────────────────────
const findDef = SHORTCUT_DEFINITIONS.find((definition) => definition.action === "transcript.find");
eq(findDef !== undefined, true, "transcript.find is registered in SHORTCUT_DEFINITIONS");
eq(findDef?.preventDefault, true, "Ctrl+F suppresses the webview's native find");
eq(findDef?.allowInEditable, true, "Ctrl+F works while the composer has focus");
eq(defaultShortcutCombo("transcript.find", "windows").key.toLowerCase(), "f", "Windows binding is Ctrl+F");
eq(matchesShortcut({ key: "F", ctrlKey: true, target: null }, "transcript.find", "windows"), true, "Ctrl+Shift+F still matches (key value case)");
eq(matchesShortcut({ key: "f", metaKey: true, target: null }, "transcript.find", "darwin"), true, "macOS binding is Cmd+F");
const conflict = shortcutConflict("transcript.find", defaultShortcutCombo("transcript.find", "windows"), "windows");
eq(conflict ?? null, null, "no other shortcut claims Ctrl+F on Windows");

console.log(`\n${passed}/${passed + failed} passed`);
if (failed > 0) process.exit(1);
