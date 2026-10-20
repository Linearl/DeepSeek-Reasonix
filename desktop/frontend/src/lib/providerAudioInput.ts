import type { ProviderModelCapabilityView, ProviderModelOverrideView } from "./types";
import { matchingModelKey } from "./providerImageInput";

// 任务 1008（模型音频输入）：音频勾选的三态判定，与 providerImageInput 同构。
// 能力来自后端探测（inputModalities / audioInputEnableAllowed），用户的每模型
// 覆盖优先；协议黑名单（Anthropic）由后端 audioInputEnableAllowed=false 下发。
// 不维护任何 SKU 白名单。

export type AudioInputMode = "auto" | "on" | "off";

export function audioInputHardBlocked(capability?: ProviderModelCapabilityView): boolean {
  if (capability?.audioInputEnableAllowed !== undefined) return !capability.audioInputEnableAllowed;
  return false;
}

export function audioInputModes(overrides?: ProviderModelOverrideView[] | null): Record<string, AudioInputMode> {
  return Object.fromEntries((overrides ?? []).map((item) => [item.model, item.audio == null ? "auto" : item.audio ? "on" : "off"]));
}

export function audioInputModeForModel(modes: Record<string, AudioInputMode>, model: string): AudioInputMode {
  const key = matchingModelKey(Object.keys(modes), model);
  return key ? modes[key] : "auto";
}

export function audioInputState(mode: AudioInputMode, capability?: ProviderModelCapabilityView, fallback = "unknown"): string {
  if (capability?.audioInputEnableAllowed === false) return "unsupported";
  if (mode !== "auto") return mode === "on" ? "supported" : "unsupported";
  return capability?.inputModalities?.includes("audio") ? "supported" : capability ? "unsupported" : fallback;
}

// Only audio ownership changes; the other override fields survive untouched.
export function mergeAudioInputModes(
  overrides: ProviderModelOverrideView[] | null | undefined,
  models: string[],
  modes: Record<string, AudioInputMode>,
): ProviderModelOverrideView[] {
  return models.flatMap((model) => {
    const key = matchingModelKey((overrides ?? []).map((item) => item.model), model);
    const previous = overrides?.find((item) => item.model === key);
    const mode = audioInputModeForModel(modes, model);
    if (!previous && mode === "auto") return [];
    const item: ProviderModelOverrideView = {
      reasoningProtocol: "", supportedEfforts: [], defaultEffort: "",
      ...previous, model, audio: mode === "auto" ? null : mode === "on",
    };
    return [item];
  });
}
