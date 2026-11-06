// tl1439 修②：协作中心聊天面板「上次打开」记忆。
//
// 与 collabHub.view（856 视图持久化）/ tl933 面板偏好同模式：localStorage，
// 不进桌面 config 数据面。按面板 scope 各自记忆一条：
//   - collabChat.lastTab           聊天通道 tab（对话链/群聊）最后停留处
//   - collabChat.lastDirectsPair   对话链（单聊）面板最后打开的 pairId
//   - collabChat.lastGroupsChannel 群聊面板最后打开的 channelId
//
// 读侧契约（挂载恢复）由调用方持有：必须先对照列表校验记忆目标仍存在才恢复
// ——已被删除/归档/解散 → 丢弃记忆、回落默认视图（不报错不空白，验收③）。
// 存储失败（私密模式等）只损失「记住上次」，绝不阻塞面板。

export type CollabChatTabMemory = "directs" | "groups";

const TAB_KEY = "collabChat.lastTab";
const DIRECTS_PAIR_KEY = "collabChat.lastDirectsPair";
const GROUPS_CHANNEL_KEY = "collabChat.lastGroupsChannel";

/** 上次停留的通道 tab；无记录/损坏值回落群聊（现状默认视图）。 */
export function readCollabChatTab(): CollabChatTabMemory {
  try {
    const raw = window.localStorage.getItem(TAB_KEY);
    return raw === "directs" || raw === "groups" ? raw : "groups";
  } catch {
    return "groups";
  }
}

export function writeCollabChatTab(tab: CollabChatTabMemory): void {
  try {
    window.localStorage.setItem(TAB_KEY, tab);
  } catch {
    // 存储失败只损失记忆，不阻塞。
  }
}

/** 上次打开的对话链 pairId；无记录/损坏值/空串 → null。 */
export function readCollabChatLastPairId(): string | null {
  try {
    const raw = window.localStorage.getItem(DIRECTS_PAIR_KEY);
    return typeof raw === "string" && raw ? raw : null;
  } catch {
    return null;
  }
}

/** 写对话链记忆；null = 清除（显式回到链列表时同步清，重开不再代开）。 */
export function writeCollabChatLastPairId(pairId: string | null): void {
  try {
    if (pairId) window.localStorage.setItem(DIRECTS_PAIR_KEY, pairId);
    else window.localStorage.removeItem(DIRECTS_PAIR_KEY);
  } catch {
    // 存储失败只损失记忆，不阻塞。
  }
}

/** 上次打开的群聊 channelId；无记录/损坏值/空串 → null。 */
export function readCollabChatLastChannelId(): string | null {
  try {
    const raw = window.localStorage.getItem(GROUPS_CHANNEL_KEY);
    return typeof raw === "string" && raw ? raw : null;
  } catch {
    return null;
  }
}

/** 写群聊记忆；null = 清除（显式返回群列表/退群/解散时同步清）。 */
export function writeCollabChatLastChannelId(channelId: string | null): void {
  try {
    if (channelId) window.localStorage.setItem(GROUPS_CHANNEL_KEY, channelId);
    else window.localStorage.removeItem(GROUPS_CHANNEL_KEY);
  } catch {
    // 存储失败只损失记忆，不阻塞。
  }
}
