// 任务 686 —— 命令面板「工作流面板」插条 helper（sessionWall 的
// insertSessionWallEntry 同款语义）：flag 关 = 原数组逐字节不变（返回同一
// 引用）。
//
// 单独成模块（而非并入 workflowDag.ts）的原因：App.tsx 急切 import 本
// helper；若它与 DAG fixture/布局同模块，fixture 数据会被拖进 initial
// chunk（686 首版实测炸 initial 线的教训）。本模块只应依赖零重载荷。

export function insertWorkflowPanelEntry<T extends { id: string }>(cmds: T[], enabled: boolean, entry: T): T[] {
  if (!enabled) return cmds;
  const at = cmds.findIndex((c) => c.id === "cmd-session-wall");
  if (at >= 0) {
    const out = cmds.slice();
    out.splice(at + 1, 0, entry);
    return out;
  }
  const reload = cmds.findIndex((c) => c.id === "cmd-reload-runtime");
  if (reload < 0) return [...cmds, entry];
  const out = cmds.slice();
  out.splice(reload + 1, 0, entry);
  return out;
}
