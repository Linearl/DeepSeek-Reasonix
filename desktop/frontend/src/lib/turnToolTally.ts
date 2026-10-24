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

/** run strip 的汇总行文案：「12 工具 · 3 文件 · 1 检索」；零工具轮返回 null。 */
export function turnTallyLabel(items: readonly Item[], t: Translator): string | null {
  const tally = turnTally(items);
  if (tally.tools === 0) return null;
  return [
    t("transcript.tally.tools", { n: tally.tools }),
    t("transcript.tally.files", { n: tally.files }),
    t("transcript.tally.searches", { n: tally.searches }),
  ].join(" · ");
}
