<!--meta title=命令参考 desc=CLI 子命令、常用启动参数与会话内斜杠命令速查。 -->

# 命令参考

CLI 与桌面端共用一套本地引擎；桌面端无需命令行。本文是速查表，完整说明见仓库 [`docs/CLI.zh-CN.md`](https://github.com/Linearl/DeepSeek-Reasonix/blob/main-v2-stable/docs/CLI.zh-CN.md)。

## CLI 子命令

| 命令 | 作用 |
| --- | --- |
| `reasonix` | 启动交互式 TUI 会话 |
| `reasonix setup` | 配置 provider / 模型（`--local` 管理项目配置） |
| `reasonix run "任务"` | 一次性任务；`--auto` 全自动，管道输入也支持 |
| `reasonix -p "问题"` | 只输出最终结果（适合脚本），可加 `--output-format json` |
| `reasonix config currency` | 配置费用展示币种（auto / CNY / USD） |
| `reasonix config compact-ratio` | 配置自动压缩阈值（`--local` 写项目覆盖） |
| `reasonix upgrade` | 升级到最新正式版（`--check` 只查看） |
| `reasonix doctor` | 检查配置、桌面派生状态与常见安装问题 |
| `reasonix doctor repair` | 用户确认后做安全修复 |
| `reasonix acp` | 启动 ACP 后端（VS Code 扩展使用） |
| `reasonix serve` | 启动浏览器接入 |

## 常用启动参数

| 参数 | 用途 |
| --- | --- |
| `--model NAME` | 选择 provider 或 `provider/model` |
| `--effort LEVEL` | 覆盖推理强度 |
| `--max-steps N` | 工具调用轮数上限；`0` 为自动 |
| `--dir PATH` | 切换 workspace 根目录 |
| `--add-dir PATH` | 增加允许写入的目录（可重复） |
| `-c` / `--continue` | 恢复最近一次会话 |
| `-r` / `--resume [QUERY]` | 会话选择器 / 恢复匹配会话 |
| `--copy` | 复制会话到可写副本继续 |
| `--allowed-tools RULES` | 当前会话额外的权限 allow 规则 |
| `--permission-mode MODE` | 指定权限姿态启动 |
| `--yolo` | YOLO 模式启动 |
| `--ablate SUBSYSTEMS` | 基准对照：关闭某子系统测成功率归因 |
| `--trajectory PATH` | 记录完整事件流 JSONL（离线回放归因） |

## 会话内斜杠命令

输入框中输入 `/` 唤起命令列表。内置高频命令（权威清单见 `internal/cli/slash_registry.go`）：

| 命令 | 作用 |
| --- | --- |
| `/new` / `/clear` / `/cls` | 新会话 / 丢弃上下文 / 只清屏 |
| `/compact` | 立即压缩上下文 |
| `/context` | 查看上下文占用 |
| `/rewind` | 回退到之前的 checkpoint |
| `/resume` | 恢复历史会话 |
| `/branch` / `/switch` | 会话分支 / 切换分支 |
| `/model` / `/provider` | 切换模型 / provider |
| `/effort` | 调整推理强度 |
| `/preset` | 切换工作模式（别名 `/work-mode`、`/profile`） |
| `/todo` / `/goal` | 待办面板 / 目标持续执行 |
| `/mcp` / `/plugins` / `/skills` / `/hooks` | MCP / 插件 / 技能 / 钩子管理 |
| `/sandbox` | 查看与调整沙箱状态 |
| `/memory` / `/remember` / `/forget` | 记忆查看与写入 |
| `/migrate` | 旧数据迁移（`--from` 指定旧目录） |
| `/export` | 导出会话（Markdown / JSON / PDF / 图片） |
| `/status` | 当前状态总览 |
| `/theme` / `/language` / `/currency` | 外观与本地化 |
| `/verbose` | 详细 reasoning 显示 |
| `/help` | 全部命令帮助 |

除内置命令外，`<Reasonix home>/commands/` 与项目 `.reasonix/commands/` 下的 Markdown 文件也是自定义斜杠命令（如 README 提到的 `/init`）。

## 下一步

- [配置项参考](config.html)
- [快捷键](../getting-started/shortcuts.html)
