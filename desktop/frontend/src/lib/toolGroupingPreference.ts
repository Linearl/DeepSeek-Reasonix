import { useSyncExternalStore } from "react";

// 任务 668 — 消息流工具分组用户开关（纯渲染层展示偏好）。
//
// 开=维持现状：连续同类工具卡片聚合为组卡（ToolGroup / 只读批量 ReadOnlyBatch）；
// 关=逐条平铺，每张工具卡独立展示。与 zcode 的 toolGrouping* 设置同语义：只改
// 消息流渲染，工具调用轮次与模型上下文在两态完全一致（668 第一步调研：
// docs/report/调研报告【非诊断】/工具聚合机制调研-20261009.md §1/§3）。
// 分组本体早已内建于 transcriptRows.processBodyRows，本模块补的是用户开关。
//
// 落点取舍：桌面设置的展示偏好，不进 experimental_tool_optimizations（那是工具
// 行为语义，调研报告 §4 建议、派单拍板照办）；也不加 Go 后端字段——新增后端
// 设置需要 wails generate 更新 wailsjs 绑定，668 明令禁止，localStorage 即为
// 持久层（getSnapshot 首读惰性水合，首帧即取到用户上次的选择，无闪烁）。

const STORAGE_KEY = "reasonix-tool-grouping";
const CHANGE_EVENT = "reasonix:tool-grouping";

let current = true;
let hydrated = false;
const listeners = new Set<() => void>();
const beforeChangeListeners = new Set<(previous: boolean, next: boolean) => void>();

function emit(): void {
  for (const listener of listeners) listener();
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(CHANGE_EVENT, { detail: current }));
  }
}

export function getToolGroupingEnabled(): boolean {
  if (!hydrated) {
    hydrated = true;
    try {
      current = localStorage.getItem(STORAGE_KEY) !== "off";
    } catch {
      current = true; // Storage unavailable (hardened webviews): keep the default.
    }
  }
  return current;
}

export function applyToolGroupingEnabled(value: boolean): void {
  const next = value === true;
  const previous = getToolGroupingEnabled();
  if (previous === next) return;
  // Notify viewport-anchoring listeners while the old value is still current
  // (same discipline as onSessionExperienceWillChange), then commit and emit.
  for (const listener of beforeChangeListeners) listener(previous, next);
  current = next;
  try {
    localStorage.setItem(STORAGE_KEY, next ? "on" : "off");
  } catch {
    // Storage may be unavailable; the in-memory value still applies this run.
  }
  emit();
}

/** 视口锚保护挂点：分组切换增删行、改变几何，Transcript 在变化提交前借此开启
 * 结构性事务（与 onSessionExperienceWillChange 同一纪律）。 */
export function onToolGroupingWillChange(
  listener: (previous: boolean, next: boolean) => void,
): () => void {
  beforeChangeListeners.add(listener);
  return () => beforeChangeListeners.delete(listener);
}

export function useToolGroupingEnabled(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getToolGroupingEnabled,
    () => true,
  );
}
