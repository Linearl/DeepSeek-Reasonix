// tl1665：fork 官方链接的单一真相源。
//
// 背景：更新页（设置 > 更新）有四个对外链接——「前往官网下载」「在网页查看」
// 「提交问题」「查看已有问题」。此前这些 URL 以字符串字面量散落在
// SettingsPanel.tsx / App.tsx / useSessionBannerCommands.ts / crash.ts /
// bridge.ts 五处，同一条语义（fork 官网 / fork 仓库 / fork issues）有两套
// 真相源，必有一套腐化。宪法判据 D2 要求「同语义只允许一处权威源，其余是
// 只读投影」，故收口到本模块：改指向只动这一个文件。
//
// 口径：fork 的 web 存在面是 GitHub 上的 Linearl/DeepSeek-Reasonix 仓库 +
// 其 GitHub Pages 文档站；上游 esengine/DeepSeek-Reasonix 与 reasonix.io
// 官网均不属于本 fork（后者由上游 main-v2 分支的 pages.yml 部署，且站点
// 内容仍指向上游仓库；tl1824 起该站已归档到 docs/_archive/site/，见其
// src/pages/index.astro 的 repo 常量）。
//
// tl1665 实测（2026-11-18，本机网络可达时）：下列 URL 全部 HTTP 200；
// issues/new/choose 对未登录请求 302 到登录页是 GitHub 的正常行为。

/** fork 仓库主页。 */
export const FORK_REPO_URL = "https://github.com/Linearl/DeepSeek-Reasonix";

/** fork 发布页（桌面端安装包实际落在这里，「前往官网下载」的落点）。 */
export const FORK_RELEASES_URL = `${FORK_REPO_URL}/releases`;

/** fork issues 列表（全部状态）。 */
export const FORK_ISSUES_URL = `${FORK_REPO_URL}/issues`;

/**
 * fork 未关闭 issues 列表。
 * 「查看已有问题」用这个：用户来反馈页多半是找已知问题，默认只列 open
 * 能直接命中未处理的坑；已关闭的留在 /issues 里可查。
 */
export const FORK_OPEN_ISSUES_URL = `${FORK_REPO_URL}/issues?q=is%3Aissue%20state%3Aopen`;

/** fork 新建 issue（走仓库的 issue 模板选择器）。 */
export const FORK_NEW_ISSUE_URL = `${FORK_REPO_URL}/issues/new/choose`;

/**
 * fork 仓库 slug（owner/repo）。tl1744 问号菜单的 gh-issue-submit 预填 prompt
 * 用：终端里的 gh 命令认的是 slug 不是完整 URL，且会话工作区是用户自己的项目
 * （git remote 解析不到 fork 仓库），prompt 必须显式点名目标仓库。从
 * FORK_REPO_URL 派生，改指向仍只动上面一处。
 */
export const FORK_REPO_SLUG = FORK_REPO_URL.replace("https://github.com/", "");

/** fork 产品文档站（GitHub Pages）。 */
export const FORK_DOCS_SITE_URL = "https://linearl.github.io/DeepSeek-Reasonix/";

/** fork 更新日志页（文档站的 changelog 分区）。 */
export const FORK_CHANGELOG_URL = `${FORK_DOCS_SITE_URL}changelog/`;
