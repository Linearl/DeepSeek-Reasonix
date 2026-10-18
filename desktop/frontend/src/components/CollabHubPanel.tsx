// 任务 856 ①：协作中心容器（壳与融合）。
//
// 信件中心（task 320 CollabInboxPanel）与群聊/协作视图（task 409/802）原本
// 是侧栏工具行上两个独立图标、两个独立面板——用户核心不满即此入口割裂。
// 本组件把它们融合为一个「协作中心」入口 + 面板壳：标题栏 + 信件/聊天双
// 视图切换（segmented control）+ 关闭。
//
//   - 底层视图（信件）= 原样嵌入 CollabInboxPanel（embedded 模式，壳层头部
//     由本组件承担）；顶层视图（聊天）= CollabChatView（P1 = ChannelChatPanel
//     群聊 + 409 观察窗降级为「协作动态」次级浮层；P3 扩展对话链通道）。
//   - 视图选择持久化 localStorage `collabHub.view`（与 802 未读记账同模式），
//     重开面板恢复上次视图。
//   - 外部打开信号重定向：本模块 import 时即注册 forwardCollabInboxOpen /
//     forwardCollabGroupOpen——setCollabInboxOpen(true)（设置页副入口、派单
//     通知等既有调用方）升级为「打开 Hub 并切到信件视图」，setCollabGroupOpen
//     同理切聊天视图；调用方零改动（856 计划 ① 风险面）。
//   - 开关关 = 入口按钮不渲染（802 零痕迹策略沿用；入口挂 collabHub 实验室
//     开关，前置依赖 sessionCollab——见 experimentTiers.LAB_FEATURE_DEPS）。
//   - 任务 980 ①：外点空白 / Esc 关闭（面板家族 mousedown 外点先例；自家
//     portal 浮层排除见组件内注释）。inboxBindings 是注入接缝（测试注入信件
//     fixture；生产不传 = 走真实 bridge，行为零变化）。

import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  CollabInboxPanel,
  forwardCollabInboxOpen,
  syncCollabInboxOpen,
} from "./CollabInboxPanel";
import type { CollabInboxBindings } from "./CollabInboxPanel";
import {
  forwardCollabGroupOpen,
  syncCollabGroupOpen,
} from "./CollabGroupPanel";
import { CollabChatView } from "./CollabChatView";
import { useT } from "../lib/i18n";

/** 聊天视图（P1 骨架 = 群聊 + 协作动态浮层；P3 挂对话链通道）。 */
export { CollabChatView };

export type CollabHubView = "mail" | "chat";

/** 视图持久化键（856 计划 ①钦定：collabHub.view）。 */
const VIEW_STORE_KEY = "collabHub.view";

function readStoredView(): CollabHubView {
  try {
    return window.localStorage.getItem(VIEW_STORE_KEY) === "chat" ? "chat" : "mail";
  } catch {
    return "mail";
  }
}

function writeStoredView(view: CollabHubView): void {
  try {
    window.localStorage.setItem(VIEW_STORE_KEY, view);
  } catch {
    // 私密模式等存储失败只影响「记住上次视图」，不阻塞面板。
  }
}

let panelOpen = false;
const openListeners = new Set<(open: boolean, view: CollabHubView) => void>();

export function isCollabHubOpen(): boolean {
  return panelOpen;
}

/**
 * 开/关协作中心。view 给定时同时落持久化（外部信号「切到对应视图」的实现位；
 * 已开面板重复打开也会应用新视图——设置页在聊天视图打开时点「打开信件中心」
 * 仍能切回信件）。
 */
export function setCollabHubOpen(next: boolean, view?: CollabHubView): void {
  if (view) writeStoredView(view);
  panelOpen = next;
  const current = readStoredView();
  for (const listener of openListeners) listener(next, current);
}

export function onCollabHubOpenChange(cb: (open: boolean, view: CollabHubView) => void): () => void {
  openListeners.add(cb);
  return () => openListeners.delete(cb);
}

// 外部信号重定向（856 计划 ①：老入口信号必须 grep 全量重定向，这里以转发缝
// 实现——调用方零改动、未挂 Hub 的独立渲染（既有测试）行为不变）。打开信号
// 带目标视图；关闭信号原样透传。
forwardCollabInboxOpen((next) => setCollabHubOpen(next, next ? "mail" : undefined));
forwardCollabGroupOpen((next) => setCollabHubOpen(next, next ? "chat" : undefined));

/** 任务 856 ①：Hub 开合同步两个子面板的模块 open（未读徽标刷新时机保持
 * 「面板开/合即刷」；子面板信号不再反向触发 forward）。 */
function publishSubPanelOpen(next: boolean): void {
  syncCollabInboxOpen(next);
  syncCollabGroupOpen(next);
}

export function CollabHubPanel({ inboxBindings }: { inboxBindings?: CollabInboxBindings } = {}) {
  const t = useT();
  const [open, setOpen] = useState(panelOpen);
  const [view, setViewState] = useState<CollabHubView>(readStoredView());
  const panelRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const off = onCollabHubOpenChange((next, current) => {
      publishSubPanelOpen(next);
      setOpen(next);
      setViewState(current);
    });
    return () => { off(); };
  }, []);

  // 任务 980 ①（用户验收实测）：外点空白 / Esc 关闭面板。检测模式照用面板家
  // 族既有先例（QuestionSearchPanel 的 mousedown 外点 + Esc；AnchoredPopover
  // 的 document click 同源）。两条排除规则与「点击即关闭」的语义同等重要：
  //   ① 面板内部（含 embedded 子面板后代）不算外部；
  //   ② 本面板子树 portal 到 body 的浮层（ConfirmDialog 家族的 .modal-backdrop
  //      与对话链右键菜单 .collab-chat-view__ctx）不算外部——它们 DOM 上不在
  //      面板容器内（portal 脱离），但语义上属于面板：点确认/取消/菜单项若
  //      同时触发外点关闭，浮层会被连带卸载（确认变取消、跳转失效）。
  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (panelRef.current?.contains(target)) return;
      if (target instanceof Element && target.closest(".modal-backdrop, .collab-chat-view__ctx")) return;
      setCollabHubOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setCollabHubOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const switchView = (next: CollabHubView) => {
    writeStoredView(next);
    setViewState(next);
  };

  if (!open) return null;

  return createPortal(
    <div ref={panelRef} className="collab-hub-panel" role="dialog" aria-label={t("collabHub.title")}>
      {/* 壳层头部：标题 + 双视图 segmented control（demo v2 ①）+ 关闭。 */}
      <div className="collab-hub-panel__head">
        <span className="collab-hub-panel__title">{t("collabHub.title")}</span>
        <div className="collab-hub-panel__views" role="tablist" aria-label={t("collabHub.viewGroup")}>
          <button
            type="button"
            role="tab"
            aria-selected={view === "mail"}
            className={`collab-hub-panel__view${view === "mail" ? " collab-hub-panel__view--on" : ""}`}
            onClick={() => switchView("mail")}
          >
            {t("collabHub.view.mail")}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={view === "chat"}
            className={`collab-hub-panel__view${view === "chat" ? " collab-hub-panel__view--on" : ""}`}
            onClick={() => switchView("chat")}
          >
            {t("collabHub.view.chat")}
          </button>
        </div>
        <button type="button" className="btn btn--small collab-hub-panel__close" onClick={() => setCollabHubOpen(false)}>
          {t("collabHub.close")}
        </button>
      </div>

      <div className="collab-hub-panel__body">
        {view === "mail"
          ? <CollabInboxPanel embedded bindings={inboxBindings} />
          : <CollabChatView />}
      </div>
    </div>,
    document.body,
  );
}
