import { useT } from "../lib/i18n";

// tl1541「已确认 / 知道了」：prompt 族决策卡（工具审批/计划审批/提问/mcp
// 交互）下的显式确认动作条。挂在 footer 决策区这一现有载体上，不新增独立
// 入口；非 prompt 类决策面（close_active / clear_context / workspace_conflict
// / 扩展表单）有自己的取消/确认语义，不渲染本条。
export type PromptAckSurface = "tool_approval" | "plan_approval" | "ask" | "mcp_interaction";

export function isPromptAckSurface(surface: string | null | undefined): surface is PromptAckSurface {
  return surface === "tool_approval" || surface === "plan_approval" || surface === "ask" || surface === "mcp_interaction";
}

export function DecisionAckRow({ surface, onAcknowledge }: { surface: string | null | undefined; onAcknowledge: () => void }) {
  const t = useT();
  if (!isPromptAckSurface(surface)) return null;
  return (
    <div className="decision-ack-row">
      <button
        type="button"
        className="btn btn--secondary btn--small"
        title={t("decision.acknowledgeDesc")}
        onClick={onAcknowledge}
      >
        {t("decision.acknowledge")}
      </button>
    </div>
  );
}
