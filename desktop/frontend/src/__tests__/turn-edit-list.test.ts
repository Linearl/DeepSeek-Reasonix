// Unit tests for task 113 turn edit list and task 114 session side files.

import assert from "node:assert/strict";
import { buildTurnEditList, turnEditListHeader } from "../lib/turnEditList";
import { collectSessionSideFiles, formatReferenceListForPrompt } from "../lib/sessionSideFiles";
import type { Translator } from "../lib/i18n";

const t: Translator = ((key: string, vars?: Record<string, string | number>) => {
  const map: Record<string, string> = {
    "completion.filesChanged": "Edited {count} files",
    "completion.filesCounted": "{count} files counted",
    "completion.partialStats": "partial",
  };
  let out = map[key] ?? key;
  if (vars) for (const [k, v] of Object.entries(vars)) out = out.replace(`{${k}}`, String(v));
  return out;
}) as Translator;

// --- 113 ---
{
  const model = buildTurnEditList({
    id: "r1",
    turn: 2,
    coverage: "complete",
    added: 10,
    removed: 3,
    reasons: [],
    files: [
      { path: "a.go", kind: "modify", added: 4, removed: 1 },
      { path: "b.go", kind: "create", added: 6, removed: 0 },
      { path: "c.go", kind: "modify", added: 0, removed: 2 },
      { path: "d.go", kind: "modify", added: 1, removed: 0 },
      { path: "e.go", kind: "modify", added: 1, removed: 0 },
      { path: "f.go", kind: "modify", added: 1, removed: 0 },
      { path: "g.go", kind: "delete", added: 0, removed: 5 },
    ],
  }, 5);
  assert.ok(model, "model builds");
  const m = model!;
  assert.equal(m.totalFiles, 7);
  assert.equal(m.visible.length, 5, "initial window shows 5");
  assert.equal(m.hiddenCount, 2, "2 files hidden");
  assert.match(turnEditListHeader(m, t), /Edited 7 files/);

  const expanded = buildTurnEditList({
    id: "r1", turn: 2, coverage: "complete", added: 10, removed: 3, reasons: [],
    files: Array.from({ length: 7 }, (_, i) => ({ path: `f${i}`, kind: "modify" as const, added: 1, removed: 0 })),
  }, 0);
  assert.equal(expanded!.visible.length, 7, "expanded window shows all");

  assert.equal(buildTurnEditList(undefined), undefined, "empty diff has no list");
  assert.equal(
    buildTurnEditList({ id: "r", turn: 0, coverage: "unknown", added: 0, removed: 0, reasons: [], files: [] }),
    undefined,
    "unknown coverage has no list",
  );
  console.log("  PASS  turn edit list collapse + header");
}

// --- 114 ---
{
  const { artifacts, references } = collectSessionSideFiles([
    { kind: "user", },
    { kind: "tool", name: "read_file", args: JSON.stringify({ path: "src/a.ts" }) },
    { kind: "tool", name: "read_file", args: JSON.stringify({ path: "src/a.ts" }) },
    { kind: "tool", name: "write_file", args: JSON.stringify({ path: "out/new.md" }) },
    { kind: "tool", name: "edit_file", args: JSON.stringify({ path: "src/a.ts" }) },
    { kind: "tool", name: "bash", args: JSON.stringify({ command: "ls" }) },
  ] as never);
  assert.deepEqual(artifacts.map((f) => f.path), ["out/new.md"], "only creates are artifacts");
  assert.deepEqual(references.map((f) => f.path), ["src/a.ts"], "reads and modifies are references, deduped");

  const prompt = formatReferenceListForPrompt(references);
  assert.match(prompt, /src\/a\.ts/, "prompt lists reference paths");
  assert.equal(formatReferenceListForPrompt([]), "", "empty refs produce no prompt");
  console.log("  PASS  session side files artifacts vs references");
}
