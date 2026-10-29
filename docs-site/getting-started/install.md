<!--meta title=安装 desc=安装 Reasonix：CLI/TUI、桌面端、VS Code 扩展或从源码构建。 -->

# 安装

Reasonix 是开源（MIT）的单 Go 二进制编码 Agent，一套本地引擎提供四个入口：终端（CLI/TUI）、桌面端、浏览器（Web）与编辑器（ACP/VS Code）。所有入口共用同一套本地引擎与会话数据。

## 系统要求

- 操作系统：Windows 10+、macOS 12+、主流 Linux 发行版
- 网络：需访问所配置的模型 provider（如 DeepSeek API）
- CLI 构建需要 Go 1.25+；桌面端源码构建需要 Node 24+ 与 pnpm 10

## 路径 A：CLI / TUI

任意支持的平台都可以通过 npm 安装原生二进制；macOS 也可以使用 Homebrew：

```sh
npm i -g reasonix                  # 任意系统；自动拉取对应平台的原生二进制
brew install esengine/reasonix/reasonix   # macOS
```

预编译归档（`darwin|linux|windows × amd64|arm64`）和 `SHA256SUMS` 见每个 [GitHub release](https://github.com/Linearl/DeepSeek-Reasonix/releases)。

## 路径 B：桌面端

前往[官方下载页](https://reasonix.io/?download=desktop#start)获取最新桌面版本。

| 平台 | 安装包 | 架构 |
| --- | --- | --- |
| macOS | 通用 `.dmg` 或 `.zip` | Apple Silicon / Intel |
| Windows | 安装器 `.exe` 或便携 `.zip` | x64 / ARM64 |
| Linux | `.deb` 或 `.tar.gz` | x64 |

Windows 安装器通过 [SignPath.io](https://signpath.io/) 完成代码签名，证书由 [SignPath 基金会](https://signpath.org/) 免费提供。

> 本仓库（fork）只验证 Windows 桌面版（`win-amd64-desktop` + 经典布局）；其他平台与布局未经验证。稳定版本为 `1.31.4` – `1.34` 以及 `1.38.3`，`1.38.1` 已知不稳定请勿使用。

## 路径 C：VS Code 扩展

请先完成路径 A：扩展不内置 CLI，而是启动本机的 `reasonix acp` 后端，提供原生聊天、编辑器上下文、工具调用审批、模型选择和工作区会话。

- VS Code：[从 Visual Studio Marketplace 安装](https://marketplace.visualstudio.com/items?itemName=SivanLiu.reasonix-agent)
- VSCodium / Eclipse Theia：[从 Open VSX Registry 安装](https://open-vsx.org/extension/SivanLiu/reasonix-agent)
- 扩展 ID：`SivanLiu.reasonix-agent`

## 路径 D：从源码构建

```sh
git clone https://github.com/esengine/DeepSeek-Reasonix.git
cd DeepSeek-Reasonix
make build      # CLI -> bin/reasonix(.exe)
make cross      # 交叉编译 -> dist/（darwin|linux|windows × amd64|arm64）
```

桌面端构建额外需要 Wails CLI（与仓库 `.wails-version` 固定版本一致）：

```sh
make wails-install
cd desktop
wails build
```

平台相关的 WebView 依赖见仓库 `desktop/README.md`。

## 升级

```sh
reasonix upgrade            # 安装最新正式版
reasonix upgrade --check    # 只报告目标版本
reasonix upgrade --force    # 重新安装当前正式版
```

更新器只选择严格的 `vX.Y.Z` 非 prerelease GitHub Release。桌面端可在「设置 → 更新」中检查更新。

## 下一步

- [首次运行](first-run.html)
- [快捷键](shortcuts.html)
