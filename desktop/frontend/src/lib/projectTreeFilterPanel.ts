// tl1170: 筛选面板（classic「筛选与排序」）时间/颜色两区的展开态持久化。
// 两区默认折叠；用户展开/收起即写穿 localStorage，重挂载（含刷新）读回。
// 惯例与 useTopicTimeFilter 的 "projectTree:timeFilter" 同源：裸 localStorage +
// try/catch（不可用时静默退回默认值），读取端做形状校验（脏数据不炸 UI）。

export const FILTER_PANEL_EXPANDED_STORAGE_KEY = "projectTree:filterPanelExpanded";

export type FilterPanelSections = { time: boolean; color: boolean };

export function readFilterPanelExpanded(): FilterPanelSections {
  try {
    const raw = localStorage.getItem(FILTER_PANEL_EXPANDED_STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<FilterPanelSections> | null;
      if (parsed && (typeof parsed.time === "boolean" || typeof parsed.color === "boolean")) {
        return { time: parsed.time === true, color: parsed.color === true };
      }
    }
  } catch { /* localStorage unavailable / corrupt payload */ }
  return { time: false, color: false };
}

export function writeFilterPanelExpanded(sections: FilterPanelSections): void {
  try {
    localStorage.setItem(FILTER_PANEL_EXPANDED_STORAGE_KEY, JSON.stringify(sections));
  } catch { /* ignore */ }
}
