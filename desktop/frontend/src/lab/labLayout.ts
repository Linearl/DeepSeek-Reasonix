// 任务 724 — 实验室布局解析器：yaml → 校验 → 布局数据；任何一步失败
// 回退内置默认（labLayoutDefault.ts），附 warnings 供 dev 诊断。
//
// 安全网契约（任务书 724）：yaml 缺失或损坏 ⇒ 回退内置默认，不白屏。
// 「损坏」= 解析失败、schema 不合、引用越界（未知组 key / 未登记档位 /
// 未知配置键引用 / 字典里不存在的 labelKey / 重复或未登记的 id）——宁可
// 回退也不用半份数据渲染。
//
// 硬约束落点：配置键引用只认 labLightKeys 白名单（键语义零变化）；文案
// 一律 labelKey 且逐键验字典（禁止内联，三语不回归）。

import { en } from "../locales/en";
import { EXPERIMENT_FEATURE_TIERS, LAB_TIER_ORDER, type LabTier } from "../lib/experimentTiers";
import type { SettingsView } from "../lib/settingsViewTypes";
import { LAB_LAYOUT_DEFAULT_DATA } from "./labLayoutDefault";
import { LAB_LIGHT_KEYS, LAB_LIGHT_KEY_IDS } from "./labLightKeys";
import { parseSimpleYaml, YamlParseError } from "./parseSimpleYaml";
import { LAB_GROUP_KEYS, type LabGroupKey, type LabLayoutData, type LabLayoutEntry } from "./labLayoutTypes";
import type { DictKey } from "../locales/en";

const LAB_TIER_VALUES: ReadonlySet<string> = new Set<string>(LAB_TIER_ORDER);
const REGISTER_IDS: ReadonlySet<string> = new Set(Object.keys(EXPERIMENT_FEATURE_TIERS));

export interface ResolvedLabLayout {
  layout: LabLayoutData;
  /** "yaml" = lab-layout.yaml 通过全部校验；"default" = 回退内置默认。 */
  source: "yaml" | "default";
  /** 解析/校验问题清单（回退时非空；dev 控制台可见）。 */
  warnings: string[];
}

export interface ResolveLabLayoutOptions {
  /** Pane 白名单：entry.id 必须是 SettingsPanel 里真实存在的卡片分支。 */
  allowedEntryIds: ReadonlySet<string>;
}

/** Resolve the lab layout from a yaml document; fall back to the built-in
 * default on ANY problem. Pure — same input, same output. */
export function resolveLabLayout(yamlText: string | null | undefined, opts: ResolveLabLayoutOptions): ResolvedLabLayout {
  if (yamlText == null || yamlText.trim() === "") {
    return { layout: LAB_LAYOUT_DEFAULT_DATA, source: "default", warnings: ["lab-layout.yaml missing — built-in default in use"] };
  }
  let parsed: unknown;
  try {
    parsed = parseSimpleYaml(yamlText);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return { layout: LAB_LAYOUT_DEFAULT_DATA, source: "default", warnings: [`lab-layout.yaml unusable (${message}) — built-in default in use`] };
  }
  const problems: string[] = [];
  try {
    const layout = validateLayout(parsed, opts, problems);
    return { layout, source: "yaml", warnings: [] };
  } catch {
    // Every problem is already collected in `problems`; half-validated data
    // never renders — fall back to the built-in default wholesale.
    return { layout: LAB_LAYOUT_DEFAULT_DATA, source: "default", warnings: [...problems, "lab-layout.yaml rejected — built-in default in use"] };
  }
}

function isPlainObject(v: unknown): v is { [key: string]: unknown } {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function fail(warnings: string[], message: string): never {
  warnings.push(message);
  throw new YamlParseError(message);
}

function validateLayout(root: unknown, opts: ResolveLabLayoutOptions, warnings: string[]): LabLayoutData {
  if (!isPlainObject(root)) fail(warnings, "root must be a mapping");
  if (root.version !== 1) fail(warnings, `unsupported layout version ${String(root.version)} (expected 1)`);
  const rawGroups = root.groups;
  if (!Array.isArray(rawGroups) || rawGroups.length === 0) fail(warnings, "groups must be a non-empty list");

  const seenKeys = new Set<string>();
  const seenIds = new Set<string>();
  const groups = rawGroups.map((rawGroup, gi) => {
    if (!isPlainObject(rawGroup)) fail(warnings, `groups[${gi}] must be a mapping`);
    const key = rawGroup.key;
    if (typeof key !== "string" || !(LAB_GROUP_KEYS as readonly string[]).includes(key)) {
      fail(warnings, `groups[${gi}].key "${String(key)}" is not a known group key`);
    }
    if (seenKeys.has(key)) fail(warnings, `duplicate group key "${key}"`);
    seenKeys.add(key);
    checkLabelKey(rawGroup.labelKey, `groups[${gi}].labelKey`, warnings);

    const rawEntries = rawGroup.entries;
    if (!Array.isArray(rawEntries) || rawEntries.length === 0) fail(warnings, `group "${key}" entries must be a non-empty list`);
    const entries = rawEntries.map((rawEntry, ei) => {
      if (!isPlainObject(rawEntry)) fail(warnings, `group "${key}" entries[${ei}] must be a mapping`);
      const id = rawEntry.id;
      if (typeof id !== "string" || id === "") fail(warnings, `group "${key}" entries[${ei}].id must be a non-empty string`);
      if (!opts.allowedEntryIds.has(id)) fail(warnings, `entry "${id}" has no pane branch in SettingsPanel`);
      if (seenIds.has(id)) fail(warnings, `duplicate id "${id}"`);
      seenIds.add(id);
      checkLabelKey(rawEntry.labelKey, `entry "${id}".labelKey`, warnings);

      let tier: LabTier | undefined;
      if (rawEntry.tier != null) {
        if (typeof rawEntry.tier !== "string" || !LAB_TIER_VALUES.has(rawEntry.tier)) {
          fail(warnings, `entry "${id}".tier "${String(rawEntry.tier)}" is not a lab tier`);
        }
        tier = rawEntry.tier as LabTier;
      }

      const onKeys = checkOnKeys(rawEntry.onKeys, `entry "${id}"`, warnings);

      let members: LabLayoutData["groups"][number]["entries"][number]["members"];
      const rawMembers = rawEntry.members;
      if (rawMembers != null) {
        if (!Array.isArray(rawMembers)) fail(warnings, `entry "${id}".members must be a list`);
        members = rawMembers.map((rawMember, mi) => {
          if (!isPlainObject(rawMember)) fail(warnings, `entry "${id}" members[${mi}] must be a mapping`);
          const memberId = rawMember.id;
          if (typeof memberId !== "string" || memberId === "") fail(warnings, `entry "${id}" members[${mi}].id must be a non-empty string`);
          if (!REGISTER_IDS.has(memberId)) fail(warnings, `member "${memberId}" is not in the tier register`);
          if (seenIds.has(memberId)) fail(warnings, `duplicate id "${memberId}"`);
          seenIds.add(memberId);
          checkLabelKey(rawMember.labelKey, `member "${memberId}".labelKey`, warnings);
          if (typeof rawMember.tier !== "string" || !LAB_TIER_VALUES.has(rawMember.tier)) {
            fail(warnings, `member "${memberId}".tier must be a lab tier (a member exists on the rail only through its badge)`);
          }
          return { id: memberId, labelKey: rawMember.labelKey as DictKey, tier: rawMember.tier as LabTier };
        });
      }

      return { id, labelKey: rawEntry.labelKey as DictKey, tier, onKeys, members };
    });
    return { key: key as LabGroupKey, labelKey: rawGroup.labelKey as DictKey, entries };
  });

  return { version: 1, groups };
}

function checkLabelKey(value: unknown, where: string, warnings: string[]): void {
  if (typeof value !== "string" || !(value in en)) {
    fail(warnings, `${where} "${String(value)}" is not a dictionary key (文案必须走 locale 键)`);
  }
}

function checkOnKeys(value: unknown, where: string, warnings: string[]): string[] {
  if (!Array.isArray(value)) fail(warnings, `${where}.onKeys must be a list of light-key ids`);
  for (const key of value) {
    if (typeof key !== "string" || !LAB_LIGHT_KEY_IDS.has(key)) {
      fail(warnings, `${where}.onKeys references unknown light key "${String(key)}"`);
    }
  }
  return value as string[];
}

// ── 消费侧 helper（SettingsPanel rail / 墙状态从这里取）────────────────

/** Flat render table: every entry with its group, in layout order. */
export function labEntries(layout: LabLayoutData): Array<{ entry: LabLayoutEntry; group: LabGroupKey }> {
  return layout.groups.flatMap((group) => group.entries.map((entry) => ({ entry, group: group.key })));
}

export function findLabEntry(layout: LabLayoutData, id: string): LabLayoutEntry | undefined {
  for (const group of layout.groups) {
    const hit = group.entries.find((entry) => entry.id === id);
    if (hit) return hit;
  }
  return undefined;
}

/** The card light: boolean OR over the entry's referenced config keys. */
export function labEntryLightOn(entry: LabLayoutEntry, s: SettingsView): boolean {
  return entry.onKeys.some((key) => {
    const predicate = (LAB_LIGHT_KEYS as Record<string, (s: SettingsView) => boolean>)[key];
    return predicate ? predicate(s) : false;
  });
}

/** Distinct badge tiers of a rail entry (own tier + member tiers), in the
 * canonical badge order. Merged cards surface every distinct member tier;
 * standalone entries their own; entries without any tier (none today) none. */
export function labEntryBadgeTiers(layout: LabLayoutData, entryId: string): LabTier[] {
  const entry = findLabEntry(layout, entryId);
  if (!entry) return [];
  const tiers = new Set<LabTier>(entry.tier ? [entry.tier] : []);
  for (const member of entry.members ?? []) tiers.add(member.tier);
  return LAB_TIER_ORDER.filter((tier) => tiers.has(tier));
}

/** 任务 562 兼容面：内置默认布局上的徽章档位（测试与墙视图用）。 */
export function railTiersForDefault(entryId: string): LabTier[] {
  return labEntryBadgeTiers(LAB_LAYOUT_DEFAULT_DATA, entryId);
}

/** 任务 722/724 — 注册表一致性检查：默认布局里每个带档位的 id 必须与 562
 * 档位注册表同档（含 727 的 heartbeatRotation）。合并卡 id（contextGovernance
 * 等无自有档位）跳过。供测试调用——不在模块加载期抛错（模块加载抛错正是
 * 724 安全网要防的白屏面）。 */
export function findLabLayoutDefaultRegisterDrift(): string[] {
  const register = EXPERIMENT_FEATURE_TIERS as Readonly<Record<string, LabTier>>;
  const drift: string[] = [];
  for (const group of LAB_LAYOUT_DEFAULT_DATA.groups) {
    for (const entry of group.entries) {
      if (entry.tier && register[entry.id] && register[entry.id] !== entry.tier) {
        drift.push(`entry ${entry.id}: layout ${entry.tier} ≠ register ${register[entry.id]}`);
      }
      for (const member of entry.members ?? []) {
        if (register[member.id] && register[member.id] !== member.tier) {
          drift.push(`member ${member.id}: layout ${member.tier} ≠ register ${register[member.id]}`);
        }
      }
    }
  }
  return drift;
}
