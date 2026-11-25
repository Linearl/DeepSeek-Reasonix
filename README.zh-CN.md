<p align="center">
  <img src="docs/logo-ghost-wave-effect.svg" alt="Reasonix" width="360"/>
</p>

<p align="center">
  <a href="./README.md">English</a>
  &nbsp;·&nbsp;
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./docs/GUIDE.zh-CN.md">指南</a>
  &nbsp;·&nbsp;
  <a href="./docs/ACP.zh-CN.md">ACP</a>
  &nbsp;·&nbsp;
  <a href="./docs/EXTENSIONS.zh-CN.md">扩展开发</a>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.zh-CN.md">规格</a>
  &nbsp;·&nbsp;
  <a href="https://linearl.github.io/DeepSeek-Reasonix/">文档站</a>
  &nbsp;·&nbsp;
  <strong><a href="https://discord.gg/XF78rEME2D">Discord</a></strong>
</p>

<p align="center">
  <a href="https://www.npmjs.com/package/reasonix"><img src="https://img.shields.io/npm/v/reasonix.svg?style=flat-square&color=cb3837&labelColor=161b22&logo=npm&logoColor=white" alt="npm version"/></a>
  <a href="https://github.com/Linearl/DeepSeek-Reasonix/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Linearl/DeepSeek-Reasonix/ci.yml?style=flat-square&label=ci&labelColor=161b22&logo=githubactions&logoColor=white" alt="CI"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/npm/l/reasonix.svg?style=flat-square&color=8b949e&labelColor=161b22" alt="license"/></a>
  <a href="https://www.npmjs.com/package/reasonix"><img src="https://img.shields.io/npm/dm/reasonix.svg?style=flat-square&color=3fb950&labelColor=161b22&label=downloads" alt="downloads"/></a>
  <a href="https://github.com/esengine/DeepSeek-Reasonix/stargazers"><img src="https://img.shields.io/github/stars/esengine/DeepSeek-Reasonix.svg?style=flat-square&color=dbab09&labelColor=161b22&logo=github&logoColor=white" alt="GitHub stars"/></a>
  <a href="https://atomgit.com/esengine/DeepSeek-Reasonix"><img src="https://atomgit.com/esengine/DeepSeek-Reasonix/star/badge.svg" alt="AtomGit stars"/></a>
  <a href="https://github.com/esengine/DeepSeek-Reasonix/graphs/contributors"><img src="https://img.shields.io/github/contributors/esengine/DeepSeek-Reasonix.svg?style=flat-square&color=bc8cff&labelColor=161b22&logo=github&logoColor=white" alt="contributors"/></a>
  <a href="https://github.com/esengine/DeepSeek-Reasonix/discussions"><img src="https://img.shields.io/github/discussions/esengine/DeepSeek-Reasonix.svg?style=flat-square&color=58a6ff&labelColor=161b22&logo=github&logoColor=white" alt="Discussions"/></a>
  <a href="https://discord.gg/XF78rEME2D"><img src="https://img.shields.io/badge/discord-join-5865F2.svg?style=flat-square&labelColor=161b22&logo=discord&logoColor=white" alt="Discord"/></a>
</p>

<p align="center"><sub>npm / stars / contributors / trendshift 等徽章统计的是上游 esengine/DeepSeek-Reasonix 仓库；CI 徽章为本仓库。</sub></p>

<p align="center">
  <a href="https://trendshift.io/repositories/27020?utm_source=trendshift-badge&amp;utm_medium=badge&amp;utm_campaign=badge-trendshift-27020" target="_blank" rel="noopener noreferrer"><img src="https://trendshift.io/api/badge/trendshift/repositories/27020/monthly?language=Go" alt="esengine/DeepSeek-Reasonix | Trendshift" width="250" height="55"/></a>
  <a href="https://trendshift.io/repositories/27020?utm_source=repository-badge&amp;utm_medium=badge&amp;utm_campaign=badge-repository-27020" target="_blank" rel="noopener noreferrer"><img src="https://trendshift.io/api/badge/repositories/27020" alt="esengine/DeepSeek-Reasonix | Trendshift" width="250" height="55"/></a>
</p>

<br/>

<p align="center"><strong>开源 · MIT · 单个 Go 二进制</strong></p>
<h3 align="center">可以一直开着跑的编码 Agent。</h3>
<p align="center">一套本地引擎,四个入口——终端、桌面端、浏览器,或通过 ACP 接入你的编辑器。计划模式、权限、工作区沙箱与逐轮 checkpoint,让长时间自治运行始终可读、可撤销。</p>

<div align="center">
  <video src="https://github.com/user-attachments/assets/ab2f3878-e224-4931-8254-060e7695cfb9" controls preload="metadata" width="560"></video>
</div>

<br/>

> [!IMPORTANT]
> **加入社区 · Community** — 上游社区的双语 Discord（非本 fork 维护；fork 的问题反馈请提本仓库 issue），提供安装答疑（`#help` / `#求助`）、工作流展示与功能想法。→ **<https://discord.gg/XF78rEME2D>**

## 关于本仓库（fork 说明）

本仓库是 [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 的**长期维护
fork**（`main-v2-stable`）：保留我们确实需要、但上游不一定合并的改动——**2026-10-05 起不再追齐上游
版本**；上游于 2026-11-12 公告停止维护后，转入**独立演进**（按需 cherry-pick 上游修复）。具体含义如下：

- **数据与上游兼容。** 会话数据按上游格式写入、按上游的宽松程度读取；在此之上还有一条**修复路径**：
  上游读不动的会话（写入中断 / 文件受损）在本仓库仍能正常打开。**随时可以切回官方版**：用官方安装包
  覆盖安装即可，走内置自动更新也可以。但**不要回退到 `1.34` / `1.38.1` 以下**——会话格式单向升级，
  旧构建会拒绝加载（文件本身不损坏）。
- **只测 Windows 桌面版** —— `win-amd64-desktop` + **经典布局（经典模式）**，这是我们日常使用的组合。
  其他目标（其他平台、CLI/TUI、VS Code 扩展、workbench 布局）在本仓库**未经验证**，不保证易用性，
  也不保证功能一致。
- **不再整批追齐上游。** 上游 fix 按需 cherry-pick（安全 / 数据完整性 / 崩溃三类优先甄别），其余按自身节奏独立演进。
- **每一项魔改都有版本对照清单**，见
  [`release-notes/FORK-vs-upstream.md`](release-notes/FORK-vs-upstream.md)：逐版本列出开关、默认值、
  承载文件，以及上游是否已吸收。完整入口文档是 [`FORK.md`](FORK.md)，逐版 release notes 见
  [`release-notes/`](release-notes/)。
- **稳定版本：`1.38.3-YYYYMMDD-HHMM` 时间戳包**，每包配 release notes，推荐使用最新正式 Release。
  `1.38.1` 已知不稳定请勿使用，也不要回退到 `1.34` / `1.38.1` 以下。
- **使用问题、Bug、建议**：欢迎提
  [issue](https://github.com/Linearl/DeepSeek-Reasonix/issues)（另见
  [CONTRIBUTING.md](CONTRIBUTING.md)）。

<br/>

## 本 fork 增加了什么

这是精简视图。**完整清单** —— 每项能力及其在界面里的入口 —— 见
[`FORK.md`](FORK.md#这个-fork-提供了什么)；按版本对照的差异台账见
[`release-notes/FORK-vs-upstream.md`](release-notes/FORK-vs-upstream.md)；面向用户的
13 条日常可感知改动见
[`release-notes/FORK-features-intro.md`](release-notes/FORK-features-intro.md)。

| 领域 | 重点能力 |
|---|---|
| **多会话协作** | 通讯录/跨会话信箱与派单（hop 链、任务卡）、群聊频道（频道历史、@投递、副管理员）、协作心跳与守护线 |
| **上下文与压缩** | 压缩进度悬停数据卡（耗时/速度/折叠数/释放量）、压后视图大幅瘦身（86.6%→28.9% 实测）、五入口归一化、尾部保留 32K/3 回合新口径、投影失效可见化与有界降级 |
| **消息可靠性** | 新消息必渲染、切回即显示（账本对账门）、回合结束排队、压缩中消息注入、回溯不丢消息 |
| **项目与会话管理** | 项目分组、会话分组、项目颜色筛选、回收站（删除可恢复+dry-run 预演）、对话版本管理（回溯/快照） |
| **记忆系统** | 背景记忆索引与召回工具、置顶记忆索引化（150 字符行+按需召回）、记忆候选建议与归档 |
| **实验室（实验特性）** | 分组/卡片/徽章配置驱动 + 推荐/可选/未稳定三档徽章、特性速览墙、生效时机明示（即时/重启/边界） |
| **更新与版本** | 无感升级一键铺包重启、多版本快速切换（状态栏一键换入暂存构建）、时间戳包+逐包 notes |
| **稳定性与可观测** | 崩溃与卡顿根因治理（issue #92/#93/#94/#96 收口）、doctor 判活与诊断包、boot/首响性能治理、CDP 界面自验 |
| **经典布局保留** | classic 双布局完整保留（上游已分批退役，fork 每次合并手工恢复） |
| **界面打磨** | 侧栏漂移行级锚定、跳转条遮挡修复、亮色主题对比度、模型下拉修正等一批细节 |

## 特性

- **配置驱动**：provider、agent、启用的工具、插件全部在 `reasonix.toml` 中声明，
  内核无硬编码模型。
- **多模型 · 可组合**：DeepSeek 作为预设内置；任何 OpenAI 兼容
  端点都只是一条配置。可选让两个模型协同（执行器 + 规划器），各自独立、缓存稳定的 session。
- **插件驱动**：MCP server 提供工具、提示词和资源；Extension Protocol v1
  Sidecar 还可以拦截运行时事件、提供 Provider 与结构化 UI，并通过版本化插件包分发。
- **缓存友好的上下文维护**：启动时注入稳定的环境摘要；旧工具输出会先 snip/prune，
  再进入摘要 compaction；内置工具 schema 合约有文档和回归测试保护。
- **零摩擦分发**：`CGO_ENABLED=0` 单二进制；一条命令交叉编译到六个目标平台。
  产物是完全自包含的静态二进制——目标机器上除二进制本身外无需安装任何东西。

## 安装

选择适合你的使用路径。CLI/TUI、桌面端和 VS Code 扩展都使用同一套本地
Reasonix 引擎。

### 路径 A：CLI / TUI

npm / Homebrew 为**上游发行渠道**，装到的是上游版本（不含 fork 特性；本 fork 的 CLI 不单独出包，
源码构建见路径 D）。仍可照常安装上游版：

```sh
npm i -g reasonix                  # 任意系统;自动拉取对应平台的原生二进制
brew install esengine/reasonix/reasonix   # macOS
```

预编译归档(`darwin|linux|windows × amd64|arm64`)和 `SHA256SUMS` 见每个
[GitHub release](https://github.com/esengine/DeepSeek-Reasonix/releases)。

### 路径 B：桌面端

前往本仓库的 [Releases](https://github.com/Linearl/DeepSeek-Reasonix/releases) 选取
`1.38.3-YYYYMMDD-HHMM` 时间戳包（每包配 release notes），推荐最新正式 Release。
上游官网 reasonix.io 分发的是上游版本。

| 平台 | 安装包 | 架构 |
| --- | --- | --- |
| macOS | 通用 `.dmg` 或 `.zip` | Apple Silicon / Intel |
| Windows | 安装器 `.exe` 或便携 `.zip` | x64 / ARM64 |
| Linux | `.deb` 或 `.tar.gz` | x64 |

本 fork 的发布包**不做代码签名**（无 MINISIGN / SignPath / Apple 签名，也不生成自动更新用的
`latest.json`），请手动安装，**不要依赖内置自动更新**。

### 路径 C：VS Code 扩展

请先完成路径 A。下列扩展为**上游扩展**——本 fork 未验证其与 fork 版的兼容性（见上方 fork 说明）。
扩展不内置 CLI，而是启动本机的 `reasonix acp` 后端，
并提供原生聊天、编辑器上下文、工具调用审批、模型选择和工作区会话。

- **VS Code：** [从 Visual Studio Marketplace 安装](https://marketplace.visualstudio.com/items?itemName=SivanLiu.reasonix-agent)
- **VSCodium / Eclipse Theia：** [从 Open VSX Registry 安装](https://open-vsx.org/extension/SivanLiu/reasonix-agent)
- **扩展 ID：** `SivanLiu.reasonix-agent` · [源码与使用说明](https://github.com/SivanCola/reasonix-vscode)

### 路径 D：从源码构建

先克隆仓库：

```sh
git clone https://github.com/Linearl/DeepSeek-Reasonix.git
cd DeepSeek-Reasonix
```

#### CLI

CLI 构建需要 **Go 1.26+**。模块固定了 `toolchain` 指令；
保持 `GOTOOLCHAIN=auto` 让 Go 自动下载固定的工具链，或自行安装。

```sh
make build      # -> bin/reasonix(.exe)
make cross      # -> dist/（darwin|linux|windows × amd64|arm64）
```

#### 桌面端

桌面端构建额外需要：

- **Node 24+ 和 pnpm 10**（`npm install -g pnpm@10`）用于前端
- **Wails CLI**，与共享的 `.wails-version` 固定版本一致

```sh
make wails-install
cd desktop
wails build      # 开发构建
```

fork 正式出包口径（ldflags 版本注入 + NSIS 安装器）：

```sh
bash scripts/desktop-build.sh windows/amd64 <版本号>
```

平台相关的 WebView 依赖和 Linux 构建标签见
[桌面端构建指南](desktop/README.md#prerequisites)。

## 快速开始

### CLI / TUI

以下命令仅适用于通过路径 A 安装的 CLI/TUI：

```sh
reasonix setup                      # 配置 provider 和模型
reasonix                            # 启动交互式会话
reasonix run "把 main.go 里的 TODO 实现掉"
```

需要项目指令时，可在交互式会话中运行 `/init`。

### 桌面端

从 [Releases](https://github.com/Linearl/DeepSeek-Reasonix/releases) 下载对应系统的时间戳安装包，
安装并启动 Reasonix，然后在应用内配置 provider 和模型即可使用。桌面端无需执行
上面的 CLI 命令。

CLI 进阶用法和详细配置见 **[CLI 命令参考](./docs/CLI.zh-CN.md)**、
**[指南](./docs/GUIDE.zh-CN.md)** 和
**[配置路径](./docs/CONFIG_PATHS.zh-CN.md)**。

## 文档

- **开始使用：** [指南](./docs/GUIDE.zh-CN.md) ·
  [CLI 命令参考](./docs/CLI.zh-CN.md) · [配置路径](./docs/CONFIG_PATHS.zh-CN.md) ·
  [ACP 编辑器接入](./docs/ACP.zh-CN.md)
- **功能与排障：** [子智能体 Profile](./docs/SUBAGENT_PROFILES.zh-CN.md) ·
  [Context Engine v2](./docs/SESSION_MEMORY_RETRIEVAL.zh-CN.md) ·
  [能力诊断](./docs/CAPABILITY_DIAGNOSTICS.zh-CN.md) ·
  [恢复与安全模式](./docs/RECOVERY.zh-CN.md) ·
  [机器人使用指南](./docs/BOT_GUIDE.zh-CN.md) ·
  [Checkpoints 与 rewind](./docs/CHECKPOINTS.zh-CN.md)
- **工程与迁移：** [规格](./docs/SPEC.zh-CN.md) ·
  [任务合约与暂停策略](./docs/TASK_CONTRACT.zh-CN.md) ·
  [工具合约](./docs/TOOL_CONTRACT.zh-CN.md) ·
  [从 0.x 迁移](./docs/_archive/MIGRATING.zh-CN.md)
- **扩展开发：** [扩展概览](./docs/EXTENSIONS.zh-CN.md) ·
  [插件包与 Manifest v1](./docs/PLUGIN_PACKAGES.zh-CN.md) ·
  [Extension Protocol](./docs/EXTENSION_PROTOCOL.zh-CN.md) ·
  [Go SDK 与 starter](./sdk/go/README.md)

## Star 趋势

以下为上游仓库的 Star 趋势数据。

<a href="https://www.star-history.com/?repos=esengine%2FDeepSeek-Reasonix&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/esengine/DeepSeek-Reasonix/star-history/assets/star-history/star-history-dark.svg" />
   <source media="(prefers-color-scheme: light)" srcset="https://raw.githubusercontent.com/esengine/DeepSeek-Reasonix/star-history/assets/star-history/star-history-light.svg" />
   <img alt="Star History Chart" src="https://raw.githubusercontent.com/esengine/DeepSeek-Reasonix/star-history/assets/star-history/star-history-light.svg" />
 </picture>
</a>

<br/>

## 致谢

下面这些朋友的工作塑造了 Reasonix 今天的样子 —— 当前按 commit 数统计的前 20 名贡献者。
完整贡献者列表在
[GitHub](https://github.com/esengine/DeepSeek-Reasonix/graphs/contributors?all=1)。

<!-- reasonix-top-contributors:start -->
| Contributor | Contributor | Contributor | Contributor |
| --- | --- | --- | --- |
| [**SivanCola**](https://github.com/SivanCola) | [**esengine**](https://github.com/esengine) | [**ttmouse**](https://github.com/ttmouse) | [**lifu963**](https://github.com/lifu963) |
| **reasonix** | [**HUQIANTAO**](https://github.com/HUQIANTAO) | [**GTC2080**](https://github.com/GTC2080) | [**light-front-theory**](https://github.com/light-front-theory) |
| **merge-order-check** | [**Li-Charles-One**](https://github.com/Li-Charles-One) | [**eghrhegpe**](https://github.com/eghrhegpe) | **wufengfan** |
| [**CVEngineer66**](https://github.com/CVEngineer66) | [**dependabot\[bot\]**](https://github.com/apps/dependabot) | [**lanshi17**](https://github.com/lanshi17) | [**SuMuxi66**](https://github.com/SuMuxi66) |
| [**CnsMaple**](https://github.com/CnsMaple) | [**cyq1017**](https://github.com/cyq1017) | [**JesonChou**](https://github.com/JesonChou) | [**XTLine**](https://github.com/XTLine) |
<!-- reasonix-top-contributors:end -->

特别感谢 [**Bernardxu123**](https://github.com/Bernardxu123) 设计的项目 logo和开场视频。

<p align="center">
  <a href="https://github.com/esengine/DeepSeek-Reasonix/graphs/contributors">
    <img src="https://contrib.rocks/image?repo=esengine/DeepSeek-Reasonix&max=100&columns=12" alt="esengine/DeepSeek-Reasonix 贡献者" width="860"/>
  </a>
</p>

<br/>

---

<p align="center">
  <sub>MIT —— 见 <a href="./LICENSE">LICENSE</a></sub>
  <br/>
  <sub>由 <a href="https://github.com/esengine/DeepSeek-Reasonix/graphs/contributors">esengine/DeepSeek-Reasonix</a> 社区共建</sub>
</p>

---

<p align="center"><sub><strong>支持本项目</strong></sub></p>

如果 Reasonix 帮你省了时间或 token，欢迎请杯咖啡。捐助不会换来 feature
优先级，也不会影响 issue 的处理顺序——就是「谢谢」。以下收款渠道为**上游作者**所有
（支持的是上游项目的维护，非本 fork）。

- **国内** — 微信支付（扫下方二维码）
- **海外** — PayPal: [paypal.me/yuhuahui](https://paypal.me/yuhuahui)

<p align="center">
  <img src=".github/sponsor/wechat-pay.jpg" alt="微信支付收款码" width="180"/>
</p>
