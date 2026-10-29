<!--meta title=配置项参考 desc=Reasonix 配置文件位置、优先级、常用分区与凭据存放规则。 -->

# 配置项参考

Reasonix 使用一个用户可见的全局目录（Reasonix home）存放配置和用户状态，CLI 与桌面端共用。本文是速查表；完整权威源见仓库 [`docs/CONFIG_PATHS.zh-CN.md`](https://github.com/Linearl/DeepSeek-Reasonix/blob/main-v2-stable/docs/CONFIG_PATHS.zh-CN.md)。

## 目录位置

| 平台 | Reasonix home |
| --- | --- |
| Windows | `%APPDATA%\reasonix` |
| macOS | `~/.reasonix` |
| Linux | `~/.reasonix` |

可设 `REASONIX_HOME` 覆盖（自包含模式，主要用于测试 / CI / 便携安装）；`REASONIX_STATE_HOME` 只移动会话、归档、记忆等运行状态；缓存默认在系统缓存目录（Windows 为 `%LOCALAPPDATA%\reasonix\cache`），可用 `REASONIX_CACHE_HOME` 覆盖。

## 目录内容速查

| 数据 | 路径 |
| --- | --- |
| 全局配置 | `<Reasonix home>/config.toml` |
| 全局 provider 凭据 | `<Reasonix home>/.env` |
| 全局斜杠命令 | `<Reasonix home>/commands/` |
| 全局 skills | `<Reasonix home>/skills/` |
| 全局 hooks | `<Reasonix home>/settings.json` |
| 会话 | `<state root>/sessions/` |
| 归档 | `<state root>/archive/` |
| 记忆 | `<state root>/memory/` 与 `<state root>/projects/` |

## 配置优先级

```text
命令行参数
> 项目 ./reasonix.toml
> 全局 <Reasonix home>/config.toml
> 兼容读取的旧全局配置
> 内置默认值
```

## 全局 `config.toml` 常用内容

```toml
config_version = 1
default_model = "deepseek/deepseek-v4-flash"
language = "zh"

[ui]
theme = "auto"
show_turn_usage = true        # TUI 每轮 token/费用回执

[desktop]
provider_access = ["deepseek"]

[[providers]]
name        = "deepseek"
kind        = "anthropic"
base_url    = "https://api.deepseek.com/anthropic"
models      = ["deepseek-v4-flash", "deepseek-v4-pro"]
default     = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
web_search  = true

[[plugins]]
name    = "example"
command = "example-mcp-server"
```

要点：

- **API Key 真实值不进 `config.toml`**。provider 条目只有 `api_key_env` 变量名，密钥值统一存全局 `.env`；文件读取工具与受沙箱保护的 shell 均读不到该文件。
- 自定义 provider 的 `api_key_env` 由名称稳定生成（如 `LOCAL_GATEWAY_API_KEY`），同名 provider 重启后仍读同一凭据槽位。
- 桌面端自定义 provider 表单里的「API 地址」按完整请求地址写入 `request_url`，不会被追加路径。

## 常用配置命令

```sh
reasonix config currency           # 查看/设置费用展示币种（auto|CNY|USD）
reasonix config compact-ratio      # 查看/设置自动压缩阈值（30–85%，默认 80%）
reasonix config compact-ratio --local 75   # 写入项目级覆盖
```

## 旧版本迁移

v1.8.1 起启动时自动做一次性非破坏性迁移（旧文件保留）。补救场景在任意前端运行 `/migrate`；旧 v0.x 自定义数据目录用 `/migrate --from "D:\OldReasonix"`（只导入 sessions）。

## 下一步

- [命令参考](commands.html)
- [日志与诊断](../troubleshooting/logs.html)
