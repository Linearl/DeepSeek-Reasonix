// Run: tsx src/__tests__/settings-preapprove-managed.test.ts
//
// Task 231 — managed-path pre-approval settings surface (source guards):
// the render-table entry exists (81/123 lost-save lesson), the detail card
// carries the master switch + four independent checkboxes + the visible risk
// line, the bridge setter takes all five values in one write, and all three
// locales ship the same key set.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

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
const panel = readFileSync(join(root, "components/SettingsPanel.tsx"), "utf8").replace(/\n\s*/g, " ");
const bridge = readFileSync(join(root, "lib/bridge.ts"), "utf8");
const en = readFileSync(join(root, "locales/en.ts"), "utf8");
const zh = readFileSync(join(root, "locales/zh.ts"), "utf8");
const zhTW = readFileSync(join(root, "locales/zh-TW.ts"), "utf8");

// ── render table: the entry light must exist or the save silently drops ─────
ok(panel.includes('{ id: "preapproveManagedPaths", group: "misc",'),
  "render table carries the preapproveManagedPaths entry (settings list = render table)");
ok(panel.includes('| "preapproveManagedPaths"'), "the detail-card union includes the id");

// ── detail card: master switch + four independent checkboxes + risk line ────
ok(panel.includes('app.SetPreapproveManagedPaths('), "the card writes through the five-value setter");
const checkboxFields = ["preapproveSkills", "preapproveHooks", "preapproveSessionStores", "preapproveBashEscape"];
for (const field of checkboxFields) {
  ok(panel.includes(`checked: Boolean(s.${field})`), `${field} renders as its own checked box`);
}
ok(panel.includes('type="checkbox"'), "the categories are real checkboxes (independent ticks)");
ok(panel.includes("set-preapprove__warning") && panel.includes("settings.preapproveManagedPaths.warning"),
  "the prompt-injection risk line is rendered visibly");
ok(/disabled=\{busy\}[^}]*onChange/.test(panel) || panel.includes("disabled={busy}"),
  "checkbox edits respect the busy gate");

// ── bridge: one five-value write (no half-applied saves) ────────────────────
ok(bridge.includes("SetPreapproveManagedPaths(enabled: boolean, skills: boolean, hooks: boolean, sessionStores: boolean, bashEscape: boolean): Promise<void>;"),
  "bridge interface exposes the five-value setter");

// ── three locales ship the same nine keys ───────────────────────────────────
const keys = [
  "settings.preapproveManagedPaths",
  "settings.preapproveManagedPathsHint",
  "settings.preapproveManagedPaths.on",
  "settings.preapproveManagedPaths.off",
  "settings.preapproveManagedPaths.skills",
  "settings.preapproveManagedPaths.hooks",
  "settings.preapproveManagedPaths.sessionStores",
  "settings.preapproveManagedPaths.bashEscape",
  "settings.preapproveManagedPaths.warning",
];
for (const key of keys) {
  ok(en.includes(`"${key}"`), `en ships ${key}`);
  ok(zh.includes(`"${key}"`), `zh ships ${key}`);
  ok(zhTW.includes(`"${key}"`), `zh-TW ships ${key}`);
}
ok(zh.includes("prompt injection") && zhTW.includes("prompt injection") && en.includes("prompt injection"),
  "the risk line names prompt injection in all three locales");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
