import { useEffect, useRef, useState } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { RecoveryCall, ToolRecoveryBindings, ToolRecoverySnapshot } from "../lib/toolRecovery";
import "./ToolRecoveryPanel.css";

// Task 519（核实卡移除）：本面板不再是「中断的工具需要核实」交互卡片。人工
// 复核实际不靠谱（复核率低且曾长期阻塞）；482 已去掉拦截，本件撤掉检查/核实/
// 重试等全部交互——自动判定（433 无副作用白名单 + 519 构造期结算）把能判的
// 判掉，判不了的只记录：每条中断调用渲染为一行记录（可展开查看操作详情）。
// 记录事实与审计链（session 记录 + 事件留痕）不受影响；后端 ResolveToolRecovery
// 动作面保留供 serve API 兼容。
//
// Task 1061（忽略按钮）：519 撤掉的交互里唯独「忽略」按纯前端形态回归——历史
// 中断记录只是记录，用户不该为了清掉它再发起一轮对话。忽略只改本组件内存里的
// per-session dismissed 集合：不调 ResolveToolRecovery（不触发新 turn、不改转录
// 数据、不写会话事件日志）；面板在 Transcript 里按 resolvedSessionKey 挂 key，
// 切会话即重挂载，集合天然按会话隔离；不持久化，重启后记录重现（任务书明示
// 可接受）。
export function ToolRecoveryPanel({ tabId, sessionKey, running, refreshKey, bindings = app }: {
  tabId: string; sessionKey: string; running: boolean; refreshKey: number;
  bindings?: ToolRecoveryBindings;
}) {
  const t = useT();
  const generation = useRef(0);
  const [snapshot, setSnapshot] = useState<ToolRecoverySnapshot | null>(null);
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(() => new Set());
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

  // Record identity: attempt_id is what the React key (and the backend audit
  // chain) already use; the index fallback keeps records without one still
  // individually dismissable instead of collapsing onto an undefined key.
  const dismissKey = (call: RecoveryCall, index: number) => call.identity.attempt_id ?? `#idx-${index}`;
  const dismiss = (key: string) => setDismissed(prev => {
    if (prev.has(key)) return prev;
    const next = new Set(prev);
    next.add(key);
    return next;
  });

  // Optional-chained on calls: a snapshot whose calls arrived as JSON null (a Go
  // nil slice) used to throw here and take the whole transcript down. Defensive on the
  // client because the payload is remote data. Dismissed records drop out of the
  // render (1061): none left → the whole fold-out goes away with them.
  const calls = snapshot?.calls?.filter((call, index) => !dismissed.has(dismissKey(call, index)));
  if (!calls?.length) return null;
  return <section className="notice-line tool-recovery-panel" aria-label={t("toolRecovery.title")}>
    <details>
    <summary className="notice-line__title">{t("toolRecovery.title")}</summary>
    <div className="notice-line__text">
      {calls.map((call, index) => <div key={dismissKey(call, index)}>
        <div className="tool-recovery-panel__record-head">
          <p>{call.identity.canonical_tool} · {t("toolRecovery.unknown")}</p>
          <button type="button" className="tool-recovery-panel__dismiss" onClick={() => dismiss(dismissKey(call, index))}>{t("toolRecovery.dismiss")}</button>
        </div>
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
