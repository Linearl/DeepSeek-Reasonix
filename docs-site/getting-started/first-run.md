<!--meta title=首次运行 desc=配置 provider 与模型、发起第一段对话；CLI 与桌面端两条路径。 -->

# 首次运行

安装完成后，按你使用的入口选择路径。CLI 与桌面端共用同一套配置和会话数据，一端配置、另一端可直接使用。

## CLI / TUI：三步开始

```sh
reasonix setup                      # 配置 provider 和模型
reasonix                            # 启动交互式会话
reasonix run "把 main.go 里的 TODO 实现掉"
```

1. **`reasonix setup`**：交互式向导，添加 provider（内置 DeepSeek 预设，任何 OpenAI/Anthropic 兼容端点都只是一条配置）、填 API Key、设置默认模型。API Key 真实值保存在全局凭据文件中，配置文件里只有变量名。
2. **`reasonix`**：进入交互式终端界面。常用启动参数：

| 参数 | 用途 |
| --- | --- |
| `--model NAME` | 选择已配置的 provider 或 `provider/model` |
| `--dir PATH` | 切换 workspace 根目录 |
| `-c` / `--continue` | 恢复最近一次会话 |
| `-r` / `--resume [QUERY]` | 打开会话选择器或恢复匹配会话 |

3. **`reasonix run "..."`**：一次性任务；加 `--auto` 跳过审批全自动执行，加 `-p` 只输出最终结果（适合脚本）。

进入交互会话后，在项目根目录运行 `/init` 可生成项目指令文件。

## 桌面端：无需命令行

1. 安装并启动 Reasonix 桌面端。
2. 在应用内「设置 → 模型」中配置 provider 与 API Key（与 CLI 共用同一份全局配置）。
3. 新建会话，选择工作目录，直接用自然语言下达任务。

桌面端提供完整 GUI：项目树、多标签会话、改动面板、命令面板（`Ctrl+K`）、设置页。见[快捷键](shortcuts.html)。

## 权限与审批

Reasonix 默认对工具调用分级审批：

- **Ask**：每个写入类操作都需要确认；
- **Auto**：自动执行只读与安全操作，写入仍审批；
- **Plan**：先规划后执行（`Shift+Tab` 切换）；
- **YOLO**：全部自动（`Ctrl+Y` 切换），只建议在可信任务与受控目录使用。

工作区沙箱会限制文件写入范围；配合逐轮 checkpoint，任何改动都可以回滚。

## 常用会话内命令

在输入框中输入 `/` 可唤起命令列表。高频命令：

| 命令 | 作用 |
| --- | --- |
| `/init` | 为当前项目生成指令文件 |
| `/clear` | 丢弃当前对话上下文 |
| `/rewind` | 回退到之前的 checkpoint |
| `/compact` | 立即压缩上下文 |
| `/model` | 切换模型 |
| `/migrate` | 从旧版本路径迁移数据 |

## 下一步

- [快捷键](shortcuts.html)
- [配置项参考](../reference/config.html)
