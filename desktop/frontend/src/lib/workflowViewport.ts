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
