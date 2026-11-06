// tl1441 ①：群详情抽屉抽取件（ChannelChatPanel / ChannelWorkspacePanel 共用）。
//
// 背景（升级口径）：群聊 tab 形态要求「群聊特有功能（详情/成员管理）放会话
// 区顶部栏位置承载」——tab 工作区（ChannelWorkspacePanel）与协作中心内嵌
// 聊天视图（ChannelChatPanel）都必须能挂这只抽屉。抽取 = 单一实现两处装配，
// 不养双写（验收 5 的收敛面）。
//
// 行为契约与原内嵌实现逐字节同价（DOM 类名不变，既有 harness 零感知）：
//   - 邀请框子状态（开关/勾选/检索）与两击解散武装态收进本组件内部；
//   - 点外关闭守卫留在宿主（类名选择器 .channel-chat__detail 不变）；
//   - 成员操作（移除/邀请/副管理员/退群/解散）全部经回调上抛，数据获取
//     （频道视图/通讯录）留在宿主——抽屉保持纯装配。
// tl1369 缺陷C：显式关闭钮（modal 家族 × 基件）沿用。

import { useEffect, useState } from "react";
import { ModalCloseButton } from "./ModalCloseButton";
import type { CollabChannelMemberView, CollabChannelView } from "../lib/bridge";
import type { CollabSessionDirectoryRow } from "./CollabInboxPanel";
import { HUMAN_LOCAL } from "./ChannelChatPanel";
import { useT } from "../lib/i18n";

function shortId(id: string): string {
  return id.length > 18 ? `${id.slice(0, 17)}…` : id;
}

export interface ChannelDetailDrawerProps {
  channel: CollabChannelView;
  memberDetails: CollabChannelMemberView[];
  /** 邀请候选通讯录（宿主负责加载；null=尚未加载，undefined=不可用）。 */
  directory?: CollabSessionDirectoryRow[] | null;
  directoryFailed?: boolean;
  /** 名册显示名（title || purpose || contact id；human 端点=「我/You」）。 */
  directoryTitle: (contactId: string) => string;
  iAmOwner: boolean;
  hasOwner: boolean;
  deputyBusy: boolean;
  onClose: () => void;
  onRemoveMember: (contactId: string) => void;
  /** 契约：成功 resolve（抽屉自关邀请框+清勾选）、失败 reject（保留原状）。 */
  onInvite: (contactIds: string[]) => void | Promise<void>;
  /**
   * tl1441：邀请框子状态受控——勾选清单/检索词/开关由宿主持有（抽屉可能
   * 随详情收起而卸载，重开必须不丢勾选=856⑥「勾选不丢失」口径跨卸载存活）。
   * 点外关闭守卫经 onInviteOpenChange(false) 回写宿主，行为与内聚实现同价。
   */
  inviteOpen: boolean;
  onInviteOpenChange: (open: boolean) => void;
  invitePicked: string[];
  onInvitePickedChange: (picked: string[]) => void;
  inviteQuery: string;
  onInviteQueryChange: (query: string) => void;
  onSetDeputy: (contactId: string) => void;
  onUnsetDeputy: () => void;
  onLeave: () => void;
  onDissolve: () => void;
  /** 打开邀请框时宿主补拉通讯录（tl1216 退避重练的入口）。 */
  onEnsureDirectory?: () => void;
}

/** 工作区路径的短名（末段）——层级浏览的分组标题（tl1216）。 */
function inviteWorkspaceLabel(root: string): string {
  const trimmed = root.replace(/[\\/]+$/, "").trim();
  if (!trimmed) return trimmed;
  const parts = trimmed.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || trimmed;
}

export function ChannelDetailDrawer({
  channel,
  memberDetails,
  directory = null,
  directoryFailed = false,
  directoryTitle,
  iAmOwner,
  hasOwner,
  deputyBusy,
  onClose,
  onRemoveMember,
  onInvite,
  onSetDeputy,
  onUnsetDeputy,
  onLeave,
  onDissolve,
  onEnsureDirectory,
  inviteOpen,
  onInviteOpenChange,
  invitePicked,
  onInvitePickedChange,
  inviteQuery,
  onInviteQueryChange,
}: ChannelDetailDrawerProps) {
  const t = useT();
  // tl1217：解散两击确认武装态（切群/关详情由宿主卸载本组件自然解除——
  // 武装态是「本次抽屉会话」的瞬时确认，不跨卸载存活）。
  const [dissolveArmed, setDissolveArmed] = useState(false);

  // 任务 856 ⑥：邀请框点外部区域自动关闭（tl1441 抽取后随抽屉内聚；原宿主
  // 实现逐行同价）。tl1242 house 形态：mousedown+click 双通道、捕获阶段、
  // closest 守卫——卡片内部点击豁免（勾选/检索不受影响），双通道重复置
  // false 幂等无副作用。tl1441 受控化：经 onInviteOpenChange 回写宿主。
  useEffect(() => {
    if (!inviteOpen) return;
    const isOutside = (event: MouseEvent) => {
      const target = event.target as Element | null;
      return !target?.closest(".channel-chat__invitebox");
    };
    const onDocMouseDown = (event: MouseEvent) => {
      if (isOutside(event)) onInviteOpenChange(false);
    };
    const onDocClick = (event: MouseEvent) => {
      if (isOutside(event)) onInviteOpenChange(false);
    };
    document.addEventListener("mousedown", onDocMouseDown, true);
    document.addEventListener("click", onDocClick, true);
    return () => {
      document.removeEventListener("mousedown", onDocMouseDown, true);
      document.removeEventListener("click", onDocClick, true);
    };
  }, [inviteOpen, onInviteOpenChange]);

  const isOwner = Boolean(channel.ownerId && channel.ownerId === HUMAN_LOCAL);

  // 邀请候选（tl1216）：先排除已在群成员；关键字匹配 title/purpose/topicId/
  // contactId（大小写不敏感；topicId 兜底 title 缺失/异名的会话）。
  const inviteCandidates = (directory ?? [])
    .filter((row) => !memberDetails.some((m) => m.contact === row.contactId))
    .filter((row) => {
      const q = inviteQuery.trim().toLowerCase();
      if (!q) return true;
      return `${row.title ?? ""} ${row.purpose ?? ""} ${row.topicId ?? ""} ${row.contactId}`.toLowerCase().includes(q);
    });

  // 层级逐步定位（tl1216 预期方式②）：空查询=浏览模式，按 全局 → 项目 →
  // 归档 分组（项目内按工作区名稳定排序），逐组扫读选人；有关键字=平铺命中。
  const inviteSections: { key: string; label: string; rows: CollabSessionDirectoryRow[] }[] = (() => {
    if (inviteQuery.trim()) return [];
    const global: CollabSessionDirectoryRow[] = [];
    const archive: CollabSessionDirectoryRow[] = [];
    const byProject = new Map<string, CollabSessionDirectoryRow[]>();
    for (const row of inviteCandidates) {
      if (row.archived) archive.push(row);
      else if (row.scope === "project") {
        const root = row.workspaceRoot || "";
        const bucket = byProject.get(root);
        if (bucket) bucket.push(row);
        else byProject.set(root, [row]);
      } else global.push(row);
    }
    const sections: { key: string; label: string; rows: CollabSessionDirectoryRow[] }[] = [];
    if (global.length) sections.push({ key: "global", label: t("collabGroup.chat.inviteScopeGlobal"), rows: global });
    for (const root of [...byProject.keys()].sort((a, b) => inviteWorkspaceLabel(a).localeCompare(inviteWorkspaceLabel(b)))) {
      const rows = byProject.get(root)!;
      sections.push({ key: root || "?", label: t("collabGroup.chat.inviteScopeProject", { workspace: inviteWorkspaceLabel(root) }), rows });
    }
    if (archive.length) sections.push({ key: "archive", label: t("collabGroup.chat.inviteScopeArchive"), rows: archive });
    return sections;
  })();

  // 候选行（检索平铺与层级分组共用）；key 兜底 sessionPath——未登记 contact_id
  // 的会话同现时避免重复 key。
  const inviteRow = (row: CollabSessionDirectoryRow) => (
    <label key={row.contactId || row.sessionPath} className="channel-chat__invitecandidate">
      <input
        type="checkbox"
        checked={invitePicked.includes(row.contactId)}
        onChange={(event) => {
          onInvitePickedChange(event.target.checked
            ? [...invitePicked, row.contactId]
            : invitePicked.filter((id) => id !== row.contactId));
        }}
      />
      <span className="channel-chat__invitecandidatename">{row.title || row.purpose || row.contactId}</span>
      <span className="channel-chat__invitecandidateid">{shortId(row.contactId)}</span>
    </label>
  );

  return (
    <div className="channel-chat__detail">
      <div className="channel-chat__detailhead">
        <span className="channel-chat__detailtitle">{channel.name}</span>
        {channel.ownerId && (
          <span className="channel-chat__detailowner">
            {t("collabGroup.chat.ownerLabel", {
              owner: channel.ownerId === HUMAN_LOCAL ? t("collabGroup.chat.you") : directoryTitle(channel.ownerId),
            })}
          </span>
        )}
        {/* tl1369 缺陷C：显式关闭钮（modal 家族 × 基件）。aria-label 零新键，
            复用既有 collabGroup.chat.closeDetail。 */}
        <ModalCloseButton
          className="channel-chat__detailclose"
          label={t("collabGroup.chat.closeDetail")}
          onClick={onClose}
        />
      </div>
      {channel.topic && <div className="channel-chat__detailtopic">{channel.topic}</div>}

      <div className="channel-chat__members">
        {memberDetails.map((m) => {
          const isHuman = m.type === "human";
          const isOwnerMember = Boolean(channel.ownerId && channel.ownerId === m.contact);
          const isDeputyMember = Boolean(channel.deputyId && channel.deputyId === m.contact);
          const removable = isOwner && hasOwner && m.contact !== HUMAN_LOCAL && !isOwnerMember;
          // 任务 856 ⑤：设为副管理员——两条件同现才渲染。
          const deputyAssignable = isOwner && hasOwner && !isHuman && !isOwnerMember && m.type === "session";
          return (
            <div key={m.contact} className="channel-chat__member">
              <span className="channel-chat__membername" title={m.contact === HUMAN_LOCAL ? undefined : directoryTitle(m.contact)}>
                {m.contact === HUMAN_LOCAL ? t("collabGroup.chat.you") : directoryTitle(m.contact)}
              </span>
              <span className={`channel-chat__typebadge channel-chat__typebadge--${isHuman ? "human" : "session"}`}>
                {isHuman ? t("collabGroup.identity.human") : t("collabGroup.chat.typeSession")}
              </span>
              {isOwnerMember && <span className="channel-chat__ownerbadge channel-chat__ownerbadge--me">{t("collabGroup.chat.ownerBadge")}</span>}
              {isDeputyMember && <span className="channel-chat__deputybadge">{t("collabGroup.chat.deputyBadge")}</span>}
              {deputyAssignable && !isDeputyMember && (
                <button type="button" className="btn btn--secondary btn--small" disabled={deputyBusy} onClick={() => onSetDeputy(m.contact)}>
                  {t("collabGroup.chat.setDeputy")}
                </button>
              )}
              {isDeputyMember && (
                <button type="button" className="btn btn--secondary btn--small" disabled={deputyBusy} onClick={() => onUnsetDeputy()}>
                  {t("collabGroup.chat.unsetDeputy")}
                </button>
              )}
              {removable && (
                <button type="button" className="btn btn--secondary btn--small" onClick={() => onRemoveMember(m.contact)}>
                  {t("collabGroup.chat.remove")}
                </button>
              )}
            </div>
          );
        })}
      </div>

      {iAmOwner && (
        <div className="channel-chat__invited">
          {!inviteOpen ? (
            <button type="button" className="btn btn--small" onClick={() => { onInviteOpenChange(true); onEnsureDirectory?.(); }}>
              {t("collabGroup.chat.invite")}
            </button>
          ) : (
            <div className="channel-chat__invitebox">
              <input
                className="channel-chat__invitequery"
                value={inviteQuery}
                placeholder={t("collabGroup.chat.inviteSearch")}
                onChange={(event) => onInviteQueryChange(event.target.value)}
              />
              <div className="channel-chat__invitecandidates">
                {!directory && directoryFailed && (
                  <div className="channel-chat__empty">{t("collabGroup.chat.inviteUnavailable")}</div>
                )}
                {directory && inviteCandidates.length === 0 && <div className="channel-chat__empty">{t("collabGroup.chat.inviteEmpty")}</div>}
                {inviteQuery.trim()
                  ? inviteCandidates.map(inviteRow)
                  : inviteSections.map((section) => (
                    <div key={section.key || "unknown"} className="channel-chat__invitegroup">
                      <div className="channel-chat__invitegrouphead">{section.label} · {section.rows.length}</div>
                      {section.rows.map(inviteRow)}
                    </div>
                  ))}
              </div>
              <div className="channel-chat__inviteactions">
                <button
                  type="button"
                  className="btn btn--small"
                  disabled={invitePicked.length === 0}
                  onClick={() => {
                    // 任务 856 ⑥原口径：邀请成功才关邀请框+清勾选；失败保留
                    // 原状（宿主错误条透出原因）。reject 已在此吞掉（错误面
                    // 由宿主承担），不产生 unhandledrejection。
                    void Promise.resolve(onInvite(invitePicked))
                      .then(() => { onInviteOpenChange(false); onInvitePickedChange([]); onInviteQueryChange(""); })
                      .catch(() => { /* 失败保留勾选原状 */ });
                  }}
                >
                  {t("collabGroup.chat.inviteConfirm", { n: invitePicked.length })}
                </button>
                <button type="button" className="btn btn--secondary btn--small" onClick={() => onInviteOpenChange(false)}>
                  {t("collabGroup.close")}
                </button>
                <span className="channel-chat__invitedismiss">{t("collabGroup.chat.inviteDismissHint")}</span>
              </div>
            </div>
          )}
        </div>
      )}

      <div className="channel-chat__detailfoot">
        <button type="button" className="btn btn--secondary btn--small" onClick={() => onLeave()}>
          {t("collabGroup.chat.leave")}
        </button>
        {/* tl1217：解散群聊——群主专用出口，两击确认防误触；第一击换通用
            危险钮色示警+确认文案，第二击执行。 */}
        {iAmOwner && hasOwner && (
          <button
            type="button"
            className={`btn btn--small ${dissolveArmed ? "btn--danger" : "btn--secondary"}`}
            onClick={() => {
              if (!dissolveArmed) {
                setDissolveArmed(true);
                return;
              }
              setDissolveArmed(false);
              onDissolve();
            }}
          >
            {dissolveArmed ? t("collabGroup.chat.dissolveConfirm") : t("collabGroup.chat.dissolve")}
          </button>
        )}
      </div>
    </div>
  );
}
