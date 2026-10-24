// tl 1108/1109 — 边栏「方案 E」浮动标签组的纯逻辑（文件面板与子代理面板共用，
// 禁止各复制一套）。
//
// 语义（用户 2026-10-23 21:22 澄清）：单击=选中=预览（未钉住）、双击=钉住、
// 再单击已选中=取消选中=等同关闭预览。不引入「打开/关闭」这套新概念。
//
// 纯函数、无 DOM、无 React —— 两个面板各自持有 state，但状态机与批量关闭
// 语义由这里唯一定义。

export type SideDockTabKind = "preview" | "pinned";

export type SideDockTab<TId extends string = string> = {
  id: TId;
  kind: SideDockTabKind;
};

export type SideDockTabsState<TId extends string = string> = {
  tabs: Array<SideDockTab<TId>>;
  /** 当前选中（活动）标签 id；null = 无选中（内容区为空态）。 */
  activeId: TId | null;
};

/** 单标签关闭后的收尾：落回最近一个仍打开的标签，没有则空态。
 *  调用方随后按 activeId 是否为 null 恢复面板宽度（246 复位判据）。 */
export function closeSideDockTab<TId extends string>(
  state: SideDockTabsState<TId>,
  id: TId,
): SideDockTabsState<TId> {
  const index = state.tabs.findIndex((tab) => tab.id === id);
  if (index < 0) return state;
  const tabs = state.tabs.filter((tab) => tab.id !== id);
  if (state.activeId !== id) return { tabs, activeId: state.activeId };
  const next = tabs[Math.min(index, tabs.length - 1)];
  return { tabs, activeId: next ? next.id : null };
}

export function closeAllSideDockTabs<TId extends string>(
  _state: SideDockTabsState<TId>,
): SideDockTabsState<TId> {
  return { tabs: [], activeId: null };
}

/** 关闭 other（除 keepId 外全部）；keepId 不在列表中等价于关闭全部。 */
export function closeOtherSideDockTabs<TId extends string>(
  _state: SideDockTabsState<TId>,
  keepId: TId,
): SideDockTabsState<TId> {
  const tabs = _state.tabs.filter((tab) => tab.id === keepId);
  return { tabs, activeId: tabs.length > 0 && _state.activeId === keepId ? keepId : null };
}

/** 关闭 right（targetId 右侧全部）；targetId 不在列表中不动。 */
export function closeRightSideDockTabs<TId extends string>(
  state: SideDockTabsState<TId>,
  targetId: TId,
): SideDockTabsState<TId> {
  const index = state.tabs.findIndex((tab) => tab.id === targetId);
  if (index < 0) return state;
  const tabs = state.tabs.slice(0, index + 1);
  const activeStillOpen = state.activeId !== null && tabs.some((tab) => tab.id === state.activeId);
  return { tabs, activeId: activeStillOpen ? state.activeId : targetId };
}

/** 打开/切换到一个标签：
 *  - 已存在 ⇒ 选中它（若原为 preview 保持 preview，不静默钉住）；
 *  - 不存在 ⇒ 追加为 preview；未钉住的旧 preview 仍保留标签但不再活动
 *    （与 VSCode 一致：单击只是切换选中，关闭由再单击/× 承担）。 */
export function activateSideDockTab<TId extends string>(
  state: SideDockTabsState<TId>,
  id: TId,
): SideDockTabsState<TId> {
  const existing = state.tabs.find((tab) => tab.id === id);
  if (existing) {
    return { tabs: state.tabs, activeId: id };
  }
  const tabs = [...state.tabs, { id, kind: "preview" as const }];
  return { tabs, activeId: id };
}

/** 双击钉住：不存在则先按 activate 追加为 preview 再转正体。 */
export function pinSideDockTab<TId extends string>(
  state: SideDockTabsState<TId>,
  id: TId,
): SideDockTabsState<TId> {
  const base = activateSideDockTab(state, id);
  return {
    tabs: base.tabs.map((tab) => (tab.id === id ? { ...tab, kind: "pinned" as const } : tab)),
    activeId: id,
  };
}

/** 拖拽排序：把 id 移到 targetId 之前/之后（before=true 插到前面）。
 *  顺序由调用方持久化（文件面板走 workspaceTreeMemory，子代理面板会话内即可）。 */
export function reorderSideDockTabs<TId extends string>(
  state: SideDockTabsState<TId>,
  id: TId,
  targetId: TId,
  before = true,
): SideDockTabsState<TId> {
  const from = state.tabs.findIndex((tab) => tab.id === id);
  if (from < 0 || id === targetId) return state;
  const moving = state.tabs[from];
  const without = state.tabs.filter((tab) => tab.id !== id);
  const to = without.findIndex((tab) => tab.id === targetId);
  if (to < 0) return { ...state, tabs: [...without, moving] };
  const insertAt = before ? to : to + 1;
  const tabs = [...without.slice(0, insertAt), moving, ...without.slice(insertAt)];
  return { tabs, activeId: state.activeId };
}

/** 有活动内容 ⇒ 边栏超时自动折叠失效（1108/1109 共用判据，勿各自另写）。 */
export function sideDockHasActiveTabs(tabs: ReadonlyArray<SideDockTab<string>>): boolean {
  return tabs.length > 0;
}

/** 单双击区分（踩坑：双击必须 clearTimeout，否则先闪一次预览再钉住）。 */
export const SIDE_DOCK_CLICK_DOUBLE_MS = 200;
