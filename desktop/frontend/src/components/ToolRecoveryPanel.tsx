import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { RecoveryCall, ToolRecoveryBindings, ToolRecoverySnapshot } from "../lib/toolRecovery";
import { dismissToolRecoveryCall, dismissToolRecoveryCalls, isToolRecoveryDismissed, rekeyToolRecoveryDismissals, subscribeToolRecoveryDismissals, toolRecoveryDismissalRevision, toolRecoveryDismissalScope } from "../lib/toolRecoveryDismissalStore";
import "./ToolRecoveryPanel.css";

// Task 519（核实卡移除）：本面板不再是「中断的工具需要核实」交互卡片。人工
// 复核实际不靠谱（复核率低且曾长期阻塞）；482 已去掉拦截，本件撤掉检查/核实/
// 重试等全部交互——自动判定（433 无副作用白名单 + 519 构造期结算）把能判的
// 判掉，判不了的只记录：每条中断调用渲染为一行记录（可展开查看操作详情）。
// 记录事实与审计链（session 记录 + 事件留痕）不受影响；后端 ResolveToolRecovery
// 动作面保留供 serve API 兼容。
//
// Task 1061（忽略按钮）：519 撤掉的交互里唯独「忽略」按纯前端形态回归——历史
// 中断记录只是记录，用户不该为了清掉它再发起一轮对话。忽略是纯前端状态：不调
// ResolveToolRecovery（不触发新 turn、不改转录数据、不写会话事件日志）。
//
// Task 1068（1061 返修三修）：①忽略集合从组件 state 提升到模块级
// toolRecoveryDismissalStore（per-session，sessionPath 主键）——1061 靠
// Transcript key 重挂载隔离，同会话切 tab / turn 结束卸载重挂都会清零（用户
// 实测「切走再切回记录重现」）；②忽略按钮挂到每条记录的折叠摘要行右侧，不
// 展开详情即可点；③按钮本体复用全局 .btn--ghost.btn--small（与协作中心
// 「关闭」、SessionRecoveryBanner 详情钮同族）。store 不持久化，重启后记录
// 重现（1061 语义不变，任务书明示可接受）。
//
// Task 1089（1068 返修四修）：1068② 的「折叠态」钉错了层——它钉的是内层
// 「查看操作详情」折叠（每条记录自己的 details），而用户实测的「折叠」是外层
// 摘要块整体（本组件最外层 <details>）：外层收起时整块（含每条记录的忽略按钮）
// 都不可见，等于「看到折叠块却没有任何清理入口」。修法：外层 summary 行右侧
// 加一个「忽略（全部）」入口（.btn--ghost.btn--small 同族，按钮本体带
// tool-recovery-panel__dismiss-all 装饰类以与逐条钮区分），点击一次性忽略当前
// 快照的全部记录键；展开态逐条忽略按钮保留（每条记录 record-head 右侧，
// 1068 原 20 断言零回归）。语义边界与单条忽略一致：纯前端、per-session、
// 不持久化、零 binding 调用。
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
        if (generation.current === own) {
          // 快照到达：tab 键下的旧账（快照到达前点击忽略的极端时序）统一到
          // session 键（1068③ scope 一致性）。
          rekeyToolRecoveryDismissals(tabId, next.sessionPath);
          setSnapshot(next);
        }
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

  // 1068③：忽略集合读模块级 per-session store。快照未到达（snapshot null）时
  // scope 会退化成 tab 键——但此时 calls 为空、整块不渲染，不会出现「同一会话
  // 两种 scope 两种结果」的分裂：过滤只发生在快照到达后，scope 用 sessionPath。
  // 无论 snapshot 是不是同一个对象（探查可能每次返回新对象），同 sessionPath
  // 恒命中同一份忽略集合——「切 tab 忽略态不丢」的不变量在此成立。
  const dismissalScope = toolRecoveryDismissalScope(tabId, snapshot?.sessionPath);
  // 订阅模块级 store：点忽略 commit → 订阅方重渲染摘除该行（替代 1061 setState）。
  useSyncExternalStore(subscribeToolRecoveryDismissals, toolRecoveryDismissalRevision, toolRecoveryDismissalRevision);

  // Optional-chained on calls: a snapshot whose calls arrived as JSON null (a Go
  // nil slice) used to throw here and take the whole transcript down. Defensive on the
  // client because the payload is remote data. Dismissed records drop out of the
  // render (1061): none left → the whole fold-out goes away with them.
  const calls = snapshot?.calls?.filter((call, index) =>
    !isToolRecoveryDismissed(dismissalScope, dismissKey(call, index)));
  if (!calls?.length) return null;
  // 1089：外层摘要块折叠态的清理入口——外层 <details> 收起时，下方逐条记录
  // （含各自 record-head 的忽略按钮）整块不可见，用户「看到折叠块不想展开就
  // 清掉」的场景没有落点。把「忽略（全部）」放在 summary 行内、贴右侧：summary
  // 永远可见（它是折叠态唯一露出的部分），点击即一键忽略当前快照全部记录键。
  // summary 里的 <button> 点击会顺带切换 details 开合（浏览器默认行为），用
  // stopPropagation 阻断——用户点的是「忽略」，不是「展开」；忽略后 calls 为空
  // → 整块消失，展开态自然无从谈起。
  const dismissAllKeys = calls.map((call, index) => dismissKey(call, index));
  return <section className="notice-line tool-recovery-panel" aria-label={t("toolRecovery.title")}>
    <details>
    <summary className="notice-line__title tool-recovery-panel__summary">
      <span>{t("toolRecovery.title")}</span>
      <button type="button" className="btn btn--ghost btn--small tool-recovery-panel__dismiss-all" onClick={(event) => { event.stopPropagation(); dismissToolRecoveryCalls(dismissalScope, dismissAllKeys); }}>{t("toolRecovery.dismissAll")}</button>
    </summary>
    <div className="notice-line__text">
      {calls.map((call, index) => <div key={dismissKey(call, index)}>
        <div className="tool-recovery-panel__record-head">
          {/* tl1278（只读/写分级）：只读调用（read_only 来自中断记录，与工具
              登记表同源）不再挂「结果未知」警示——只读中断无外部副作用，文案
              直接说「可安全忽略」；写类保持原警示与忽略按钮不动。 */}
          <p>{call.identity.canonical_tool} · {call.read_only ? t("toolRecovery.readonlyInterrupted") : t("toolRecovery.unknown")}</p>
          {/* 1068①②：按钮本体复用全局 .btn--ghost.btn--small（591 圆角描边族，
              与协作中心「关闭」同族）；挂位在记录行右侧——折叠态（本行在折叠
              块外层 details 未展开时不可见）指的是记录详情 details 未展开时该
              忽略按钮仍可见可点，不需先展开「查看操作详情」。 */}
          <button type="button" className="btn btn--ghost btn--small tool-recovery-panel__dismiss" onClick={() => { dismissToolRecoveryCall(dismissalScope, dismissKey(call, index)); }}>{t("toolRecovery.dismiss")}</button>
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
