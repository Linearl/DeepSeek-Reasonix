// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/rewind-preflight-classification.test.ts
// tl1476: the preflight classifier must label every unsafe form
// (outside workspace / missing payload / permission denied / other conflict
// reasons) and keep safe files safe, both for backend-computed fileSafety and
// for the conflicts-string fallback of bridges that predate the field.

import assert from "node:assert/strict";
import { classifyRewindFiles, mapUnsafeReasonKey } from "../lib/rewindSafety";
import type { RewindPlanView } from "../lib/rewindTypes";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

// ── reason key mapping ────────────────────────────────────────────────────────
ok(mapUnsafeReasonKey("path_unsafe") === "outside_workspace", "path_unsafe maps to outside_workspace");
ok(mapUnsafeReasonKey("missing_payload") === "missing_payload", "missing_payload maps to missing_payload");
ok(mapUnsafeReasonKey("permission_denied") === "permission_denied", "permission_denied maps to permission_denied");
ok(mapUnsafeReasonKey("manual_edit") === "changed", "unknown conflict reason falls back to changed");
ok(mapUnsafeReasonKey(undefined) === "changed", "absent reason falls back to changed");

// ── backend-computed fileSafety: every unsafe form ───────────────────────────
const safetyPlan: RewindPlanView = {
  ok: true,
  files: ["a.txt", "b-outside.txt", "c-deleted.txt", "d-denied.txt", "e-changed.txt"],
  fileSafety: [
    { path: "a.txt", safe: true },
    { path: "b-outside.txt", safe: false, reason: "path_unsafe" },
    { path: "c-deleted.txt", safe: false, reason: "missing_payload" },
    { path: "d-denied.txt", safe: false, reason: "permission_denied" },
    { path: "e-changed.txt", safe: false, reason: "manual_edit" },
  ],
};
const safetyFiles = classifyRewindFiles(safetyPlan);
ok(safetyFiles.length === 5, "fileSafety plan classifies every listed file");
ok(safetyFiles[0].safe && safetyFiles[0].reasonKey === null, "safe entry stays safe with no reason key");
ok(!safetyFiles[1].safe && safetyFiles[1].reasonKey === "outside_workspace", "outside-workspace entry labeled");
ok(!safetyFiles[2].safe && safetyFiles[2].reasonKey === "missing_payload", "deleted-payload entry labeled");
ok(!safetyFiles[3].safe && safetyFiles[3].reasonKey === "permission_denied", "permission-denied entry labeled");
ok(!safetyFiles[4].safe && safetyFiles[4].reasonKey === "changed", "other-conflict entry falls back to changed");
ok(safetyFiles.some((f) => !f.safe), "plan with unsafe entries reports at least one unsafe file");

const allSafePlan: RewindPlanView = {
  ok: true,
  files: ["a.txt", "b.txt"],
  fileSafety: [{ path: "a.txt", safe: true }, { path: "b.txt", safe: true }],
};
const allSafe = classifyRewindFiles(allSafePlan);
ok(allSafe.length === 2 && allSafe.every((f) => f.safe), "all-safe plan yields one enabled-confirm screen");

// safe flag absent (legacy producer) counts as unsafe, never silently safe
const missingFlag = classifyRewindFiles({ ok: true, fileSafety: [{ path: "x.txt", safe: false, reason: "path_unsafe" }] });
ok(missingFlag.length === 1 && !missingFlag[0].safe, "unsafe entry without reason still counts unsafe");

// ── fallback: conflicts strings (bridges without fileSafety) ─────────────────
const fallbackPlan: RewindPlanView = {
  ok: true,
  canFiles: false,
  files: ["safe.txt", "blocked.txt"],
  conflicts: ["blocked.txt: path_unsafe", "active_writer"],
};
const fallbackFiles = classifyRewindFiles(fallbackPlan);
ok(fallbackFiles.length === 2, "fallback keeps the full file list");
ok(fallbackFiles.find((f) => f.path === "safe.txt")?.safe === true, "fallback leaves unconflicted files safe");
ok(fallbackFiles.find((f) => f.path === "blocked.txt")?.reasonKey === "outside_workspace", "fallback maps conflict reason");

// conflicted path missing from plan.files still surfaces
const orphanPlan: RewindPlanView = { ok: true, conflicts: ["orphan.txt: missing_payload"] };
const orphan = classifyRewindFiles(orphanPlan);
ok(orphan.length === 1 && orphan[0].path === "orphan.txt" && !orphan[0].safe, "orphan conflicted path still surfaces unsafe");

// plan-level conflicts without a path prefix produce no classification
ok(classifyRewindFiles({ ok: true, conflicts: ["active_writer", "expired_payload"] }).length === 0, "plan-level conflicts never fabricate file entries");
ok(classifyRewindFiles({ ok: true }).length === 0, "no files and no conflicts means no file scenario");
ok(classifyRewindFiles({ ok: true, files: ["a.txt"] }).length === 0, "files without conflicts/fileSafety means no unsafe entry");

// windows-style absolute path must not be split by the ": " separator
const winPlan: RewindPlanView = { ok: true, conflicts: ["C:\\repo\\x.txt: permission_denied"] };
const winFiles = classifyRewindFiles(winPlan);
ok(winFiles.length === 1 && winFiles[0].path === "C:\\repo\\x.txt" && winFiles[0].reasonKey === "permission_denied", "windows absolute path conflict parses correctly");

process.stdout.write(`rewind preflight classification: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
