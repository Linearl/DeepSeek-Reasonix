import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

let generation = 0;
// tl1315③: a failed rebuild fences status writes only until the backend
// reports a settled state (ready/degraded) — that settled read is the recovery
// signal itself, so it must take over and clear the frozen banner. Transitional
// states (opening/rebuilding/closed) stay fenced exactly as before. The old
// rule (ignore every status until the next successful rebuild) froze
// "正在整理历史 — 失败" for the life of the window even after the backend had
// recovered on its own.
const settles = (state: string) => state === "ready" || state === "degraded";
const statusWriteIsAllowed = (candidateGeneration: number, rebuildFailed: boolean, state: string) =>
  candidateGeneration === generation && (!rebuildFailed || settles(state));
const renderedStates: string[] = [];
let resolveRemoteStatus: ((state: string) => void) | undefined;
const remoteStatus = new Promise<string>((resolve) => {
  resolveRemoteStatus = resolve;
});
const remoteGeneration = generation;
const lateRemoteWrite = remoteStatus.then((state) => {
  if (statusWriteIsAllowed(remoteGeneration, false, state)) renderedStates.push(state);
});

// The Wails rebuild rejects after the finished event has already started its
// status read. Restoring the retryable failure must fence out that late read.
generation += 1;
renderedStates.push("degraded");
resolveRemoteStatus?.("opening");
await lateRemoteWrite;
assert.deepEqual(
  renderedStates,
  ["degraded"],
  "a late pre-failure status response cannot replace the restored retry state",
);

// During watcher startup a failed rebuild keeps fencing same-generation
// transitional reads: they describe a backend still cycling, not a recovery.
const postFailureGeneration = generation;
if (statusWriteIsAllowed(postFailureGeneration, true, "opening")) renderedStates.push("opening");
assert.deepEqual(
  renderedStates,
  ["degraded"],
  "transitional status reads started after a failed rebuild cannot replace its retry state during watcher startup",
);

// tl1315③: the first settled read after a failure takes over — it clears the
// failure memory and lets the banner recover instead of freezing it.
if (statusWriteIsAllowed(postFailureGeneration, true, "ready")) renderedStates.push("ready");
assert.deepEqual(
  renderedStates,
  ["degraded", "ready"],
  "a settled status read after a failed rebuild takes over and clears the frozen banner",
);

// While no rebuild failure is held, an authoritative same-generation read
// always applies, whatever state it carries.
assert.equal(
  statusWriteIsAllowed(postFailureGeneration, false, "opening"),
  true,
  "an authoritative read while no rebuild failure is held always applies",
);

const projectTreeSource = readFileSync(new URL("../components/ProjectTree.tsx", import.meta.url), "utf8");
assert.match(
  projectTreeSource,
  /catch \(error\) \{\s*catalogRebuildFailedRef\.current = true; catalogStatusGenerationRef\.current \+= 1; setCatalogStatus\(catalogStatus\)/,
  "ProjectTree invalidates older status reads before restoring a failed rebuild",
);
assert.match(
  projectTreeSource,
  /GetSessionCatalogStatus\(\)[\s\S]*catalogStatusGeneration !== catalogStatusGenerationRef\.current[\s\S]*catalogRebuildFailedRef\.current && !catalogStatusSettlesRebuildFailure\(status\)/,
  "ProjectTree fences every event-driven status write behind rebuild ownership (a settled read takes over)",
);
assert.match(
  projectTreeSource,
  /GetProjectTreeSnapshot\(\)[\s\S]*catalogStatusGeneration === catalogStatusGenerationRef\.current[\s\S]*\(!catalogRebuildFailedRef\.current \|\| catalogStatusSettlesRebuildFailure\(snapshot\.catalog\)\)/,
  "ProjectTree fences snapshot status writes behind the same rebuild ownership",
);

console.log("session catalog status ownership tests passed");
