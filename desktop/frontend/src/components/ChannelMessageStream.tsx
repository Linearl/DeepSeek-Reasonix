// tl1441 ①：群聊消息流渲染共用件（ChannelChatPanel / ChannelWorkspacePanel
// 同用，单一实现杜绝双写）。
//
// 渲染契约与原 ChannelChatPanel 内联实现逐行同价（802/856/tl1406 口径不变）：
//   - 钉钉式标注：回执（delivered）不显示、已读（read）显示，且只出现在
//     自己的消息上；失败行是诚实错误仍显示；
//   - 撤回墓碑（349 Cancel 语义）；空流诚实空态；
//   - 气泡内 @名字 渲染为「查看对话链」快捷入口（onMentionClick 注入；
//     tab 工作区没有对话链通道时传 undefined = 纯文本渲染）。
// 右键菜单本体留在宿主（portal + veil 的挂载点与视图相关）。
//
// tl1565（G1 渲染统一）：附件引用解析与卡片渲染收口到 attachmentDisplay +
// MessageAttachments（与会话内同一套真相源）。此前本组件自写 attachmentRefsOf
// 正则（只认图片扩展名+.reasonix/attachments/ 相对形态）——非图片文件与
// 绝对路径引用退化成纯文本；现在 parseAttachmentRefsForDisplay 拆出的
// DisplayAttachment 直接喂 MessageAttachments，跨会话绝对路径的读侧分流
// （isChannelAttachmentRef → ChannelAttachmentDataURL 包含边界）由
// MessageAttachments.loadPreview 原样承载，本组件零附件逻辑。

import { lazy, Suspense, type ReactNode } from "react";
import type { CollabChannelMemberView, CollabChannelMessageView } from "../lib/bridge";
import { parseAttachmentRefsForDisplay } from "../lib/attachmentDisplay";
import { HUMAN_LOCAL } from "./ChannelChatPanel";
import { getLocale, useT } from "../lib/i18n";
// 任务1012：消息列表时间戳补日期——与消息气泡同一三档契约（lib/messageTimestamp.ts）。
import { formatMessageTimestamp } from "../lib/messageTimestamp";

// 任务1066 同款 lazy 模式：MessageAttachments 挂 ImageViewer（lightbox），
// 只在消息真带附件引用时才进包。
const MessageAttachments = lazy(() => import("./MessageAttachments").then((module) => ({ default: module.MessageAttachments })));

function formatTime(ms: number): string {
  if (!ms) return "";
  const date = new Date(ms);
  return formatMessageTimestamp(date, new Date(), getLocale());
}

export interface ChannelMessageStreamProps {
  messages: CollabChannelMessageView[];
  memberDetails: CollabChannelMemberView[];
  /** 名册显示名（title || purpose || contact id；human 端点=「我/You」）。 */
  directoryTitle: (contactId: string) => string;
  /** 气泡右键（own=撤回菜单；宿主决定菜单实现）。 */
  onBubbleContextMenu?: (event: { messageId: string; sender: string; x: number; y: number }) => void;
  /** @名字 点击（对话链快捷入口；缺省 = 纯文本渲染）。 */
  onMentionClick?: () => void;
}

export function ChannelMessageStream({
  messages,
  memberDetails,
  directoryTitle,
  onBubbleContextMenu,
  onMentionClick,
}: ChannelMessageStreamProps) {
  const t = useT();

  // tl1565（G1）：附件引用解析唯一入口 = lib/attachmentDisplay（与 Message.tsx
  // 会话内同一真相源）。这里只消费它拆出的 DisplayAttachment 列表；@提及
  // 高亮作用在剥除附件引用后的显示文本上，互不干扰。
  const renderBody = (body: string): ReactNode => {
    const { text, attachments } = body.includes("@")
      ? parseAttachmentRefsForDisplay(body)
      : { text: body, attachments: [] };
    if (!text.includes("@") && attachments.length === 0) return text;
    const parts: ReactNode[] = [];
    let rest = text;
    let key = 0;
    for (const m of memberDetails) {
      const name = m.contact === HUMAN_LOCAL ? t("collabGroup.chat.you") : directoryTitle(m.contact);
      const needle = `@${name}`;
      for (;;) {
        const idx = rest.indexOf(needle);
        if (idx < 0) break;
        if (idx > 0) parts.push(rest.slice(0, idx));
        parts.push(
          <span
            key={`m:${key++}`}
            className="channel-chat__mention"
            title={t("collabGroup.chat.mentionHint")}
            role="link"
            tabIndex={0}
            onClick={() => onMentionClick?.()}
            onKeyDown={(e) => { if (e.key === "Enter") onMentionClick?.(); }}
          >
            {needle}
          </span>,
        );
        rest = rest.slice(idx + needle.length);
      }
    }
    if (rest) parts.push(rest);
    if (attachments.length > 0) {
      parts.push(
        <Suspense key="attachments" fallback={null}>
          <MessageAttachments attachments={attachments} />
        </Suspense>,
      );
    }
    return parts;
  };

  return (
    <>
      {messages.length === 0 && (
        <div className="channel-chat__empty">{t("collabGroup.chat.emptyStream")}</div>
      )}
      {messages.map((m) => {
        const own = m.sender === HUMAN_LOCAL;
        if (m.cancelledAt) {
          return (
            <div key={m.id} className="channel-chat__tombstone">
              {t("collabGroup.chat.recalled", { sender: own ? t("collabGroup.chat.you") : directoryTitle(m.sender), time: formatTime(m.cancelledAt) })}
            </div>
          );
        }
        // 钉钉口径：已读显示、回执（delivered/queued）不显示；
        // 失败行是诚实错误，保留；标注只出现在自己的消息上。
        const readMembers = own
          ? (m.fanout ?? []).filter((cell) => cell.readAt)
          : [];
        const failedMembers = own
          ? (m.fanout ?? []).filter((cell) => cell.state === "failed")
          : [];
        return (
          <div key={m.id} className={`channel-chat__row${own ? " channel-chat__row--own" : ""}`}>
            <div className="channel-chat__rowmeta">
              <span className={`channel-chat__sender${own ? " channel-chat__sender--own" : ""}`}>
                {own ? t("collabGroup.chat.you") : directoryTitle(m.sender)}
              </span>
              <span className={`channel-chat__typebadge channel-chat__typebadge--${own ? "human" : "session"}`}>
                {own ? t("collabGroup.identity.human") : t("collabGroup.chat.typeSession")}
              </span>
              <span className="channel-chat__time" title={new Date(m.at).toLocaleString()}>{formatTime(m.at)}</span>
            </div>
            <div
              className="channel-chat__bubble"
              onContextMenu={onBubbleContextMenu ? (event) => {
                event.preventDefault();
                onBubbleContextMenu({ messageId: m.id, sender: m.sender, x: event.clientX, y: event.clientY });
              } : undefined}
            >
              {renderBody(m.body)}
            </div>
            {(readMembers.length > 0 || failedMembers.length > 0) && (
              <div className="channel-chat__receipts">
                {readMembers.map((cell) => (
                  <span key={`r:${cell.member}`} className="channel-chat__receipt channel-chat__receipt--read">
                    {t("collabGroup.chat.readBy", { member: directoryTitle(cell.member) })}
                  </span>
                ))}
                {failedMembers.map((cell) => (
                  <span key={`f:${cell.member}`} className="channel-chat__receipt channel-chat__receipt--failed">
                    {t("collabGroup.chat.failedTo", { member: directoryTitle(cell.member) })}
                  </span>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </>
  );
}
