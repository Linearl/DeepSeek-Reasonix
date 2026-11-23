import type { ProviderModelCapabilityView, ProviderModelOverrideView } from "./types";
import { matchingModelKey } from "./providerImageInput";

// tl1767（视频输入接入）：视频勾选的三态判定，与 providerAudioInput 同构。
// 能力来自后端探测（inputModalities / videoInputEnableAllowed），用户的每模型
// 覆盖优先；协议黑名单（Anthropic）由后端 videoInputEnableAllowed=false 下发。
// 不维护任何 SKU 白名单。ADAPTER 门（协议能否携带 video_url）在后端 wire 层
// fail-closed：不可携带时视频投影为文本占位，请求不失败。

export type VideoInputMode = "auto" | "on" | "off";

export function videoInputHardBlocked(capability?: ProviderModelCapabilityView): boolean {
  if (capability?.videoInputEnableAllowed !== undefined) return !capability.videoInputEnableAllowed;
  return false;
}

export function videoInputModes(overrides?: ProviderModelOverrideView[] | null): Record<string, VideoInputMode> {
  return Object.fromEntries((overrides ?? []).map((item) => [item.model, item.video == null ? "auto" : item.video ? "on" : "off"]));
}

export function videoInputModeForModel(modes: Record<string, VideoInputMode>, model: string): VideoInputMode {
  const key = matchingModelKey(Object.keys(modes), model);
  return key ? modes[key] : "auto";
}

export function videoInputState(mode: VideoInputMode, capability?: ProviderModelCapabilityView, fallback = "unknown"): string {
  if (capability?.videoInputEnableAllowed === false) return "unsupported";
  if (mode !== "auto") return mode === "on" ? "supported" : "unsupported";
  return capability?.inputModalities?.includes("video") ? "supported" : capability ? "unsupported" : fallback;
}

// Only video ownership changes; the other override fields survive untouched.
export function mergeVideoInputModes(
  overrides: ProviderModelOverrideView[] | null | undefined,
  models: string[],
  modes: Record<string, VideoInputMode>,
): ProviderModelOverrideView[] {
  return models.flatMap((model) => {
    const key = matchingModelKey((overrides ?? []).map((item) => item.model), model);
    const previous = overrides?.find((item) => item.model === key);
    const mode = videoInputModeForModel(modes, model);
    if (!previous && mode === "auto") return [];
    const item: ProviderModelOverrideView = {
      reasoningProtocol: "", supportedEfforts: [], defaultEffort: "",
      ...previous, model, video: mode === "auto" ? null : mode === "on",
    };
    return [item];
  });
}
