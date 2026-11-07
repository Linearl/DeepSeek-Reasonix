// tl1477: 子代理工具调用行的单行摘要与只读详情投影。纯函数（无 React、无
// DOM），SubagentsDockPanel 的 ChildToolRow 是唯一渲染方；主转录的 ToolCard
// 流式参数预览属 tl1475，不经本模块。
//
// 核心纪律：半截 JSON 不猜默认值。args 是模型流式产出的 JSON 文本，可能出现
// 三种诚实状态：完整（可解析）、截断（JSON.parse 失败——按原文显示，绝不从
// 残片里「解析」出参数值冒充真值）、接收中（后端只报了已收字符数）。旧的
// tools.parse() 对一切解析失败静默返回 {}，摘要面无从区分「无参数」与「参数
// 没到全」——本模块把三种状态显式分层，渲染方按状态出标记。

import { subjectOf } from "./tools";

/** 工具调用 args 的四种诚实状态（tl1477）：
 *  - complete: JSON 可解析（value 为解析结果；非对象标量/数组也如实携带）；
 *  - truncated: args 非空但 JSON.parse 失败（半截 JSON）——raw 按原文展示，
 *    不推断任何参数值；
 *  - receiving: args 为空而 argChars>0（后端 partial dispatch，参数仍在流式
 *    传输，只有已收字符数可知）；
 *  - empty: args 为空且无流式痕迹（工具本就无参，如实「无参数」）。 */
export type ToolCallArgsState =
  | { kind: "complete"; value: unknown }
  | { kind: "truncated"; raw: string }
  | { kind: "receiving"; chars?: number }
  | { kind: "empty" };

export function toolCallArgsState(args: string | undefined, argChars?: number): ToolCallArgsState {
  const text = typeof args === "string" ? args : "";
  if (text.trim() === "") {
    return argChars && argChars > 0 ? { kind: "receiving", chars: argChars } : { kind: "empty" };
  }
  try {
    return { kind: "complete", value: JSON.parse(text) as unknown };
  } catch {
    // 半截 JSON：原文照登，绝不猜值。空 catch 是判定本身——失败即截断态。
    return { kind: "truncated", raw: text };
  }
}

/** 单行摘要（tl1477 验收①）：工具名 + 关键参数。参数只在 args 完整可解析时
 *  才提取（复用 subjectOf 的 per-tool 关键参数选择）；截断/接收中/无参时绝不
 *  从 args 提取——半截 JSON 的残片永远不变身参数值。subject（后端盖章的稳定
 *  主题，历史归档卡的权威元数据）仅在参数提取不出时兜底——它是权威数据不是
 *  推断值；截断行仍带截断标记（渲染方），不会冒充「参数完整」。展示名优先
 *  resolvedName（MCP server/tool 全名），与转录卡同一回退链。 */
export function toolCallSummaryLine(item: {
  name: string;
  resolvedName?: string;
  args: string;
  argChars?: number;
  subject?: string;
}): string {
  const display = item.resolvedName || item.name;
  const state = toolCallArgsState(item.args, item.argChars);
  let params = "";
  if (state.kind === "complete" && typeof state.value === "object" && state.value !== null) {
    params = subjectOf(item.name, item.args);
  }
  if (!params) params = item.subject || "";
  return params ? `${display} · ${params}` : display;
}

/** 已收参数字符数的紧凑展示（与 ToolCard formatArgChars 同语义；本地复制以
 *  避免面板反向依赖组件内部工具函数）。 */
export function formatToolArgChars(chars: number): string {
  if (chars >= 1000) return `${(chars / 1000).toFixed(1)}k`;
  return String(chars);
}

/** 详情展开的参数区文本（tl1477 验收②）：完整= pretty JSON；截断= 原文逐字
 *  （一个字符都不改——截断态的诚实就是原样）；接收中/无参= 空串（渲染方出
 *  对应注记，不出参数块）。 */
export function toolCallArgsDetailText(state: ToolCallArgsState): string {
  if (state.kind === "complete") {
    try {
      return JSON.stringify(state.value, null, 2);
    } catch {
      // 循环引用等极端形态（转录数据不会出现，防御面）：退回字符串形态。
      return String(state.value);
    }
  }
  if (state.kind === "truncated") return state.raw;
  return "";
}
