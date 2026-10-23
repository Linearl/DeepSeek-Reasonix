import { matchingModelKey } from "./providerImageInput";
import type { ProviderModelOverrideView } from "./types";

// tl 1104: draft 与 ProviderModelDialog 的 ModelDetailsDraft 对齐——字段名与三态
// 取值完全同构，弹窗 apply 直接落这里，避免两套草稿形状互转。
export type ModelDraft = { model: string; contextWindow: string; maxOutputTokens: number; vision: boolean | null; audio: boolean | null };

/** Validate explicit overrides without turning an inherited value into a guessed limit.
 *  tl 1104: 上区弹窗校验口径收紧——上下文 / 输出上限必须走数字输入（0 = 未设 = 继承，
 *  输出上限额外允许 -1 = 不下发可选限制），杜绝「留空存 0、语义靠约定」。 */
export function modelDraftError(draft: { model: string; contextWindow: string; maxOutputTokens: number }, existing: string[], original?: string): "required" | "duplicate" | "syntax" | "context" | "output" | null {
  const model = draft.model.trim();
  if (!model) return "required";
  if (/[\s,，]/.test(model)) return "syntax";
  if (existing.some((name) => name !== original && name === model)) return "duplicate";
  const context = Number(draft.contextWindow);
  if (!Number.isSafeInteger(context) || context < 0) return "context";
  const output = Number(draft.maxOutputTokens);
  if (!Number.isSafeInteger(output) || output < -1 || output === 0) return "output";
  return null;
}

/** Keep unedited reasoning, audio and future view fields when changing one model. */
export function applyModelDraft(overrides: ProviderModelOverrideView[], draft: ModelDraft, original?: string): ProviderModelOverrideView[] {
  const key = matchingModelKey(overrides.map((item) => item.model), original ?? draft.model);
  const previous = overrides.find((item) => item.model === key);
  const next: ProviderModelOverrideView = {
    reasoningProtocol: "", supportedEfforts: [], defaultEffort: "", ...previous,
    model: draft.model.trim(), contextWindow: Number(draft.contextWindow) || 0, maxOutputTokens: Number(draft.maxOutputTokens) || 0,
    vision: draft.vision, audio: draft.audio,
  };
  return [...overrides.filter((item) => item.model !== key && item.model !== next.model), next];
}
