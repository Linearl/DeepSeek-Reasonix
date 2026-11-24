// tl1744：右上角问号帮助菜单的纯逻辑层。
//
// 问号菜单五项里「问题上报 / 给产品提需求」不跳浏览器，而是发起新会话并在
// composer 预填 gh-issue-submit 技能的调用词——用户补一句描述、回车即触发
// 技能流水线（搜重复 → 选模板起草 → 用户确认 → 提交）。本模块只负责
// 预填 prompt 的拼装；菜单项文案在三语 locale 的 helpMenu.* 键里，仓库
// slug 单源在 forkLinks.ts。
//
// 不自动发送（设计定论，非偷懒）：新会话的 controller 未必就绪（composer 有
// workspaceStarting 守卫），且 gh-issue-submit 硬规则要求「用户看过草稿才提交」，
// 反正第一轮就要用户补描述——预填即终态，回车是用户补完描述的自然动作。

import { FORK_REPO_SLUG } from "./forkLinks";

/** 问号菜单里走 gh-issue-submit 链路的两个入口（bug=问题上报，feature=提需求）。 */
export type GhIssueKind = "bug" | "feature";

/**
 * 拼装新会话 composer 的预填文本。
 *
 * @param promptText locale 里的引导语模板（helpMenu.issuePrompt*，含 {repo}
 *   占位符；bug/feature 的区分由调用方选哪条模板承载，本函数不感知）。
 * @param repoSlug 目标仓库 slug；缺省用 fork 官方仓库。
 * @returns 以 /gh-issue-submit 开头的完整 prompt。slash 调用词永远原样开头、
 *          单空格衔接——会话侧按前缀识别技能调用，不能被翻译或换行截断。
 */
export function ghIssueComposerPrompt(promptText: string, repoSlug: string = FORK_REPO_SLUG): string {
  return `/gh-issue-submit ${promptText.replace("{repo}", repoSlug).trim()}`;
}

/** 问号菜单五项的稳定 key（App.tsx 菜单数组与测试都按此寻址）。 */
export const HELP_MENU_ITEM_KEYS = [
  "productDocs",
  "reportIssue",
  "featureRequest",
  "exportLogs",
  "about",
] as const;

export type HelpMenuItemKey = (typeof HELP_MENU_ITEM_KEYS)[number];
