// Run: tsx src/__tests__/task325-autopilot-yolo.test.ts
//
// Task 325 — autopilot is yolo-only. The gate lives in Go (desktop/
// autopilot_gate.go); what this suite pins down is the user-visible half:
// both refusal/closure notices localize by their stable code instead of
// falling through to the backend's bilingual fallback, the copy names yolo in
// all three locales (so "提示含需要 yolo" holds for every language), and the
// settings panel maps SetDesktopAutopilot's refusal to its own message.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { localizedNoticeText, quietTranscriptNoticeKey } from "../lib/controllerNotices";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

const testDir = dirname(fileURLToPath(import.meta.url));
const rawBackend = "Autopilot requires the YOLO approval mode (需要 yolo 审批模式); switch approval to YOLO first.";

// 1. Localize by code: the code decides, the backend text never does.
assert.equal(
  localizedNoticeText(rawBackend, "autopilot_requires_yolo"),
  en["notice.autopilotRequiresYolo"],
  "refusal notice localizes by code",
);
assert.equal(
  localizedNoticeText("some other backend text", "autopilot_closed_off_yolo"),
  en["notice.autopilotClosedOffYolo"],
  "reverse-linkage notice localizes by code",
);

// 2. Both are ordinary notices (not quiet lifecycle messages).
for (const code of ["autopilot_requires_yolo", "autopilot_closed_off_yolo"]) {
  assert.equal(quietTranscriptNoticeKey(rawBackend, code), "", `${code} must stay visible in the transcript`);
}

// 3. Every locale names yolo, so the refusal always tells the user which
//    switch to flip — this is the acceptance line "提示含需要 yolo".
const locales: Array<[string, Record<string, string>]> = [
  ["en", en],
  ["zh", zh],
  ["zh-TW", zhTW],
];
for (const [name, dict] of locales) {
  for (const key of ["notice.autopilotRequiresYolo", "notice.autopilotClosedOffYolo", "settings.errorAutopilotRequiresYolo"] as const) {
    assert.ok(dict[key], `${name} is missing ${key}`);
    assert.ok(dict[key].toLowerCase().includes("yolo"), `${name} ${key} must name yolo`);
  }
}

// 4. The settings toggle refusal reaches the panel as that localized copy.
const panel = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
assert.match(panel, /autopilot requires the yolo approval mode/i, "panel must recognize the Go refusal");
assert.ok(panel.includes('t("settings.errorAutopilotRequiresYolo")'), "panel must render the localized refusal");

// 5. The backend gate is still wired: both notice codes the panel-side copy
//    keys off are emitted by the Go gate, so a rename cannot strand them.
const gate = readFileSync(resolve(testDir, "../../../autopilot_gate.go"), "utf8");
assert.ok(gate.includes('"autopilot_requires_yolo"'), "Go gate still emits autopilot_requires_yolo");
assert.ok(gate.includes('"autopilot_closed_off_yolo"'), "Go gate still emits autopilot_closed_off_yolo");

console.log("  PASS  task 325 autopilot yolo gate copy is localized in all three locales");
