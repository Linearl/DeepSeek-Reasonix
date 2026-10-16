// 任务 686 —— 工作流可视化面板的 DAG 数据契约（预拟）+ 分层布局算法（纯函数）。
//
// 契约地位：685（路线 C 编排引擎）尚未落地，本文件的 schema 按其任务书语义
// **预拟**：节点类型 = 子代理任务（subagent）/ 确定性门（gate，bash+exit
// code）/ 汇聚（join）；边 = 依赖与数据流；phase = 阶段分组（着色用）。
// 685 交付后以其 handoff 的 schema 为准对齐字段名（handoff-686.md 等待点①）。
//
// 布局是纯函数（无 DOM）：DFS 判定回边（门控循环的反馈边）→ 回边不参与分层
// → 最长路径分层 → 同层按 phase 序 + 声明序排序 → 网格坐标。边路径为 SVG
// path 字符串：前向边用水平贝塞尔，回边从源底部绕行到目标底部。

export type WorkflowNodeKind = "subagent" | "gate" | "join";

export interface WorkflowDagNode {
  id: string;
  name: string;
  kind: WorkflowNodeKind;
  /** phase 分组键（对应 WorkflowDag.phases 的一项）；缺省 = 未分组。 */
  phase?: string;
  /** subagent 节点：ask 指令全文（下钻展示）。 */
  instruction?: string;
  /** subagent 节点：类型化结果的字段说明（子代理读到的提示）。 */
  resultSpec?: string;
  /** gate 节点：门命令字面量（下钻展示）。 */
  command?: string;
}

export interface WorkflowDagEdge {
  from: string;
  to: string;
  /** 边标签（如门的 pass/fail 分支、循环的 retry）。 */
  label?: string;
}

export interface WorkflowDag {
  id: string;
  name: string;
  /** 有序 phase 键表；着色按索引取 --wf-phase-N token。 */
  phases?: string[];
  nodes: WorkflowDagNode[];
  edges: WorkflowDagEdge[];
}

export interface WorkflowNodePlacement {
  id: string;
  x: number;
  y: number;
  layer: number;
}

export interface WorkflowEdgePlacement {
  from: string;
  to: string;
  /** SVG path d 属性。 */
  path: string;
  /** 回边（门控循环反馈）走底部绕行。 */
  back: boolean;
  label?: string;
  labelX?: number;
  labelY?: number;
}

export interface WorkflowDagLayout {
  width: number;
  height: number;
  nodes: WorkflowNodePlacement[];
  edges: WorkflowEdgePlacement[];
}

export const WORKFLOW_NODE_WIDTH = 176;
export const WORKFLOW_NODE_HEIGHT = 68;
const LAYER_GAP_X = 96;
const ROW_GAP_Y = 28;
const PADDING = 36;
/** 回边绕行时向下方探出的距离。 */
const BACK_EDGE_DROPOUT = 44;

function indexNodes(dag: WorkflowDag): Map<string, WorkflowDagNode> {
  const map = new Map<string, WorkflowDagNode>();
  for (const node of dag.nodes) map.set(node.id, node);
  return map;
}

/** DFS 判定回边：指向「当前递归栈上」节点的边（门控循环的反馈边）。
 * 返回的键是 `from>to`。 */
function findBackEdges(dag: WorkflowDag): Set<string> {
  const outgoing = new Map<string, string[]>();
  for (const node of dag.nodes) outgoing.set(node.id, []);
  for (const edge of dag.edges) {
    const list = outgoing.get(edge.from);
    if (list) list.push(edge.to);
  }
  const back = new Set<string>();
  const state = new Map<string, "visiting" | "done">();
  const visit = (id: string) => {
    state.set(id, "visiting");
    for (const to of outgoing.get(id) ?? []) {
      const toState = state.get(to);
      if (toState === "visiting") back.add(`${id}>${to}`);
      else if (!toState) visit(to);
    }
    state.set(id, "done");
  };
  for (const node of dag.nodes) {
    if (!state.has(node.id)) visit(node.id);
  }
  return back;
}

/** 最长路径分层（回边除外）：layer(v) = max(layer(u) + 1)。拓扑序迭代一轮即收敛。 */
function assignLayers(dag: WorkflowDag, backEdges: Set<string>): Map<string, number> {
  const indegree = new Map<string, number>();
  const outgoing = new Map<string, string[]>();
  for (const node of dag.nodes) {
    indegree.set(node.id, 0);
    outgoing.set(node.id, []);
  }
  for (const edge of dag.edges) {
    if (backEdges.has(`${edge.from}>${edge.to}`)) continue;
    if (!indegree.has(edge.to) || !outgoing.has(edge.from)) continue; // 悬空边：忽略
    indegree.set(edge.to, (indegree.get(edge.to) ?? 0) + 1);
    outgoing.get(edge.from)!.push(edge.to);
  }
  const layer = new Map<string, number>();
  for (const node of dag.nodes) layer.set(node.id, 0);
  const queue = dag.nodes.filter((n) => (indegree.get(n.id) ?? 0) === 0).map((n) => n.id);
  while (queue.length > 0) {
    const id = queue.shift()!;
    const base = layer.get(id) ?? 0;
    for (const to of outgoing.get(id) ?? []) {
      if (base + 1 > (layer.get(to) ?? 0)) layer.set(to, base + 1);
      const remaining = (indegree.get(to) ?? 1) - 1;
      indegree.set(to, remaining);
      if (remaining === 0) queue.push(to);
    }
  }
  // 环内残余节点（只经回边成环时可达性为 0 的情形）保持 0——回边已剥离，
  // 剩余图若有环（多个互相依赖的门）按声明序落在第 0 层，不炸布局。
  return layer;
}

/** 分层布局。纯函数：同一份 dag 永远得到同一份坐标（测试可复现）。 */
export function layoutWorkflowDag(dag: WorkflowDag): WorkflowDagLayout {
  const nodes = indexNodes(dag);
  const backEdges = findBackEdges(dag);
  const layer = assignLayers(dag, backEdges);

  const phaseIndex = new Map<string, number>();
  (dag.phases ?? []).forEach((phase, index) => phaseIndex.set(phase, index));
  const declareIndex = new Map<string, number>();
  dag.nodes.forEach((node, index) => declareIndex.set(node.id, index));

  const maxLayer = dag.nodes.reduce((max, node) => Math.max(max, layer.get(node.id) ?? 0), 0);
  const columns: string[][] = Array.from({ length: maxLayer + 1 }, () => []);
  for (const node of dag.nodes) columns[layer.get(node.id) ?? 0].push(node.id);
  for (const column of columns) {
    column.sort((a, b) => {
      const pa = phaseIndex.get(nodes.get(a)?.phase ?? "") ?? Number.MAX_SAFE_INTEGER;
      const pb = phaseIndex.get(nodes.get(b)?.phase ?? "") ?? Number.MAX_SAFE_INTEGER;
      return pa - pb || (declareIndex.get(a) ?? 0) - (declareIndex.get(b) ?? 0);
    });
  }

  const rowHeight = WORKFLOW_NODE_HEIGHT + ROW_GAP_Y;
  const colWidth = WORKFLOW_NODE_WIDTH + LAYER_GAP_X;
  const maxRows = columns.reduce((max, column) => Math.max(max, column.length), 1);
  const height = PADDING * 2 + maxRows * rowHeight - ROW_GAP_Y;
  const width = PADDING * 2 + maxLayer * colWidth + WORKFLOW_NODE_WIDTH;

  const placements: WorkflowNodePlacement[] = [];
  const position = new Map<string, { x: number; y: number }>();
  columns.forEach((column, layerIndex) => {
    const columnHeight = column.length * rowHeight - ROW_GAP_Y;
    const offsetY = PADDING + (maxRows * rowHeight - ROW_GAP_Y - columnHeight) / 2;
    column.forEach((id, rowIndex) => {
      const x = PADDING + layerIndex * colWidth;
      const y = offsetY + rowIndex * rowHeight;
      position.set(id, { x, y });
      placements.push({ id, x, y, layer: layerIndex });
    });
  });

  const edges: WorkflowEdgePlacement[] = [];
  for (const edge of dag.edges) {
    const from = position.get(edge.from);
    const to = position.get(edge.to);
    if (!from || !to) continue; // 悬空边不画
    const isBack = backEdges.has(`${edge.from}>${edge.to}`);
    if (!isBack) {
      const x1 = from.x + WORKFLOW_NODE_WIDTH;
      const y1 = from.y + WORKFLOW_NODE_HEIGHT / 2;
      const x2 = to.x;
      const y2 = to.y + WORKFLOW_NODE_HEIGHT / 2;
      const midX = (x1 + x2) / 2;
      edges.push({
        from: edge.from,
        to: edge.to,
        back: false,
        label: edge.label,
        labelX: midX,
        labelY: Math.min(y1, y2) - 8,
        path: `M ${x1} ${y1} C ${midX} ${y1}, ${midX} ${y2}, ${x2} ${y2}`,
      });
    } else {
      const x1 = from.x + WORKFLOW_NODE_WIDTH / 2;
      const y1 = from.y + WORKFLOW_NODE_HEIGHT;
      const x2 = to.x + WORKFLOW_NODE_WIDTH / 2;
      const y2 = to.y + WORKFLOW_NODE_HEIGHT;
      const dip = Math.max(y1, y2) + BACK_EDGE_DROPOUT;
      edges.push({
        from: edge.from,
        to: edge.to,
        back: true,
        label: edge.label,
        labelX: (x1 + x2) / 2,
        labelY: dip - 6,
        path: `M ${x1} ${y1} C ${x1} ${dip}, ${x2} ${dip}, ${x2} ${y2}`,
      });
    }
  }

  return { width, height, nodes: placements, edges };
}

/** 命令面板插条（sessionWall 的 insertSessionWallEntry 同款语义）：
 * flag 关 = 原数组逐字节不变（返回同一引用）。 */
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

/** 685 示例编排的前端 fixture（扇出 → 汇聚 → 门控循环 → 确认 → 报告）。
 * 同时承担两重角色：① 运行前全图预览的演示数据；② 布局/渲染测试的固定输入。 */
export const WORKFLOW_FIXTURE_DAG: WorkflowDag = {
  id: "fixture-migration",
  name: "配置迁移编排（演示）",
  phases: ["规划", "调研", "验证", "交付"],
  nodes: [
    {
      id: "plan",
      name: "制定迁移方案",
      kind: "subagent",
      phase: "规划",
      instruction:
        "通读任务书与目标配置结构，产出迁移方案：列出要搬迁的键、顺序与回退步骤。方案须回答「哪些键有默认值等价面」。",
      resultSpec: "plan: string（方案全文）, risks: string[]（风险清单）",
    },
    {
      id: "read-facts",
      name: "读取事实源",
      kind: "subagent",
      phase: "调研",
      instruction: "读取现有 config 渲染表与 decode 路径，列出每个目标键的当前读写面（含 legacy 别名）。",
      resultSpec: "keys: {name, readPath, writePath}[]",
    },
    {
      id: "scan-deps",
      name: "扫描依赖面",
      kind: "subagent",
      phase: "调研",
      instruction: "扫描调用方：哪些测试/面板/桥接读这些键；输出引用清单与断言点。",
      resultSpec: "refs: {file, line, assertion}[]",
    },
    {
      id: "gate-tests",
      name: "包级测试门",
      kind: "gate",
      phase: "验证",
      command: "go test ./internal/config/... -count=1",
    },
    {
      id: "fix-failures",
      name: "修复红灯用例",
      kind: "subagent",
      phase: "验证",
      instruction: "对门反馈的失败用例逐条修复：先复现、再改、再跑同一命令验证；不得放宽断言。",
      resultSpec: "fixed: string[]（修复的用例名）, notes: string?",
    },
    {
      id: "confirm",
      name: "独立确认",
      kind: "subagent",
      phase: "交付",
      instruction: "第二子代理从证据（门的 exit code + 输出）重现结论；自查不算。确认失败保留并标 unconfirmed。",
      resultSpec: "confirmed: boolean, evidence: string",
    },
    {
      id: "report",
      name: "汇总报告",
      kind: "join",
      phase: "交付",
    },
  ],
  edges: [
    { from: "plan", to: "read-facts" },
    { from: "plan", to: "scan-deps" },
    { from: "read-facts", to: "gate-tests" },
    { from: "scan-deps", to: "gate-tests" },
    { from: "gate-tests", to: "fix-failures", label: "exit≠0" },
    { from: "fix-failures", to: "gate-tests", label: "retry" },
    { from: "gate-tests", to: "confirm", label: "exit=0" },
    { from: "confirm", to: "report" },
  ],
};
