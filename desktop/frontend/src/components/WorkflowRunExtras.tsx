// tl1454 —— 模板开关开启时工作流画布的运行附加面：失败横幅、节点详情附加
// 行、选中节点的附加信息投影。开关关闭时 WorkflowPanel 不调用这里任何东西。

import { useT } from "../lib/i18n";
import { reasonKey } from "../lib/workflowTemplates";
import type { WireWorkflowRun } from "../lib/workflowTypes";

/** 节点详情附加面（失败原因双轨 + 用量 + 契约提示 + 运行结束时未完成）。 */
export interface NodeTemplateExtras {
  reason?: string;
  detail?: string;
  tokens?: number;
  requests?: number;
  contract?: string;
  stale?: boolean;
}

/** 选中节点的附加信息投影（wire 节点 + 运行级失败定位 + 在途投影集合）。 */
export function templateExtrasFor(
  run: WireWorkflowRun | undefined,
  nodeId: string | undefined,
  stale: Set<string> | undefined,
): NodeTemplateExtras | undefined {
  if (!run || !nodeId) return undefined;
  const wire = run.nodes.find((n) => n.nodeId === nodeId);
  return {
    reason: wire?.reason,
    detail: run.failure && run.failure.nodeId === nodeId ? run.failure.detail : undefined,
    tokens: wire?.tokens,
    requests: wire?.requests,
    contract: wire?.contract,
    stale: stale?.has(nodeId),
  };
}

type Row = { label: string; value: string; mono?: boolean };

/** 附加行（顺序：未完成 → 原因人话 → 技术细节 → 用量 → 契约提示）。 */
export function templateExtraRows(extras: NodeTemplateExtras | undefined, t: ReturnType<typeof useT>, formatTokens: (n?: number) => string): Row[] {
  if (!extras) return [];
  const rows: Row[] = [];
  if (extras.stale) rows.push({ label: t("workflowPanel.detail.reason"), value: t("workflowPanel.detail.stopped") });
  if (extras.reason) {
    rows.push({ label: t("workflowPanel.detail.reason"), value: t(reasonKey(extras.reason)) });
    if (extras.detail) rows.push({ label: t("workflowPanel.templates.technical"), value: extras.detail, mono: true });
  }
  if (extras.tokens || extras.requests) {
    rows.push({
      label: t("workflowPanel.detail.usage"),
      value: t("workflowPanel.detail.usageValue", { tokens: formatTokens(extras.tokens), requests: String(extras.requests ?? 0) }),
    });
  }
  if (extras.contract) rows.push({ label: t("workflowPanel.detail.contract"), value: extras.contract, mono: true });
  return rows;
}

/** 运行失败横幅：节点 + 人话原因 + 技术细节（预算越线节点本身已完成，标题用「完成后停止」）。 */
export function RunFailureBanner({ failure }: { failure: NonNullable<WireWorkflowRun["failure"]> }) {
  const t = useT();
  const title = failure.nodeId
    ? t(failure.reason === "budget" ? "workflowPanel.failure.titleAfter" : "workflowPanel.failure.title", { node: failure.nodeId })
    : t("workflowPanel.failure.titleNoNode");
  return (
    <div className="wf-panel__failure" role="status" data-testid="wf-run-failure">
      <strong>{title}</strong>
      <span>{t(reasonKey(failure.reason))}</span>
      <span className="wf-panel__failure-tech">{t("workflowPanel.templates.technical")}: {failure.detail}</span>
    </div>
  );
}
