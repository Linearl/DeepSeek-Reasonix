# Fork 实验特性推荐配置表

> **用途**：随 Release Notes 一起发布，供用户按需开启实验特性。
> **三档定义**：**推荐** = 核心特性，不开启会有体验缺口；**可选** = 按需开启的增强；**未稳定** = 尝鲜，可能有副作用。
> **「我们的值」**= fork 开发机实际运行配置（2026-10-07），供参考。
> 开启位置：**设置 → 实验室**（对应 config.toml 的 `experimental_*` 键）。

## 一、推荐（建议开启）

| 特性 | config 键 | 我们 | 说明 |
|---|---|---|---|
| 自动驾驶（autopilot） | `experimental_autopilot_ask_timeout` 等 | ✅ | 无人值守运行：超时高风险审批自动拒答并继续 |
| 会话协作 | `experimental_session_collab` | ✅ | 跨会话消息/收件箱/派单——多会话协作的基础 |
| 乐观写并行 | `optimistic_write`（agent 段） | ✅ | 子代理并行写入，多线效率关键（配 bash 守卫使用） |
| Bash 重命令守护 | `experimental_bash_heavy_guard`（agent 段） | ✅ | 多会话并行时重型命令排队互斥，保护共享目录（与乐观写配套） |
| 预算控制 / 压缩优化 / 消息合并 | `budget/compress/message-merge` 族 | ✅ | 长会话稳定性三件套 |
| 快捷指令 | `experimental_quick_commands` | ✅ | 输入框 + 菜单快速插入常用文本片段 |
| 标签自动压缩 | `experimental_tab_compress` | ✅ | >8 个标签自动分级收窄，不再挤爆标签栏 |
| 待办侧栏 | `experimental_todo_sidebar` | ✅ | 待办列表右侧边栏化 |
| 提示历史选择器 | `experimental_prompt_history_picker` | ✅ | composer 时钟图标翻历史输入 |
| 会话图墙 | `experimental_session_wall` | ✅ | 一屏总览所有会话，点开即跳转 |
| 会话监视板 | `experimental_session_monitor` | ✅ | 左栏会话监视看板 |
| 分栏视图 | `experimental_split_view` | ✅ | 标签栏分栏（40/50/60 三档） |
| 反馈通道 | `experimental_feedback` | ✅ | 意见箱提交工具+面板 |
| 本地服务 | `experimental_local_server` | ✅ | 设置 → Local server（手机端/GC 远程连接需要） |
| 重启并更新 | `experimental_restart_update` | ▲ | 显示重启更新入口——⚠️ 需本地有 staging 包，**一般用户推荐手动下载安装包覆盖安装** |

## 二、可选（按需开启）

| 特性 | config 键 | 我们 | 说明 |
|---|---|---|---|
| 子代理面板 | `experimental_subagent_panel` | ✅ | 右侧栏子代理 tab，已结束卡片默认折叠 |
| 子代理详情视图 | `experimental_subagent_detail` | ✅ | 点行开只读详情 |
| 子代理 TPS 读数 | `experimental_subagent_tps` | ✅ | 子代理卡片显示 tok/s |
| 完成摘要通知 | `experimental_completion_summary` | ✅ | 每轮结果通知卡 |
| 提问搜索 | `experimental_question_search` | ✅ | 标题栏搜索我的提问 |
| 回答风格 UI | `experimental_output_style_ui` | ✅ | 实验室「回答风格」设置节 |
| 高速模型档 | （agent 段族） | ✅ | 模型高速档 ⚡ 徽标与总闸 |
| 压缩并行 / 事件等待复查 / trace 状态化 | 族 | ✅ | 引擎侧增强 |
| dream / 空闲自终止 / 循环注记 | 族 | ✅/❌ | 按需 |
| CDP 调试端口 | `experimental_cdp_debug_port` | ❌ | 仅调试 WebView2 时开 |

## 三、未稳定（尝鲜慎开）

| 特性 | config 键 | 我们 | 说明 |
|---|---|---|---|
| 子代理策略面板 | `experimental_subagent_policy` | ❌ | 子代理策略管理 UI，仍在打磨 |
| 标签模式着色 | `experimental_tab_mode_tint` | ❌ | 低透明度模式底色代替徽章（观感实验） |
| 反馈主动邀请 | `experimental_feedback_nudge` | ❌ | 完成后邀请反馈（易打扰） |
| zcode 任务总线内置化 | `experimental_zcode_task_bus` | ❌ | 嵌入式总线 MCP（多机协同实验） |
| 路径规则引擎 | `experimental_path_rules` | ❌ | 结构化路径Scope 评估（实验） |
| base_process 底座 | `experimental_base_process` | ❌ | 常驻子进程底座（S1 实验线） |
| 生命周期噪音门 | `experimental_lifecycle_noise_gate` | ❌ | 干净关闭的崩溃报告降噪 |

---

**注意**：开启/关闭实验特性后，部分项需重启应用生效（键注释标 boot snapshot 的）；`settings save re-applies` 的项保存即生效。具体以 设置 → 实验室 内说明与三档徽章为准。
