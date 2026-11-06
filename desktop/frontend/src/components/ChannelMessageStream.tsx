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

import { useEffect, useState, type ReactNode } from "react";
import type { CollabChannelMemberView, CollabChannelMessageView } from "../lib/bridge";
import { HUMAN_LOCAL } from "./ChannelChatPanel";
import { getLocale, useT } from "../lib/i18n";
// 任务1012：消息列表时间戳补日期——与消息气泡同一三档契约（lib/messageTimestamp.ts）。
import { formatMessageTimestamp } from "../lib/messageTimestamp";

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
  /**
   * tl1441 ③（验收 1「成员侧可见图」）：群聊附件预览读回——消息体 @引用里
   * 指向群聊附件目录的图片路径渲染为内嵌缩略图（多图逐张）；缺省=纯文本。
   */
  attachmentPreview?: (path: string) => Promise<string>;
  /** 气泡右键（own=撤回菜单；宿主决定菜单实现）。 */
  onBubbleContextMenu?: (event: { messageId: string; sender: string; x: number; y: number }) => void;
  /** @名字 点击（对话链快捷入口；缺省 = 纯文本渲染）。 */
  onMentionClick?: () => void;
}

/** 单张附件缩略图：读回成功才渲染，失败静默（气泡文本仍在）。 */
function ChannelAttachmentImage({ path, preview }: { path: string; preview: (path: string) => Promise<string> }) {
  const [dataUrl, setDataUrl] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    preview(path)
      .then((url) => { if (alive) setDataUrl(url); })
      .catch(() => { /* 预览失败=少一张图，不是错误条 */ });
    return () => { alive = false; };
  }, [path, preview]);
  if (!dataUrl) return null;
  return (
    <img
      className="channel-chat__attpic"
      src={dataUrl}
      alt={path}
      loading="lazy"
      data-attachment-path={path}
    />
  );
}

export function ChannelMessageStream({
  messages,
  memberDetails,
  directoryTitle,
  onBubbleContextMenu,
  onMentionClick,
  attachmentPreview,
}: ChannelMessageStreamProps) {
  const t = useT();

  // 附件引用提取：空白分隔 token 以 @ 开头、归一化分隔符后落在
  // .reasonix/attachments/ 内、图片扩展名结尾；拒绝包含 .. 的 token（tl1421
  // 同款保守判据——这里只挑预览候选，真正的越界拒绝在 Go 读桥）。
  const attachmentRefsOf = (body: string): string[] => {
    if (!attachmentPreview || !body.includes("@")) return [];
    const out: string[] = [];
    for (const raw of body.split(/\s+/u)) {
      if (!raw.startsWith("@") || raw.length > 512) continue;
      const norm = raw.slice(1).replace(/\\/gu, "/");
      if (norm.includes("..")) continue;
      if (!norm.toLowerCase().includes(".reasonix/attachments/")) continue;
      if (!/\.(?:png|jpe?g|gif|webp)$/iu.test(norm)) continue;
      if (!out.includes(raw)) out.push(raw);
    }
    return out;
  };

  const renderBody = (body: string): ReactNode => {
    if (!body.includes("@")) return body;
    const parts: ReactNode[] = [];
    let rest = body;
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
              {attachmentPreview && attachmentRefsOf(m.body).length > 0 && (
                <span className="channel-chat__attpics">
                  {attachmentRefsOf(m.body).map((path) => (
                    <ChannelAttachmentImage key={path} path={path} preview={attachmentPreview} />
                  ))}
                </span>
              )}
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
