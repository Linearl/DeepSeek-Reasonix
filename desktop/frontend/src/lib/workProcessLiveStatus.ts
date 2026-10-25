// tl 1141: concise 档运行中的动态过程状态（当前动作行 + 思考摘要行）数据源。
//
// 帧预算约束①（事件驱动，禁全量扫描）：本模块只暴露「从尾部倒序找首个命中」
// 的纯函数——调用点必须包在 useMemo 里、以 displayItems 引用为依赖，扫描只在
// 工具/思考事件到达（行模型重建、引用变化）时执行，秒 tick 重渲染命中缓存，
// 严禁任何 setInterval 周期遍历 transcript 行。倒序首中即返回，成本 O(命中
// 距离)，与千步会话的总行数无关。
//
// 与 tl 1137 的边界：1137 是回合级静态信息（token 总量/时间戳），本模块只为
// 运行中（hasRunningWork）的动态状态服务，回合落定后无消费者。

import { useEffect, useRef, useState } from "react";
import type { Item } from "./useController";

type ToolItem = Extract<Item, { kind: "tool" }>;

/** 当前动作：displayItems 尾部最近的 running 工具（含子代理 task 卡）。 */
export function latestRunningTool(items: readonly Item[]): ToolItem | undefined {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    if (item.kind === "tool" && item.status === "running") return item;
  }
  return undefined;
}

/** 最新思考段：displayItems 尾部最近的 assistant（reasoning）全文；正文由
 * 复用的 ReasoningSummary 负责取尾行与 160 截断（streaming 语义取尾不取头）。 */
export function latestReasoningText(items: readonly Item[]): string {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    if (item.kind === "assistant") return item.reasoning;
  }
  return "";
}

/** 该 segment 的最新思考段是否就是当前流式 assistant（live stream 命中）。
 * 命中时调用方应改用 live.reasoning——它比落进 items 的快照更及时。 */
export function hasLiveAssistant(items: readonly Item[], liveId: string | undefined): boolean {
  if (!liveId) return false;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    if (item.kind === "assistant") return item.id === liveId;
  }
  return false;
}

/** 单行有界截断：160 code point（grapheme 上界）+ 省略号，保证状态行单次
 * DOM 文本替换的写入量有界（帧预算约束②的配套）。先按 UTF-16 粗切上界窗口
 * 再按 code point 收尾，超长入参不产生全长字符串数组分配。 */
export function clampLine(text: string, max = 160): string {
  if (text.length <= max) return text;
  const chars = Array.from(text.slice(0, max * 2));
  return chars.length <= max ? chars.join("") : `${chars.slice(0, max - 1).join("")}…`;
}

/** 帧预算约束②：思考摘要文本采纳节流下限（毫秒）。动作切换不经此节流——
 * 工具事件本身低频且由 displayItems 引用变化即时驱动。 */
export const LIVE_TEXT_THROTTLE_MS = 500;

/** tl 1141 帧预算约束②：运行中文本的节流采纳。interval 只把最新值落进本
 * 组件的 state（同值 bail-out 零渲染、零扫描），不做任何行遍历；当前动作
 * 切换不经此节流——工具事件低频，由 displayItems 引用变化即时驱动。 */
export function useThrottledValue<T>(value: T, intervalMs: number): T {
  const [committed, setCommitted] = useState(value);
  const latestRef = useRef(value);
  latestRef.current = value;
  useEffect(() => {
    const id = window.setInterval(() => {
      setCommitted((previous) => {
        const latest = latestRef.current;
        return Object.is(previous, latest) ? previous : latest;
      });
    }, intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs]);
  return committed;
}
