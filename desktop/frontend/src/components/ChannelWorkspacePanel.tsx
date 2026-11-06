// tl1441 ①：群聊 tab 工作区（ChannelWorkspacePanel）——「群聊 = 会话 tab 的
// 同型实体、数据源换 channel」的承载面。
//
// 升级口径（用户 20261106 00:35）：协作中心聊天视图「打开标签页」→ 标签栏
// 出现群聊 tab → tab 内复用会话工作区的形制：
//   - 顶部栏（会话区顶部栏位置）：返回列表 / 群名 / topic / 成员数 /
//     详情/成员管理（④：会话专属槽位——模型/effort/权限——此处天然不存在）；
//   - 消息流：channel 消息（ChannelMessageStream 共用件，与内嵌视图同渲染
//     契约——802 钉钉式标注/tl1406 墓碑/右键撤回全同价）；
//   - 输入区：①步落 textarea 过渡件（data-tl1441-composer 可测锚），
//     ②步换装 Composer 群聊模式（发送桥接 channel_send、draft 按 channel
//     分键、六件能力对齐）。
//
// 刷新机制与 ChannelChatPanel 同契约：挂载即拉尾窗 + COLLAB_GROUP_POLL_MS
// 轮询 + 失败错误条（任务587 同款：失败≠空态，旧数据优先）。
// 详情抽屉 = ChannelDetailDrawer 共用件（成员/邀请/副管理员/退群/解散）。

import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { app as appBridge } from "../lib/bridge";
import { ChannelDetailDrawer } from "./ChannelDetailDrawer";
import { ChannelMessageStream } from "./ChannelMessageStream";
import { HUMAN_LOCAL, type ChannelChatBindings } from "./ChannelChatPanel";
import { COLLAB_GROUP_POLL_MS } from "./CollabGroupPanel";
import type {
  CollabChannelMemberView,
  CollabChannelMessageView,
  CollabChannelView,
} from "../lib/bridge";
import type { CollabSessionDirectoryRow } from "./CollabInboxPanel";
import type { ComposerInsertRequest } from "../lib/types";
import type { QuickCommandEntry } from "../lib/settingsViewTypes";
import { Composer } from "./Composer";
import { useT } from "../lib/i18n";

const MESSAGE_TAIL_LIMIT = 50;

/** tab 工作区需要的绑定面（ ChannelChatBindings 的子集；测试可注入 mock）。 */
export type ChannelWorkspaceBindings = Pick<
  ChannelChatBindings,
  | "ChannelMessagesForHuman"
  | "ChannelSendFromHuman"
  | "ChannelCancelFromHuman"
  | "ChannelInvite"
  | "ChannelRemoveMember"
  | "ChannelSetDeputy"
  | "ChannelUnsetDeputy"
  | "ChannelLeaveHuman"
  | "ChannelDissolveForHuman"
  | "ListAddressableSessions"
> & {
  /** tl1441 ③：群聊附件预览读回（全局附件目录锚点；测试注入点）。 */
  ChannelAttachmentDataURL(path: string): Promise<string>;
};

export function ChannelWorkspacePanel({
  channelRef,
  fallbackName,
  bindings,
  onBackToList,
  onOpenSession,
  quickCommands,
  onManageQuickCommands,
}: {
  channelRef: string;
  /** 通讯录/尾窗加载前的群名兜底（打开链路带上，杜绝首帧空白）。 */
  fallbackName?: string;
  bindings?: ChannelWorkspaceBindings;
  /** 「返回列表」：激活态切回会话工作区（tab 保留），并回协作中心聊天视图。 */
  onBackToList?: () => void;
  /** 右键菜单「打开发信方会话」（App 层既有打开会话路由）。 */
  onOpenSession?: (contactId: string) => void;
  /** tl1441 ③：快捷指令（App 既有 settings 来源透传，与会话同源）。 */
  quickCommands?: QuickCommandEntry[];
  /** tl1441 ③：打开快捷指令管理（App 既有 manager 对话框）。 */
  onManageQuickCommands?: () => void;
}) {
  const t = useT();
  const b: ChannelWorkspaceBindings = bindings ?? appBridge;

  const [chat, setChat] = useState<{ channel: CollabChannelView; messages: CollabChannelMessageView[] } | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [deputylessHint, setDeputylessHint] = useState(false);
  // tl1441 ③：快捷指令插入经 Composer 的 insertRequest 通道（同会话面语义）。
  const [composerInsertRequest, setComposerInsertRequest] = useState<ComposerInsertRequest | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  // tl1441：邀请框子状态受控上提（跨抽屉卸载存活，856⑥「勾选不丢失」同口径）。
  const [inviteOpen, setInviteOpen] = useState(false);
  const [invitePicked, setInvitePicked] = useState<string[]>([]);
  const [inviteQuery, setInviteQuery] = useState("");
  const [deputyBusy, setDeputyBusy] = useState(false);
  const [directory, setDirectory] = useState<CollabSessionDirectoryRow[] | null>(null);
  const [directoryFailed, setDirectoryFailed] = useState(false);
  // 右键菜单（与 ChannelChatPanel 同语义：own=撤回；他人=打开发信方会话）。
  const [menu, setMenu] = useState<{ messageId: string; sender: string; x: number; y: number } | null>(null);

  const channelRefRef = useRef(channelRef);
  channelRefRef.current = channelRef;

  // 名册显示名：通讯录 title/purpose 优先，缺失降级 contact id（320 同款
  // 口径——绝不渲染空白；human 端点=「我/You」）。
  const directoryTitle = useCallback((contactId: string): string => {
    if (contactId === HUMAN_LOCAL) return t("collabGroup.chat.you");
    const row = directory?.find((r) => r.contactId === contactId);
    return row?.title || row?.purpose || contactId;
  }, [directory, t]);

  const refresh = useCallback(async () => {
    try {
      const tail = await b.ChannelMessagesForHuman(channelRefRef.current, 0, MESSAGE_TAIL_LIMIT);
      setChat(tail);
      setLoadError(false);
    } catch {
      // 频道读取失败不拖垮已渲染内容——保留旧尾窗，错误条如实声明。
      setLoadError(true);
    }
  }, [b]);

  // 挂载即拉 + 30s 轮询；ref 加载成功顺带喂通讯录缓存用目录（邀请候选）。
  useEffect(() => {
    setChat(null);
    setActionError(null);
    setDeputylessHint(false);
    setDetailOpen(false);
    setInviteOpen(false);
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, COLLAB_GROUP_POLL_MS);
    return () => window.clearInterval(timer);
  }, [refresh, channelRef]);

  // 通讯录：挂载即拉（成员名/邀请候选共用）；失败不按空目录缓存（重开抽屉
  // 时由 onEnsureDirectory 补拉）。
  const ensureDirectory = useCallback(async () => {
    if (directory) return;
    try {
      const rows = await b.ListAddressableSessions();
      setDirectoryFailed(false);
      setDirectory(Array.isArray(rows) ? rows : []);
    } catch {
      setDirectoryFailed(true);
    }
  }, [b, directory]);

  useEffect(() => { void ensureDirectory(); }, [ensureDirectory]);

  // 详情抽屉点外关闭（ChannelChatPanel 同款 house 形态：mousedown+click 双
  // 通道、捕获阶段、closest 守卫；豁免四处与内嵌视图一致——抽屉自身/触发
  // toggle/右键菜单+veil/modal-backdrop）。Esc 不在此接管（tab 工作区无
  // Hub 壳，但同款留三路关闭：点外/×/toggle）。
  useEffect(() => {
    if (!detailOpen) return;
    const isOutside = (event: MouseEvent) => {
      const target = event.target as Element | null;
      return !target?.closest(".channel-chat__detail, .channel-workspace__detailtoggle, .channel-chat__menu, .channel-chat__menuveil, .modal-backdrop");
    };
    const onDocMouseDown = (event: MouseEvent) => {
      if (isOutside(event)) setDetailOpen(false);
    };
    const onDocClick = (event: MouseEvent) => {
      if (isOutside(event)) setDetailOpen(false);
    };
    document.addEventListener("mousedown", onDocMouseDown, true);
    document.addEventListener("click", onDocClick, true);
    return () => {
      document.removeEventListener("mousedown", onDocMouseDown, true);
      document.removeEventListener("click", onDocClick, true);
    };
  }, [detailOpen]);

  // Esc/点击外部关闭右键菜单（CollabChatView 同款）。
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") close(); };
    document.addEventListener("click", close);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("click", close);
      document.removeEventListener("keydown", onKey);
    };
  }, [menu]);

  // tl1441 ②：发送桥接——Composer 的 onSend 注入 channel_send。submitText
  // 含附件 @路径 引用串（composer 提交路径组装），body 取 submitText ?? displayText；
  // 清 draft 由 Composer 持久化层接管（sessionKey 分键，channel 各自独立）。
  const sendingRef = useRef(false);
  const sendViaComposer = useCallback(async (_displayText: string, submitText?: string) => {
    const body = (submitText ?? _displayText).trim();
    if (!body || sendingRef.current) return;
    sendingRef.current = true;
    setActionError(null);
    try {
      const msg = await b.ChannelSendFromHuman(channelRefRef.current, body);
      // tl1406：零投递分支如实提示（后端路由注记，非本地猜测）。
      setDeputylessHint(Boolean(msg.deputyless));
      // 乐观上屏 + 立刻对账（轮询刷新已读标注）。
      setChat((prev) => {
        if (!prev) return prev;
        if (prev.messages.some((m) => m.id === msg.id)) return prev;
        return { ...prev, messages: [...prev.messages, msg] };
      });
      await refresh();
    } catch (err) {
      setActionError(String(err)); // 小时上限等风暴治理错误如实透出
    } finally {
      sendingRef.current = false;
    }
  }, [b, refresh]);

  // tl1441 ③：快捷指令插入（Composer insertRequest 通道，光标处插入）。
  const insertQuickCommand = useCallback((text: string) => {
    if (!text) return;
    setComposerInsertRequest({ id: Date.now(), text, mode: "insert" });
  }, []);

  // tl1441 ③：改写草稿上下文=群尾窗（发送方：body 行，human 行=「我」）——
  // 与会话链（HistoryPageForTab）同语义的群聊版；档位裁剪在 Composer 侧。
  const buildRewriteTail = useCallback(async (): Promise<string> => {
    const current = chat?.messages ?? [];
    const lines = current
      .slice(-8)
      .map((m) => `${directoryTitle(m.sender)}：${m.body}`);
    return lines.join("\n");
  }, [chat, directoryTitle]);

  // tl1465：附件预览读回回调引用稳定化——ChannelAttachmentImage 的 effect 以
  // [path, preview] 为依赖，内联箭头每渲染换身份会让宿主任意重渲染都重发读桥
  // 调用，读失败时形成 WARN 刷屏循环（与 ChannelChatPanel 同修一处机制）。
  const channelAttachmentPreview = useCallback(
    (path: string) => b.ChannelAttachmentDataURL(path),
    [b],
  );

  const cancelMessage = useCallback(async (messageId: string) => {
    setMenu(null);
    setActionError(null);
    try {
      await b.ChannelCancelFromHuman(channelRefRef.current, messageId);
      setChat((prev) => prev
        ? { ...prev, messages: prev.messages.map((m) => (m.id === messageId ? { ...m, cancelledAt: Date.now() } : m)) }
        : prev);
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, refresh]);

  const leave = useCallback(async () => {
    setActionError(null);
    try {
      await b.ChannelLeaveHuman(channelRefRef.current);
      setDetailOpen(false);
      onBackToList?.(); // 退群成功 = 该 tab 的实体已退出，回列表入口
      await refresh();
    } catch (err) {
      // 群主退群被拒（803：最后一个群主退出拒绝）——文案原样透出。
      setActionError(String(err));
    }
  }, [b, onBackToList, refresh]);

  const dissolve = useCallback(async () => {
    setActionError(null);
    try {
      await b.ChannelDissolveForHuman(channelRefRef.current);
      setDetailOpen(false);
      onBackToList?.(); // 解散成功 = 群已不存在，回列表入口
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, onBackToList, refresh]);

  const removeMember = useCallback(async (contactId: string) => {
    setActionError(null);
    try {
      const updated = await b.ChannelRemoveMember(channelRefRef.current, contactId);
      setChat((prev) => (prev ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, refresh]);

  const invite = useCallback(async (picked: string[]) => {
    if (picked.length === 0) return;
    setActionError(null);
    try {
      const updated = await b.ChannelInvite(channelRefRef.current, picked);
      setChat((prev) => (prev ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
      throw err; // 抽屉契约：失败 reject（勾选保留），错误条在此透出
    }
  }, [b, refresh]);

  const setDeputy = useCallback(async (contactId: string) => {
    if (deputyBusy) return;
    setDeputyBusy(true);
    setActionError(null);
    try {
      const updated = await b.ChannelSetDeputy(channelRefRef.current, contactId);
      setChat((prev) => (prev ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    } finally {
      setDeputyBusy(false);
    }
  }, [b, deputyBusy, refresh]);

  const unsetDeputy = useCallback(async () => {
    if (deputyBusy) return;
    setDeputyBusy(true);
    setActionError(null);
    try {
      const updated = await b.ChannelUnsetDeputy(channelRefRef.current);
      setChat((prev) => (prev ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    } finally {
      setDeputyBusy(false);
    }
  }, [b, deputyBusy, refresh]);

  const openCh = chat?.channel ?? null;
  const messages = chat?.messages ?? [];
  const displayName = openCh?.name || fallbackName || channelRef;
  const iAmOwner = Boolean(openCh?.ownerId && openCh.ownerId === HUMAN_LOCAL);
  const hasOwner = Boolean(openCh?.ownerId); // 无主老群：隐藏全部管理入口（803 需知③）
  const memberDetails: CollabChannelMemberView[] = openCh?.memberDetails
    ?? (openCh?.members ?? []).map((contact) => ({ contact, type: "session" as const, joinedAt: 0 }));

  return (
    <div className="channel-workspace" data-channel-ref={channelRef} aria-label={t("channelWorkspace.title")}>
      {/* 顶部栏（会话区顶部栏位置承载群聊特有功能——升级口径③）：返回列表/
          群名/topic/成员数/详情管理。 */}
      <div className="channel-workspace__topbar">
        <button
          type="button"
          className="channel-workspace__backlist"
          onClick={() => onBackToList?.()}
          aria-label={t("collabGroup.chat.back")}
          title={t("collabGroup.chat.back")}
        >
          ‹
        </button>
        <span className="channel-workspace__name" title={displayName}>{displayName}</span>
        {openCh?.topic && <span className="channel-workspace__topic" title={openCh.topic}>{openCh.topic}</span>}
        <span className="channel-workspace__meta">{t("collabGroup.membersCount", { n: openCh?.members?.length ?? 0 })}</span>
        {openCh && (
          <button
            type="button"
            className={`btn btn--small channel-workspace__detailtoggle${detailOpen ? " channel-workspace__detailtoggle--on" : ""}`}
            onClick={() => setDetailOpen((v) => !v)}
          >
            {detailOpen ? t("collabGroup.chat.closeDetail") : t("collabGroup.chat.detail")}
          </button>
        )}
      </div>

      <div className="channel-workspace__body">
        {!openCh && !loadError && (
          <div className="channel-workspace__placeholder">{t("collabGroup.loading")}</div>
        )}

        {loadError && (
          <div className="channel-chat__actionerror channel-workspace__errorstrip" role="alert">
            <span>{t("collabGroup.error.stale")}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>{t("collabGroup.retry")}</button>
          </div>
        )}

        {openCh && actionError && (
          <div className="channel-chat__actionerror" role="alert">
            <span>{actionError}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => setActionError(null)}>{t("collabGroup.close")}</button>
          </div>
        )}

        {/* tl1406：零投递提示——与内嵌视图同源（后端回执注记驱动）。 */}
        {openCh && deputylessHint && (
          <div className="channel-chat__deputyless" role="status">
            <span>{t("collabGroup.chat.deputylessHint")}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => setDeputylessHint(false)}>{t("collabGroup.close")}</button>
          </div>
        )}

        {openCh && (
          <>
            <div className="channel-workspace__stream">
              <ChannelMessageStream
                messages={messages}
                memberDetails={memberDetails}
                directoryTitle={directoryTitle}
                onBubbleContextMenu={(point) => setMenu(point)}
                attachmentPreview={channelAttachmentPreview}
              />
            </div>

            {/* 输入区：tl1441 ② 换装 Composer 群聊模式（同一组件双态的载体）。
                发送桥接 onSend→ChannelSendFromHuman；draft 按 sessionKey=
                channel:<ref> 分键（composer 既有 draftKey 惯例）；channelMode
                结构性隐藏模型/effort/权限三档（④口径）。 */}
            <div className="channel-workspace__composer" data-tl1441-composer="channel-mode">
              <Composer
                running={false}
                collaborationMode="normal"
                toolApprovalMode="ask"
                channelMode
                modelLabel=""
                sessionKey={`channel:${channelRef}`}
                tabId={undefined}
                ready
                imageInputEnabled
                audioInputEnabled={false}
                attachmentInputEnabled
                historyPickerEnabled
                quickCommands={quickCommands}
                onInsertQuickCommand={insertQuickCommand}
                onManageQuickCommands={onManageQuickCommands}
                insertRequest={composerInsertRequest}
                onSend={sendViaComposer}
                rewriteContextProvider={buildRewriteTail}
                splitTarget="primary"
                // 会话专属回调：channelMode 下控件结构性隐藏，回调不可达——
                // 传 no-op 仅满足必填签名，不承载任何会话语义。
                onCancel={() => Promise.resolve({
                  receipt: { id: "", status: "rejected", reason: "unreachable" },
                  restoredText: "",
                  restoredQueuedIDs: [],
                } as never)}
                onCycleMode={() => undefined}
                onSetMode={() => undefined}
                onSetCollaborationMode={() => undefined}
                onSetToolApprovalMode={() => undefined}
                onToggleYoloApprovalMode={() => Promise.resolve(true)}
                onClearGoal={() => undefined}
                onPauseGoal={() => undefined}
                onResumeGoal={() => undefined}
                onSwitchModel={() => true}
                onSetEffort={() => undefined}
              />
            </div>
          </>
        )}
      </div>

      {/* 详情抽屉（升级口径③：详情/成员管理放会话区顶部栏位置承载）——
          共用件 ChannelDetailDrawer，行为与内嵌视图同价。 */}
      {detailOpen && openCh && (
        <ChannelDetailDrawer
          channel={openCh}
          memberDetails={memberDetails}
          directory={directory}
          directoryFailed={directoryFailed}
          directoryTitle={directoryTitle}
          iAmOwner={iAmOwner}
          hasOwner={hasOwner}
          deputyBusy={deputyBusy}
          onClose={() => setDetailOpen(false)}
          onRemoveMember={(contactId) => void removeMember(contactId)}
          onInvite={(picked) => invite(picked)}
          onSetDeputy={(contactId) => void setDeputy(contactId)}
          onUnsetDeputy={() => void unsetDeputy()}
          onLeave={() => void leave()}
          onDissolve={() => void dissolve()}
          onEnsureDirectory={() => void ensureDirectory()}
          inviteOpen={inviteOpen}
          onInviteOpenChange={setInviteOpen}
          invitePicked={invitePicked}
          onInvitePickedChange={setInvitePicked}
          inviteQuery={inviteQuery}
          onInviteQueryChange={setInviteQuery}
        />
      )}

      {/* 右键菜单：own=撤回（349 Cancel 墓碑语义）；他人=打开发信方会话 +
          收信方（群）提示项——与 ChannelChatPanel 同语义。 */}
      {menu && createPortal(
        <div className="channel-chat__menu" style={{ left: menu.x, top: menu.y }} role="menu">
          {menu.sender === HUMAN_LOCAL ? (
            <button
              type="button"
              role="menuitem"
              onClick={() => { setMenu(null); void cancelMessage(menu.messageId); }}
            >
              {t("collabGroup.chat.recall")}
            </button>
          ) : (
            <>
              {onOpenSession && (
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => { setMenu(null); onOpenSession(menu.sender); }}
                >
                  {t("collabHub.directs.ctxSender", { name: directoryTitle(menu.sender) })}
                </button>
              )}
              <button
                type="button"
                role="menuitem"
                className="channel-chat__menuitem--muted"
                onClick={() => setMenu(null)}
                title={t("collabHub.directs.ctxGroupSelf")}
              >
                {t("collabHub.directs.ctxRecipientGroup")}
              </button>
            </>
          )}
        </div>,
        document.body,
      )}
      {menu && (
        <div className="channel-chat__menuveil" onContextMenu={(event) => { event.preventDefault(); setMenu(null); }} onClick={() => setMenu(null)} />
      )}
    </div>
  );
}
