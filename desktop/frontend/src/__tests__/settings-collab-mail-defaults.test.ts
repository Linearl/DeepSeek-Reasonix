/**
 * Task 309 contract: the mailbox-defaults trio is wired across the settings
 * stack — view fields, bridge signature, panel controls, and all three
 * locales. Source-guard style on purpose: each wire point is a text anchor
 * that must exist, and the test asserts a lower bound (≥7 keys per locale)
 * so a broken/empty locales file cannot pass vacuously.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import process from "node:process";

let failures = 0;
function expectContains(label: string, haystack: string, needle: string): void {
  if (!haystack.includes(needle)) {
    console.error(`FAIL ${label}: missing ${JSON.stringify(needle)}`);
    failures += 1;
  }
}

const src = join(process.cwd(), "src");

// 1. View fields (settingsViewTypes) — the SettingsView trio.
const view = readFileSync(join(src, "lib", "settingsViewTypes.ts"), "utf8");
for (const field of [
  "sessionCollabMailIdempotentDefault?: boolean",
  "sessionCollabMailReceiptDefault?: boolean",
  "sessionCollabDefaultDelivery?: string",
]) {
  expectContains("settingsViewTypes", view, field);
}

// 2. Config mirror type (types.ts) — same trio on the config face.
const types = readFileSync(join(src, "lib", "types.ts"), "utf8");
for (const field of [
  "sessionCollabMailIdempotentDefault?: boolean",
  "sessionCollabMailReceiptDefault?: boolean",
  "sessionCollabDefaultDelivery?: string",
]) {
  expectContains("types", types, field);
}

// 3. Bridge: declaration + mock both present.
const bridge = readFileSync(join(src, "lib", "bridge.ts"), "utf8");
expectContains("bridge decl", bridge, "SetSessionCollabMailDefaults(idempotent: boolean, receiptDefault: boolean, defaultDelivery: string): Promise<void>");
expectContains("bridge mock", bridge, "async SetSessionCollabMailDefaults() {}");

// 4. Panel: the field calls the setter and renders both checkboxes and the
// delivery select with its two options.
const panel = readFileSync(join(src, "components", "SettingsPanel.tsx"), "utf8");
expectContains("panel field", panel, 'label={t("settings.sessionCollabMailDefaults")}');
expectContains("panel idempotent checkbox", panel, "sessionCollabMailIdempotentDefault");
expectContains("panel receipt checkbox", panel, "sessionCollabMailReceiptDefault");
expectContains("panel delivery select option steer", panel, 'value="steer"');
expectContains("panel delivery select option followup", panel, 'value="followup"');
expectContains("panel calls setter", panel, "app.SetSessionCollabMailDefaults(");

// 5. Locales: all three dialects carry the full key set (≥7 keys each).
for (const locale of ["zh", "zh-TW", "en"]) {
  const text = readFileSync(join(src, "locales", `${locale}.ts`), "utf8");
  const keys = [
    "settings.sessionCollabMailDefaults",
    "settings.sessionCollabMailDefaultsHint",
    "settings.sessionCollabMailIdempotent",
    "settings.sessionCollabMailReceipt",
    "settings.sessionCollabDefaultDelivery",
    "settings.sessionCollabDefaultDeliverySteer",
    "settings.sessionCollabDefaultDeliveryFollowup",
  ];
  let hit = 0;
  for (const key of keys) {
    if (text.includes(`"${key}"`)) {
      hit += 1;
    } else {
      console.error(`FAIL locales/${locale}: missing key ${key}`);
      failures += 1;
    }
  }
  if (hit < 7) {
    console.error(`FAIL locales/${locale}: only ${hit}/7 keys`);
    failures += 1;
  }
}

if (failures > 0) {
  console.error(`task-309 mailbox-defaults contract: ${failures} failure(s)`);
  process.exit(1);
}
console.log("task-309 mailbox-defaults contract: all wire points present");
