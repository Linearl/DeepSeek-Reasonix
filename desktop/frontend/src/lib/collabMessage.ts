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
  // 任务1092: 投递 header 里 backend stamping 的「，发送于 <RFC3339 周几>」
  // 解析为 epoch ms，供卡片时间戳渲染（发送方墙上时钟，而非本端接收时刻）。
  // 旧投递文本/单行 preview 没有可解析时间时为 undefined——卡片退回「无时间」
  // 而非渲染错误时间，与 Message.tsx 气泡的 createdAt 语义一致。
  sentAt?: number;
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
// 任务1092: 从 header 里提取「，发送于 <RFC3339 带周几>」（866/1553 stamping）。
// 周三后缀（` Mon`…）可有可无——单行 preview 截断可能削掉它。
const SENT_TIME_STAMP_RE = /，发送于\s*(\d{4}-\d{2}-\d{2}T\S*(?:\s+[A-Za-z]{3})?)/;

/** header stamping 的发送时刻（epoch ms）；时间缺失或不可解析返回 undefined。 */
export function parseCollabSentAt(header: string): number | undefined {
  // 周几后缀（` Mon`…）是给人读的标注，Date.parse 不认——剥掉再解析（stamp
  // 主体是 RFC3339 带时区偏移，跨时区渲染不会错位）。
  const stamp = SENT_TIME_STAMP_RE.exec(header)?.[1].replace(/\s+[A-Za-z]{3}$/, "");
  if (!stamp) return undefined;
  const parsed = Date.parse(stamp);
  return Number.isNaN(parsed) ? undefined : parsed;
}

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
  // 任务1092: 发送时刻只从 header 行取（866/1553 的 stamping 位置）——多行投递
  // 的正文/元数据尾里出现同形片段也不会误解析。两种形态的 header 变量都是同一
  // 条头行：单行形态 = 整条折叠后的一行；多行形态 = 首行（prefix 已剥）。
  return { provider: "collab", label: "", sender: match[1], chat: match[2], text: body, sentAt: parseCollabSentAt(header) };
}
