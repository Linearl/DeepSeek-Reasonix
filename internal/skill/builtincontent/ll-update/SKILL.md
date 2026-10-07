---
name: ll-update
description: 把 Reasonix 桌面版安装包（NSIS installer）拆成版本目录三件套，再用 restart_update 工具做快速切换版本（升级/回滚）。当用户说「新版本安装包我放到 versions/xxx 了，帮我走快速切换版本来更新」「切回旧版本」「更新 Reasonix」，或要把桌面版换到某个已安装版本时使用。
metadata:
  applies-to: Windows 桌面版 Reasonix（版本目录布局：安装根目录/versions/<版本>/）
---

# Reasonix 版本切换（安装包 → 快速切换）

安装包**不能**直接丢进 `versions/` 目录生效。正确做法分两段：先用脚本把安装包拆成"版本目录三件套"，再在会话里调 `restart_update` 工具切换。

## 机制（为什么是这样）

- **安装根目录**：含 `current.json` 与 `versions/` 的那一层（默认从正在运行的 reasonix-desktop 进程路径推断；本机为 `C:/Users/yinji/AppData/Local/Programs/Reasonix`）。注意：目录名若含特殊字符（如中文顿号）是合法的，写命令时引号包裹即可。
- **版本目录**：`<根目录>/versions/<版本名>/`，必需成员恰好三个 —— `reasonix-desktop.exe`、`reasonix-cli.exe`、`reasonix-update-helper.exe`。缺 `reasonix-cli.exe` 会被判 `UNHEALTHY` 并拒绝切换（切过去会让 servepool 返回 503）。
- **切换 = 只改指针**：当前版本记录在 `<根目录>/current.json` 的 `activeVersion` / `activeDir`。切换只改这个文件并重启应用，不复制二进制、不删旧版本。
- **前提开关**：`%APPDATA%\reasonix\config.toml` 里 `experimental_restart_update = true` 且 `experimental_autonomous_update = true`（两者都是"仅写配置"类开关，可随时改）。关着的话 `set_target` / `execute` 会被拒。`restart`（纯重启，见下节）不需要 `experimental_restart_update`——它不触碰安装，只要 `experimental_autonomous_update`（工具注册开关）开着即可。
- 版本目录名规则：`^v[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.-]+)?$`，所以 `v1.38.3`、`v1.38.3-20260930-1520`、`v1.38.3-new` 都合法。

## 流程

### 第 1 段：拆包铺目录（可脚本化）

```bash
bash scripts/switch-version.sh --installer "<安装包.exe>" [--version v1.38.3-<日期>-<时间>]
```

脚本会：解包 → 校验三件套齐全并读出版本串 → 拷到 `versions/<版本>/` → md5 比对。

- 省略 `--installer`：自动在 `versions/*/` 里找最新的 `*installer*.exe`。
- 省略 `--version`：用安装包内 CLI 的版本串（`reasonix 1.38.3-20260930-1520` → `v1.38.3-20260930-1520`）。
- 省略 `--root`：从正在运行的 `reasonix-desktop` 进程路径推断安装根目录。
- 目标目录已存在且非空时**默认拒绝**（防止覆盖正在运行的版本），确认可覆盖再加 `--force`。

### 第 2 段：切换（只能在 Reasonix 会话里做）

依次调 `restart_update` 工具，三步不可跳：

1. `list_versions` —— 确认目标版本是 `healthy`（不是 `UNHEALTHY`）。
2. `set_target("<版本名>")` —— 暂存目标；指向当前 active 版本会被拒（无事可做）。
3. `execute` —— 提交切换。**返回即成功，绝不能重试**；随后应用重启到新版本。

> `execute` 会结束当前进程，会话按 `autonomous_update_resume` 配置自动恢复（本机 = `all`），所以本会话的工作会在新版本里继续。

## 纯重启（不切版本）

改完「仅启动时读取」的配置（boot 期注册的工具、`config.toml` 里启动时才生效的开关等）后，调 `restart_update` 工具的 restart action 让改动立即生效：

```text
restart_update {"action": "restart"}
```

- 只重启当前版本：不改 `current.json`、不发布版本树，与「切换版本」正交；无需 target。
- 返回即成功，**绝不能重试**（重试 = 再重启一次）；随后应用重启，版本不变。
- 「重启并继续」：重启走计划内路径（写更新重启标记 + 登记自动恢复名册），本会话按 `autonomous_update_resume` 配置在新进程里自动续跑；不会被续跑时（attended 会话）工具结果会显形「未入册」，此时不要假装它会自动继续。
- 与设置页「立即重启」的差异：设置页重启不写标记，重启后**不会**自动恢复任何会话；工具面 `restart` 写标记、可自动续跑。agent 一律走工具。

## 验收标准

- `versions/<版本>/` 三件套齐全，且 md5 与解包源一致（脚本已自动校验）。
- `restart_update list_versions`：目标版本显示 `healthy`，切换后带 `[active]`。
- `<根目录>/current.json`：`activeVersion` 与 `activeDir` 都指向新版本。
- 运行进程：`Get-Process reasonix-desktop | Select Path` 指向 `versions\<版本>\reasonix-desktop.exe`。
- 新版 CLI 自报版本：`versions/<版本>/reasonix-cli.exe --version`（fork 构建形如 `reasonix 1.38.3-20260930-1520`；上游构建没有日期戳后缀 —— 用它判断拿到的是不是 fork 包）。

## 坑

- 安装包直接丢进 `versions/vX-new/` → `list_versions` 报 `UNHEALTHY (missing desktop/CLI binary)`，`set_target` 拒绝。必须先拆包。
- 想「重启让配置生效」时**禁止 taskkill + 手动 start launcher**（2026-10-06 事故：app 被关掉后没有自动拉起，用户得手动开，被打断的 turn 还留下「结果未确认」卡片）。用「纯重启」节的 `restart_update {"action": "restart"}`——计划内重启，会话不丢。
- MSYS 下解包用 `7z x`（保留路径），**不要用 `7z e`**：安装包里 `$R9\` 与根层存在同名文件，扁平解包会互相覆盖。
- 切换前确认**没有其它标签页在跑**（有活跃工作会被 busy 守卫拒绝）；本会话自己的回合被豁免。
- `RetainPreviousVersions` 只在安装器/自动更新路径调用，所以手工切换**不会**清掉旧版本，回滚随时可用。
- 装完可以在版本目录里留一份安装包原件（无害），但它是下载产物，想干净可自行删掉。

## 回滚

```text
restart_update set_target("<旧版本名>")  →  restart_update execute
```

旧版本目录仍在，无需重新拆包。本机历史上可回滚的版本见 `versions/` 目录。
