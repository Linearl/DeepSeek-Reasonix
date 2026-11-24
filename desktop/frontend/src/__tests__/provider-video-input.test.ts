import assert from "node:assert/strict";
// tl1767：视频三态判定与 providerAudioInput 同构——协议黑名单置灰、
// 覆盖优先、能力探测兜底；不维护 SKU 白名单。ADAPTER 门（协议能否携带
// video_url）在后端 wire 层 fail-closed，此处只判 MODEL 门。
import { videoInputHardBlocked, videoInputModes, videoInputModeForModel, videoInputState, mergeVideoInputModes } from "../lib/providerVideoInput";
import type { ProviderModelCapabilityView } from "../lib/types";

const videoModel: ProviderModelCapabilityView = { model: "m", inputModalities: ["text", "video"], state: "supported", source: "adapter", videoInputEnableAllowed: true };
const textModel: ProviderModelCapabilityView = { model: "m", inputModalities: ["text"], state: "unsupported", source: "adapter", videoInputEnableAllowed: true };
const blockedModel: ProviderModelCapabilityView = { model: "m", inputModalities: ["text", "video"], state: "supported", source: "adapter", videoInputEnableAllowed: false };

// protocol blacklist: the checkbox hard-greys and the state reads unsupported.
assert.equal(videoInputHardBlocked(blockedModel), true);
assert.equal(videoInputHardBlocked(videoModel), false);
assert.equal(videoInputHardBlocked(undefined), false);
assert.equal(videoInputState("on", blockedModel), "unsupported");

// explicit on/off beats the modality facts.
assert.equal(videoInputState("on", textModel), "supported");
assert.equal(videoInputState("off", textModel), "unsupported");

// auto falls back to the declared video modality; unknown stays unknown.
assert.equal(videoInputState("auto", videoModel), "supported");
assert.equal(videoInputState("auto", textModel), "unsupported");
assert.equal(videoInputState("auto", undefined, "unknown"), "unknown");

// override mapping.
const modes = videoInputModes([
  { model: "a", reasoningProtocol: "", supportedEfforts: [], defaultEffort: "", video: true },
  { model: "b", reasoningProtocol: "", supportedEfforts: [], defaultEffort: "", video: false },
]);
assert.deepEqual(modes, { a: "on", b: "off" });
assert.equal(videoInputModeForModel(modes, "a"), "on");
assert.equal(videoInputModeForModel(modes, "missing"), "auto");

// merging keeps untouched override fields and drops a fresh auto entry.
const merged = mergeVideoInputModes(
  [{ model: "a", reasoningProtocol: "deepseek", supportedEfforts: ["high"], defaultEffort: "high", vision: true, audio: true, contextWindow: 99 }],
  ["a", "b"],
  { a: "off", b: "auto" },
);
assert.equal(merged.length, 1);
assert.equal(merged[0].model, "a");
assert.equal(merged[0].video, false);
assert.equal(merged[0].vision, true);
assert.equal(merged[0].audio, true);
assert.equal(merged[0].contextWindow, 99);
assert.equal(merged[0].reasoningProtocol, "deepseek");

console.log("provider-video-input: all assertions passed");
