// 任务 856 ①：协作中心聊天视图。
//
// P1 骨架：群聊半边 = ChannelChatPanel 原样嵌入（802 复用面零回退）；409
// 观察窗（sessions 视图）按计划降级为本视图的辅助入口——「协作动态」次级
// 按钮弹出 CollabGroupPanel（embedded + sessionsOnly 形态），不再是顶层 tab
// （409 是观察窗，与「通道」语义不同级）。P3 在本视图加对话链（单聊）通道。
//
// 刷新机制零新增：ChannelChatPanel 自管 30s 轮询 + 退避；观察窗浮层
// （CollabGroupPanel）自管轮询，浮层关即卸载即停（409 同款零成本）。

import { useState } from "react";
import { ChannelChatPanel } from "./ChannelChatPanel";
import { CollabGroupPanel } from "./CollabGroupPanel";
import { useT } from "../lib/i18n";

export function CollabChatView() {
  const t = useT();
  // 409 观察窗降级形态：次级按钮弹出，关即卸载（切走视图/关 Hub 同样卸载）。
  const [activityOpen, setActivityOpen] = useState(false);

  return (
    <div className="collab-chat-view">
      <div className="collab-chat-view__tools">
        <span className="collab-chat-view__label">{t("collabHub.chatLabel")}</span>
        <span className="collab-chat-view__spacer" aria-hidden="true" />
        <button
          type="button"
          className={`btn btn--small${activityOpen ? " collab-chat-view__activitybtn--on" : ""}`}
          aria-expanded={activityOpen}
          onClick={() => setActivityOpen((v) => !v)}
        >
          {t("collabHub.activity")}
        </button>
      </div>
      <div className="collab-chat-view__body">
        <ChannelChatPanel />
      </div>
      {activityOpen && (
        // 协作动态浮层：观察窗内容（活跃协作会话聚合），只读（409 语义）。
        <div className="collab-chat-view__activity" role="region" aria-label={t("collabGroup.title")}>
          <div className="collab-chat-view__activityhead">
            <span className="collab-chat-view__activitytitle">{t("collabHub.activity")}</span>
            <button type="button" className="btn btn--small" onClick={() => setActivityOpen(false)}>
              {t("collabHub.closeActivity")}
            </button>
          </div>
          <CollabGroupPanel embedded sessionsOnly />
        </div>
      )}
    </div>
  );
}
