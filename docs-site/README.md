# docs-site

产品文档静态站（GitHub Pages）。零运行时依赖：纯 Markdown + 一个 Node 构建脚本（标准库实现「够用子集」的 Markdown→HTML 渲染）。

## 目录结构

```
docs-site/
  index.md                  首页（产品定位 + 分区导航）
  getting-started/          安装 / 首次运行 / 快捷键
  features/                 功能总览 / 本 fork 特性
  reference/                配置项 / 命令参考
  troubleshooting/          日志与诊断 / FAQ
  assets/styles.css         站点样式
  scripts/build.mjs         构建脚本（docs-site/{index.md,四分区}/*.md -> dist/）
```

## 本地构建与预览

```sh
cd docs-site
npm run build        # node scripts/build.mjs，输出 dist/（Git 忽略）
npm run preview      # npx serve dist
```

Windows 无 npx 时可用：`python -m http.server 8000 --directory dist`。

## 写作约定

- 每篇 md 顶部可选 front-matter：`<!--meta title=页面标题 desc=一句话描述 -->`（title 含空格用引号包住）。
- 构建脚本只支持「够用子集」：标题 / 段落 / 列表（不嵌套）/ 表格 / 代码块 / 引用 / 行内代码 / 粗体 / 斜体 / 链接。不要用任务列表、图片、脚注、删除线。
- **站内链接直接写 `.html` 相对路径**（如 `first-run.html`、`../reference/config.html`）；构建脚本不改写链接。
- 表格单元格内不能出现竖线 `|` 字面量。
- 素材来源：仓库 `README.zh-CN.md`、`docs/*.zh-CN.md`、`release-notes/FORK-*.md`、`internal/cli/slash_registry.go`（斜杠命令权威清单）。

## 发布

`.github/workflows/docs-site-pages.yml`：push 到 `main-v2-stable` 且改动涉及 `docs-site/**` 时自动构建发布（也可手动 workflow_dispatch 触发）。构建用 Node 22 标准库，无需 npm ci。

**发布后必做核对**（不能只看 workflow 绿）：

1. `curl -I <pages-url>` 断言 200。
2. 拉取首页 grep 新增文案，断言线上内容已更新。
3. 首页能点到 4 个一级目录，逐页断言 200。

**基址对齐**：构建基址由 `DOCS_BASE_URL` 控制（workflow 中当前为 `/DeepSeek-Reasonix/docs-site/`），并与前端 `desktop/frontend/src/lib/docsSite.ts` 的 `DOCS_SITE_BASE_URL` 常量对齐（命令面板「产品文档」与 F1 跳转用）。Pages 实际启用后若真实路径不同，**两处一起改**。

## 已知约束

- 同仓库的 `site/`（Astro 官网，`pages.yml`）也部署 Pages；GitHub 同仓 Pages 只有一个发布源，两者能否共存取决于仓库 Pages 配置（详见 workflow 文件头注释）。若冲突，退路是把 docs-site 发布到独立仓库。
