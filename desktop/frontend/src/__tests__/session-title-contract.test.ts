// Run: tsx src/__tests__/session-title-contract.test.ts

import {
  historyIndexedTitle,
  historySearchHitDisplayTitle,
  historySessionDisplayTitle,
  paletteSessionDisplayTitle,
  paletteSessionHint,
  paletteSessionKeywords,
} from "../lib/session";
import type { SessionMeta } from "../lib/types";

let passed = 0;
let failed = 0;

function eq<T>(actual: T, expected: T, label: string) {
  if (Object.is(actual, expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n    expected: ${String(expected)}\n    actual:   ${String(actual)}\n`);
    failed += 1;
  }
}

function deepEq<T>(actual: T, expected: T, label: string) {
  const actualJson = JSON.stringify(actual);
  const expectedJson = JSON.stringify(expected);
  if (actualJson === expectedJson) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n    expected: ${expectedJson}\n    actual:   ${actualJson}\n`);
    failed += 1;
  }
}

function session(overrides: Partial<SessionMeta> = {}): SessionMeta {
  return {
    path: "/sessions/topic-backed.jsonl",
    preview: "first saved prompt preview",
    title: "Renamed saved session",
    turns: 3,
    createdAt: 100,
    lastActivityAt: 200,
    modTime: 200,
    current: false,
    open: false,
    workspaceRoot: "/work/project-alpha",
    topicId: "topic-1",
    topicTitle: "Shared topic",
    ...overrides,
  };
}

console.log("\nsession title contracts");

// tl1547 (C1): both FE chains flipped to the Go sidebar order
// (title > topicTitle > preview > fallback). Sessions carrying BOTH a custom
// title and a topic title now show the same name in the sidebar, history
// panel and command palette — the sidebar is the Go-side authority.
{
  const hit = {
    sessionPath: "/sessions/version.jsonl",
    sessionTitle: "Private version note",
    topicTitle: "Shared topic",
  };
  eq(
    historySearchHitDisplayTitle(hit, "(empty session)"),
    "Shared topic",
    "History content search keeps physical-version notes out of the logical title",
  );
  eq(
    historySearchHitDisplayTitle({ ...hit, topicTitle: "" }),
    "Private version note",
    "Legacy search hits without a topic title retain the session-title fallback",
  );
}

// tl1547 (C5): the sessionPath tier is gone — a hit with neither title nor
// topic title renders the caller's localized safe text, never the internal
// transcript path.
{
  const bare = { sessionPath: "/home/user/.reasonix/sessions/store/t-9f2a.jsonl", sessionTitle: "", topicTitle: "" };
  eq(
    historySearchHitDisplayTitle(bare, "(empty session)"),
    "(empty session)",
    "Search hits with no title tier never leak the internal session path",
  );
  eq(
    historySearchHitDisplayTitle({ ...bare, sessionTitle: "  ", topicTitle: undefined as unknown as string }, "Untitled"),
    "Untitled",
    "Whitespace-only titles fall through to the safe fallback too",
  );
}

{
  const item = session();
  eq(
    paletteSessionDisplayTitle(item, "Untitled"),
    "Renamed saved session",
    "Cmd+K displays the custom title first (Go sidebar chain order, tl1547 C1)",
  );
  deepEq(
    paletteSessionKeywords(item),
    ["first saved prompt preview"],
    "Cmd+K keeps per-version notes out of the logical session search contract",
  );
  eq(
    paletteSessionHint(item),
    "first saved prompt preview · /work/project-alpha",
    "Cmd+K uses the content preview rather than a physical-version note",
  );
}

{
  const item = session();
  eq(
    historySessionDisplayTitle(item, "Untitled"),
    "Renamed saved session",
    "History displays the custom title first — same chain as the sidebar (tl1547 C1)",
  );
}

{
  const item = session({ title: "  " });
  eq(
    historySessionDisplayTitle(item, "Untitled"),
    "Shared topic",
    "Unrenamed topic-backed history sessions fall back to topic title",
  );
  deepEq(
    paletteSessionKeywords(item),
    ["first saved prompt preview"],
    "Blank session titles are not added as Cmd+K keywords",
  );
  eq(
    paletteSessionHint(item),
    "first saved prompt preview · /work/project-alpha",
    "Cmd+K falls back to preview text in the hint when there is no custom session title",
  );
}

{
  const item = session({ title: "Shared topic", preview: "first saved prompt preview" });
  eq(
    paletteSessionHint(item),
    "first saved prompt preview · /work/project-alpha",
    "Cmd+K skips duplicate title hints that repeat the visible topic title",
  );
}

// tl1547 (C4): the indexing placeholder is the localized status text plus the
// backend stamp (the English literal left the payload).
eq(
  historyIndexedTitle("20261031 22:04", "正在整理历史"),
  "正在整理历史 — 20261031 22:04",
  "Indexed placeholder renders the localized status with the stamp",
);
eq(
  historyIndexedTitle("", "正在整理历史"),
  "正在整理历史",
  "Indexed placeholder without a stamp is the localized status alone",
);
eq(
  historyIndexedTitle("   ", "Indexing history"),
  "Indexing history",
  "Whitespace-only stamp does not add a dangling separator",
);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
