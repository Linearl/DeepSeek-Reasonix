<!--meta title=Reasonix 产品文档 desc=Reasonix 产品文档首页：快速开始、功能、参考与疑难排查。 -->

# Reasonix 产品文档

Reasonix 是一款开源（MIT）的编码 Agent：**可以一直开着跑**。一套本地引擎（单个 Go 二进制），四个入口——终端、桌面端、浏览器，或通过 ACP 接入你的编辑器。计划模式、分级审批、工作区沙箱与逐轮 checkpoint，让长时间自治运行始终可读、可撤销。

## 核心特性

- **多模型 · 可组合**：DeepSeek 预设内置，任何 OpenAI/Anthropic 兼容端点都只是一条配置；可选双模型协同（执行器 + 规划器）。
- **配置驱动**：provider、agent、工具、插件全部在 `reasonix.toml` 声明，内核无硬编码模型。
- **权限与沙箱**：Ask / Auto / YOLO 三档审批 + Plan 先规划后执行；工作区沙箱 + 已授权写目录面板。
- **可恢复**：逐轮 checkpoint 随时回退；会话分支、合并恢复副本、崩溃诊断一条龙。
- **插件生态**：MCP server、Extension Protocol Sidecar、自定义斜杠命令 / skills / hooks。

## 从这里开始

| 分区 | 说明 |
| --- | --- |
| [快速开始](getting-started/) | 安装（CLI / 桌面端 / VS Code / 源码）、首次运行、快捷键 |
| [功能](features/) | 功能总览与本 fork 独有特性 |
| [参考](reference/) | 配置项、命令、斜杠命令的查表式参考 |
| [疑难排查](troubleshooting/) | 日志位置、诊断命令、更新卡住、FAQ |

## 关于本站所文档化的仓库

本站文档化的是 [Linearl/DeepSeek-Reasonix](https://github.com/Linearl/DeepSeek-Reasonix)——上游 [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 的长期维护 fork（`main-v2-stable` 分支）。数据与上游兼容，随时可切回官方版；fork 独有改动见[本 fork 特性](features/fork-highlights.html)。

## 快速链接

- [在 GitHub 仓库查看源文件](https://github.com/Linearl/DeepSeek-Reasonix/tree/main-v2-stable/docs-site)
- [提交文档问题或建议](https://github.com/Linearl/DeepSeek-Reasonix/issues)
- [上游官方网站](https://esengine.github.io/DeepSeek-Reasonix/)
