<!--meta title=Reasonix 产品文档 desc=Reasonix 产品文档首页：快速开始、功能、参考与疑难排查。 -->

# Reasonix 产品文档

Reasonix 是一款开源（MIT）的编码 Agent：**可以一直开着跑**。一套本地引擎（单个 Go 二进制），四个入口——终端、桌面端、浏览器，或通过 ACP 接入你的编辑器。计划模式、分级审批、工作区沙箱与逐轮 checkpoint，让长时间自治运行始终可读、可撤销。

## 产品宪法（Decision Constitution）

> **权威源**：仓库根 [`CONSTITUTION.md`](https://github.com/Linearl/DeepSeek-Reasonix/blob/main-v2-stable/CONSTITUTION.md)（2026-11-06 用户逐条拍板确立，修改需用户拍板）；本页为同步投影，冲突时以仓库为准。

> **Reasonix fork = 一个「透明 · 好用 · 高效 · 多端」的 AI 工作台。**

| 维度 | 含义 |
| --- | --- |
| **透明** | 对人类与对 agent 双透明：过程可见可审 + 信息/功能对 agent 同样可及 |
| **好用** | 易用性：输入 / 交互 / 设置 / 草稿 / 快捷命令 |
| **高效** | autopilot × 跨会话协作 × 工作流（心跳自动化、任务卡、信箱、workflow 引擎） |
| **多端** | GC 手机客户端：serve + GrandCouncil App |

**准入判据**：任何需求先问“服务哪一维？”——答不上来 = 不做或降级为实验。

### 四条宪法

1. **四维定位：不服务四维者不做**——答不出“让哪一维变好”→ 不做或降级为实验（云端多租户、通用 IDE、模型训练、多用户权限、创作类工具、通用 RPA 均非四维）。
2. **独立演进（不再 merge 上游）**——上游与竞品是参考源，不是合并对象；安全 / 数据完整性 / 崩溃三类上游 fix 必看（周扫心跳甄别）；差异收益证据三选一：**实测数字 / 可复现缺陷 / 用户明示**；新机制立项必须写 non-goals。
3. **单一真相源 + 可回退**——同一语义只允许一处权威源，其余是只读投影；两查（SSOT 查 / 回退查）；变更三件套（设计评审 → 迁移方式 → 变更记录）。
4. **减法优先，开关转正即删开关**——新增前三步否决（能不能不加 / 现有机制能否覆盖 / non-goals）；开关只有三种终局：转正（默认开 + 删开关）、保留（登记 owner + 守护用例）、删除；**不存在“永远默认关”的开关**——那是待清账的欠条。

### 判据表（12 条，日常直接用）

| 层 | # | 判据 | 用在哪 |
| --- | --- | --- | --- |
| 定位 | L1 | 服务四维之一吗？ | 需求准入 |
| 决策 | D1 | 上游/竞品怎么做？差异收益有证据吗？成本 ≤ 收益吗？ | 架构拍板 |
| 决策 | D2 | 这条语义几处真相源？先收口再改 | 状态/配置/判据改动 |
| 决策 | D3 | 出问题怎么退？无退路不做 | 破坏性变更 |
| 决策 | D4 | 能不能不加？non-goals 是什么？ | 新增机制 |
| 决策 | D5 | 功能正确性 > 新特性；实验特性是头号嫌疑 | 排障与取舍 |
| 决策 | D6 | 需求真实性 ≠ 该产品化 ≠ 该保留 | 特性存废 |
| 执行 | E1 | 先测量再下手（改造前 / 后 / 回归三次） | 性能类 |
| 执行 | E2 | 风险未验证 ≠ 风险低 | 清理/删除类 |
| 执行 | E3 | 派生数字必须对得上原始读数 | 任何量化结论 |
| 执行 | E4 | 修完补防线（上限 + 新增即红） | 缺陷修复 |
| 执行 | E5 | 破坏性操作：先 ls、禁多目标、回收语义、敏感文件先备份 | 一切删除/替换 |

> 用法：按 L1 → D1/D4 → D2 → D3 顺序过；任一条不过，落到“不做 / 降级 / 先补证据”。

### 不做清单（N1–N6）

云端多租户 / SaaS 化、通用 IDE 替代、模型训练 / 微调、多用户权限体系、创作类工具（写作/小说/配图流水线）、通用 RPA / 桌面操作套件——均与「个人工作台」定位不符，不立项。

> 决策台账（Q1–Q5 裁定）、D2 细化（修复设施 / 业务态边界）、执行落点与变更记录见仓库 [`CONSTITUTION.md`](https://github.com/Linearl/DeepSeek-Reasonix/blob/main-v2-stable/CONSTITUTION.md)。

## 介绍视频

**2 分 03 秒看完这个 fork 分支**——为什么存在、与上游的关系、独有特性一览：

<video src="assets/reasonix-fork-intro.mp4" controls preload="metadata" width="720">你的浏览器不支持内嵌视频，可打开仓库内 docs-site/assets/reasonix-fork-intro.mp4 直接观看。</video>

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
| [功能](features/) | 关键特性详介、全部特性一览表（81 项编号索引）、内置 skill、本 fork 独有特性 |
| [参考](reference/) | 配置项、命令、斜杠命令的查表式参考 |
| [疑难排查](troubleshooting/) | 日志位置、诊断命令、更新卡住、FAQ |
| [更新日志](changelog/) | 10.12 起的版本时间线：正式 Release 与内部包分开标注 |
| [关于](about/) | 上游问题、开发方法论、fork 取舍清单、版本规则与维护现状 |

## 关于本站所文档化的仓库

本站文档化的是 [Linearl/DeepSeek-Reasonix](https://github.com/Linearl/DeepSeek-Reasonix)——上游 [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 的长期维护 fork（`main-v2-stable` 分支）。数据与上游兼容，随时可切回官方版；fork 独有改动见[本 fork 特性](features/key-features.html)。

## 快速链接

- [在 GitHub 仓库查看源文件](https://github.com/Linearl/DeepSeek-Reasonix/tree/main-v2-stable/docs-site)
- [提交文档问题或建议](https://github.com/Linearl/DeepSeek-Reasonix/issues)
- [上游官方网站](https://esengine.github.io/DeepSeek-Reasonix/)
