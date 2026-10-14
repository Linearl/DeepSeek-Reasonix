// 任务758: 跨会话投递文本的卡片化判定 —— 两渲染实体共用的唯一入口。
//
// 一封 talk_to_session 投递消息只有一种文本形态（desktop/session_collab.go
// sessionCollabDeliveryText 的 stamping：`[跨会话消息] 来自 contact_id=<from>
// → 发至 contact_id=<to>`（可带 ` (hop=N)`）+ 正文 + `---` 元数据尾），但它
// 能以两种实体进 transcript：user row（UserMessage → im-source 卡片）与 ↪
// notice 行（SteerCard 气泡）。此前判定逻辑只住在 Message.tsx 的
// collabAsImSource，SteerCard 从不调用 —— 同一封消息因此有时卡片有时裸全文
// （取决于注入时机与消费状态，与内容无关）。提升到 lib 让两条实体走同一
// 判定，SteerCard 命中即渲染同款卡片。
//
// 判定入口的加固（只影响显示，不改投递文本；模型读到的内容保持 id 不变，
// 任务462 口径）：
// - 剥 STEER_NOTICE_PREFIX 与 BOM/零宽字符 —— ↪ notice 复制体（实时 steer、
//   收件箱 preview 重建、历史回放行）也能命中；user row 防前缀污染；
// - 单行形态不再丢正文：收件箱 preview 由 PreviewText 做空白折叠 + 120 rune
//   截断（internal/sessioninbox/ids.go），整条消息只有一行。header 正则命中
//   后，行内剩余部分（剥掉 hop 尾缀）即正文。
//
// 纯显示层判定：返回 null 表示「不是跨会话形态」，调用方维持原渲染。

import { STEER_NOTICE_PREFIX } from "./useController";

export type CollabImSourceMessage = {
  provider: "collab";
  label: string;
  sender: string;
  chat: string;
  text: string;
};

/** 投递文本 header 的字面前缀（sessionCollabDeliveryText 保证）。 */
export const COLLAB_MSG_PREFIX = "[跨会话消息]";

// 任务811: id 捕获停在全角/半角逗号前。866/1553 给 header 尾部加了
// 「，发送于 <RFC3339 周几>」——它是 header 的时间戳而非正文，而 `，` 不是
// 空白，旧的 `(\S+)` 会把 `，发送于` 一起吞进 chat id：meta 标签被污染成
// 「sc_…，发送于」，且单行 preview 分支的 body 退化成时间戳残片
// （120-rune 截断点实测正好落在 `…T13:18:00+08…`，即 811 症状2样本）。
const COLLAB_HEADER_RE = /来自 contact_id=([^\s，,]+)\s*→\s*发至 contact_id=([^\s，,]+)/;
const HOP_TAIL_RE = /^\s*\(hop=\d+\)\s*/;
// 任务811: 从单行 header 余量里剥掉「，发送于 <时间>」尾巴。锚定日期形态，
// 容忍 `…` 截断尾，不会误伤正文首词。
const SENT_TIME_TAIL_RE = /^，发送于\s*\d{4}-\d{2}-\d{2}T\S*(?:\s+[A-Za-z]{3})?/;

export function collabAsImSource(text: string): CollabImSourceMessage | null {
  const normalized = text.replace(/^[\uFEFF\u200B]+/, "");
  const withoutNotice = normalized.startsWith(STEER_NOTICE_PREFIX)
    ? normalized.slice(STEER_NOTICE_PREFIX.length)
    : normalized;
  if (!withoutNotice.startsWith(COLLAB_MSG_PREFIX)) return null;
  const rest = withoutNotice.slice(COLLAB_MSG_PREFIX.length);
  const newline = rest.indexOf("\n");
  const header = (newline < 0 ? rest : rest.slice(0, newline)).trim();
  const match = COLLAB_HEADER_RE.exec(header);
  if (!match) return null;
  const body = newline < 0
    // 单行（空白折叠 preview）形态：header 之后的行内残余即正文。hop 尾与
    // 「，发送于 <时间>」都属于 header，剥掉后剩下的才是正文（任务811）。
    ? header.slice(match.index + match[0].length).replace(HOP_TAIL_RE, "").replace(SENT_TIME_TAIL_RE, "").trim()
    : rest.slice(newline + 1).replace(/^\r?\n/, "");
  return { provider: "collab", label: "", sender: match[1], chat: match[2], text: body };
}
