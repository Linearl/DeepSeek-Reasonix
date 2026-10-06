import { useEffect, useRef, useState } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { ToolRecoveryBindings, ToolRecoverySnapshot } from "../lib/toolRecovery";
import "./ToolRecoveryPanel.css";

// Task 519（核实卡移除）：本面板不再是「中断的工具需要核实」交互卡片。人工
// 复核实际不靠谱（复核率低且曾长期阻塞）；482 已去掉拦截，本件撤掉全部交互
// ——自动判定（433 无副作用白名单 + 519 构造期结算）把能判的判掉，判不了的
// 只记录：每条中断调用渲染为不可交互的一行记录（可展开查看操作详情），不提
// 供检查/核实/忽略/重试等任何按钮。记录事实与审计链（session 记录 + 事件留
// 痕）不受影响；后端 ResolveToolRecovery 动作面保留供 serve API 兼容。
export function ToolRecoveryPanel({ tabId, sessionKey, running, refreshKey, bindings = app }: {
  tabId: string; sessionKey: string; running: boolean; refreshKey: number;
  bindings?: ToolRecoveryBindings;
}) {
  const t = useT();
  const generation = useRef(0);
  const [snapshot, setSnapshot] = useState<ToolRecoverySnapshot | null>(null);
  useEffect(() => {
    const own = ++generation.current;
    setSnapshot(null);
    if (!running && bindings.GetToolRecoveryForTab) {
      void bindings.GetToolRecoveryForTab(tabId).then(next => {
        if (generation.current === own) setSnapshot(next);
      }).catch(() => {
        // A failed probe is not a failed recovery (task 103): the tab may be mid-switch, or its
        // controller mid-rebuild. Rendering an error for it produced a panel the user could only
        // dismiss — and this panel no longer asks the user to do anything at all.
      });
    }
    return () => { generation.current++; };
  }, [bindings, tabId, sessionKey, running, refreshKey]);

  // Optional-chained on calls: a snapshot whose calls arrived as JSON null (a Go
  // nil slice) used to throw here and take the whole transcript down. Defensive on the
  // client because the payload is remote data.
  if (!snapshot?.calls?.length) return null;
  return <section className="notice-line tool-recovery-panel" aria-label={t("toolRecovery.title")}>
    <details>
    <summary className="notice-line__title">{t("toolRecovery.title")}</summary>
    <div className="notice-line__text">
      {snapshot.calls.map(call => <div key={call.identity.attempt_id}>
        <p>{call.identity.canonical_tool} · {t("toolRecovery.unknown")}</p>
        <details><summary>{t("toolRecovery.details")}</summary>
          <p>{call.identity.resource_scope}</p>
          <p>{call.identity.argument_digest}</p>
        </details>
        {call.inspection_state && <p>{t(call.inspection_state === "present" || call.inspection_state === "postcondition_satisfied" ? "toolRecovery.present" : call.inspection_state === "absent_fenced" ? "toolRecovery.absent" : "toolRecovery.unproven")}</p>}
        {call.resolution === "reject" && <p>{t("toolRecovery.rejected")}</p>}
      </div>)}
    </div>
    </details>
  </section>;
}
