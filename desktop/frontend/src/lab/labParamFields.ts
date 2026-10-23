// 任务 775 — 实验室参数字段绑定注册表（kind:param 卡的字段语义层）。
//
// 分层契约（与 724 的 onKeys→labLightKeys 同构）：lab-layout.yaml 只描述
// 结构与引用（entry id / labelKey / field id / input 形态），值的读取、
// 写入与边界收拢在本注册表——yaml 引用未登记字段 ⇒ resolver 整份回退内置
// 默认（宁可回退也不用半份数据渲染）。
//
// 渲染契约（SettingsPanel 通用参数卡）：
//   number / text  — 草稿态键入、onBlur 落盘（与原硬编码参数行同款手感，
//                    配置层自行钳制，min/max 仅作输入端提示）；
//   segment        — 分段按钮即点即存，options 由注册表给出（含三语 labelKey）；
//   checkbox       — 勾选即存。
// 渐进迁移（设计 §4.2）：已登记 param 走数据渲染，未登记参数保持
// SettingsPanel 硬编码分支兜底——迁移一张登记一张，登记后删旧行。

import type { DictKey } from "../locales/en";
import type { SettingsView } from "../lib/settingsViewTypes";
import { app } from "../lib/bridge";
import type { LabParamInput } from "./labLayoutTypes";

/** segment 选项：值 + 三语标签键（labelKey 逐键验字典，禁止内联文案）。 */
export interface LabParamSegmentOption {
  value: string | number | boolean;
  labelKey: DictKey;
}

/** One param field binding: display shape + dictionary hint + value wiring. */
export interface LabParamFieldBinding {
  /** 输入形态（yaml field.input 必须与此一致，不一致 ⇒ resolver 回退）。 */
  input: LabParamInput;
  /** i18n hint key（逐键验字典）。 */
  hintKey: DictKey;
  /** 多字段卡的行级标签；缺省 = 卡 labelKey（单字段卡的常态）。 */
  labelKey?: DictKey;
  /** Read the current value off a settings snapshot. */
  read: (s: SettingsView) => string | number | boolean;
  /** Persist through the app bridge (the config layer clamps). */
  write: (value: string | number | boolean) => Promise<unknown>;
  /** number 输入端边界（配置层仍自行钳制，这里只作 input min/max）。 */
  min?: number;
  max?: number;
  /** segment 选项表（segment 形态必填）。 */
  options?: LabParamSegmentOption[];
}

/** 参数字段注册表——迁移一个参数 = 这里加一条绑定 + yaml 登记一张卡。 */
export const LAB_PARAM_FIELDS: Readonly<Record<string, LabParamFieldBinding>> = {
  // 任务 308-O4：后台会话闲置释放（分钟；0 = 从不释放）。首批迁移（775）。
  detachedIdleReleaseMinutes: {
    input: "number",
    hintKey: "settings.detachedIdleReleaseHint",
    min: 0,
    max: 10080,
    read: (s) => s.detachedIdleReleaseMinutes ?? 0,
    write: (value) => app.SetDetachedIdleReleaseMinutes(Number(value)),
  },
  // 任务 308-O3：Go 软内存上限（debug.SetMemoryLimit，MB；0 = 不设限）。首批迁移（775）。
  goMemLimitMB: {
    input: "number",
    hintKey: "settings.goMemLimitHint",
    min: 0,
    max: 65536,
    read: (s) => s.goMemLimitMB ?? 0,
    write: (value) => app.SetGoMemLimitMB(Number(value)),
  },
  // 任务 1084：ask 轮次上限（0 = 无限制；有效值 0 或 2~100；1 非法）。
  askRoundLimit: {
    input: "number",
    hintKey: "settings.askRoundLimitHint",
    min: 0,
    max: 100,
    read: (s) => s.askRoundLimit ?? 3,
    write: (value) => app.SetAskRoundLimit(Number(value)),
  },
};

/** Registry key union — the compile gate for default-layout field refs. */
export type LabParamFieldId = keyof typeof LAB_PARAM_FIELDS;

/** Resolver whitelist: yaml param fields may only reference these ids. */
export const LAB_PARAM_FIELD_IDS: ReadonlySet<string> = new Set(Object.keys(LAB_PARAM_FIELDS));
