import assert from "node:assert/strict";
// 任务 1008：音频三态判定与 providerImageInput 同构——协议黑名单置灰、
// 覆盖优先、能力探测兜底；不维护 SKU 白名单。
import { audioInputHardBlocked, audioInputModes, audioInputModeForModel, audioInputState, mergeAudioInputModes } from "../lib/providerAudioInput";
import type { ProviderModelCapabilityView } from "../lib/types";

const audioModel: ProviderModelCapabilityView = { model: "m", inputModalities: ["text", "audio"], state: "supported", source: "adapter", audioInputEnableAllowed: true };
const textModel: ProviderModelCapabilityView = { model: "m", inputModalities: ["text"], state: "unsupported", source: "adapter", audioInputEnableAllowed: true };
const blockedModel: ProviderModelCapabilityView = { model: "m", inputModalities: ["text", "audio"], state: "supported", source: "adapter", audioInputEnableAllowed: false };

// protocol blacklist: the checkbox hard-greys and the state reads unsupported.
assert.equal(audioInputHardBlocked(blockedModel), true);
assert.equal(audioInputHardBlocked(audioModel), false);
assert.equal(audioInputHardBlocked(undefined), false);
assert.equal(audioInputState("on", blockedModel), "unsupported");

// explicit on/off beats the modality facts.
assert.equal(audioInputState("on", textModel), "supported");
assert.equal(audioInputState("off", textModel), "unsupported");

// auto falls back to the declared audio modality; unknown stays unknown.
assert.equal(audioInputState("auto", audioModel), "supported");
assert.equal(audioInputState("auto", textModel), "unsupported");
assert.equal(audioInputState("auto", undefined, "unknown"), "unknown");

// override mapping.
const modes = audioInputModes([
  { model: "a", reasoningProtocol: "", supportedEfforts: [], defaultEffort: "", audio: true },
  { model: "b", reasoningProtocol: "", supportedEfforts: [], defaultEffort: "", audio: false },
]);
assert.deepEqual(modes, { a: "on", b: "off" });
assert.equal(audioInputModeForModel(modes, "a"), "on");
assert.equal(audioInputModeForModel(modes, "missing"), "auto");

// merging keeps untouched override fields and drops a fresh auto entry.
const merged = mergeAudioInputModes(
  [{ model: "a", reasoningProtocol: "deepseek", supportedEfforts: ["high"], defaultEffort: "high", vision: true, contextWindow: 99 }],
  ["a", "b"],
  { a: "off", b: "auto" },
);
assert.equal(merged.length, 1);
assert.equal(merged[0].model, "a");
assert.equal(merged[0].audio, false);
assert.equal(merged[0].vision, true);
assert.equal(merged[0].contextWindow, 99);
assert.equal(merged[0].reasoningProtocol, "deepseek");

console.log("provider-audio-input: all assertions passed");
