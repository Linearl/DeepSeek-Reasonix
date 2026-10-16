// 任务 686 —— 工作流运行态 store 的 React 挂载点。
//
// store 本体在 lib/workflowEvents（纯模块、可插拔事件源）；本 hook 只做
// 「模块级单例 + 懒建」：App 挂一次，WorkflowPanel 经 useSyncExternalStore
// 订阅。685 编排引擎落地后，真实事件源（bridge onEvent 的 workflow 事件）
// 在 App 侧 dispatch 进同一 store——面板零改动。
//
// 以 fixture DAG 为底（685 未落地的降级路径，handoff-686.md 等待点②）。

import { useMemo } from "react";
import { WORKFLOW_FIXTURE_DAG } from "../lib/workflowDag";
import { createWorkflowRunStore, type WorkflowRunStore } from "../lib/workflowEvents";

/** 建一次、全局一份：App 根部调用；面板开合不重建运行态。 */
export function useWorkflowRunStore(): WorkflowRunStore {
  return useMemo(() => createWorkflowRunStore(WORKFLOW_FIXTURE_DAG), []);
}
