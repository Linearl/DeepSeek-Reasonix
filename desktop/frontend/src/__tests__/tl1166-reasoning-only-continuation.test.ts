// tl-1166 harness: the reasoning-only final repair (B continuation + C
// fallback body) — source wiring, notice action, and the three-locale copy.
// Mirrors the task372 contract-check style: read the wired sources and pin
// every seam this task adds, so a silent unwrap cannot ship.
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { ok } from "node:assert/strict";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

const here = dirname(fileURLToPath(import.meta.url));
const read = (p) => readFileSync(resolve(here, p), "utf8");

const runLoop = read("../../../../internal/agent/run_loop.go");
const core = read("../../../../internal/agent/reasoning_only_final.go");
const editGo = read("../../../../internal/config/edit.go");
const bootGo = read("../../../../internal/boot/boot.go");
const notices = read("../lib/controllerNotices.ts");
const renderer = read("../components/useTranscriptRowRenderer.tsx");
const cards = read("../components/TranscriptCards.tsx");
const controller = read("../lib/useController.ts");
const settingsGo = read("../../../settings_app.go");
const prefsGo = read("../../../settings_preferences.go");
const labSection = read("../components/ExperimentalLabSection.tsx");

// ① B: the reasoning-only final injects ONE reminder through the established
//    HostGeneratedUserMessage channel, ceiling const = 1, and the rejected
//    plan A (whole-round auto-retry) stays documented as rejected.
ok(core.includes("maxReasoningOnlyContinuations = 1"), "B is bounded to exactly one continuation per turn");
ok(core.includes("HostGeneratedUserMessage(a.withTurnPreferences(reasoningOnlyContinuationMessage()))"), "B rides the established host-message injection channel");
ok(core.includes("已否决"), "plan-A rejection is documented in-code (防后人重走)");
ok(runLoop.includes("reasoningOnlyFinalShape(text, reasoning, nil, responsesItems, serverSearch)"), "B's trigger proves no structured work (no calls/items/search)");
ok(runLoop.includes("maybeInjectReasoningOnlyContinuation"), "handleFinalResponse wires the B branch");

// ② C: the fallback body carries BOTH thinking segments chronologically.
ok(core.includes("state.terminal.reasoningOnlyFirstReasoning"), "C preserves the original turn's thinking across the continuation");
ok(runLoop.includes("maybeSynthesizeReasoningOnlyDisplay"), "commit boundary wires the C fallback");
ok(core.includes("ReasoningOnlyThinking1") && core.includes("ReasoningOnlyThinking2"), "C labels the two segments");

// ③ slog trail: greppable injection + fallback markers.
ok(core.includes("injecting one continuation reminder") && core.includes("tl-1166"), "B leaves a grep-able slog trail");
ok(core.includes("synthesizing fallback body"), "C leaves a grep-able slog trail");

// ④ notice codes: stable wire codes for both faces.
ok(notices.includes("reasoning_only_continuation") && notices.includes("reasoning_only_final"), "controller maps both tl-1166 notice codes");
ok(notices.includes('"retry_reasoning_only"'), "reasoning_only_final carries the retry action");
ok(controller.includes('"retry_reasoning_only"'), "notice action union admits retry_reasoning_only");
ok(renderer.includes('row.item.action === "retry_reasoning_only"') && renderer.includes('onPrompt(t("notice.reasoningOnlyRetryPrompt"))'), "renderer dispatches the retry action into onPrompt");
ok(cards.includes('item.action === "retry_reasoning_only" ? t("notice.reasoningOnlyRetry")'), "notice card renders the retry label");

// ⑤ the switch: full chain config → agent → settings UI.
ok(editGo.includes("ReasoningOnlyContinuationEnabled() bool"), "config exposes the resolved gate");
ok(bootGo.includes("ReasoningOnlyContinuation: cfg.ReasoningOnlyContinuationEnabled()"), "boot passes the resolved gate into agent Options");
ok(settingsGo.includes("ReasoningOnlyContinuation bool `json:\"reasoningOnlyContinuation\"`"), "settings view carries the toggle readback");
ok(prefsGo.includes("SetExperimentalReasoningOnlyContinuation"), "app setter writes the config override");
ok(labSection.includes("SetExperimentalReasoningOnlyContinuation"), "lab section renders the toggle row");

// ⑥ Locale keys across the three languages.
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  const keys = [
    "notice.reasoningOnlyContinuation",
    "notice.reasoningOnlyFinal",
    "notice.reasoningOnlyRetry",
    "notice.reasoningOnlyRetryPrompt",
    "settings.reasoningOnlyContinuation",
    "settings.reasoningOnlyContinuationHint",
    "settings.reasoningOnlyContinuation.on",
    "settings.reasoningOnlyContinuation.off",
  ];
  const missing = keys.filter((k) => !(k in dict));
  ok(missing.length === 0, `locale ${name} carries all 8 tl-1166 keys (missing: ${missing.join(",")})`);
}
// The C notice copy must name both the thinking-only fact and the body placement.
ok(zh["notice.reasoningOnlyFinal"].includes("思考过程") && zh["notice.reasoningOnlyFinal"].includes("正文"), "zh C notice says thinking-only + body placement");
ok(en["notice.reasoningOnlyRetry"].length > 0 && zhTW["notice.reasoningOnlyRetry"].length > 0, "retry button label present in en/zh-TW");

console.log("  PASS  tl1166 reasoning-only continuation wiring aligned");
