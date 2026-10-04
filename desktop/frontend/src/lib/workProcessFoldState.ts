import { useSyncExternalStore } from "react";

// 任务 463：composer 的「收起/展开全部工作过程」按钮是双向开关，其渲染方向
// 必须与 transcript 的真实折叠状态一致（用户手动折叠/展开部分块后不得错位）。
// Composer 与 Transcript 没有父子关系——任务 269 因此用 window 事件做单向命令；
// 但「状态」用事件会在挂载顺序上丢（后挂载的一方错过先派发的那条），所以
// 这里用仓内既有的模块级 store 范式（同 sessionExperience / selectionStore）：
// 每个 Transcript 实例按 tabId 上报自己的全折叠快照，Composer 订阅聚合值。

export type WorkProcessFoldSurfaceState = {
  /** 该 transcript 是否存在可折叠的工作过程块。 */
  hasFoldables: boolean;
  /** 所有可折叠块当前都处于关闭态（缺失条目按默认展开规则计入）。 */
  allCollapsed: boolean;
};

export type WorkProcessFoldAggregate = {
  /**
   * true 表示按钮应渲染为「展开」（当前全部折叠）。
   * 没有任何存活 transcript 上报时为 false：按钮保持原有「收起」外观，
   * 空会话/纯预览面的行为与改动前一致（点击是无操作）。
   */
  allCollapsed: boolean;
};

const EMPTY_AGGREGATE: WorkProcessFoldAggregate = { allCollapsed: false };

const surfaces = new Map<string, WorkProcessFoldSurfaceState>();
const listeners = new Set<() => void>();
let aggregate: WorkProcessFoldAggregate = EMPTY_AGGREGATE;

function recompute(): void {
  let allCollapsed = surfaces.size > 0;
  for (const surface of surfaces.values()) {
    if (!surface.hasFoldables || !surface.allCollapsed) {
      allCollapsed = false;
      break;
    }
  }
  const next = allCollapsed ? { allCollapsed: true } : EMPTY_AGGREGATE;
  if (next === aggregate) return;
  aggregate = next;
  for (const listener of listeners) listener();
}

/** Transcript 在折叠状态或可折叠块集合变化时上报（tabId 缺失的预览面不上报）。 */
export function publishWorkProcessFoldState(
  tabId: string,
  state: WorkProcessFoldSurfaceState,
): void {
  const previous = surfaces.get(tabId);
  if (previous
    && previous.hasFoldables === state.hasFoldables
    && previous.allCollapsed === state.allCollapsed) return;
  surfaces.set(tabId, state);
  recompute();
}

/** Transcript 卸载或切换会话时注销其上报，避免陈旧表面污染聚合值。 */
export function clearWorkProcessFoldState(tabId: string): void {
  if (!surfaces.delete(tabId)) return;
  recompute();
}

export function subscribeWorkProcessFoldAggregate(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getWorkProcessFoldAggregate(): WorkProcessFoldAggregate {
  return aggregate;
}

export function useWorkProcessFoldAggregate(): WorkProcessFoldAggregate {
  return useSyncExternalStore(subscribeWorkProcessFoldAggregate, getWorkProcessFoldAggregate);
}

/** 仅供测试：清空全部上报，隔离用例间状态。 */
export function resetWorkProcessFoldStateForTest(): void {
  surfaces.clear();
  recompute();
}
