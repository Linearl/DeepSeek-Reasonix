// 任务 1102：产品文档静态站（GitHub Pages）的前端接线点。
//
// 静态站本体在仓库根 `docs-site/`，由 `.github/workflows/docs-site-pages.yml`
// 构建并发布到 GitHub Pages；本模块只持有「应用去哪里打开文档」的 URL 常量
// 与跳转函数——命令面板「产品文档」项（任务 1101 规划的帮助与支持组）与
// F1 快捷键都走这里，改指向只动这一个文件。
//
// URL 说明：站点已发布（tl1253b 回填），Pages 站点根为
// https://linearl.github.io/DeepSeek-Reasonix/（实测无 docs-site 段；
// workflow 侧 DOCS_BASE_URL 与此对齐）。若 Pages 地址再变，改下面这一行
// 常量 + 同步更新 docs-site-entry.test.ts 的 URL 断言。

import { openExternal } from "./bridge";

export const DOCS_SITE_BASE_URL = "https://linearl.github.io/DeepSeek-Reasonix/";

// 打开产品文档：走系统默认浏览器（openExternal → Wails BrowserOpenURL），
// 不在 webview 内导航。整个调用链是同步 fire-and-forget，无等待、无网络往返。
export function openProductDocs(): void {
  openExternal(DOCS_SITE_BASE_URL);
}
