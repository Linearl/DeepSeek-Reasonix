// 任务 986（弹窗层级最小方案，用户 20261018 裁决「先按 986 做」）：
// 层内 LIFO 单调 order——竞品调研四家（zcode/mimo/Codex/Claude Desktop）均无
// 应用内动态层管理器，全靠静态 z-index + portal 撞层逐个抬数打补丁；fork 的
// 最小方案 = 语义层 token 不动 + 层内单调序号，zIndex = calc(<语义 token> + order)。
//   - 后弹置前（用户原则①）：打开 overlay 时取单调递增序号；
//   - 点击置前（用户原则②）：mousedown 时重新取号（显式抬 order，不依赖 DOM 重排）。
// 设计红线（调研报告判据①）：序号键必须显式冻结——模块级 counter 单调递增、
// 从不回收复用，禁用挂载顺序/组件 id 等会随自身写入失效的自毁键。
// 溢出不设防：number 安全整数 2^53，单会话 overlay 打开量级远不可及。
let counter = 0;

/** 打开或点击置前 overlay 时调用：返回严格递增的新序号。 */
export function acquireOverlayOrder(): number {
  counter += 1;
  return counter;
}

/** 测试钩子：重置计数（仅供单测隔离用，运行时勿调）。 */
export function resetOverlayOrderForTest(): void {
  counter = 0;
}
