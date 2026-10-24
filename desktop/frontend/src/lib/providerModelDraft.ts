import { matchingModelKey } from "./providerImageInput";
import type { ProviderModelOverrideView } from "./types";

// tl 1104: draft 与 ProviderModelDialog 的 ModelDetailsDraft 对齐——字段名与三态
// 取值完全同构，弹窗 apply 直接落这里，避免两套草稿形状互转。
// Task 1053: `efforts` 是可选的模型级 effort 词表声明；缺省（undefined，来自
// 尚未认识该字段的调用方）保持已存词表不动。
export type ModelDraft = { model: string; contextWindow: string; maxOutputTokens: number; vision: boolean | null; audio: boolean | null; efforts?: string[] };

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
  // Task 1053: the model dialog now carries a per-model effort vocabulary.
  // An absent `efforts` array (callers that predate the field) keeps the
  // stored list, exactly like the other unedited fields. Normalization and
  // default handling live in normalizedEffortSelection so the upper-zone
  // apply chain (SettingsPanel applyModelDetails) shares the exact semantics.
  if (draft.efforts !== undefined) {
    const selection = normalizedEffortSelection(draft.efforts, next.defaultEffort);
    next.supportedEfforts = selection.supportedEfforts;
    if (selection.supportedEfforts.length > 0) next.defaultEffort = selection.defaultEffort;
  }
  return [...overrides.filter((item) => item.model !== key && item.model !== next.model), next];
}

/** Task 1053: per-model effort vocabulary semantics shared by both apply
 *  chains (the model dialog's applyModelDraft above and the upper-zone
 *  applyModelDetails in SettingsPanel). Levels normalize case and whitespace
 *  and never store "auto"; a stored default_effort outside the new list is
 *  dropped here so the draft stays honest (the backend normalizer would drop
 *  it later anyway); an empty list means "inherit the connection vocabulary",
 *  which keeps the pinned default (task 1053's zero-migration contract).
 *  The model dialog has no default-effort picker of its own — that lives on
 *  the model settings effort rows (task 1053). */
export function normalizedEffortSelection(efforts: string[], previousDefault = ""): { supportedEfforts: string[]; defaultEffort: string } {
  const supportedEfforts = efforts.map((level) => level.toLowerCase().trim()).filter((level) => level && level !== "auto");
  if (supportedEfforts.length === 0) return { supportedEfforts, defaultEffort: previousDefault };
  const storedDefault = previousDefault.trim();
  let defaultEffort = "";
  for (const level of supportedEfforts) {
    if (level === storedDefault) {
      defaultEffort = storedDefault;
      break;
    }
  }
  return { supportedEfforts, defaultEffort };
}
