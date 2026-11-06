// tl1454 —— 用户自定义工作流模板的纯函数层（模板视图与画布附加面共用）。
// 不碰 React、不碰 bridge：原因码/诊断类别 → 文案键、编辑器行列定位、运行
// 终止后的在途节点投影、预算消耗读数。全部可在 tsx-runner 下直接断言。

import type { DictKey } from "../locales/en";
import type { WorkflowRunSnapshot } from "./workflowEvents";
import type { WorkflowDiagnosticKind, WorkflowFailureReason, WireWorkflowRun } from "./workflowTypes";

const REASON_KEYS: Record<WorkflowFailureReason, DictKey> = {
  timeout: "workflowPanel.reason.timeout",
  cancelled: "workflowPanel.reason.cancelled",
  budget: "workflowPanel.reason.budget",
  subagent_error: "workflowPanel.reason.subagent_error",
  result_contract: "workflowPanel.reason.result_contract",
  gate_failed: "workflowPanel.reason.gate_failed",
  gate_error: "workflowPanel.reason.gate_error",
  gate_denied: "workflowPanel.reason.gate_denied",
  internal: "workflowPanel.reason.internal",
};

/** 原因码 → 人话文案键（未知码按引擎内部错误兜底，绝不显示空白）。 */
export function reasonKey(reason: string | undefined): DictKey {
  return REASON_KEYS[(reason ?? "internal") as WorkflowFailureReason] ?? REASON_KEYS.internal;
}

const DIAG_KEYS: Record<WorkflowDiagnosticKind, DictKey> = {
  syntax: "workflowPanel.templates.diagKind.syntax",
  field: "workflowPanel.templates.diagKind.field",
  structure: "workflowPanel.templates.diagKind.structure",
  reference: "workflowPanel.templates.diagKind.reference",
  version: "workflowPanel.templates.diagKind.version",
};

/** 诊断类别 → 人话文案键。 */
export function diagKindKey(kind: string | undefined): DictKey {
  return DIAG_KEYS[(kind ?? "field") as WorkflowDiagnosticKind] ?? DIAG_KEYS.field;
}

/** 1-based 行/列 → 文本字符偏移（越界钳到文本范围，编辑器据此放光标）。 */
export function lineColumnOffset(text: string, line: number | undefined, column: number | undefined): number {
  if (!line || line < 1) return 0;
  let offset = 0;
  for (let current = 1; current < line; current += 1) {
    const next = text.indexOf("\n", offset);
    if (next < 0) return text.length;
    offset = next + 1;
  }
  const lineEnd = text.indexOf("\n", offset);
  const end = lineEnd < 0 ? text.length : lineEnd;
  const col = Math.max(1, column ?? 1);
  return Math.min(offset + col - 1, end);
}

/**
 * 运行已终止而节点仍停在 running：引擎的 fail-fast 只给波次序第一个失败
 * 节点发终态事件，其余在途节点停在最后一次 running。模板视图开启时把它们
 * 投影成「未完成」（排队灰 + stale 标记），默认视图不经此函数（关闭态等价）。
 */
export function settleStaleRunning(
  snapshot: WorkflowRunSnapshot,
  liveState: WireWorkflowRun["state"],
): { snapshot: WorkflowRunSnapshot; stale: Set<string> } {
  const stale = new Set<string>();
  if (liveState === "running") return { snapshot, stale };
  let changed = false;
  const nodes: WorkflowRunSnapshot["nodes"] = {};
  for (const [id, node] of Object.entries(snapshot.nodes)) {
    if (node.state === "running") {
      stale.add(id);
      nodes[id] = { ...node, state: "queued" };
      changed = true;
    } else {
      nodes[id] = node;
    }
  }
  return { snapshot: changed ? { ...snapshot, nodes } : snapshot, stale };
}

/** 运行级预算读数片段（未声明预算 = 空数组，工具条不渲染）。 */
export function budgetReadout(run: WireWorkflowRun): Array<{ key: DictKey; vars: Record<string, string> }> {
  const budget = run.budget;
  if (!budget) return [];
  const out: Array<{ key: DictKey; vars: Record<string, string> }> = [];
  if (budget.tokens) {
    out.push({ key: "workflowPanel.budget.tokens", vars: { used: compactNumber(run.usedTokens ?? 0), limit: compactNumber(budget.tokens) } });
  }
  if (budget.rounds) {
    out.push({ key: "workflowPanel.budget.rounds", vars: { used: String(run.usedRequests ?? 0), limit: String(budget.rounds) } });
  }
  if (budget.wallMs) {
    out.push({ key: "workflowPanel.budget.wall", vars: { limit: formatWall(budget.wallMs) } });
  }
  return out;
}

function compactNumber(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n);
}

function formatWall(ms: number): string {
  const minutes = Math.round(ms / 60000);
  if (minutes >= 60) return `${Math.floor(minutes / 60)}h${minutes % 60 ? `${minutes % 60}m` : ""}`;
  if (minutes >= 1) return `${minutes}m`;
  return `${Math.round(ms / 1000)}s`;
}

/** 新建模板的起始文本：最小可运行骨架 + 五项配套字段示例（全部可删）。 */
export const NEW_TEMPLATE_SKELETON = `version: 1
name: my-workflow
description: describe what this workflow does
budget:
  tokens: 200000
  wall: 30m
nodes:
  - id: research
    type: subagent
    prompt: Investigate the question and report what you found.
    tools: [read_file, grep, glob, ls]
    timeout: 10m
    result:
      - {name: summary, type: string, description: one-paragraph answer}
  - id: check
    type: gate
    command: "git status --short"
    timeout: 60s
edges:
  - {from: research, to: check}
`;
