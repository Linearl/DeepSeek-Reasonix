// 任务 1082：本轮工具/文件汇总的单一计数源。
//
// 折叠头（ProcessFoldHeader）与运行态 run strip（Composer）必须对「一轮里
// 有几个工具」得到同一个数字——两处各写一遍 reduce 就会出现口径漂移，所以
// 这里只暴露一个计数函数：折叠头把它的 displayItems 传进来（displayItems
// 本身就是这一轮的折叠内容），run strip 经 Transcript 上报的整轮 items 传
// 进来（displayItems 的超集，工具计数同口径）。
//
// 文件数与检索次数是 run strip 独有的两列：
//  - 文件 = args.path / args.file_path / move_file 两端路径去重（同一文件
//    被读 5 次只计 1；路径不做大小写归一，与磁盘语义保持一致的保守计数）；
//  - 检索 = 只读检索工具白名单（与 ToolGroup 的「搜索」口径一致：grep /
//    glob / code_index，加 web_search / web_fetch 两个联网检索）。

import type { Item } from "./useController";
import type { Translator } from "./i18n";

type ToolItem = Extract<Item, { kind: "tool" }>;

/** 折叠头与 run strip 共用的工具计数（displayItems 口径，与改动前逐字一致）。 */
export function countTools(displayItems: readonly Item[]): number {
  return displayItems.reduce((n, it) => n + (it.kind === "tool" ? 1 : 0), 0);
}

/** 检索类工具白名单（判定基于工具名，与 ToolGroup 的搜索集合同源）。 */
const SEARCH_TOOL_NAMES = new Set(["grep", "glob", "code_index", "web_search", "web_fetch"]);

function parseArgs(args: string): Record<string, unknown> {
  if (!args) return {};
  try {
    const parsed: unknown = JSON.parse(args);
    return parsed && typeof parsed === "object" ? parsed as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

function stringArg(args: Record<string, unknown>, key: string): string | undefined {
  const value = args[key];
  return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

/** 该工具调用动过的文件路径（write/edit/read 的 path、move 的两端）。 */
function toolFilePaths(item: ToolItem): string[] {
  // 检索类工具的 path 是「检索范围」（目录或全库），不是被改文件——排除
  // 否则一次 `grep --path src` 会把文件数虚增成一个假文件。
  if (SEARCH_TOOL_NAMES.has(item.name)) return [];
  const args = parseArgs(item.args);
  const paths: string[] = [];
  const path = stringArg(args, "path") ?? stringArg(args, "file_path");
  if (path) paths.push(path);
  if (item.name === "move_file") {
    const source = stringArg(args, "source_path");
    const destination = stringArg(args, "destination_path");
    if (source) paths.push(source);
    if (destination) paths.push(destination);
  }
  return paths;
}

export interface TurnTally {
  tools: number;
  files: number;
  searches: number;
}

/**
 * turnTally 从一轮的条目聚合工具/文件/检索三列。零工具轮返回全 0，调用方
 * 据此整段不渲染（纯对话不制造噪音）。args 解析失败按无路径计入工具数、
 * 不计入文件数——工具调用真实发生过，静默丢计会让「12 工具 · 0 文件」
 * 自相矛盾。
 */
export function turnTally(items: readonly Item[]): TurnTally {
  const files = new Set<string>();
  let tools = 0;
  let searches = 0;
  for (const item of items) {
    if (item.kind !== "tool") continue;
    tools += 1;
    if (SEARCH_TOOL_NAMES.has(item.name)) searches += 1;
    for (const path of toolFilePaths(item)) files.add(path);
  }
  return { tools, files: files.size, searches };
}

/**
 * 任务 1155：把聚合值格式化为「12 工具 · 3 文件 · 1 检索」；零工具轮返回
 * null（纯对话不制造噪音）。run strip（输入框状态行）与回合尾部工具栏共用
 * 这一个格式化函数——两处文案由同一份代码产出，口径永不漂移。
 */
export function turnTallyText(tally: TurnTally, t: Translator): string | null {
  if (tally.tools === 0) return null;
  return [
    t("transcript.tally.tools", { n: tally.tools }),
    t("transcript.tally.files", { n: tally.files }),
    t("transcript.tally.searches", { n: tally.searches }),
  ].join(" · ");
}

/** run strip 的汇总行文案：「12 工具 · 3 文件 · 1 检索」；零工具轮返回 null。 */
export function turnTallyLabel(items: readonly Item[], t: Translator): string | null {
  return turnTallyText(turnTally(items), t);
}

/**
 * 任务 1137：该回合动过的文件路径明细（去重保序）。与 turnTally 的 files
 * 计数走同一个 toolFilePaths——计数是这份明细的 size，两处口径永不漂移。
 */
export function turnFilePaths(items: readonly Item[]): string[] {
  const files: string[] = [];
  const seen = new Set<string>();
  for (const item of items) {
    if (item.kind !== "tool") continue;
    for (const path of toolFilePaths(item)) {
      if (seen.has(path)) continue;
      seen.add(path);
      files.push(path);
    }
  }
  return files;
}

/** 任务 1137：回合级 token 快照（turn_done 时由 useController 写入回合最后
 *  一个 assistant 条目）。字段与状态栏同源：outputTokens = State.turnTokens
 *  （状态栏回合读数同字段），inputTokens = State.turnInputTokens（usage 事件
 *  链的 prompt 累积，与 usageTotalTokens 的 prompt 口径同式）。 */
export interface TurnUsageSnapshot {
  inputTokens: number;
  outputTokens: number;
  /** 回合内任一发 usage 带 estimated 标记（状态栏 ≈ 前缀同语义）。 */
  estimated?: boolean;
}

/** 任务 1137：回合尾部信息带的三要素聚合。 */
export interface TurnTailInfo {
  /** 该回合动过的文件明细（turnFilePaths；零工具轮为空数组）。 */
  files: string[];
  /** 回合 token 快照（运行时回合经 turn_done 落条目；历史回合后端不存，undefined）。 */
  usage?: TurnUsageSnapshot;
  /** 回合完成时刻（epoch ms）：运行时回合 = turn_done 到达时刻；历史回合
   *  降级为后端入库的最后 assistant 消息 createdAt。两者皆无 → undefined。 */
  doneAt?: number;
}

/**
 * 任务 1137：从一回合的条目聚合尾部信息带。文件走 turnFilePaths（与
 * tally.files 同口径）；token/完成时刻读回合最后一个 assistant 条目上的
 * 快照（useController 在 turn_done 写入）。纯函数，无任何订阅。
 */
export function turnTailInfo(items: readonly Item[]): TurnTailInfo {
  const files = turnFilePaths(items);
  let lastAssistant: Extract<Item, { kind: "assistant" }> | undefined;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    if (item.kind === "assistant") { lastAssistant = item; break; }
  }
  return {
    files,
    usage: lastAssistant?.turnUsage,
    doneAt: lastAssistant?.turnDoneAt ?? lastAssistant?.createdAt,
  };
}
