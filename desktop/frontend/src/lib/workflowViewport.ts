// 任务 tl933 —— 686 工作流面板视口交互的纯逻辑片（pan / 缩放 / 持久化）。
//
// 与组件的分工：DOM 手势状态机留在 WorkflowPanel.tsx（指针事件绑定天然贴
// DOM），本模块只放可独立验证的常量与纯函数——测试无需挂载即可覆盖数学面，
// 行为面另由 JSDOM 挂载测试驱动真指针/滚轮事件。
//
// 持久化机制：纯前端偏好走 localStorage（sessionWallPrefs / dockTabs 先例：
// live 生效、不进桌面 config 数据面——tl933 纪律：934 接真数据前不动数据面）。

/** 长按判定窗口：指针按下到进入 pan 模式的等待时长。 */
export const WORKFLOW_PAN_LONG_PRESS_MS = 320;
/** 滑动容差：长按生效前指针位移超过该值即取消手势（视为误触，不进 pan）。 */
export const WORKFLOW_PAN_SLOP_PX = 8;

/** 指针自按下点累计位移是否超过滑动容差（超差 = 取消长按手势）。 */
export function exceedsPanSlop(dx: number, dy: number): boolean {
  return Math.hypot(dx, dy) > WORKFLOW_PAN_SLOP_PX;
}

// ── 缩放（tl933 需求③）：Ctrl+滚轮以光标为锚，范围 25%~300% ─────────

export const WORKFLOW_ZOOM_MIN = 0.25;
export const WORKFLOW_ZOOM_MAX = 3;
export const WORKFLOW_ZOOM_DEFAULT = 1;

/** 缩放钳制：越界拉回 [25%, 300%]，非有限值回默认。 */
export function clampZoom(zoom: number): number {
  if (!Number.isFinite(zoom)) return WORKFLOW_ZOOM_DEFAULT;
  return Math.min(WORKFLOW_ZOOM_MAX, Math.max(WORKFLOW_ZOOM_MIN, zoom));
}

/** 滚轮 delta → 乘法缩放因子（Chrome 式：向下滚 = 缩小）。行/页模式按
 * 16px/100px 折算成像素 delta。指数步进让手感在任何倍率下均匀。 */
export function zoomFactorFromWheel(deltaY: number, deltaMode?: number): number {
  const unit = deltaMode === 1 ? 16 : deltaMode === 2 ? 100 : 1;
  return Math.exp(-deltaY * unit * 0.0015);
}

/** 锚点缩放（滚动容器模型）：缩放前后让锚点下的内容点纹丝不动。
 * 内容点 = scroll + anchor；缩放后 scroll' = 内容点×ratio − anchor。 */
export function anchoredScroll(scroll: number, ratio: number, anchor: number): number {
  return (scroll + anchor) * ratio - anchor;
}

// ── 持久化（tl933 需求④）：视角（滚动偏移）+缩放跨会话随面板偏好存储 ──
//
// 机制：纯前端 UI 偏好走 localStorage（sessionWallPrefs / dockTabs 先例），
// 不进桌面 config 数据面（tl933 纪律：934 接真数据前不动数据面）。形状损坏
// 一律降级为缺省而不是抛错；配额错误尽力而为忽略。

const WORKFLOW_VIEWPORT_STORAGE_KEY = "workflowPanel:viewport:v1";

export interface WorkflowViewportPrefs {
  /** 缩放级别（已钳制到 25%~300%）。 */
  zoom?: number;
  /** 视口滚动偏移（pan 的落点；重开后由浏览器按内容范围再钳制）。 */
  scrollLeft?: number;
  scrollTop?: number;
}

export function loadWorkflowViewportPrefs(): WorkflowViewportPrefs {
  try {
    const raw = window.localStorage.getItem(WORKFLOW_VIEWPORT_STORAGE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return {};
    const src = parsed as Record<string, unknown>;
    const out: WorkflowViewportPrefs = {};
    if (typeof src.zoom === "number") out.zoom = clampZoom(src.zoom);
    if (typeof src.scrollLeft === "number" && Number.isFinite(src.scrollLeft)) out.scrollLeft = src.scrollLeft;
    if (typeof src.scrollTop === "number" && Number.isFinite(src.scrollTop)) out.scrollTop = src.scrollTop;
    return out;
  } catch {
    return {};
  }
}

export function saveWorkflowViewportPrefs(patch: WorkflowViewportPrefs): void {
  try {
    const merged = { ...loadWorkflowViewportPrefs(), ...patch };
    window.localStorage.setItem(WORKFLOW_VIEWPORT_STORAGE_KEY, JSON.stringify(merged));
  } catch {
    /* 配额/隐私模式等错误忽略——偏好是尽力而为 */
  }
}
