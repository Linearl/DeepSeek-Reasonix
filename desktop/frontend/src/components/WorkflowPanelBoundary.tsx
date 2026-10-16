import { Component, type ReactNode } from "react";
import { reportCrash } from "../lib/crash";

// 任务 686：工作流面板是实验覆盖层（SessionWallBoundary 的 627 先例同款
// 语义）——渲染期异常只带走面板本身，绝不让整个窗口白屏；崩溃进
// desktop.log（feature=react:workflow-panel 可归因）。
export class WorkflowPanelBoundary extends Component<{ children: ReactNode }, { crashed: boolean }> {
  state = { crashed: false };

  static getDerivedStateFromError() {
    return { crashed: true };
  }

  componentDidCatch(error: unknown, info: { componentStack?: string | null }) {
    reportCrash("react:workflow-panel", error, info.componentStack ?? undefined);
  }

  render() {
    return this.state.crashed ? null : this.props.children;
  }
}
