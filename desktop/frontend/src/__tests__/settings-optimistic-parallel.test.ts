// Run: tsx src/__tests__/settings-optimistic-parallel.test.ts
//
// Task 280 — "parallel-write safety check" re-homed into the lab as
// "optimistic parallel writes": the permissions-area entry is gone, the lab
// entry reads `sandbox.optimistic_write` upright (On = optimistic),
// the underlying key never changed (old configs load as-is and roll back
// losslessly), and all three locales ship the new pair with the old one gone.
// Task 650 — the detail checkbox became a two-segment capsule control
// (Off | On) in the permission-tier switch style; binding stays upright.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(root, "..", "..", "..");
const panelRaw = readFileSync(join(root, "components/SettingsPanel.tsx"), "utf8");
const panel = panelRaw.replace(/\n\s*/g, " ");
const en = readFileSync(join(root, "locales/en.ts"), "utf8");
const zh = readFileSync(join(root, "locales/zh.ts"), "utf8");
const zhTW = readFileSync(join(root, "locales/zh-TW.ts"), "utf8");

// ── old entry removed, new entry present (no double入口) ────────────────────
ok(!panelRaw.includes("settings.optimisticWrite"), "the permissions-area entry no longer references the old label key");
ok(panel.includes('{ id: "optimisticParallel", group: "automation",'),
  "the lab entry lives in the efficiency group (render table)");
ok(panel.includes('| "optimisticParallel"'), "the detail union includes the id");

// ── segmented-control binding: reads the field upright (On=乐观, task 650) ──
ok(panel.includes('className={`set-seg__btn${Boolean(s.sandbox?.optimisticWrite) === on ? " set-seg__btn--on" : ""}`}'),
  "the detail capsule control binds the field upright (On segment = optimistic on)");
ok(panel.includes("onClick={() => void apply(() => app.SetOptimisticWrite(on))}"),
  "the setter receives the segment value directly (no double negation)");
ok(panel.includes('{t(on ? "settings.optimisticParallel.on" : "settings.optimisticParallel.off")}'),
  "the segments are labelled via the optimisticParallel.on/off keys");
ok(!panelRaw.includes("checked={Boolean(s.sandbox?.optimisticWrite)}"),
  "the old checkbox binding is gone");
ok(!panelRaw.includes('onChange={(e) => void apply(() => app.SetOptimisticWrite(e.target.checked))}'),
  "the old checkbox onChange is gone");
ok(panel.includes("on: Boolean(s.sandbox?.optimisticWrite)"),
  "the lab list entry's on-state mirrors the field (default false = off = safety check on)");

// ── the underlying key is untouched (lossless rollback) ─────────────────────
const configGo = readFileSync(join(repoRoot, "internal", "config", "config.go"), "utf8");
const renderGo = readFileSync(join(repoRoot, "internal", "config", "render.go"), "utf8");
const shellSupport = readFileSync(join(repoRoot, "desktop", "shell_support.go"), "utf8");
const bootGo = readFileSync(join(repoRoot, "internal", "boot", "boot.go"), "utf8");
ok(configGo.includes('OptimisticWrite bool `toml:"optimistic_write"`'),
  "the toml key stays optimistic_write (old configs load as-is)");
ok(renderGo.includes('optimistic_write = %v') && renderGo.includes("optimistic_write = false"),
  "the render table still writes optimistic_write explicitly (81/123 lesson)");
ok(shellSupport.includes("OptimisticWrite:       cfg.Sandbox.OptimisticWrite") || shellSupport.includes("OptimisticWrite: cfg.Sandbox.OptimisticWrite") || /OptimisticWrite:\s+cfg\.Sandbox\.OptimisticWrite/.test(shellSupport),
  "the settings readback still feeds SandboxView");
ok(bootGo.includes("subagentScheduler.SetOptimistic(cfg.Sandbox.OptimisticWrite)"),
  "the 315 subagent gate still reads the same field (boot snapshot; restart to apply)");

// ── locales: new pair present in all three, old pair gone ───────────────────
for (const [name, table] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  ok(table.includes('"settings.optimisticParallel"'), `${name} ships settings.optimisticParallel`);
  ok(table.includes('"settings.optimisticParallelHint"'), `${name} ships settings.optimisticParallelHint`);
  ok(table.includes('"settings.optimisticParallel.on"'), `${name} ships settings.optimisticParallel.on`);
  ok(table.includes('"settings.optimisticParallel.off"'), `${name} ships settings.optimisticParallel.off`);
  ok(!table.includes('"settings.optimisticWrite"'), `${name} dropped the old settings.optimisticWrite key`);
  ok(!table.includes('"settings.optimisticWriteHint"'), `${name} dropped the old hint key`);
}

assert.ok(passed >= 31, `expected at least 31 checks, got ${passed}`);
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
// ── Task 374fix: read-back chain pinned at the Go source ──────────────────
{
  ok(
    /OptimisticWrite\s+bool\s+`json:"optimisticWrite"`/.test(shellSupport),
    "SandboxView carries the optimisticWrite JSON field",
  );
  // (the assignment itself is already pinned above by the 280-era assertion,
  // which covers all three spacing shapes of the struct literal)
}

if (failed > 0) process.exit(1);
