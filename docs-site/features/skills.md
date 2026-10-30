<!--meta title="内置 skill 介绍" desc="随安装包内置的 19 个 skill：装完即有，按需触发。" -->

# 内置 skill 介绍

skill 是写给 agent 的「操作手册」——一段可复用的流程说明，遇到对应场景时 agent 自动加载并按它执行。本 fork 随安装包内置 **19 个 skill**，装完即有，无需额外安装。

它们按用途分四类：**fork 开发流程**、**通用工程实践**、**调研与分析**、**自诊断与配置**。

## fork 开发流程（6 个）

这一组是 fork 自身开发流程的沉淀，也是本 fork 区别于上游的「工程资产」。

| skill | 用途 |
| --- | --- |
| `ll-fork-guide` | fork 开发指南：定位与上游协作方针、开发铁律、合并上游规则、出包收尾三件套、构建与预算棘轮、常见坑速查 |
| `ll-iteration-intake` | 迭代收口（阶段 1/3）：把意见箱、崩溃残留、性能告警、tasklist 状态、上游动向扫成需求初稿 |
| `ll-iteration-plan` | 迭代规划（阶段 2/3）：需求排序、定单轮开发范围、会话分工、分支与验收标准 |
| `ll-iteration-dev` | 并行 worktree 开发（阶段 3/3）：主会话分解任务、建 worktree、派开发会话、拉取式收货、合并与发版 |
| `ll-iteration-audit-fix` | 跨会话对抗式审计与修复：「查找者 / 对抗审查者」对子逐范围审计，审计阶段严格只读 |
| `ll-iteration-review` | 方案评审三档分级：S 轻量迭代 / M 标准互评 / L 重专家团双提案双复查 |

## 协作与版本管理（3 个）

| skill | 用途 |
| --- | --- |
| `ll-zcode-collaboration` | Reasonix 与 zcode 协同开发流水线：handoff 书写、心跳+留言板通信、并行子代理逐件交付、逐件审计不攒批 |
| `ll-update` | 版本切换：把 NSIS 安装包拆成版本目录三件套，一键升级或回滚 |
| `desktop-automation` | 桌面版 UI 自动化自验：给运行中的桌面实例开 CDP 端口，用 playwright 连接做导航、点击、截图、断言 |

## 通用工程实践（6 个）

| skill | 用途 |
| --- | --- |
| `gh-issue-submit` | 提交高质量 issue：发现仓库模板、查重（含已关闭）、源码调研定位根因并给分级修复建议 |
| `gh-issue-triage` | issue 分诊闭环：收集、分类、修复、以测试与 commit 验证，只有带证据才关闭 |
| `collect_issues` | 把本地意见箱的未处理反馈逐条分析分类，转成 tasklist 里的任务 |
| `dogfood` | 系统性探索与测试一个应用，找出 bug、体验问题与其他毛病 |
| `feature-boundary-planner` | 把一处行为改动映射到 UI 载体、状态归属、协议命令、持久化与验证方式，做影响面分析 |
| `react-best-practices` | React / Next.js 性能优化实践（Vercel Engineering，MIT）：组件、数据获取、bundle 优化 |

## 调研与分析（3 个）

| skill | 用途 |
| --- | --- |
| `deep-research` | 多源深度调研：把问题拆成独立子问题并行取证，只保留有出处的结论，最终收敛成一份书面结论 |
| `data-analytics` | 有据可查的量化分析：质量检查、指标诊断、KPI 设计，每条建议都指向一个数字 |
| `memory-search` | 跨会话轨迹分析：找重复出现的错误、工具使用模式、执行链与提示词习惯 |

## 自诊断与配置（1 个）

| skill | 用途 |
| --- | --- |
| `reasonix-guide` | 自诊断指南：排查与配置各能力面——skills 的优先级与发现目录、命令覆盖顺序、hooks 事件等 |

## 怎么用

- **自动触发**：正常对话即可。当你的请求命中某个 skill 的适用场景，agent 会自己加载它——不需要你记住名字。
- **显式指定**：说「用 `ll-update` 帮我切到这个版本」，直接点名。
- **查看内容**：每个 skill 都是仓库里的一份 Markdown，源文件在 [`internal/skill/builtincontent/`](https://github.com/Linearl/DeepSeek-Reasonix/tree/main-v2-stable/internal/skill/builtincontent) 下按目录组织，可直接阅读。

## 自定义

内置 skill 是起点不是终点。你可以在项目级或用户级目录放置自己的 skill，同名时自定义版本优先生效。写法见 [reasonix-guide](https://github.com/Linearl/DeepSeek-Reasonix/tree/main-v2-stable/internal/skill/builtincontent/reasonix-guide) 或仓库文档。

## 下一步

- [关键特性详介](key-features.html)
- [全部特性一览表](feature-index.html)
