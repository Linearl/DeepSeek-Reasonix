// tl1441 ①：群聊消息流渲染共用件（ChannelChatPanel / ChannelWorkspacePanel
// 同用，单一实现杜绝双写）。
//
// 渲染契约与原 ChannelChatPanel 内联实现逐行同价（802/856/tl1406 口径不变）：
//   - 钉钉式标注：回执（delivered）不显示、已读（read）显示，且只出现在
//     自己的消息上；失败行是诚实错误仍显示；
//   - 撤回墓碑（349 Cancel 语义）；空流诚实空态；
//   - 气泡内 @名字 渲染为「查看对话链」快捷入口（onMentionClick 注入；
//     tab 工作区没有对话链通道时传 undefined = 纯文本渲染）；
//   - tl1564 派活卡：tl1406 确定性定向投递的消息以任务分配卡呈现
//     （谁→谁 + 任务摘要），判定依据见组件内 dispatchRecipientsOf 注释。
// 右键菜单本体留在宿主（portal + veil 的挂载点与视图相关）。

import { useEffect, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
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
  /**
   * tl1564：群聊 owner 类型（CollabChannelView.ownerType 透传）。仅
   * "human"（human-owned 群）启用 tl1406 副管理员默认收件人分支（Go
   * routeRecipients 分支3 前的分支2）；session-owned/无主群是 fan-out-all
   * 广播语义（Go 分支4），不是派活——缺省 undefined = 该分支不参与判定。
   */
  channelOwnerType?: string;
  /** tl1564：在册副管理员 contact id（CollabChannelView.deputyId 透传；缺省=无）。 */
  deputyId?: string;
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
  channelOwnerType,
  deputyId,
}: ChannelMessageStreamProps) {
  const t = useT();

  // ── tl1564 派活判定（dispatchRecipientsOf）─────────────────────────────
  // 镜像 Go routeRecipients（internal/collabchannel/collabchannel.go tl1406
  // 四分支，用户 20261105 裁决）中「确定性定向投递」的呈现面，按本组件现有
  // 数据结构（routedTo 注记 / memberDetails / directoryTitle）落地。判定依据
  // （优先级与 Go 一致）：
  //   ① m.routedTo 非空 = 发布期权威注记（Go Publish 只在返回值携带；@ 命中
  //     与副管理员默认两路投递都汇聚于此，发送者已被 Go 排除）→ 收件人=routedTo；
  //   ② 历史读回 routedTo 恒缺（Go 不落库）→ 按 body 解析 @ 提及兜底：
  //     needle = @显示名(directoryTitle) 与 @contactId——与 Go mentionedMembers
  //     的「@contactID/@alias」needle 家族、renderBody 的可见高亮同一匹配口径
  //     （所见高亮即所派；isNameByte 边界与最长 needle 语义由 Go 侧持有）。
  //     命中 ≥1（含仅 @ 自己）即进提及分支 → 收件人=提及成员−发送者（Go
  //     Publish 发送者自排除同款；仅 @ 自己=零投递=非派活）；
  //   ③ 无提及 + human-owned（channelOwnerType==="human"）+ deputy 在册
  //     （deputyId ∈ memberDetails，Go「离册副管理员降级零投递」同款）+
  //     发送者≠deputy → 收件人=副管理员（tl1406 分支2「副管理员=默认任务
  //     收件人」裁决；发送者=deputy 的无提及发言自派自收被 Go 发送者排除
  //     =零投递=非派活）；
  //   ④ 其余不渲染卡：deputyless 零投递（Go 分支3——已有零投递提示条承载）；
  //     session-owned/无主群 fan-out-all（Go 分支4 广播语义非派活）。
  // 返回 null = 普通气泡（非派活消息渲染零回归）。
  const dispatchRecipientsOf = (m: CollabChannelMessageView): string[] | null => {
    if (m.routedTo && m.routedTo.length > 0) return m.routedTo;
    if (m.body.includes("@")) {
      const mentioned: string[] = [];
      for (const mm of memberDetails) {
        const displayName = directoryTitle(mm.contact);
        const hit =
          (displayName && m.body.includes(`@${displayName}`)) ||
          m.body.includes(`@${mm.contact}`);
        if (hit && !mentioned.includes(mm.contact)) mentioned.push(mm.contact);
      }
      if (mentioned.length > 0) {
        const recipients = mentioned.filter((c) => c !== m.sender);
        // 仅 @ 自己（或仅 @ 其他发送者形态）= Go 发送者自排除后零投递 ——
        // 归一化为 null（普通气泡），绝不渲染空卡。
        return recipients.length > 0 ? recipients : null;
      }
    }
    if (
      channelOwnerType === "human" &&
      deputyId &&
      deputyId !== m.sender &&
      memberDetails.some((mm) => mm.contact === deputyId)
    ) {
      return [deputyId];
    }
    return null;
  };

  // 附件引用提取：空白分隔 token 以 @ 开头、归一化分隔符后落在
  // .reasonix/attachments/ 内、图片扩展名结尾；拒绝包含 .. 的 token（tl1421
  // 同款保守判据——这里只挑预览候选，真正的越界拒绝在 Go 读桥）。
  // tl1465：推入候选前剥掉 @ 前缀——预览读桥收裸路径（读桥侧 tl1465 也做
  // @ 规范化兜底，双端一致）；此前原样推入带 @ 的 token，绝对路径 @C:\…
  // 过不了 IsAbs 分支被当相对路径拒收，图片静默丢失 + WARN 刷屏。
  const attachmentRefsOf = (body: string): string[] => {
    if (!attachmentPreview || !body.includes("@")) return [];
    const out: string[] = [];
    for (const raw of body.split(/\s+/u)) {
      if (!raw.startsWith("@") || raw.length > 512) continue;
      const bare = raw.slice(1);
      const norm = bare.replace(/\\/gu, "/");
      if (norm.includes("..")) continue;
      if (!norm.toLowerCase().includes(".reasonix/attachments/")) continue;
      if (!/\.(?:png|jpe?g|gif|webp)$/iu.test(norm)) continue;
      if (!out.includes(bare)) out.push(bare);
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
        // tl1564：派活判定（判定依据见 dispatchRecipientsOf 注释）。非 null =
        // tl1406 确定性定向投递 → 气泡同位换装任务分配卡（徽标 + 谁→谁 +
        // 任务摘要），行结构/行meta/回执标注/右键撤回全部原样保留。
        const dispatchRecipients = dispatchRecipientsOf(m);
        const bubbleContextMenu = onBubbleContextMenu
          ? (event: ReactMouseEvent) => {
              event.preventDefault();
              onBubbleContextMenu({ messageId: m.id, sender: m.sender, x: event.clientX, y: event.clientY });
            }
          : undefined;
        // 卡体/气泡共用的消息体渲染（@提及快捷入口 + 附件缩略图契约原样）。
        const bodyContent = (
          <>
            {renderBody(m.body)}
            {attachmentPreview && attachmentRefsOf(m.body).length > 0 && (
              <span className="channel-chat__attpics">
                {attachmentRefsOf(m.body).map((path) => (
                  <ChannelAttachmentImage key={path} path={path} preview={attachmentPreview} />
                ))}
              </span>
            )}
          </>
        );
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
            {dispatchRecipients ? (
              <div
                className="channel-chat__dispatch"
                data-dispatch-to={dispatchRecipients.map((c) => directoryTitle(c)).join(", ")}
                onContextMenu={bubbleContextMenu}
              >
                <div className="channel-chat__dispatchhead">
                  <span className="channel-chat__dispatchbadge">{t("collabGroup.chat.dispatchBadge")}</span>
                  <span className="channel-chat__dispatchto">
                    {t("collabGroup.chat.dispatchTo", {
                      sender: own ? t("collabGroup.chat.you") : directoryTitle(m.sender),
                      executors: dispatchRecipients.map((c) => directoryTitle(c)).join(", "),
                    })}
                  </span>
                </div>
                <div className="channel-chat__dispatchbody">{bodyContent}</div>
              </div>
            ) : (
              <div className="channel-chat__bubble" onContextMenu={bubbleContextMenu}>
                {bodyContent}
              </div>
            )}
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
