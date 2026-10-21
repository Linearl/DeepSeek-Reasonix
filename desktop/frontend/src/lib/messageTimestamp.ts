// 任务1012：消息时间戳补日期。
//
// 症状（用户 2026-10-19 截图实证）：消息气泡时间只有时分（22:09），横跨五天
// 的会话里用户无法判断某条消息属于哪一天——排查「哪条是 10-16 的」时被实际
// 误导。修法：按「同日 / 跨天 / 跨年」三档渲染绝对时间，不用相对时间
//（「3 天前」会丢掉排查所需的精确日期）。
//
// 三档契约（验收可证伪）：
//   - 同一日历日（本地时区）→ "HH:mm"（24 小时制，保留既有形态，零回归）；
//   - 跨日但同年  → "MM-DD HH:mm"（如「10-19 22:09」）；
//   - 跨年        → "YYYY-MM-DD HH:mm"。
//
// 分档锚点是调用方显式传入的 now（消息列表用当前渲染时刻，悬浮/详情也用同一
// 刻）：显式注入让三档在任意时钟环境下可确定性测试——测试可以钉死
// 2026-10-19 渲染 2026-10-15 的消息必为「10-15 …」，不必让被测件偷偷读
// Date.now()。
//
// 格式为手工拼接而非 toLocaleString：三语 locale（zh / zh-TW / en）下输出
// 逐字节一致，且不引入美式「10/19, 22:09」的逗号歧义——窄窗口里短且稳定是
// 硬约束（任务书：最小宽度不得溢出/丢日期）。title 属性仍走 toLocaleString()
// 给出完整本地化日期做悬浮兜底。
export type MessageTimestampLocale = "en" | "zh" | "zh-TW";

const pad2 = (n: number): string => String(n).padStart(2, "0");

/**
 * formatMessageTimestamp renders the timestamp shown under a message bubble —
 * and, since 任务1012, also the collab chat faces that historically rendered
 * a bare toLocaleString()/时分 (回放视图 / 单聊列表)：one formatter, one tier
 * contract, no per-face drift.
 *
 * @param date  the message's send time
 * @param now   the render instant — decides which of the three tiers applies
 * @param _locale kept in the signature so a future localized tier (e.g. zh-TW
 *   「10月19日」) can land without touching call sites; the current tiers are
 *   locale-neutral by design (see the header comment).
 */
export function formatMessageTimestamp(date: Date, now: Date, _locale: MessageTimestampLocale): string {
  const hhmm = `${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
  if (date.getFullYear() !== now.getFullYear()) {
    return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${hhmm}`;
  }
  const sameDay = date.getFullYear() === now.getFullYear()
    && date.getMonth() === now.getMonth()
    && date.getDate() === now.getDate();
  if (!sameDay) {
    return `${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${hhmm}`;
  }
  return hhmm;
}
