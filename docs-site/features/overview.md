<!--meta title=功能总览 desc=Reasonix 核心能力地图：会话、上下文工程、权限沙箱、检查点、扩展、多端。 -->

# 功能总览

Reasonix 是「可以一直开着跑的编码 Agent」：配置驱动、多模型可组合、插件驱动，内核无硬编码模型。这里是核心能力地图。

## 多模型 · 可组合

- DeepSeek 作为预设内置；任何 OpenAI 兼容端点都只是一条配置（`reasonix.toml` 声明）。
- 可选双模型协同：执行器 + 规划器各自独立，缓存稳定的 session。
- 模型能力（视觉输入、web 搜索等）按 provider 配置自动发现。

## 会话体验

- 多标签会话、转录/轨迹双视图。**命令面板（`Ctrl+K`）经 fork 大幅增强**——搜索并打开任意会话、面板与特性入口，是全应用最快的导航方式，建议熟练使用。
- 会话分支（`/branch`）、切换（`/switch`）、重命名、导出（Markdown / JSON / PDF / 图片）。
- 长会话历史搜索与按需加载更早记录。
- 输入框草稿跨重启不丢（fork 增强，见 [fork 特性](fork-highlights.html)）。

## 上下文工程

- 缓存友好的上下文维护：启动注入稳定环境摘要；旧工具输出先 snip/prune 再进入摘要压缩。
- 自动压缩阈值可配（`reasonix config compact-ratio`，默认 80%）。
- `/compact` 手动压缩、`/context` 查看占用；每轮显示上下文预算行（fork 新增）。

## 权限与沙箱

- 三档审批姿态：Ask / Auto / YOLO（`Ctrl+Y` 切换），外加 Plan 先规划后执行。
- 工作区沙箱限制写入范围；已授权写目录面板可视化管理（fork 增强）。
- 全局凭据与项目配置隔离：模型与工具读不到 provider 密钥。

## 检查点与恢复

- 逐轮 checkpoint：任何改动可回滚（`/rewind`）。
- 会话崩溃后可恢复；`reasonix doctor` 检查配置与安装问题。
- 合并恢复副本管理（fork 独有）：多副本工作阶段可预览、比对、合并。

## 扩展与自动化

- MCP server 提供工具、提示词和资源；Extension Protocol Sidecar 可拦截运行时事件、提供 Provider 与结构化 UI，经版本化插件包分发。
- 斜杠命令、hooks、skills 均可用户级自定义。
- 计划任务 / 心跳自动化，每条任务可指定 provider 与模型（fork 增强）。

## 多端

一套本地引擎，四个入口：

| 入口 | 形态 |
| --- | --- |
| CLI / TUI | 终端交互、一次性任务（`reasonix run`）、脚本化（`-p`） |
| 桌面端 | Wails GUI，项目树 + 多标签会话 + 设置中心 |
| 浏览器 | 本地 serve 或远程网关接入 |
| 编辑器 | ACP 协议接入 VS Code 等编辑器 |

## 下一步

- [本 fork 独有特性](fork-highlights.html)
- [配置项参考](../reference/config.html)
