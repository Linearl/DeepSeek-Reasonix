// 任务 686 —— 工作流可视化运行面板：DAG 流程图 + 运行进展动态点亮。
//
// 双诉求映射（任务书口径）：
//   诉求②（结构）：主视图渲染 DAG 编排定义——运行前即可预览全图（计划视图），
//     节点=卡片（类型图标+名称）、边=依赖/数据流、phase 分组着色；
//   诉求①（进展）：运行中按事件流实时点亮节点（排队灰/运行中脉冲/完成绿/
//     unconfirmed 黄/失败红），顶部工具条=运行态+进度（n/N、耗时、token）+阶段。
// 下钻：点击节点看详情——指令全文/类型化结果/确认记录/门命令+exit+stderr/耗时。
//
// 形态：独立覆盖层面板（sessionWall 同款 backdrop + 懒加载 chunk），不新增 tab。
// 门控：整个组件只在 labFlag "workflowPanel" 开启时被 App 懒加载挂载（铁律 2）。
// 事件源：WorkflowRunStore（可插拔；685 落地前用 fixture 演示脚本 playFixtureRun）。

import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { GitBranch, Play, RotateCcw, Square, X } from "lucide-react";
import { useT } from "../lib/i18n";
import type { DictKey } from "../locales/en";
import { useMountTransition } from "../lib/useMountTransition";
import { reportFrontendLog } from "../lib/frontendLog";
import { layoutWorkflowDag, WORKFLOW_FIXTURE_DAG, WORKFLOW_NODE_HEIGHT, WORKFLOW_NODE_WIDTH, type WorkflowDag, type WorkflowDagNode } from "../lib/workflowDag";
import {
  playFixtureRun,
  workflowRunProgress,
  type WorkflowNodeState,
  type WorkflowRunStore,
  type WorkflowRunState,
} from "../lib/workflowEvents";

const NODE_STATE_CLASS: Record<WorkflowNodeState, string> = {
  queued: "wf-node--queued",
  running: "wf-node--running",
  done: "wf-node--done",
  unconfirmed: "wf-node--unconfirmed",
  failed: "wf-node--failed",
};

const RUN_STATE_KEY: Record<WorkflowRunState, DictKey> = {
  planned: "workflowPanel.run.planned",
  running: "workflowPanel.run.running",
  completed: "workflowPanel.run.completed",
  errored: "workflowPanel.run.errored",
  stopped: "workflowPanel.run.stopped",
};

function formatElapsed(ms?: number): string {
  if (ms === undefined) return "—";
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m${String(seconds % 60).padStart(2, "0")}s`;
}

function formatTokens(tokens?: number): string {
  if (!tokens) return "—";
  return tokens >= 1000 ? `${(tokens / 1000).toFixed(1)}k` : String(tokens);
}

/** 节点详情下钻面（四类信息 + 耗时/tokens；缺失段不渲染）。 */
function NodeDetailPane({ node, run }: { node: WorkflowDagNode; run: ReturnType<WorkflowRunStore["getSnapshot"]>["nodes"][string] }) {
  const t = useT();
  const detail = run.detail;
  const rows: Array<{ label: string; value: string; mono?: boolean }> = [];
  if (node.instruction) rows.push({ label: t("workflowPanel.detail.instruction"), value: node.instruction });
  if (node.command) rows.push({ label: t("workflowPanel.detail.command"), value: node.command, mono: true });
  if (detail?.gate) {
    rows.push({ label: t("workflowPanel.detail.exitCode"), value: String(detail.gate.exitCode ?? "—"), mono: true });
    if (detail.gate.stderr) rows.push({ label: t("workflowPanel.detail.stderr"), value: detail.gate.stderr, mono: true });
  }
  if (detail?.result) rows.push({ label: t("workflowPanel.detail.result"), value: detail.result });
  if (detail?.confirmation) {
    rows.push({
      label: t("workflowPanel.detail.confirmation"),
      value: `${detail.confirmation.confirmed ? t("workflowPanel.detail.confirmed") : t("workflowPanel.detail.unconfirmed")} · ${detail.confirmation.evidence}`,
    });
  }
  if (detail?.durationMs !== undefined) {
    rows.push({ label: t("workflowPanel.detail.duration"), value: formatElapsed(detail.durationMs) });
  }
  if (detail?.tokens !== undefined) {
    rows.push({ label: t("workflowPanel.detail.tokens"), value: formatTokens(detail.tokens) });
  }
  return (
    <div className="wf-panel__detail">
      <div className="wf-panel__detail-head">
        <span className={`wf-node-dot wf-node-dot--${run.state}`} aria-hidden="true" />
        <span className="wf-panel__detail-name">{node.name}</span>
        <span className="wf-panel__detail-kind">{t(`workflowPanel.kind.${node.kind}`)}</span>
      </div>
      {rows.length === 0 ? (
        <p className="wf-panel__detail-empty">{t("workflowPanel.detail.empty")}</p>
      ) : (
        rows.map((row) => (
          <div key={row.label} className="wf-panel__detail-row">
            <div className="wf-panel__detail-label">{row.label}</div>
            <div className={row.mono ? "wf-panel__detail-value wf-panel__detail-value--mono" : "wf-panel__detail-value"}>
              {row.value}
            </div>
          </div>
        ))
      )}
    </div>
  );
}

export function WorkflowPanel({
  open,
  onClose,
  dag = WORKFLOW_FIXTURE_DAG,
  store,
}: {
  open: boolean;
  onClose: () => void;
  dag?: WorkflowDag;
  store: WorkflowRunStore;
}) {
  const t = useT();
  const { mounted, status } = useMountTransition(open, 200);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const stopDemoRef = useRef<(() => void) | null>(null);
  const [demoPlaying, setDemoPlaying] = useState(false);

  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
  const layout = useMemo(() => layoutWorkflowDag(dag), [dag]);
  const nodeById = useMemo(() => new Map(dag.nodes.map((n) => [n.id, n])), [dag]);

  const progress = useMemo(() => workflowRunProgress(snapshot), [snapshot]);

  const stopDemo = useCallback(() => {
    stopDemoRef.current?.();
    stopDemoRef.current = null;
    setDemoPlaying(false);
  }, []);

  const startDemo = useCallback(() => {
    stopDemo();
    store.reset(dag);
    stopDemoRef.current = playFixtureRun(store);
    setDemoPlaying(true);
    reportFrontendLog("workflowPanel", "demo replay started", dag.id);
  }, [dag, store, stopDemo]);

  // Esc 关闭（document 级，焦点漂移也生效）；关闭时停掉演示脚本。
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  useEffect(() => {
    if (!open) stopDemo();
  }, [open, stopDemo]);

  useEffect(() => () => stopDemo(), [stopDemo]);

  // 演示脚本跑完自动收摊（completed/errored/stopped 都算一轮结束）。
  useEffect(() => {
    if (demoPlaying && snapshot.runState !== "running" && snapshot.runState !== "planned") {
      stopDemoRef.current = null;
      setDemoPlaying(false);
    }
  }, [demoPlaying, snapshot.runState]);

  const close = useCallback(() => {
    stopDemo();
    onClose();
  }, [onClose, stopDemo]);

  const onBackdropPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) close();
    },
    [close],
  );

  if (!mounted) return null;

  const selected = selectedId ? nodeById.get(selectedId) : undefined;
  const phases = dag.phases ?? [];

  return (
    <div className="drawer-backdrop wf-panel-backdrop" onPointerDown={onBackdropPointerDown} data-state={status}>
      <section className="wf-panel" role="dialog" aria-modal="true" aria-label={t("workflowPanel.title")}>
        <header className="wf-panel__toolbar">
          <div className="wf-panel__title">
            <GitBranch size={15} aria-hidden="true" />
            <span className="wf-panel__name">{dag.name}</span>
            <span className={`wf-panel__runstate wf-panel__runstate--${snapshot.runState}`}>
              {t(RUN_STATE_KEY[snapshot.runState])}
            </span>
          </div>
          <div className="wf-panel__progress" data-testid="wf-progress">
            <span>
              {t("workflowPanel.progress", { done: String(progress.done), total: String(progress.total) })}
            </span>
            <span>{t("workflowPanel.elapsed", { value: formatElapsed(progress.elapsedMs) })}</span>
            <span>{t("workflowPanel.tokens", { value: formatTokens(progress.tokens) })}</span>
            {progress.failed > 0 && (
              <span className="wf-panel__stat wf-panel__stat--failed">
                {t("workflowPanel.failed", { value: String(progress.failed) })}
              </span>
            )}
            {progress.unconfirmed > 0 && (
              <span className="wf-panel__stat wf-panel__stat--unconfirmed">
                {t("workflowPanel.unconfirmed", { value: String(progress.unconfirmed) })}
              </span>
            )}
          </div>
          <div className="wf-panel__actions">
            {snapshot.runState === "planned" && !demoPlaying && (
              <button type="button" className="wf-panel__action" onClick={startDemo}>
                <Play size={13} aria-hidden="true" />
                {t("workflowPanel.action.demo")}
              </button>
            )}
            {demoPlaying && (
              <button type="button" className="wf-panel__action" onClick={stopDemo}>
                <Square size={13} aria-hidden="true" />
                {t("workflowPanel.action.stop")}
              </button>
            )}
            <button type="button" className="wf-panel__action" onClick={() => store.reset(dag)}>
              <RotateCcw size={13} aria-hidden="true" />
              {t("workflowPanel.action.reset")}
            </button>
            <button type="button" className="wf-panel__action wf-panel__action--close" onClick={close} aria-label={t("common.close")}>
              <X size={14} aria-hidden="true" />
            </button>
          </div>
        </header>

        {phases.length > 0 && (
          <div className="wf-panel__legend">
            {phases.map((phase, index) => (
              <span key={phase} className="wf-panel__legend-item">
                <span className={`wf-panel__legend-swatch wf-phase-${index % 4}`} aria-hidden="true" />
                {phase}
              </span>
            ))}
            <span className="wf-panel__legend-sep" aria-hidden="true" />
            <span className="wf-panel__legend-item wf-panel__legend-item--state">
              <span className="wf-node-dot wf-node-dot--queued" aria-hidden="true" />
              {t("workflowPanel.state.queued")}
            </span>
            <span className="wf-panel__legend-item wf-panel__legend-item--state">
              <span className="wf-node-dot wf-node-dot--running" aria-hidden="true" />
              {t("workflowPanel.state.running")}
            </span>
            <span className="wf-panel__legend-item wf-panel__legend-item--state">
              <span className="wf-node-dot wf-node-dot--done" aria-hidden="true" />
              {t("workflowPanel.state.done")}
            </span>
            <span className="wf-panel__legend-item wf-panel__legend-item--state">
              <span className="wf-node-dot wf-node-dot--unconfirmed" aria-hidden="true" />
              {t("workflowPanel.state.unconfirmed")}
            </span>
            <span className="wf-panel__legend-item wf-panel__legend-item--state">
              <span className="wf-node-dot wf-node-dot--failed" aria-hidden="true" />
              {t("workflowPanel.state.failed")}
            </span>
          </div>
        )}

        <div className="wf-panel__body">
          <div className="wf-panel__canvas-scroll">
            <div className="wf-panel__canvas" style={{ width: layout.width, height: layout.height }}>
              <svg className="wf-panel__edges" width={layout.width} height={layout.height} aria-hidden="true">
                {layout.edges.map((edge) => {
                  const targetState = snapshot.nodes[edge.to]?.state ?? "queued";
                  const sourceState = snapshot.nodes[edge.from]?.state ?? "queued";
                  const lit = targetState === "running" || (sourceState === "done" && targetState === "done");
                  return (
                    <g key={`${edge.from}>${edge.to}`} className={lit ? "wf-edge wf-edge--lit" : "wf-edge"}>
                      <path d={edge.path} className={edge.back ? "wf-edge__path wf-edge__path--back" : "wf-edge__path"} />
                      {edge.label && edge.labelX !== undefined && edge.labelY !== undefined && (
                        <text className="wf-edge__label" x={edge.labelX} y={edge.labelY} textAnchor="middle">
                          {edge.label}
                        </text>
                      )}
                    </g>
                  );
                })}
              </svg>
              {layout.nodes.map((placement) => {
                const node = nodeById.get(placement.id);
                if (!node) return null;
                const run = snapshot.nodes[placement.id] ?? { state: "queued" as const };
                const phaseIndex = phases.indexOf(node.phase ?? "");
                const className =
                  phaseIndex >= 0
                    ? `wf-node ${NODE_STATE_CLASS[run.state]} wf-node--phase-${phaseIndex % 4}`
                    : `wf-node ${NODE_STATE_CLASS[run.state]}`;
                return (
                  <button
                    key={node.id}
                    type="button"
                    className={className}
                    style={{ left: placement.x, top: placement.y, width: WORKFLOW_NODE_WIDTH, height: WORKFLOW_NODE_HEIGHT }}
                    aria-pressed={selectedId === node.id}
                    onClick={() => setSelectedId((prev) => (prev === node.id ? null : node.id))}
                  >
                    <span className="wf-node__kind" aria-hidden="true">
                      {node.kind === "gate" ? "⌗" : node.kind === "join" ? "⋈" : "◈"}
                    </span>
                    <span className="wf-node__text">
                      <span className="wf-node__name">{node.name}</span>
                      {node.phase && <span className="wf-node__phase">{node.phase}</span>}
                    </span>
                    <span className="wf-node__spinner" aria-hidden="true" />
                  </button>
                );
              })}
            </div>
          </div>
          {selected && <NodeDetailPane node={selected} run={snapshot.nodes[selected.id] ?? { state: "queued" }} />}
        </div>
      </section>
    </div>
  );
}
