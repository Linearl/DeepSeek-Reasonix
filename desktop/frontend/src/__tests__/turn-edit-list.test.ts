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

// --- 114 (revised by 629: modified files are artifacts too) ---
{
  const { artifacts, references } = collectSessionSideFiles([
    { kind: "user", },
    { kind: "tool", name: "read_file", args: JSON.stringify({ path: "src/a.ts" }) },
    { kind: "tool", name: "read_file", args: JSON.stringify({ path: "src/a.ts" }) },
    { kind: "tool", name: "write_file", args: JSON.stringify({ path: "out/new.md" }) },
    { kind: "tool", name: "edit_file", args: JSON.stringify({ path: "src/a.ts" }) },
    { kind: "tool", name: "multi_edit", args: JSON.stringify({ path: "src/b.ts", edits: [] }) },
    { kind: "tool", name: "move_file", args: JSON.stringify({ source_path: "old.txt", destination_path: "new.txt" }) },
    { kind: "tool", name: "bash", args: JSON.stringify({ command: "ls" }) },
  ] as never);
  // Creates and modifies are artifacts, deduped by path (src/a.ts was both
  // read and edited — one entry, first writer wins as `via`).
  assert.deepEqual(
    artifacts.map((f) => f.path),
    ["out/new.md", "src/a.ts", "src/b.ts", "new.txt"],
    "creates and modifies are artifacts, deduped",
  );
  assert.deepEqual(
    artifacts.map((f) => f.via),
    ["write_file", "edit_file", "multi_edit", "move_file"],
    "via keeps the first writer per path",
  );
  assert.deepEqual(
    references.map((f) => f.path),
    ["src/a.ts", "old.txt"],
    "reads stay references; move_file's renamed-away source stays a reference",
  );

  const prompt = formatReferenceListForPrompt(references);
  assert.match(prompt, /src\/a\.ts/, "prompt lists reference paths");
  assert.equal(formatReferenceListForPrompt([]), "", "empty refs produce no prompt");
  console.log("  PASS  session side files artifacts vs references");
}

// --- 629: fileDiff branch — a previewed diff proves a write even for tools
// outside the whitelists; the path comes from the unified-diff header. ---
{
  const { artifacts, references } = collectSessionSideFiles([
    {
      kind: "tool",
      name: "future_writer",
      args: "",
      fileDiff: { diff: "--- a/gen/report.md\n+++ b/gen/report.md\n@@ -0,0 +1 @@\n+hi\n", added: 1, removed: 0 },
    },
    // A diff whose render was omitted (too large) carries no header — the
    // path is unknown, so it stays out rather than being guessed.
    {
      kind: "tool",
      name: "future_writer_big",
      args: "",
      fileDiff: { diff: "(diff omitted: change too large to render — +9000 / -1 lines)", added: 9000, removed: 1 },
    },
    // bash never carries a fileDiff today, and stays out even if one appears.
    { kind: "tool", name: "bash", args: "", fileDiff: { diff: "--- a/x\n+++ b/x\n", added: 1, removed: 0 } },
    // Reads never produce a fileDiff (read-only tools skip the preview), and
    // the branch must not pull them anywhere.
    { kind: "tool", name: "read_file", args: JSON.stringify({ path: "src/in.ts" }) },
  ] as never);
  assert.deepEqual(
    artifacts.map((f) => f.path),
    ["gen/report.md"],
    "fileDiff header path lands in artifacts; omitted-header and bash stay out",
  );
  assert.deepEqual(references.map((f) => f.path), ["src/in.ts"], "fileDiff branch never touches references");
  console.log("  PASS  session side files fileDiff branch (629)");
}

// --- 629: a hydrated write_file whose args are archived is rescued by its
// persisted fileDiff header when the subject is missing too. ---
{
  const { artifacts } = collectSessionSideFiles([
    { kind: "tool", name: "write_file", args: "", fileDiff: { diff: "--- a/rescued.md\n+++ b/rescued.md\n@@\n+x\n", added: 1, removed: 0 } },
  ] as never);
  assert.deepEqual(artifacts.map((f) => f.path), ["rescued.md"], "fileDiff rescues a hydrated write without args/subject");
  console.log("  PASS  session side files fileDiff hydration rescue (629)");
}

// --- 659: bash write collection (C phase of 629) — explicit, literal output
// targets from the command text land in artifacts; everything the shell would
// resolve at run time stays out. ---
{
  const bash = (command: string) =>
    collectSessionSideFiles([{ kind: "tool", name: "bash", args: JSON.stringify({ command }) }] as never)
      .artifacts.map((f) => f.path);

  assert.deepEqual(bash("echo hi > out/report.txt"), ["out/report.txt"], "simple redirect lands");
  assert.deepEqual(bash("echo hi >> out/log.txt"), ["out/log.txt"], "append redirect lands");
  assert.deepEqual(bash("printf '%s' x >f"), ["f"], "operator glued to the target still lands");
  assert.deepEqual(bash("make > build.log 2>&1"), ["build.log"], "file redirect lands, fd dup stays out");
  assert.deepEqual(bash("cmd 2> err.log"), ["err.log"], "stderr file redirect lands");
  assert.deepEqual(bash("go test ./... | tee test.out | tail -3"), ["test.out"], "tee in a pipeline lands");
  assert.deepEqual(bash("tee -a a.log b.log < in"), ["a.log", "b.log"], "tee options skipped, both files land");
  assert.deepEqual(bash("cat > doc.md <<EOF\nbody with > fake.txt\nEOF"), ["doc.md"], "heredoc body dropped (live multi-line)");
  assert.deepEqual(bash("cat > doc.md <<EOF body with > fake.txt EOF"), ["doc.md"], "heredoc body dropped (flattened hydrated subject)");
  assert.deepEqual(bash('echo x > "my report.md"'), ["my report.md"], "quoted target keeps its space");
  assert.deepEqual(bash("grep x $input > \"$out\""), [], "variable targets stay out");
  assert.deepEqual(bash("echo x > /dev/null 2>&1"), [], "devices and fd dups stay out");
  assert.deepEqual(bash("cmd > nul"), [], "windows NUL device stays out");
  assert.deepEqual(bash("sort < in.txt"), [], "input redirect never becomes an artifact");
  assert.deepEqual(bash("pnpm build"), [], "command with no explicit target yields nothing");
  assert.deepEqual(bash("echo $(cmd > inner.log) > outer.txt"), ["inner.log", "outer.txt"], "substitution writes are real writes; punctuation never leaks into paths");
  assert.deepEqual(bash("cp a b && rm c"), [], "cp/mv/rm writes are out of scope (no redirect, no guess)");

  // Hydrated: no args, the persisted subject carries the command text.
  const hydrated = collectSessionSideFiles([
    { kind: "tool", name: "bash", args: "", subject: "go test ./... | tee test.out | tail -3" },
  ] as never);
  assert.deepEqual(hydrated.artifacts.map((f) => f.path), ["test.out"], "hydrated bash subject drives extraction");

  // Dedup across tools: first writer wins, a later bash write does not reshuffle.
  const dedup = collectSessionSideFiles([
    { kind: "tool", name: "write_file", args: JSON.stringify({ path: "same.txt" }) },
    { kind: "tool", name: "bash", args: JSON.stringify({ command: "echo x >> same.txt" }) },
  ] as never);
  assert.deepEqual(dedup.artifacts.map((f) => f.via), ["write_file"], "first writer keeps via");

  console.log("  PASS  session side files bash write targets (659)");
}
