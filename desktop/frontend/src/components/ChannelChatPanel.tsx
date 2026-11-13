// 任务 802 — 频道消息 tab 的微信式群聊视图（人机混合真群聊）。
//
// 三条口径（20261013 用户重申，收口验收面）：
// ① 渲染与钉钉群聊一致：回执（delivered）不显示、已读（read）显示——投递
//    途中的状态是系统噪音，只有「谁已读」对人类有信息量；失败行是诚实错误
//    仍显示。标注只出现在自己的消息上（别人的消息不渲染已读回执）。
// ② 与信件中心去重：本视图只承载 channel 真实消息流（ChannelMessagesForHuman
//    读口），不做收件箱复刻；预览走 ReadCollabChannel（409 读口，不结算 human
//    已读——列表预览不能吞掉未读语义）。
// ③ 使用门槛：真正创建/加入的群聊才有记录；空频道与空目录渲染诚实空态，
//    dev mock 同样不造假数据。
//
// 刷新机制（④）：复用 409 的刷新信号——开面板期间 30s 轮询（同一
// COLLAB_GROUP_POLL_MS 常量）+ 手动刷新 + 2/4/8/15s 失败退避（任务587 同款：
// 失败≠空态，旧数据优先 + 显式错误条）。打开中的频道每 tick 拉尾窗
// （Messages(sinceMs=0, limit=50)，SQL 语义=最新 50 条），已读标注因此每
// tick 全新——这是验收第 3 步「送达已读逐成员正确」的硬前提；未打开频道
// 零消息拉取，列表未读用 messages 总数差（localStorage 记账）。

import { useCallback, useEffect, useRef, useState, type ClipboardEvent as ReactClipboardEvent } from "react";
import { createPortal } from "react-dom";
import { app as appBridge } from "../lib/bridge";
// tl1465 D：群聊输入框粘图——@ 引用组装复用 refToken 同一语法层（escapeRefPath
// 处理路径空格），草稿持久化复用 composerDraftPersistence 同一存储（键=
// channel:<ref>，与群聊 tab 的 Composer 同一份草稿，跨面板切换粘图仍在）。
import { escapeRefPath } from "../lib/refToken";
import { loadPersistedComposerDraft, persistComposerDraft } from "../lib/composerDraftPersistence";
import type { CollabSessionDirectoryRow } from "./CollabInboxPanel";
// tl1441 ①：抽屉本体抽取为 ChannelDetailDrawer（群聊 tab 工作区同用一只，
// tl1369 缺陷C 显式关闭钮在抽取件内部）；打开标签页经 channelTabStore
// （App 层接线伪 tab 实体）；消息流渲染抽取为 ChannelMessageStream 共用件。
import { ChannelDetailDrawer } from "./ChannelDetailDrawer";
import { ChannelMessageStream } from "./ChannelMessageStream";
import { openChannelTab } from "../lib/channelTabStore";
import {
  COLLAB_GROUP_POLL_MS,
  collabGroupRetryDelaysMs,
} from "./CollabGroupPanel";
import type {
  CollabChannelCancelResultView,
  CollabChannelMemberView,
  CollabChannelMessageView,
  CollabChannelView,
} from "../lib/bridge";
import { useT } from "../lib/i18n";
// tl1439 修②：「上次打开的群」记忆落盘（读侧恢复由 CollabChatView 注入初值）。
import { writeCollabChatLastChannelId } from "../lib/collabChatMemory";

/** 803 定稿：桌面端单用户固定身份（collabchannel.HumanLocal）。 */
export const HUMAN_LOCAL = "human:local";

const MESSAGE_TAIL_LIMIT = 50;
const PREVIEW_LIMIT = 1;
const PREVIEW_MAX_CHANNELS = 24;

/** 未读记账的 localStorage 键（channelId → 上次已见的消息总数）。 */
const LAST_SEEN_STORE_KEY = "reasonix.channelChat.lastSeenCounts";

export type ChannelChatBindings = {
  ListCollabChannels(): Promise<CollabChannelView[]>;
  /** 预览专用：409 读口（不结算 human 已读），取每群最新 1 条做列表预览。 */
  ReadCollabChannel(ref: string, limit: number): Promise<{ channel: CollabChannelView; messages: CollabChannelMessageView[] }>;
  ChannelMessagesForHuman(ref: string, sinceMs: number, limit: number): Promise<{ channel: CollabChannelView; messages: CollabChannelMessageView[] }>;
  ChannelCreateForHuman(name: string, topic: string, hourlyLimit: number): Promise<CollabChannelView>;
  ChannelLeaveHuman(ref: string): Promise<void>;
  /** tl1217: 解散群聊（仅 human 群主；硬删成员/消息，前端两击确认防误触）。 */
  ChannelDissolveForHuman(ref: string): Promise<void>;
  ChannelSendFromHuman(ref: string, body: string): Promise<CollabChannelMessageView>;
  ChannelInvite(ref: string, contactIDs: string[]): Promise<CollabChannelView>;
  ChannelRemoveMember(ref: string, contactID: string): Promise<CollabChannelView>;
  /** 任务 856 ⑤: 任命/撤销单副管理员（human 群主口）。 */
  ChannelSetDeputy(ref: string, contactID: string): Promise<CollabChannelView>;
  ChannelUnsetDeputy(ref: string): Promise<CollabChannelView>;
  ChannelCancelFromHuman(ref: string, messageID: string): Promise<CollabChannelCancelResultView>;
  ListAddressableSessions(): Promise<CollabSessionDirectoryRow[]>;
  /** tl1441 ③：群聊附件预览读回（内嵌视图同样渲染成员侧图片；缺省=纯文本）。 */
  ChannelAttachmentDataURL?(path: string): Promise<string>;
  /** tl1465 D：群聊粘图落盘（全局工作区锚点，返回绝对路径；缺省=无粘图能力）。 */
  SaveChannelAttachment?(dataUrl: string): Promise<string>;
  /** tl1465 D：原生剪贴板图片（无文件载荷的 image-hint 粘贴）同锚全局目录。 */
  SaveChannelClipboardImage?(): Promise<string>;
};

/**
 * tl1465 D：群聊输入框粘图 chip——pending=落盘中的占位（即时反馈），落盘成功
 * 后补 path/previewUrl 变缩略图；发送时以 @<绝对路径> 并入消息体（与群聊 tab
 * Composer 提交链同一组装形态）。
 */
interface ChannelDraftChip {
  path?: string;
  previewUrl?: string;
  displayName?: string;
  pending: boolean;
}

function readLastSeenCounts(): Record<string, number> {
  try {
    const raw = window.localStorage.getItem(LAST_SEEN_STORE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !(parsed instanceof Array)) {
      const out: Record<string, number> = {};
      for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
        const n = Number(v);
        if (k && Number.isFinite(n) && n >= 0) out[k] = Math.trunc(n);
      }
      return out;
    }
  } catch {
    // 损坏的记账按全新处理（首见即初始化），绝不阻塞 UI。
  }
  return {};
}

function writeLastSeenCounts(counts: Record<string, number>): void {
  try {
    window.localStorage.setItem(LAST_SEEN_STORE_KEY, JSON.stringify(counts));
  } catch {
    // 私密模式等存储失败只影响未读徽标，不阻塞聊天。
  }
}

/** 工作区路径的短名（末段）——层级浏览的分组标题（tl1216）。 */
export function ChannelChatPanel({ bindings, onOpenSession, onViewDirectChains, initialOpenChannelId }: { bindings?: ChannelChatBindings; onOpenSession?: (contactId: string) => void; onViewDirectChains?: () => void; initialOpenChannelId?: string | null } = {}) {
  const t = useT();
  const b: ChannelChatBindings = bindings ?? appBridge;

  const [channels, setChannels] = useState<CollabChannelView[] | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [chat, setChat] = useState<{ channel: CollabChannelView; messages: CollabChannelMessageView[] } | null>(null);
  const [previews, setPreviews] = useState<Record<string, CollabChannelMessageView | undefined>>({});
  const [directory, setDirectory] = useState<CollabSessionDirectoryRow[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  // tl1406：零投递提示——human 群未设副管理员且未 @ 时发送成功但没人被投递，
  // 用后端回执的 deputyless 注记驱动（不猜群状态）；下一条消息/切群即清。
  const [deputylessHint, setDeputylessHint] = useState(false);
  const [draft, setDraft] = useState("");
  // tl1465 D：粘图 chip 队列（pending=落盘中的占位——即时反馈；成功后补
  // path/previewUrl 变缩略图）。与草稿同键 channel:<ref> 持久化，切群聊 tab
  // 后 Composer 冷启动恢复同一份（粘图不丢）。
  const [chips, setChips] = useState<ChannelDraftChip[]>([]);
  const [sending, setSending] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  // tl1441：邀请框子状态受控上提（勾选/检索跨抽屉卸载存活——856⑥「勾选
  // 不丢失」口径；开关随抽屉关闭回 false，重开显示邀请按钮，与原实现同价）。
  const [inviteOpen, setInviteOpen] = useState(false);
  const [invitePicked, setInvitePicked] = useState<string[]>([]);
  const [inviteQuery, setInviteQuery] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createTopic, setCreateTopic] = useState("");
  const [createLimit, setCreateLimit] = useState("");
  // 任务 856 ⑦：右键菜单扩展——own 消息=撤回（既有）；他人消息=打开发信方
  // 会话（onOpenSession 注入）+ 收信方（群）提示项。
  const [menu, setMenu] = useState<{ messageId: string; sender: string; x: number; y: number } | null>(null);
  // 任务 856 ⑤：副管理员操作（详情抽屉成员行）。
  const [deputyBusy, setDeputyBusy] = useState(false);
  // 任务 856 ⑥：@ 选人浮层（↑↓ 选择 / Enter 确认 / Esc 关闭）。
  const [atOpen, setAtOpen] = useState(false);
  const [atIndex, setAtIndex] = useState(0);

  // refs：轮询闭包里要读「当前打开的群」与最新退避状态，不作为渲染依赖。
  const openIdRef = useRef<string | null>(null);
  // tl1439 修②：「上次打开的群」待恢复记忆（挂载注入一次；恢复或判失即消费）。
  const pendingRestoreRef = useRef<string | null>(initialOpenChannelId ?? null);
  const retryRef = useRef<{ timer: ReturnType<typeof setTimeout> | null; attempt: number }>({ timer: null, attempt: 0 });
  const refreshRef = useRef<() => void>(() => {});
  const lastSeenRef = useRef<Record<string, number> | null>(null);

  const cancelRetry = useCallback(() => {
    if (retryRef.current.timer !== null) {
      clearTimeout(retryRef.current.timer);
      retryRef.current.timer = null;
    }
  }, []);

  const scheduleRetry = useCallback(() => {
    if (retryRef.current.timer !== null) return;
    const delays = collabGroupRetryDelaysMs;
    const delay = delays[Math.min(retryRef.current.attempt, delays.length - 1)];
    retryRef.current.timer = setTimeout(() => {
      retryRef.current.timer = null;
      retryRef.current.attempt += 1;
      refreshRef.current();
    }, delay);
  }, []);

  const markSeen = useCallback((channelId: string, count: number) => {
    if (!lastSeenRef.current) lastSeenRef.current = readLastSeenCounts();
    if (lastSeenRef.current[channelId] === count) return;
    lastSeenRef.current[channelId] = count;
    writeLastSeenCounts(lastSeenRef.current);
  }, []);

  const refresh = useCallback(async () => {
    setLoadError(null);
    try {
      const list = await b.ListCollabChannels();
      setChannels(list);
      // 首见的群按「已见当前条数」初始化记账——使用门槛口径：没有记录的
      // 老频道不凭空冒未读红点。
      if (!lastSeenRef.current) lastSeenRef.current = readLastSeenCounts();
      let seenChanged = false;
      for (const c of list) {
        if (!(c.id in lastSeenRef.current)) {
          lastSeenRef.current[c.id] = c.messages;
          seenChanged = true;
        }
      }
      if (seenChanged) writeLastSeenCounts(lastSeenRef.current);

      // tl1439 修②：「上次打开的群」恢复——群列表就绪后校验记忆目标仍在列表
      // 里才恢复打开；已被删除/解散/归档 → 丢弃记忆，回落默认列表视图（验收③：
      // 不报错不空白）。恢复在同 tick 尾窗拉取之前生效，打开群的消息一并就位；
      // 只消费一次（重试/轮询不重复代开）。
      const pendingRestore = pendingRestoreRef.current;
      if (pendingRestore && !openIdRef.current) {
        pendingRestoreRef.current = null;
        if (list.some((c) => c.id === pendingRestore || c.name === pendingRestore)) {
          setOpenId(pendingRestore);
          openIdRef.current = pendingRestore;
        }
      }

      // 打开中的频道：拉尾窗（最新 50 条），已读标注每 tick 全新；
      // 打开群的消息即视为已见（UI 可见即已读，803 ⑥在侧）。
      const opened = openIdRef.current;
      if (opened) {
        try {
          const tail = await b.ChannelMessagesForHuman(opened, 0, MESSAGE_TAIL_LIMIT);
          setChat(tail);
          markSeen(opened, tail.channel.messages);
        } catch (err) {
          // 频道读取失败不拖垮群列表——保留旧尾窗，错误条如实声明。
          setActionError(String(err));
        }
      }
      // 列表预览：每群最新 1 条（409 读口，不结算已读，保未读语义）。
      const previewTargets = list.slice(0, PREVIEW_MAX_CHANNELS);
      const nextPreviews: Record<string, CollabChannelMessageView | undefined> = {};
      await Promise.all(previewTargets.map(async (c) => {
        try {
          const preview = await b.ReadCollabChannel(c.id, PREVIEW_LIMIT);
          nextPreviews[c.id] = preview.messages[preview.messages.length - 1];
        } catch {
          nextPreviews[c.id] = undefined; // 预览失败只是少一行预览，不是错误
        }
      }));
      setPreviews(nextPreviews);
      retryRef.current.attempt = 0;
      cancelRetry();
    } catch (err) {
      setLoadError(String(err));
      scheduleRetry();
    }
  }, [b, cancelRetry, markSeen, scheduleRetry]);

  useEffect(() => {
    refreshRef.current = () => void refresh();
  }, [refresh]);

  // 409 同款：挂载即拉 + 30s 轮询；卸载（切 tab/关面板）即停，零成本。
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, COLLAB_GROUP_POLL_MS);
    return () => {
      window.clearInterval(timer);
      cancelRetry();
    };
  }, [refresh, cancelRetry]);

  // 通讯录加载（tl1216）：挂载即拉；失败不再按「空目录」缓存——旧实现把空
  // 数组当成已加载且永不重试，一次桥接抖动就把邀请面板钉死成「没有可邀请的
  // 会话」。现按任务587 同款 2/4/8/15s 退避重试，打开邀请框时也补拉一次；
  // 名牌在目录缺失期间回落 contact_id（既有口径不变）。
  const [directoryFailed, setDirectoryFailed] = useState(false);
  const directoryRetryRef = useRef<{ timer: ReturnType<typeof setTimeout> | null; attempt: number }>({ timer: null, attempt: 0 });
  const cancelDirectoryRetry = useCallback(() => {
    if (directoryRetryRef.current.timer !== null) {
      clearTimeout(directoryRetryRef.current.timer);
      directoryRetryRef.current.timer = null;
    }
  }, []);
  const ensureDirectory = useCallback(async () => {
    if (directory) return;
    try {
      const rows = await b.ListAddressableSessions();
      cancelDirectoryRetry();
      directoryRetryRef.current.attempt = 0;
      setDirectoryFailed(false);
      setDirectory(Array.isArray(rows) ? rows : []);
    } catch {
      setDirectoryFailed(true);
      if (directoryRetryRef.current.timer !== null) return; // 已有待执行重试
      const delays = collabGroupRetryDelaysMs;
      const delay = delays[Math.min(directoryRetryRef.current.attempt, delays.length - 1)];
      directoryRetryRef.current.attempt += 1;
      directoryRetryRef.current.timer = setTimeout(() => {
        directoryRetryRef.current.timer = null;
        ensureDirectoryRef.current();
      }, delay);
    }
  }, [b, directory, cancelDirectoryRetry]);

  // 任务587 同款：retry 回调经 ref 取最新闭包，重建 ensureDirectory 不叠定时器。
  const ensureDirectoryRef = useRef<() => void>(() => {});
  useEffect(() => { ensureDirectoryRef.current = () => void ensureDirectory(); }, [ensureDirectory]);

  useEffect(() => { void ensureDirectory(); }, [ensureDirectory]);
  // unmount cleanup: no directory retry timer may outlive the panel.
  useEffect(() => cancelDirectoryRetry, [cancelDirectoryRetry]);

  // 任务 856 ⑥：邀请框点外部区域自动关闭——tl1441 抽取后由 ChannelDetailDrawer
  // 内部承担（同一 house 形态守卫，类名不变，行为同价）。
  // tl1369 缺陷A（用户 20261103 报）：详情抽屉点外部关闭——此前必须点右上角
  // 被 300px 宽抽屉（inset 44px 起、右侧锚定）盖住的「详情」toggle 才能关，
  // 唯一关闭路不可达。形态照 980/1242 家族 house 形态（mousedown+click 双通道、
  // 捕获阶段、closest 守卫；拖拽起始等 click 不派发路径由 mousedown 兜底）。
  // 豁免四处（「点内不误关」的边界）：
  //   ① 抽屉自身（成员操作/邀请框/退群解散全在其中）；
  //   ② chathead 触发 toggle（.channel-chat__detailtoggle）——不豁免则
  //      mousedown 先关、click 的 toggle 再开，按钮永远关不掉抽屉；
  //   ③ 本面板 portal 到 body 的右键菜单与全屏 veil（980 同款排除：语义属本
  //      面板，右键撤回/菜单项点击若计入外部会把抽屉连带关掉）；
  //   ④ .modal-backdrop（980 同款：子树内确认框语义属面板）。
  // Esc 不在此接管：Hub 壳已有 Esc 关 Hub（980），子浮层再挂 Esc 会在同一
  // 次按键里把抽屉与 Hub 一起关掉；抽屉走 点外/×/触发toggle 三路（1242 同款无 Esc）。
  useEffect(() => {
    if (!detailOpen) return;
    const isOutside = (event: MouseEvent) => {
      const target = event.target as Element | null;
      return !target?.closest(".channel-chat__detail, .channel-chat__detailtoggle, .channel-chat__menu, .channel-chat__menuveil, .modal-backdrop");
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

  // tl1369 同类收口：建群对话框点外部关闭（对话框此前只能点内「关闭」钮）。
  // 豁免：对话框自身 / 触发钮（.channel-chat__createtoggle，toggle 冲突同上）/
  // 右键菜单+veil / .modal-backdrop（同详情抽屉四条边界）。表单值存组件 state，
  // 点外关闭不丢已输入内容（重开即见，与邀请框「勾选保留」同一口径）。
  useEffect(() => {
    if (!createOpen) return;
    const isOutside = (event: MouseEvent) => {
      const target = event.target as Element | null;
      return !target?.closest(".channel-chat__create, .channel-chat__createtoggle, .channel-chat__menu, .channel-chat__menuveil, .modal-backdrop");
    };
    const onDocMouseDown = (event: MouseEvent) => {
      if (isOutside(event)) setCreateOpen(false);
    };
    const onDocClick = (event: MouseEvent) => {
      if (isOutside(event)) setCreateOpen(false);
    };
    document.addEventListener("mousedown", onDocMouseDown, true);
    document.addEventListener("click", onDocClick, true);
    return () => {
      document.removeEventListener("mousedown", onDocMouseDown, true);
      document.removeEventListener("click", onDocClick, true);
    };
  }, [createOpen]);

  // tl1369 同类收口：@ 选人浮层点外部关闭（此前只能 Esc/选中/继续输入关闭，
  // 点流区/别处浮层悬着不走）。守卫严格只豁免浮层自身：触发是输入 @ 而非按钮，
  // 无 toggle 冲突；点输入框/发送钮等「外部」即收起（顺带修掉「@ 悬浮时点
  // 发送、草稿清空而浮层残留」的边缘），再输入 @ 即重开。补全浮层非面板形态，
  // 不加 ×（Esc 即显式关闭；1242 家族点外关闭对齐）。
  useEffect(() => {
    if (!atOpen) return;
    const isOutside = (event: MouseEvent) => {
      const target = event.target as Element | null;
      return !target?.closest(".channel-chat__atpop");
    };
    const onDocMouseDown = (event: MouseEvent) => {
      if (isOutside(event)) setAtOpen(false);
    };
    const onDocClick = (event: MouseEvent) => {
      if (isOutside(event)) setAtOpen(false);
    };
    document.addEventListener("mousedown", onDocMouseDown, true);
    document.addEventListener("click", onDocClick, true);
    return () => {
      document.removeEventListener("mousedown", onDocMouseDown, true);
      document.removeEventListener("click", onDocClick, true);
    };
  }, [atOpen]);

  const openChannel = useCallback((id: string) => {
    setOpenId(id);
    openIdRef.current = id;
    writeCollabChatLastChannelId(id); // tl1439 修②：落「上次打开」记忆
    setChat(null);
    setActionError(null);
    setDeputylessHint(false); // tl1406：切群清零投递提示
    setDetailOpen(false);
    setInviteOpen(false);
    setAtOpen(false);
    // tl1465 D：与群聊 tab 的 Composer 同一份持久化草稿（channel:<ref>）——
    // 打开群时恢复文本+粘图 chip，两面板接力不丢（tab 发送后草稿清空=这里
    // 恢复出空态，天然一致）。
    const persisted = loadPersistedComposerDraft(`channel:${id}`);
    setDraft(persisted?.text ?? "");
    setChips((persisted?.attachments ?? []).map((a) => ({ path: a.path, displayName: a.displayName, pending: false })));
    void refresh();
  }, [refresh]);

  const closeChannel = useCallback(() => {
    setOpenId(null);
    openIdRef.current = null;
    // tl1439 修②：显式回到群列表（返回键/退群/解散成功）即清记忆——重开面板
    // 尊重用户最后一次动作（列表态），不再代开。
    writeCollabChatLastChannelId(null);
    setChat(null);
    setDetailOpen(false);
    setInviteOpen(false);
    setAtOpen(false);
    setDeputylessHint(false); // tl1406：关群清零投递提示
    setDraft("");
    setChips([]); // tl1465 D：本地态清空；持久化层保留（切回 tab/重开即恢复）
  }, []);

  // tl1465 D：草稿持久化（文本+粘图 chip）——与群聊 tab Composer 同键
  // channel:<ref>，两面板共享一份草稿；空草稿由持久化层自行判空删键。
  useEffect(() => {
    const ref = openIdRef.current;
    if (!ref) return;
    const saved = chips.filter((c) => !c.pending && c.path);
    persistComposerDraft(`channel:${ref}`, {
      text: draft,
      pastedBlocks: [],
      attachments: saved.map((c) => ({ path: c.path!, ...(c.displayName ? { displayName: c.displayName } : {}) })),
      attachmentDedupKeys: Object.fromEntries(saved.map((c) => [c.path!, { hash: "", source: `channel-hub:${c.path}` }])),
      workspaceRefs: [],
    });
  }, [draft, chips]);

  const send = useCallback(async () => {
    const ref = openIdRef.current;
    // tl1465 D：发送体=文本+粘图 @引用（与群聊 tab Composer 提交链同一组装
    // 形态：`@${path}`，空格经 escapeRefPath 转义存活 @-token 语法）。
    const refs = chips.filter((c) => !c.pending && c.path).map((c) => `@${escapeRefPath(c.path!)}`);
    const body = [draft.trim(), ...refs].filter(Boolean).join(" ");
    if (!body || !ref || sending) return;
    setSending(true);
    setActionError(null);
    try {
      const msg = await b.ChannelSendFromHuman(ref, body);
      setDraft("");
      setChips([]); // tl1465 D：随发送清空粘图（与清空文本同口径）
      // tl1406：零投递分支如实提示（后端路由注记，非本地猜测）。
      setDeputylessHint(Boolean(msg.deputyless));
      // 乐观上屏 + 立刻对账（refresh 同时刷新群列表计数与自己的未读记账）。
      setChat((prev) => {
        if (!prev || (prev.channel.id !== ref && prev.channel.name !== ref)) return prev;
        if (prev.messages.some((m) => m.id === msg.id)) return prev;
        return { ...prev, messages: [...prev.messages, msg] };
      });
      await refresh();
    } catch (err) {
      setActionError(String(err)); // 小时上限等风暴治理错误如实透出
    } finally {
      setSending(false);
    }
  }, [b, draft, chips, refresh, sending]);

  // ── tl1465 D：粘图链——文件载荷走 SaveChannelAttachment（全局工作区锚点，
  // 返回绝对路径）；无文件载荷的 image-hint（截图工具等）走
  // SaveChannelClipboardImage 原生读口。占位 chip 先上屏（即时反馈），落盘
  // 成功换缩略图，失败撤占位并在错误条如实透出（发送体不受影响）。
  const readFileAsChannelDataURL = (file: File) =>
    new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    });

  const attachChannelImageFiles = async (files: File[]) => {
    const images = files.filter((f) => f.type.startsWith("image/"));
    if (images.length === 0 || typeof b.SaveChannelAttachment !== "function") return;
    for (const file of images) {
      const chipId = `chip-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      setChips((prev) => [...prev, { pending: true, displayName: file.name || "image", path: chipId }]);
      try {
        const dataUrl = await readFileAsChannelDataURL(file);
        const path = await b.SaveChannelAttachment(dataUrl);
        let previewUrl: string | undefined;
        try {
          previewUrl = await b.ChannelAttachmentDataURL?.(path);
        } catch { /* 预览失败=文件名 chip，缩略图不阻断 */ }
        setChips((prev) => prev.map((c) => (c.path === chipId ? { ...c, pending: false, path, previewUrl } : c)));
      } catch (err) {
        setChips((prev) => prev.filter((c) => c.path !== chipId));
        setActionError(`${t("composer.attachImageFailed")}${err ? `: ${String(err)}` : ""}`);
      }
    }
  };

  const attachChannelClipboardImage = async () => {
    if (typeof b.SaveChannelClipboardImage !== "function") return;
    const chipId = `chip-${Date.now()}-clip`;
    setChips((prev) => [...prev, { pending: true, displayName: t("collabGroup.chat.clipboardImage"), path: chipId }]);
    try {
      const path = await b.SaveChannelClipboardImage();
      let previewUrl: string | undefined;
      try {
        previewUrl = await b.ChannelAttachmentDataURL?.(path);
      } catch { /* 预览失败=文件名 chip */ }
      setChips((prev) => prev.map((c) => (c.path === chipId ? { ...c, pending: false, path, previewUrl } : c)));
    } catch (err) {
      setChips((prev) => prev.filter((c) => c.path !== chipId));
      setActionError(`${t("composer.pasteImageFailed")}${err ? `: ${String(err)}` : ""}`);
    }
  };

  const onChannelPaste = (e: ReactClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData?.files ?? []);
    if (files.some((f) => f.type.startsWith("image/"))) {
      e.preventDefault();
      void attachChannelImageFiles(files);
      return;
    }
    const text = e.clipboardData?.getData("text") ?? "";
    const hasImageHint = Array.from(e.clipboardData?.items ?? []).some((i) => i.type.startsWith("image/"));
    if (hasImageHint || text === "") {
      e.preventDefault();
      void attachChannelClipboardImage();
      return;
    }
    // tl1465 D：文本粘贴手动插入（Composer 同款理由——受控输入下浏览器默认
    // 粘贴会与 React 受控值更新竞态丢字符；这里同步 splice 进 draft）。
    e.preventDefault();
    const el = e.currentTarget;
    const start = el.selectionStart ?? draft.length;
    const end = el.selectionEnd ?? start;
    const next = draft.slice(0, start) + text + draft.slice(end);
    setDraft(next);
    requestAnimationFrame(() => {
      const caret = start + text.length;
      try { el.setSelectionRange(caret, caret); } catch { /* 受控值未就位时静默 */ }
    });
  };

  const cancelMessage = useCallback(async (messageId: string) => {
    const ref = openIdRef.current;
    if (!ref) return;
    setMenu(null);
    setActionError(null);
    try {
      await b.ChannelCancelFromHuman(ref, messageId);
      setChat((prev) => prev
        ? { ...prev, messages: prev.messages.map((m) => (m.id === messageId ? { ...m, cancelledAt: Date.now() } : m)) }
        : prev);
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, refresh]);

  const leave = useCallback(async () => {
    const ref = openIdRef.current;
    if (!ref) return;
    setActionError(null);
    try {
      await b.ChannelLeaveHuman(ref);
      closeChannel();
      await refresh();
    } catch (err) {
      // 群主退群被拒（803：最后一个群主退出拒绝）——文案原样透出。
      setActionError(String(err));
    }
  }, [b, closeChannel, refresh]);

  // tl1217：解散群聊（群主专用出口）。tl1441 抽取后两击武装态在抽屉内部，
  // 这里只承担第二击的执行面。成功后关详情回到群列表（群已不存在）。
  const dissolve = useCallback(async () => {
    const ref = openIdRef.current;
    if (!ref) return;
    setActionError(null);
    try {
      await b.ChannelDissolveForHuman(ref);
      closeChannel();
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, closeChannel, refresh]);

  const removeMember = useCallback(async (contactId: string) => {
    const ref = openIdRef.current;
    if (!ref) return;
    setActionError(null);
    try {
      const updated = await b.ChannelRemoveMember(ref, contactId);
      setChat((prev) => (prev && (prev.channel.id === updated.id || prev.channel.name === updated.name) ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, refresh]);

  // tl1441：勾选清单由抽屉内部持有——契约=onInvite 失败 reject（抽屉保留
  // 勾选原状，错误条由这里透出）、成功 resolve（抽屉自关邀请框+清勾选，
  // 与既有行为同价：详情抽屉保持打开）。
  const invite = useCallback(async (picked: string[]) => {
    const ref = openIdRef.current;
    if (!ref || picked.length === 0) return;
    setActionError(null);
    try {
      const updated = await b.ChannelInvite(ref, picked);
      setChat((prev) => (prev && (prev.channel.id === updated.id || prev.channel.name === updated.name) ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
      throw err;
    }
  }, [b, refresh]);

  // 任务 856 ⑤：任命/撤销副管理员（仅 human 群主可见入口；失败原样透出）。
  const setDeputy = useCallback(async (contactId: string) => {
    const ref = openIdRef.current;
    if (!ref || deputyBusy) return;
    setDeputyBusy(true);
    setActionError(null);
    try {
      const updated = await b.ChannelSetDeputy(ref, contactId);
      setChat((prev) => (prev && (prev.channel.id === updated.id || prev.channel.name === updated.name) ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    } finally {
      setDeputyBusy(false);
    }
  }, [b, deputyBusy, refresh]);

  const unsetDeputy = useCallback(async () => {
    const ref = openIdRef.current;
    if (!ref || deputyBusy) return;
    setDeputyBusy(true);
    setActionError(null);
    try {
      const updated = await b.ChannelUnsetDeputy(ref);
      setChat((prev) => (prev && (prev.channel.id === updated.id || prev.channel.name === updated.name) ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    } finally {
      setDeputyBusy(false);
    }
  }, [b, deputyBusy, refresh]);

  const create = useCallback(async () => {
    const name = createName.trim();
    if (!name) return;
    setActionError(null);
    try {
      const limit = Number.parseInt(createLimit, 10);
      const created = await b.ChannelCreateForHuman(name, createTopic.trim(), Number.isFinite(limit) && limit > 0 ? limit : 0);
      setCreateOpen(false);
      setCreateName("");
      setCreateTopic("");
      setCreateLimit("");
      openChannel(created.id || created.name);
    } catch (err) {
      setActionError(String(err));
    }
  }, [createLimit, createName, createTopic, openChannel]);

  const directoryTitle = useCallback((contactId: string): string => {
    const row = directory?.find((r) => r.contactId === contactId);
    return row?.title || row?.purpose || contactId;
  }, [directory]);

  if (!channels && !loadError) {
    return <div className="channel-chat channel-chat--loading">{t("collabGroup.loading")}</div>;
  }

  const openCh = chat?.channel ?? channels?.find((c) => c.id === openId || c.name === openId) ?? null;
  const messages = chat?.messages ?? [];
  const iAmOwner = Boolean(openCh?.ownerId && openCh.ownerId === HUMAN_LOCAL);
  const hasOwner = Boolean(openCh?.ownerId); // 无主老群：隐藏全部管理入口（803 需知③）
  const memberDetails: CollabChannelMemberView[] = openCh?.memberDetails
    ?? (openCh?.members ?? []).map((contact) => ({ contact, type: "session", joinedAt: 0 }));

  const lastSeenNow = lastSeenRef.current ?? {};
  const unreadOf = (c: CollabChannelView): number =>
    Math.max(0, c.messages - (lastSeenNow[c.id] ?? c.messages));

  // 任务 856 ⑥：@ 候选=当前群成员（human 与会话同列，带类型徽标）。
  const atCandidates: CollabChannelMemberView[] = openCh?.memberDetails ?? [];
  const pickAt = (m: CollabChannelMemberView) => {
    const name = m.contact === HUMAN_LOCAL ? t("collabGroup.chat.you") : directoryTitle(m.contact);
    setDraft((prev) => prev.replace(/@$/, `@${name} `));
    setAtOpen(false);
  };

  return (
    <div className="channel-chat" aria-label={t("collabGroup.chat.title")}>
      {/* 左侧：群列表（名称/未读/最后一条预览/群主标） */}
      <div className="channel-chat__list">
        <div className="channel-chat__listhead">
          <span className="channel-chat__listtitle">{t("collabGroup.chat.title")}</span>
          {/* tl1369：触发钮挂稳定类供建群对话框点外关闭守卫豁免（同详情 toggle）。 */}
          <button type="button" className="btn btn--small channel-chat__createtoggle" onClick={() => { setCreateOpen(true); setActionError(null); }}>
            {t("collabGroup.chat.create")}
          </button>
        </div>
        {loadError && (
          <div className="channel-chat__errorstrip" role="alert">
            <span>{t("collabGroup.error.stale")}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>{t("collabGroup.retry")}</button>
          </div>
        )}
        {(channels?.length ?? 0) === 0 && !loadError && (
          <div className="channel-chat__empty">{t("collabGroup.chat.empty")}</div>
        )}
        {(channels ?? []).map((c) => {
          const unread = unreadOf(c);
          const preview = previews[c.id];
          return (
            <button
              key={c.id}
              type="button"
              className={`channel-chat__item${openId === c.id ? " channel-chat__item--on" : ""}`}
              onClick={() => openChannel(c.id)}
            >
              <span className="channel-chat__itemname" title={c.name}>
                {c.name}
                {c.ownerId && (
                  <span className={`channel-chat__ownerbadge${c.ownerId === HUMAN_LOCAL ? " channel-chat__ownerbadge--me" : ""}`}>
                    {t("collabGroup.chat.ownerBadge")}
                  </span>
                )}
                {unread > 0 && <span className="channel-chat__unread">{unread > 99 ? "99+" : unread}</span>}
              </span>
              <span className="channel-chat__itempreview" title={preview ? (preview.cancelledAt ? t("collabGroup.chat.recalled", { sender: preview.sender }) : `${preview.sender}: ${preview.body}`) : undefined}>
                {preview
                  ? (preview.cancelledAt
                    ? t("collabGroup.chat.recalled", { sender: preview.sender })
                    : `${preview.sender}: ${preview.body}`)
                  : t("collabGroup.membersCount", { n: c.members?.length ?? 0 })}
              </span>
            </button>
          );
        })}
      </div>

      {/* 右侧：消息流 + 输入框；未选群时渲染诚实空态 */}
      <div className="channel-chat__main">
        {!openCh ? (
          <div className="channel-chat__placeholder">
            {loadError ? t("collabGroup.error") : t("collabGroup.chat.placeholder")}
          </div>
        ) : (
          <>
            <div className="channel-chat__chathead">
              <button type="button" className="channel-chat__back" onClick={closeChannel} aria-label={t("collabGroup.chat.back")}>‹</button>
              <span className="channel-chat__chatname" title={openCh.name}>{openCh.name}</span>
              {openCh.topic && <span className="channel-chat__chattopic">{openCh.topic}</span>}
              <span className="channel-chat__chatmeta">{t("collabGroup.membersCount", { n: openCh.members?.length ?? 0 })}</span>
              {/* tl1441 ①「打开标签页」：把当前群聊升级为 tab 工作区实体
                  （标签栏出现群聊 tab，数据源换 channel）。打开即激活。 */}
              <button
                type="button"
                className="btn btn--small channel-chat__opentab"
                onClick={() => openChannelTab({ ref: openIdRef.current || openCh.id || openCh.name, name: openCh.name })}
              >
                {t("collabGroup.chat.openTab")}
              </button>
              {/* tl1369：触发 toggle 挂稳定类供点外关闭守卫豁免（不豁免则
                  mousedown 先关、click 再开，按钮关不掉抽屉）。 */}
              <button type="button" className="btn btn--small channel-chat__detailtoggle" onClick={() => setDetailOpen((v) => !v)}>
                {detailOpen ? t("collabGroup.chat.closeDetail") : t("collabGroup.chat.detail")}
              </button>
            </div>

            {actionError && (
              <div className="channel-chat__actionerror" role="alert">
                <span>{actionError}</span>
                <button type="button" className="btn btn--secondary btn--small" onClick={() => setActionError(null)}>{t("collabGroup.close")}</button>
              </div>
            )}

            {/* tl1406：零投递提示（Q3）——消息已入历史但没有任何会话被推送，
                引导设置副管理员或直接 @；成员仍可主动查询历史。 */}
            {deputylessHint && (
              <div className="channel-chat__deputyless" role="status">
                <span>{t("collabGroup.chat.deputylessHint")}</span>
                <button type="button" className="btn btn--secondary btn--small" onClick={() => setDeputylessHint(false)}>{t("collabGroup.close")}</button>
              </div>
            )}

            <div className="channel-chat__stream">
              {/* tl1441：消息流渲染抽取为 ChannelMessageStream 共用件（tab
                  工作区同用）；右键菜单本体仍归本面板（portal 挂载点相关）。 */}
              <ChannelMessageStream
                messages={messages}
                memberDetails={memberDetails}
                directoryTitle={directoryTitle}
                onBubbleContextMenu={(point) => setMenu(point)}
                onMentionClick={() => onViewDirectChains?.()}
              />
            </div>

            {/* tl1465 D：粘图 chip 条——pending 占位（即时反馈）→ 缩略图；
                × 移除；chip 与文本同入持久化草稿，切群聊 tab 后仍在。 */}
            {chips.length > 0 && (
              <div className="channel-chat__chips" aria-label={t("collabGroup.chat.attachmentChips")}>
                {chips.map((chip, i) => (
                  <span key={chip.path ?? `pending-${i}`} className={`channel-chat__chip${chip.pending ? " channel-chat__chip--pending" : ""}`} title={chip.path || chip.displayName}>
                    {chip.pending
                      ? <span className="channel-chat__chippending">{t("collabGroup.chat.savingImage")}</span>
                      : chip.previewUrl
                        ? <img className="channel-chat__chipthumb" src={chip.previewUrl} alt={chip.displayName || chip.path} draggable={false} />
                        : <span className="channel-chat__chipname">{chip.displayName || chip.path}</span>}
                    <button
                      type="button"
                      className="channel-chat__chipremove"
                      aria-label={t("collabGroup.chat.removeImage")}
                      onClick={() => setChips((prev) => prev.filter((_, index) => index !== i))}
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            )}

            <div className="channel-chat__composer">
              {/* 任务 856 ⑥：@ 选人浮层——输入 @ 触发，↑↓ 选择、Enter 确认、
                  Esc 关闭；选中插入「@名字 」。 */}
              {atOpen && atCandidates.length > 0 && (
                <div className="channel-chat__atpop" role="listbox" aria-label={t("collabGroup.chat.atPopLabel")}>
                  <div className="channel-chat__atpoptitle">{t("collabGroup.chat.atPopTitle")}</div>
                  {atCandidates.map((m, i) => (
                    <button
                      key={m.contact}
                      type="button"
                      role="option"
                      aria-selected={i === atIndex}
                      className={`channel-chat__atrow${i === atIndex ? " channel-chat__atrow--on" : ""}`}
                      onClick={() => pickAt(m)}
                    >
                      <span className="channel-chat__atname">{m.contact === HUMAN_LOCAL ? t("collabGroup.chat.you") : directoryTitle(m.contact)}</span>
                      <span className={`channel-chat__typebadge channel-chat__typebadge--${m.type === "human" ? "human" : "session"}`}>
                        {m.type === "human" ? t("collabGroup.identity.human") : t("collabGroup.chat.typeSession")}
                      </span>
                    </button>
                  ))}
                </div>
              )}
              <textarea
                className="channel-chat__input"
                value={draft}
                rows={2}
                placeholder={t("collabGroup.chat.sendPlaceholder")}
                onPaste={onChannelPaste}
                onChange={(event) => {
                  setDraft(event.target.value);
                  // ⑥：光标处 @ 触发浮层（简化口径：以 @ 结尾即弹，demo 同款）。
                  const atTrigger = /@$/.test(event.target.value);
                  setAtOpen(atTrigger);
                  setAtIndex(0);
                }}
                onKeyDown={(event) => {
                  // ⑥：浮层开着时 ↑↓/Enter/Esc 归浮层。
                  if (atOpen && atCandidates.length > 0) {
                    if (event.key === "ArrowDown") {
                      event.preventDefault();
                      setAtIndex((i) => (i + 1) % atCandidates.length);
                      return;
                    }
                    if (event.key === "ArrowUp") {
                      event.preventDefault();
                      setAtIndex((i) => (i - 1 + atCandidates.length) % atCandidates.length);
                      return;
                    }
                    if (event.key === "Enter" && !(event.nativeEvent as KeyboardEvent).isComposing) {
                      event.preventDefault();
                      pickAt(atCandidates[atIndex]);
                      return;
                    }
                    if (event.key === "Escape") {
                      event.preventDefault();
                      setAtOpen(false);
                      return;
                    }
                  }
                  // Enter 发送、Shift+Enter 换行；中文输入法组词中的 Enter 不发送。
                  if (event.key === "Enter" && !event.shiftKey && !(event.nativeEvent as KeyboardEvent).isComposing) {
                    event.preventDefault();
                    void send();
                  }
                }}
              />
              <button
                type="button"
                className="btn channel-chat__send"
                disabled={(!draft.trim() && !chips.some((c) => !c.pending && c.path)) || sending}
                onClick={() => void send()}
              >
                {t("collabGroup.chat.send")}
              </button>
            </div>
          </>
        )}
      </div>

      {/* 详情抽屉（②）：成员列表/邀请/移除/退群——tl1441 抽取为共用件
          ChannelDetailDrawer（群聊 tab 工作区 ChannelWorkspacePanel 同挂一只，
          单一实现杜绝双写）。行为契约逐行同价：DOM 类名/文案/两击解散/
          勾选保留全部不变。 */}
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
          onEnsureDirectory={() => ensureDirectoryRef.current()}
          inviteOpen={inviteOpen}
          onInviteOpenChange={setInviteOpen}
          invitePicked={invitePicked}
          onInvitePickedChange={setInvitePicked}
          inviteQuery={inviteQuery}
          onInviteQueryChange={setInviteQuery}
        />
      )}

      {/* 建群对话框（①）：人类建群即群主 */}
      {createOpen && (
        <div className="channel-chat__create" role="dialog" aria-label={t("collabGroup.chat.create")}>
          <div className="channel-chat__createtitle">{t("collabGroup.chat.createTitle")}</div>
          <label className="channel-chat__createfield">
            <span>{t("collabGroup.chat.nameLabel")}</span>
            <input value={createName} onChange={(event) => setCreateName(event.target.value)} autoFocus />
          </label>
          <label className="channel-chat__createfield">
            <span>{t("collabGroup.chat.topicLabel")}</span>
            <input value={createTopic} onChange={(event) => setCreateTopic(event.target.value)} />
          </label>
          <label className="channel-chat__createfield">
            <span>{t("collabGroup.chat.limitLabel")}</span>
            <input
              value={createLimit}
              inputMode="numeric"
              placeholder={t("collabGroup.chat.limitPlaceholder")}
              onChange={(event) => setCreateLimit(event.target.value.replace(/[^0-9]/g, ""))}
            />
          </label>
          <div className="channel-chat__createactions">
            <button type="button" className="btn btn--small" disabled={!createName.trim()} onClick={() => void create()}>
              {t("collabGroup.chat.createConfirm")}
            </button>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => setCreateOpen(false)}>
              {t("collabGroup.close")}
            </button>
          </div>
        </div>
      )}

      {/* 右键菜单：own=撤回（349 Cancel 墓碑语义）；他人=⑦跳转发信方会话 +
          收信方（群）提示（demo v2 返工口径：收信方为群，第二项示意已在当前视图）。 */}
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
        // 点其它任何地方即收起菜单（portal 外的透明遮罩，不挡滚动）。
        <div className="channel-chat__menuveil" onContextMenu={(event) => { event.preventDefault(); setMenu(null); }} onClick={() => setMenu(null)} />
      )}
    </div>
  );
}
