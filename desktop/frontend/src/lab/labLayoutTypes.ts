// 任务 724 — 实验室布局数据结构（schema）。
//
// 分组树：groups → entries → members。entries 是 rail 上可点开的卡片
// （每个 id 对应 SettingsPanel 里一个 pane 分支）；members 是折叠进卡片的
// 表A 成员（只在徽章与卡片内开关行体现，没有自己的 rail 入口）。
// 配置键引用：entry.onKeys 指向 lib 注册表（labLightKeys.ts）里的谓词 id，
// 卡片灯 = 各键布尔或。徽章档位：entry.tier（可缺省，合并卡无自有档位）
// + member.tier（必填）。
//
// 硬约束（任务书 724）：配置键零语义变化（本 schema 只描述结构与引用，
// 不触碰键值）；文案走 locale 键（labelKey 必须能在字典解析，禁止内联文案）。

import type { LabTier } from "../lib/experimentTiers";
import type { DictKey } from "../locales/en";

/** One folded 表A member inside a merged card (badge + card row metadata). */
export interface LabLayoutMember {
  /** Register id (TierFeatureId) — validated against the tier register. */
  id: string;
  /** i18n key of the rail-visible label; validated against the dictionary. */
  labelKey: DictKey;
  /** Required — a member exists on the rail only through its badge. */
  tier: LabTier;
}

/** One rail card entry: a clickable card whose pane branch lives in
 * SettingsPanel (id must be in the pane whitelist). */
export interface LabLayoutEntry {
  /** Pane-branch id in SettingsPanel — validated against the whitelist. */
  id: string;
  /** i18n key of the rail label (validated against the dictionary). */
  labelKey: DictKey;
  /** Own tier; omitted on merged cards (their badges come from members). */
  tier?: LabTier;
  /** labLightKeys ids whose boolean OR lights the card. */
  onKeys: string[];
  /** Folded members (badges + in-card rows), in display order. */
  members?: LabLayoutMember[];
}

/** One rail group (任务 561 的 7 组 + 603 工具优化 = 8 个固定 key）。 */
export interface LabLayoutGroup {
  /** Controlled vocabulary — see LAB_GROUP_KEYS (validated). */
  key: LabGroupKey;
  /** i18n key of the group header (validated against the dictionary). */
  labelKey: DictKey;
  entries: LabLayoutEntry[];
}

/** The full lab layout document (yaml 顶层结构 / 内置默认同构）。 */
export interface LabLayoutData {
  version: 1;
  groups: LabLayoutGroup[];
}

/** All group keys the resolver accepts (unknown group key ⇒ fallback). */
export const LAB_GROUP_KEYS = [
  "automation",
  "efficiency",
  "ui",
  "observability",
  "dev-debug",
  "storage",
  "infra",
  "tool-opt",
] as const;

export type LabGroupKey = (typeof LAB_GROUP_KEYS)[number];
