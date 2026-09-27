// Run: tsx src/__tests__/settings-opencode-go-usage.test.ts
//
// Task 163 — OpenCode Go usage card (source guards + formatting):
// the render-table entry exists (81/123 lost-save lesson), the detail card
// gates its query behind the switch (off = no call, zero regression), the
// three locales ship the same key set, and the countdown/note helpers behave.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { resetCountdown, usageNoteText } from "../lib/opencodeGoUsage";

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
const card = readFileSync(join(root, "components/SettingsOpenCodeGoUsageCard.tsx"), "utf8").replace(/\n\s*/g, " ");
const bridge = readFileSync(join(root, "lib/bridge.ts"), "utf8");
const en = readFileSync(join(root, "locales/en.ts"), "utf8");
const zh = readFileSync(join(root, "locales/zh.ts"), "utf8");
const zhTW = readFileSync(join(root, "locales/zh-TW.ts"), "utf8");

// ── render table + union + detail wiring ────────────────────────────────────
ok(panel.includes('{ id: "opencodeGoUsage", group: "misc",'), "render table carries the entry (81/123)");
ok(panel.includes('| "opencodeGoUsage"'), "the detail-card union includes the id");
ok(panel.includes("Boolean(s.experimentalOpenCodeGoUsage)") && panel.includes("<SettingsOpenCodeGoUsageCard"),
  "the detail card renders the component gated by the switch value");
ok(bridge.includes("GetOpenCodeGoUsage(baseUrl: string): Promise<") && bridge.includes("SetExperimentalOpenCodeGoUsage(enabled: boolean): Promise<void>;"),
  "bridge exposes the query and the setter");

// ── the card gates its query behind the switch (off = zero regression) ─────
ok(card.includes("if (!enabled) {") && card.includes("void refresh();"),
  "the fetch effect returns early while the switch is off (no query)");
ok(card.includes("app.GetOpenCodeGoUsage(OPENCODE_GO_OFFICIAL_BASE)"),
  "the card queries the official base constant (host allow-listed server-side)");
ok(card.includes('data-testid="opencode-go-usage-card"') && card.includes('data-window={tier.window}'),
  "each window renders a data-anchored row for the three-tier display");
ok(card.includes("resetCountdown(tier.resetsAt)") && card.includes("settings.opencodeGoUsage.resetsIn"),
  "rows show the reset countdown");

// ── null wire payload can never crash render (opencodefix) ────────────────
// The Go side used to marshal a nil Tiers slice as "tiers": null and render
// threw TypeError: null.find. Both consumers must read through the guard.
ok(card.includes("const tiers = usage?.tiers ?? []") && card.includes("tiers.find((tier) => tier.window === key)"),
  "the render lookup normalizes a null wire tiers payload");
ok(card.includes("!(usage?.tiers ?? []).some((tier) => tier.resetsAt)"),
  "the countdown gate reads through the same null guard with the original negation");

// ── formatting helpers ──────────────────────────────────────────────────────
const now = Date.parse("2026-09-25T12:00:00Z");
eqCountdown("", "", "empty iso stays empty");
eqCountdown("not-a-date", "", "invalid iso stays empty");
eqCountdown("2026-09-25T11:00:00Z", "", "past reset shows nothing");
eqCountdown("2026-09-25T12:42:00Z", "42m", "under an hour renders minutes");
eqCountdown("2026-09-25T15:12:00Z", "3h 12m", "under a day renders hours+minutes");
eqCountdown("2026-09-27T16:00:00Z", "2d 4h", "over a day renders days+hours");

const t = (key: string) => key;
assert.equal(usageNoteText("", t), "", "empty note renders nothing");
assert.equal(usageNoteText("no-subscription", t), "settings.opencodeGoUsage.note.noSubscription", "403 maps to the entitlement note");
assert.equal(usageNoteText("auth-failed", t), "settings.opencodeGoUsage.note.authFailed", "401 maps to the auth note");
assert.equal(usageNoteText("no-key", t), "settings.opencodeGoUsage.note.noKey", "missing key maps to its own note");
ok(true, "note mapping covers 403/401/no-key distinctly");

// ── three locales ship the same key set ─────────────────────────────────────
const keys = [
  "settings.opencodeGoUsage",
  "settings.opencodeGoUsageHint",
  "settings.opencodeGoUsage.on",
  "settings.opencodeGoUsage.off",
  "settings.opencodeGoUsage.window.rolling",
  "settings.opencodeGoUsage.window.weekly",
  "settings.opencodeGoUsage.window.monthly",
  "settings.opencodeGoUsage.resetsIn",
  "settings.opencodeGoUsage.refresh",
  "settings.opencodeGoUsage.note.noKey",
  "settings.opencodeGoUsage.note.noSubscription",
  "settings.opencodeGoUsage.note.authFailed",
  "settings.opencodeGoUsage.note.unsupported",
  "settings.opencodeGoUsage.note.failed",
  "settings.opencodeGoUsage.note.empty",
];
for (const key of keys) {
  ok(en.includes(`"${key}"`), `en ships ${key}`);
  ok(zh.includes(`"${key}"`), `zh ships ${key}`);
  ok(zhTW.includes(`"${key}"`), `zh-TW ships ${key}`);
}

function eqCountdown(iso: string, expected: string, label: string) {
  const actual = resetCountdown(iso, now);
  if (actual === expected) ok(true, `countdown: ${label}`);
  else ok(false, `countdown: ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

assert.ok(passed >= 57, `expected at least 57 checks (55 original + 2 null-guard), got ${passed}`);
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
