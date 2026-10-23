// 任务 1102：产品文档静态站（GitHub Pages）的前端接线点。
//
// 静态站本体在仓库根 `docs-site/`，由 `.github/workflows/docs-site-pages.yml`
// 构建并发布到 GitHub Pages；本模块只持有「应用去哪里打开文档」的 URL 常量
// 与跳转函数——命令面板「产品文档」项（任务 1101 规划的帮助与支持组）与
// F1 快捷键都走这里，改指向只动这一个文件。
//
// URL 说明（重要）：站点尚未发布，正式地址要等 Pages 启用后回填。
// 在此之前用的是 GitHub 仓库文档目录的浏览页（仓库 public，恒可访问），
// 保证「产品文档」入口任何时候点开都不是死链；回填时只改下面这一行常量。

import { openExternal } from "./bridge";

// TODO(任务 1102 回填)：Pages 启用后改为
// `https://linearl.github.io/DeepSeek-Reasonix/docs/`（以 Settings → Pages
// 显示的实际地址为准）。
export const DOCS_SITE_BASE_URL = "https://github.com/Linearl/DeepSeek-Reasonix/tree/main-v2-stable/docs-site/";

// 打开产品文档：走系统默认浏览器（openExternal → Wails BrowserOpenURL），
// 不在 webview 内导航。整个调用链是同步 fire-and-forget，无等待、无网络往返。
export function openProductDocs(): void {
  openExternal(DOCS_SITE_BASE_URL);
}
