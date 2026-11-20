# DeepSeek-Reasonix（Linearl 维护分支）

> **上游项目见 → [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)**。
> 本仓库是其**长期维护的功能超集分支**：早期追齐上游版本；上游转入仅维护态并于 2026-11-12 公告停止维护后，本 fork 转为**独立演进**——持续修复与优化，并承载上游未合并的体验与效率特性。
> 版本节奏：`1.38.3-YYYYMMDD-HHMM` 时间戳构建，每包配 release notes。

## English TL;DR

A **long-term maintained, feature-superset fork** of [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix):

- **Independent evolution.** We tracked multiple major upstream releases before upstream went maintenance-only; upstream has since announced end of maintenance (2026-11-12). Today we cherry-pick the fixes worth having instead of merging whole releases, and no longer file upstream issues/PRs.
- **Stability first**: timestamped builds (`1.38.3-YYYYMMDD-HHMM`) with per-build release notes, machine-level verification, and default-off experimental switches so the default experience stays predictable.
- **Track record**: 95 issues + 84 pull requests contributed upstream before it closed; everything not merged upstream stays maintained here.

**How we develop it**: benchmark against the best and judge for ourselves; observability first (stability means a bug can be *seen, located, and proven*); automation (autopilot, multi-session collaboration, a human-AI split with the zcode execution line); and self-iteration — the agent is a daily user too, but **a human still decides what gets adopted**.

---

## 中文速览（TL;DR）

- **上游功能的超集，已独立演进**——早期追齐过多个大版本；上游 2026-11-12 停止维护后不再整批跟进、也不再向上游提 issue/PR，改为按需 cherry-pick 修复、吸收机制。上游有的我们基本都有；我们有的（多会话协作、项目分组、群聊、autopilot…）上游不一定有。
- **长期维护**——不是临时补丁仓：持续 bug 修复、体验优化、装机级验证；**发版节奏约每周一包**。**重点维护 desktop 桌面版**（CLI 改动随源码进仓库，不单独出包）。
- **累计向上游贡献 95 个 issue + 84 个 PR**（上游关闭反馈通道前）；对上游有价值的修复留在这里长期维护。

## 介绍视频

**2 分 03 秒看完这个 fork**——为什么存在、与上游的关系、独有特性一览：[▶ 播放介绍视频](https://linearl.github.io/DeepSeek-Reasonix/)（文档站首页内嵌播放）。

---

## 1. 为什么会有这个分支（摘要）

上游 `main-v2` 在 1.35~1.38.12 与 Electron 迁移期两轮问题爆发（大量 crash/regression/升级断裂，issue 曾批量关闭而非修复），**1.38.3 是两轮爆发之间的稳定窗口**——本 fork 停留此基线（Wails），把稳定性做扎实。我们需要的能力（项目分组、颜色筛选等）长期得不到上游合并，追齐版本的冲突成本也高于收益，因此从「追齐」转为「独立演进」：好修复照 cherry-pick，不再整批合并。

> 完整历史、定位与开发方法（含标杆对标、可观测性优先、自动化与自迭代的完整论述）见 **[docs/PROJECT-BACKGROUND.zh-CN.md](./docs/PROJECT-BACKGROUND.zh-CN.md)**。

## 2. 独有改进一览

按主题归纳（逐版本明细见 [`release-notes/FORK-vs-upstream.md`](./release-notes/FORK-vs-upstream.md) 差异台账 22 张版本表）：

| 主题 | 内容 |
|---|---|
| **多会话协作** | 通讯录/跨会话信箱与派单（hop 链、任务卡）、群聊频道（频道历史、@投递、副管理员）、协作心跳与守护线 |
| **上下文与压缩** | 压缩进度悬停数据卡（耗时/速度/折叠数/释放量）、压后视图大幅瘦身（86.6%→28.9% 实测）、五入口归一化、尾部保留 32K/3 回合新口径、投影失效可见化与有界降级 |
| **消息可靠性** | 新消息必渲染、切回即显示（账本对账门）、回合结束排队、压缩中消息注入、回溯不丢消息 |
| **项目与会话管理** | 项目分组、会话分组、项目颜色筛选、回收站（删除可恢复+dry-run 预演）、对话版本管理（回溯/快照） |
| **记忆系统** | 背景记忆索引与召回工具、置顶记忆索引化（150 字符行+按需召回）、记忆候选建议与归档 |
| **实验室（实验特性）** | 分组/卡片/徽章配置驱动 + 推荐/可选/未稳定三档徽章、特性速览墙、生效时机明示（即时/重启/边界） |
| **更新与版本** | 无感升级一键铺包重启、多版本快速切换（回退一条命令）、时间戳包+逐包 notes |
| **稳定性与可观测** | 崩溃与卡顿根因治理（issue #92/#93/#94/#96 收口）、doctor 判活与诊断包、boot/首响性能治理、CDP 界面自验 |
| **经典布局保留** | classic 双布局完整保留（上游已分批退役，fork 每次合并手工恢复） |
| **界面打磨** | 侧栏漂移行级锚定、跳转条遮挡修复、亮色主题对比度、模型下拉修正等一批细节 |

## 3. 安装与使用

1. **下载**：[Releases](https://github.com/Linearl/DeepSeek-Reasonix/releases) 选 `1.38.3-YYYYMMDD-HHMM` 时间戳包（每包配 release notes）。**推荐使用最新正式 Release**。
2. **升级**：覆盖安装即可；回滚用 设置 → 实验 → 快速切换版本，或安装旧时间戳包。
3. **特性开关**：改变体验的新特性默认关闭，位于 设置 → 实验室；性能/稳定性类内部改进直接生效。
4. 存储格式与官方版兼容；**不要回退到 1.34/1.38.1 以下**（会话格式单向升级，旧构建拒绝加载，文件不损坏）。

> **推荐「经典桌面风格」**：fork 特性以经典布局为基准开发，其他布局（workbench 等）功能可能略有缺失。

## 4. 开发方法（摘要）

一句话版：**向最好的学但独立判断**（机制不合用就拆，如 recovery fence 在六家竞品中无对应即移除）；**可观测性优先**（bug 要能被看见、定位、证明）；**自动化**（autopilot、多会话协作、zcode 执行线的人机分工）；**自迭代**（agent 也是日常用户，主动报告与立项）——但**采纳与否由人决定**。

> 完整版（3.1 标杆对标 / 3.2 观测与反馈 / 3.3 组织方式）见 **[docs/PROJECT-BACKGROUND.zh-CN.md](./docs/PROJECT-BACKGROUND.zh-CN.md)**。

## 5. 仓库结构与构建

| 路径 | 说明 |
|---|---|
| `FORK.md` | 结构化纪律（开发判据/新特性规范/出包检查清单的权威文件） |
| `release-notes/` | 每包增量 notes + `FORK-vs-upstream.md` 逐版本差异台账 + `WRITING-GUIDE.md` 写作规范 |
| `docs/` | `RELEASE-GUIDE.zh-CN.md`（发版流程）· `PROJECT-BACKGROUND.zh-CN.md`（项目背景完整版） |
| `scripts/check-fork-integrity.mjs` | 合并上游后的完整性核对（每次合并必跑） |
| `scripts/check-architecture.mjs` | 结构红线（基线增量报告） |
| `desktop/` | 桌面端（独立 Go module + 前端） |

### 从源码构建

```bash
bash scripts/desktop-build.sh windows/amd64 <版本号>   # wails 官方管线 + ldflags 版本注入，产出 NSIS 安装器
```

构建依赖 wails CLI + NSIS；agent 环境需注入 `~/go/bin` 等 PATH（详见 `docs/RELEASE-GUIDE.zh-CN.md`）。构建前先更新对应 release notes。

## 6. 欢迎提 issue

- **在这个仓库提**：使用体验、bug、特性建议都欢迎——响应快、装机验证、修复直接进时间戳包。
- **建议用内置 gh 技能提**：让 agent 先做问题分析（日志取证、源码定位、复现步骤）再产出高质量 issue/PR——分析到位的修得快。
- 报 bug 附上：时间戳包版本 + 复现步骤 + `logs/desktop/desktop.log` 片段，能大幅缩短定位时间。
- 上游已于 2026-11-12 关闭反馈通道，**不再向上游提 issue/PR**；有价值的修复会留在本仓库持续维护。

---

*本 fork 与上游 DeepSeek-Reasonix 并行维护；上游项目介绍与授权协议见 [上游 README](https://github.com/esengine/DeepSeek-Reasonix#readme)。*
