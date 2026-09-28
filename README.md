# DeepSeek-Reasonix（Linearl 维护分支）

> **上游项目与完整介绍见 → [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)**（README / 官方文档 / studio 分支）。
> 本仓库是其**长期维护的功能超集分支**：保持与上游版本对齐，持续修复与优化，并承载上游未合并的体验与效率特性。
> 版本节奏：`1.38.3-YYYYMMDD-HHMM` 时间戳构建，每包配 release notes。

## English TL;DR

A **long-term maintained, feature-superset fork** of [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix):

- **Track every stable upstream release** (6 major versions aligned so far) while keeping our own fixes and features on top — everything upstream has, plus more.
- **Stability first**: the upstream `main-v2` line has been through repeated regressions and sweeping re-architecture; we ship timestamped builds (`1.38.3-YYYYMMDD-HHMM`) with per-build release notes, machine-level verification, and default-off experimental switches so the default experience stays predictable.
- **Active upstream collaboration**: 95 issues + 84 pull requests contributed upstream; anything valuable to both sides lands upstream first, anything not merged stays maintained here.

Upstream project & docs: **[esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)**.

---

## 中文速览（TL;DR）

- **上游功能的超集**——追齐上游每个稳定版本（已对齐 6 个大版本），上游有的我们都有；我们有的（多会话协作、项目分组、autopilot…）上游不一定有。
- **长期维护**——不是临时补丁仓：持续 bug 修复、体验优化、装机级验证；**发版节奏约每周一包**（时间戳构建 + 增量 release notes）。**重点维护 desktop 桌面版**，目前不做 CLI 的发版（CLI 侧改动随源码进仓库，但不单独出包）。
- **向上游持续回馈**——累计向上游提交 **95 个 issue + 84 个 PR**，能进上游的尽量进上游，进不了的留在这里长期维护。

---

## 1. 为什么会有这个分支：历史与现状

### 上游发生了什么

DeepSeek-Reasonix 主线（`main-v2`）在近期经历了连串动荡：

- **稳定性问题连续多个版本反复出现**：近 60 天内 **100+ 个 Bug 类 issue**（标题含 Bug，实际触到查询上限），近 90 天 14 个 `crash`、5 个 `regression` 标题 issue；上游 discussions 里有用户「用了两个月失望透顶，我已经弃坑了」（#10744）与「1.38.3 版本 bash 编辑被频繁拒绝」（#10497）等真实反馈。
- **旧问题未收敛即大改架构，1.38.3 之后的若干个版本都不稳定**：上游在 v1.38 系对桌面布局与运行时做了多轮结构性重做（含 Electron 迁移方向的反复），**随后的 1.39.x 若干版本持续暴露大量问题**——界面回归、升级断裂、启动链缺陷（#10222 / #10106 / #10171 等）批量出现，直到今天仍在点状修复。
- **issue 被批量关闭，而不是被修复**：issue 一度积累到 **约 1.5k**，后来 open 数「奇迹般」降到 121——见 discussions [#11140「issues 从 1.5k 降到 121，怎么做到的」](https://github.com/esengine/DeepSeek-Reasonix/discussions/11140)。**这种做法实际埋掉了大量合理的用户需求：只要用户继续停留在上游版本，这些问题就永远不会被修复。** 当前 open 仍有 123（截至 2026-09-28）。
- **主线进入仅维护态，开发精力转向 studio**：上游默认分支已切为 `studio`（studio-v2.20.x 一天可发四版），主线版本（v1.39.3）转为点状修复推进。

### 我们在跟进中遇到的困难

- **高频反馈仍跟不上上游变动速度**：95 个 issue + 84 个 PR 持续回馈，但复发有两层——
  - **上游架构级改动让老问题重犯**：新一批回归随每次大改批量出现，我们刚收敛稳定又要重查一遍；
  - **上游更新让我们的补丁失效**：上游改掉补丁赖以成立的前提（文件搬迁、结构重做、行为改写），补丁看似还在、实际已被架空——例如 classic 布局被上游分批退役后，每次合并都要手工恢复我们的布局实现；基于上一版做的优化也会在合并中被上游侧改动冲掉，只能靠每次合并后的完整性核对（`check-fork-integrity.mjs`）逐项找回。
- **我们需要的能力长期得不到合并**：项目分组（上游 #9222）、颜色筛选（#9221）等至今仍 open——「上游不一定合」正是这个 fork 长期存在的原因。
- **版本跃迁成本高**：1.34 → 1.39 跨多个大版本，每轮追齐都要做逐文件论证、双向比对与全量验证，否则就是静默丢功能。

---

## 2. 我们的定位

**长期维护（bug 修复 + 体验优化）+ 新特性，相当于上游功能的超集。**

三条原则（详见 [`FORK.md`](./FORK.md)）：

1. **保持对齐** —— 持续追齐上游稳定版本；每次合并逐文件论证、保留差异、双向 tree 比对。
2. **吸收合理改进** —— 上游的重构或修复只要更优就采用，**哪怕替换我们自己的实现**。
3. **向上游反馈** —— 对上游也有价值的能力以高质量 issue + PR 回馈；上游吸收后不再长期自己背。

---

## 3. 核心特性（区别于上游）

重点是**大幅改善日常体验与工作效率**的部分；完整清单见 [`FORK.md`](./FORK.md)。

### 多会话协作与自动化

| 特性 | 说明 |
|---|---|
| **多会话协作** | 会话间互发消息、派单/回执/进度对齐；主对话协调子对话分工（调研/开发/审计多线并行），跨会话任务链全程留痕 |
| **autopilot / 自动档** | 长任务自动推进、交付检查自动放行（无人值守跑长链）；配合 yolo/询问档分级授权 |
| **计划任务与心跳** | 侧边栏「自动化」：定时唤醒、周期任务，provider/model 可配置 |
| **子代理委派档位** | 输入框「+」菜单：light / balanced / aggressive 三档委派深度 |

### 项目组织与界面

| 特性 | 说明 |
|---|---|
| **项目分组**（上游 #9222 未合并） | 项目树新建分组、右键移动、可折叠、跨重启保持 |
| **颜色筛选与多选排序**（上游 #9221 未合并） | 调色板筛选/排序，支持多选（上游只有设色数据层，无筛选 UI） |
| **经典布局** | 上游已分批退役，我们保留——单栏/工作台布局长期可用 |
| **搜索历史提问** | 长会话内搜索并跳转历史提问，滚顶加载更早历史 |
| **输入框草稿持久化** | 草稿/粘贴块/附件路径跨重启不丢 |

### 稳定性与数据安全

| 特性 | 说明 |
|---|---|
| **会话压缩稳定性族** | 压缩失败不再拖死对话（失败截断/限流等待续跑/提前折叠）；长只读任务不再被误停 |
| **恢复副本查看与合并** | 设置→存储：列出 recovery 副本的主线/独有计数，预览分支后一键合并，孤儿副本可归档回收 |
| **存储自动瘦身** | 大会话 event log 按 records 上限自动压缩，根治长会话保存卡死 |
| **WAL 自动收缩** | 数据库日志文件超限自动回收，不再只增不减 |
| **桌面日志轮转** | `logs/desktop/desktop.log` 4MB × 25 份，GUI 无控制台时的诊断依据 |

### 效率与集成

| 特性 | 说明 |
|---|---|
| **已授权写目录面板 + 乐观并发写** | 设置→权限：会话级/全局写目录可视化管理；并行写不靠加锁换安全 |
| **本地服务器 / 远程网关** | 手机端 / Tailscale 接入，gateway token 一键复制。手机端 **GrandCouncil 尚在开发中**（能连、核心链路可用，但功能未完——不要当作成品依赖） |
| **DeepSeek effort 档位** | 4 档推理强度 + auto，按请求覆盖 |
| **OpenCode Go 用量** | 实验室开关：订阅三档窗口用量页内直看 |
| **实验室（实验特性面板）** | 大量默认关闭的实验特性集中管理，手动挡与旧行为逐位一致 |

---

## 4. 使用指南

### 安装与升级

1. **下载**：[Releases](https://github.com/Linearl/DeepSeek-Reasonix/releases) 中选 `1.38.3-YYYYMMDD-HHMM` 时间戳包（每包对应一份 release notes，注明包含的修复与特性）。
2. **升级**：直接覆盖安装；无存储格式变更时可无损升级。**回滚**：设置 → 实验 → 快速切换版本，或安装旧时间戳包。
3. **首次使用**：与上游一致；**改变使用体验的新增特性默认关闭**，在 设置 → 实验室（Laboratory） 中按需开启——不影响体验的内部改进（性能、稳定性、日志）则直接生效，无需配置。

### 仓库导览

| 路径 | 内容 |
|---|---|
| [`FORK.md`](./FORK.md) | fork 定位、与上游差异的结构化纪律、特性全表 |
| [`release-notes/`](./release-notes/) | 每个时间戳包的增量 release notes + `FORK-vs-upstream.md`（逐版本上游差异台账） |
| `scripts/check-fork-integrity.mjs` | 合并上游后的完整性核对清单（每次合并必跑） |
| `desktop/` | 桌面端（独立 Go module + 前端）；构建见 `scripts/build-local-installer.sh` |

### 从源码构建

```bash
bash scripts/build-local-installer.sh <版本号>   # 产出 NSIS 安装器并铺 staging
```

构建依赖 wails CLI + NSIS（本机工具链位置见仓库文档）；构建前请先更新 `release-notes/` 对应 notes 文件。

---

## 5. 欢迎提 issue

- **在这个仓库提**：使用体验、bug、特性建议都欢迎——我们响应快、装机验证、修复直接进时间戳包。
- **建议用内置 gh 技能提**：让 agent 直接用内置的 GitHub 技能做**问题分析**（日志取证、源码定位、复现步骤整理），再产出**高质量 issue / PR**——这是我们向上游 95 issue + 84 PR 的标准姿势，本地反馈同样适用：分析到位的 issue 修得快，PR 直接带验证面。
- **也继续向上游提**：对上游也有价值的修复，我们会整理成高质量 issue/PR 反馈给 [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)——上游合并后两边都不用再背。
- 报 bug 时附上：时间戳包版本 + 复现步骤 + `logs/desktop/desktop.log` 相关片段，能大幅缩短定位时间。

---

*本 fork 与上游 DeepSeek-Reasonix 并行维护；上游项目介绍与授权协议见 [上游 README](https://github.com/esengine/DeepSeek-Reasonix#readme)。*
