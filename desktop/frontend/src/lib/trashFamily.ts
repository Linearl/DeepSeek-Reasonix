// tl1429d2：回收站「同名多条」的渲染层同族归组。
// 真身机制：一次归档级联会把主线桶 + 十几个 recovery 副本桶一起搬进 .trash
// （tl1442-ext 实测 19 桶），桶清单逐桶出记录且标题同源，列表上同名占多行。
// 数据层约束（已定案）：桶 = 还原/清除的单桶语义，逐桶记录不动；本模块只在
// 渲染层把同族多桶合并成一行——主线（非副本）记录为主体，副本与同键旧记录
// 并入 children，由列表组件展开为明细。后端归组字段见 SessionMeta
// 的 trashFamilyKey（副本剥 -recovery-<16hex> 后的根 stem，与主线同键）。
import type { SessionMeta } from "./types";

export interface TrashFamilyRow {
  body: SessionMeta;
  children: SessionMeta[];
}

// trashFamilyKeyOf：归组键优先取后端富化字段；无字段的记录（旧快照、测试
// 夹具）按 path 独立成族，绝不误并。
export function trashFamilyKeyOf(session: SessionMeta): string {
  return session.trashFamilyKey ? `family:${session.trashFamilyKey}` : `path:${session.path}`;
}

// trashFamilyRows 把（已按删除时间倒序的）回收站记录按族合并，保持首现
// 顺序：每族主体 = 首条非副本记录（没有非副本记录——主线已还原/清除——
// 则由首条记录垫底），其余记录进 children（保持原序）。行数 = 族数。
export function trashFamilyRows(sessions: SessionMeta[]): TrashFamilyRow[] {
  const groups = new Map<string, SessionMeta[]>();
  for (const session of sessions) {
    const key = trashFamilyKeyOf(session);
    const members = groups.get(key);
    if (members) members.push(session);
    else groups.set(key, [session]);
  }
  const rows: TrashFamilyRow[] = [];
  for (const members of groups.values()) {
    const bodyIndex = members.findIndex((session) => !session.trashFamilyCopy);
    const body = members[bodyIndex >= 0 ? bodyIndex : 0];
    rows.push({ body, children: members.filter((_, index) => index !== (bodyIndex >= 0 ? bodyIndex : 0)) });
  }
  return rows;
}

// trashFamilyCopyBadge 从副本桶的 transcript 路径剥 recovery 尾号（16 hex 取
// 前 8 位，与后端命名判据同源），作明细行的区分徽标；非副本形状返回空。
export function trashFamilyCopyBadge(session: SessionMeta): string {
  if (!session.trashFamilyCopy) return "";
  const base = session.path.split(/[\\/]/).pop() ?? "";
  const match = base.match(/-recovery-([0-9a-fA-F]{16})\.jsonl$/);
  return match ? `recovery-${match[1].slice(0, 8)}` : "";
}
