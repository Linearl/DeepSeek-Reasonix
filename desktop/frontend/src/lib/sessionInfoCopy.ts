// tl 1100「复制 ▸」：`…` 菜单里复制当前会话三项信息（会话路径 / 日志路径 /
// 会话 ID）的取值逻辑。取值规则集中在这里而不是散在菜单 JSX 里，是为了能被
// 测试直接断言「复制到剪贴板的精确字符串」，也避免 UI 组件顺手改坏口径。
//
// 会话 ID 的口径与既有「复制会话ID」一致：优先 topicId（后端归档/心跳都用它
// 定位会话），没有 topicId 的 tab（如尚未落盘的会话）退回 tab id——菜单项
// 永远有值可复制，不会出现点了没反应。

import type { TabMeta } from "./types";

export interface SessionInfoCopyValues {
  sessionPath: string;
  logPath: string;
  sessionId: string;
}

export function collectSessionInfoCopyValues(
  tab: Pick<TabMeta, "id" | "topicId" | "sessionPath"> | null | undefined,
  logDirectory: string,
): SessionInfoCopyValues {
  return {
    sessionPath: tab?.sessionPath?.trim() ?? "",
    // 日志路径统一指向本机 desktop 日志目录（应用根的 logs/desktop）。
    logPath: logDirectory,
    sessionId: tab?.topicId?.trim() || tab?.id?.trim() || "",
  };
}
