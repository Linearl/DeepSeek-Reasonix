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

// ── render table: since task 364 the block lives INSIDE the autopilot card
//    as a named sub-block with no rail entry of its own (single entry).
ok(!panel.includes('{ id: "preapproveManagedPaths", group:'),
  "render table has no preapprove rail entry (task 364 folded it into the autopilot card)");
ok(panel.includes("autopilot-preapprove-subblock"), "the pre-approval sub-block renders inside the autopilot card");
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

// ── 任务461-P5 (2026-10-03 用户裁决，覆盖同日 B1 的垂直改法): header stays
//    TWO-column (intro/title/hint left, on-off switch right); checkbox rows
//    first, risk line last on its own row; narrow settings pages keep the
//    shared single-column collapse. Simplified hooks copy stays. ──
const styles = readFileSync(join(root, "styles.css"), "utf8").replace(/\n\s*/g, " ");
ok(/\.autopilot-preapprove-subblock \.settings-field \{ grid-template-columns: minmax\(180px, 260px\) minmax\(320px, 1fr\);/.test(styles),
  "the pre-approval field keeps the two-column header (intro left, switch right)");
ok(/\.autopilot-preapprove-subblock \.set-seg \{ justify-self: end;/.test(styles),
  "the on/off switch hugs the right edge of its column");
ok(/@container settings-page \(max-width: 680px\) \{ \.autopilot-preapprove-subblock \.settings-field \{ grid-template-columns: minmax\(0, 1fr\);/.test(styles),
  "narrow settings pages still collapse the field to one column");
ok(/\.set-preapprove__row \{[^}]*display: flex;/.test(styles),
  "each category row is a block-level flex line (one category per line)");
// P5 order: the risk line renders AFTER the checkbox rows (the panel source
// is whitespace-collapsed into one string; first hit = the render site).
const warningAt = panel.indexOf('className="set-preapprove__warning"');
const rowsAt = panel.indexOf("<span>{row.label}</span>");
ok(warningAt > rowsAt && rowsAt > 0,
  "the risk line renders after the checkbox rows (checkboxes first, risk last)");
for (const [name, text] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  const hooks = text.match(/"settings\.preapproveManagedPaths\.hooks": "([^"]*)"/)?.[1] ?? "";
  const warn = text.match(/"settings\.preapproveManagedPaths\.warning": "([^"]*)"/)?.[1] ?? "";
  ok(hooks.length > 0 && !hooks.includes("settings.json"), `${name} hooks label is simplified (settings.json detail moved out)`);
  ok(warn.includes("settings.json"), `${name} risk line carries the hooks settings.json detail`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
