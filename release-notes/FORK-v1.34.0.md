# Reasonix Fork 桌面版 v1.34.0 — Release Notes

> 本版本基于官方 `v1.34.0`，完整吸收上游安全加固、MCP 2026 interactions/Apps、缓存稳定能力控制、桌面稳定性修复，并保留全部 fork 增强。数据/会话/记忆目录与官方版完全兼容，**覆盖安装即可，无需迁移**。
> v1.31.4 的实时引导、子代理三档、写目录管理、项目分组、压缩刷新记忆、MiMo 推理档位等增强已包含在内（详见 [v1.31.4 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.4)）。

## 使用攻略

- 所有 fork 功能均可在 **设置 → 权限 / 通用** 或**会话输入框右上角**找到入口；CLI/TUI 用斜杠命令。
- 安装**覆盖安装即可**，无需卸载、无需迁移数据；已关闭自动更新，未来可随时切回官方版。

## 概览

**Reasonix Fork v1.34.0 — 吸收上游安全加固 + MCP 2026 Apps + 缓存稳定能力控制，四项 fork 修复**

本版本合并上游 `v1.33.0 → v1.34.0` 全部内容，同时带来四项 fork 修复：GLM 思考强度档暴露（#9642）、搜索历史提问面板样式恢复（#9218）、健康会话无法归档修复（#9617）、交付验收框 ctx 竞态修复（#9601）。

**版本对应说明**：本版 fork v1.34.0 对应上游 v1.34.0，版本号与内容精确对齐。

发布日期：2026-08-31

---

## ✨ 上游 v1.34.0 新特性

### Serve / Git 安全加固（六处安全边界）

封堵 serve DNS 重绑定（Host allowlist）、预览越界读（preview confinement）、git clean-filter 过滤器执行（gitcmd clean-filter neutralization）等六处安全边界缺口。（#9599）

### MCP 2026 interactions 与 Desktop Apps

新增 MCP 2026 多轮交互（elicitation form、interaction card）与 Desktop Apps（沙箱化渲染）支持，含扩展字段校验、富工具结果安全投影与协议版本兼容。（#9547/#9602）

### 缓存稳定的能力代理速度与质量治理

落地 use_capability 的速度与质量控制：并行调度、review 预算、指标统计，并保持缓存稳定（不破坏前缀缓存）。

### 桌面稳定性修复

- Transcript 阅读事务稳定化，消除高速推理时的反向跳动与空白帧（#9513 系列）
- 多轮思考时间线保真：同 turn 内多采样回合不再互相覆盖、工具时间线对齐（#9565）
- Composer 自动高度稳定化：改用离流 textarea 镜像，消除视口跳动（#9568）
- 模型发现与 chat 代理策略对齐，流失败可视化（#9563）
- 远端项目根不触碰本地 Topic 元数据（#9594）、远端项目组新会话行稳定显示（#9595）
- /resume 后写路径存活、/new 不再报写授权缺失（#9596）
- MCP Streamable HTTP 兼容性加固，固定 Go MCP SDK 版本（#9602）

---

## 🔧 Fork 增强

所有 fork 增强功能（实时引导、子代理三档、写目录管理、项目分组、搜索面板、压缩刷新记忆、MiMo 推理档位、serve 端点扩展等）均已包含，详见 [v1.31.4 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.4)。

---

## 🔧 Fork 修复

### GLM 思考强度档暴露（#9642）

GLM-5.2/5.3 系列其实支持 `reasoning_effort` 强度档（low/medium/high/max），但 Reasonix 的 `glmEffortCapability()` 只暴露开关（auto/enabled/disabled），且把 low/medium/high 全归一为 enabled、请求构造故意置空 `reasoning_effort`——UI 只能看到 auto。本版：① `glmEffortCapability()` 暴露 `auto/disabled/low/medium/high/max`；② `normalizeGLMEffort` 保留强度档不再归一；③ openai.go zhipu 分支按档位发送 `reasoning_effort`（low/medium/high/max → thinking.enabled + reasoning_effort=X；disabled → 仅 thinking.disabled）；④ 前端 supportedEfforts 同步更新；⑤ 修正过时注释（reasoning_effort 自 GLM-5.2 起支持）；⑥ **修正 openai.go zhipu 校验层**：早期只放行 `enabled/disabled` 二元、强度档在 boot-time 被拒（报「effort must be enabled or disabled」），现放行 `auto/disabled/low/medium/high/max`，与发送端双值对映射一致——每个档位映射为 `(thinking.type, reasoning_effort)` 一对值而非单值。用户实测修正后切换强度档成功。

### 搜索历史提问面板样式恢复（#9218）

上游合并时 auto-merge 静默丢弃了 `question-search` 全部 9 个 CSS 类（与 #9221/#9222 同类事故）——面板退化为裸 chips、`__results` 失去 `overflow-y:auto` 独立滚动区，向上滚加载更早"看似失效"。本版恢复全部样式（卡片布局、滚动容器、hover）并完成 token 迁移（`--fg-muted` → `--fg-faint`）。

### 健康会话无法关闭/归档修复（#9617）

磁盘完全健康的会话无法关闭/归档——force-archive（TrashTopicForce）被**本进程自持**的 `jsonl.lease.lock` 挡住（移除 guard 检测到本进程持有该锁）。本版新增 `reclaimTopicSelfLeasesLocked`：force 归档时释放"持有 tab 无真实活动"的自持 lease，让移除 guard 能获取；真正活跃的会话 lease 照旧保护，不会被误剥。

### 交付验收框间歇缺失修复（#9601）

交付回合结束 `turn_done` 在 `tabEventSink.ctx==nil` 时被静默丢弃，但同一次 Emit 仍写入 `awaiting_delivery` 徽章——造成"徽章挂了、验收框没出现"（分屏/切换/定时器竞态）。本版新增 `pendingRuntimeEvents` 缓冲：ctx 为 nil 时不再丢弃，而在 `setContext` 安装真实 wails ctx 后 flush，前端永不遗漏已记账的 turn_done。

### /compact 分块降级交互体验优化（#9082）

超长会话 `/compact` 走分块提取回归时（几分钟无反馈）：① 后端新增 `CompactionProgress` 事件，把 `chunkedFoldSummary` 每个 fragment 的 `done/total` 实时透传（原 `progress` 回调被传 `nil` 丢弃）；② 前端压缩卡片从静态"正在压缩…"改为**"分块压缩中 N/M"**并随进度递增；③ 过程折叠段含 compaction 事件时按 **pending 状态**显示——压缩中显示"分块压缩中 N/M"/"正在压缩对话…"，只有 `compaction_done` 后才显示"上下文已压缩"，避免把进行中的压缩误标为"已压缩"；④ 顶部 `compactionActive` 横幅保留。用户实测反馈"压缩中直接显示已压缩不合理"已修正。

### 搜索历史提问面板滚轮加载修复（#9218 补充）

搜索面板在顶部向上滚时无法加载更早提问：冒泡阶段 `onWheel` 收不到事件（被嵌套滚动处理器拦截）。本版改**捕获阶段 `onWheelCapture`**——`deltaY<0 && scrollTop<=8` 时调用 `onReachTop` 加载更早问题并 `preventDefault`/`stopPropagation`。用户实测确认正常。

### 心跳任务删除级联清理孤儿空壳会话（#9614）

心跳任务 `NewConversationEachRun` 每次创建新 topic，若该次会话在写入真实历史前失败（如首个消息 400），topic 残留为空壳 Global 会话（无主 `*.jsonl`，仅 meta/context/inbox，`created_at_ms=0`）。本版：删除/停用心跳任务时（`ReplaceTasks`/`ReplaceConfig`），级联归档该任务创建/运行过的空壳 topic——以 **`topic-state.CreatedAtMS==0`** 为可靠空壳信号（正常历史时间戳恒非零；会话索引看不到无 jsonl 的壳）。清理复用 `TrashTopicForce`（removal guards + 回收站，绝不硬删）；有真实历史的 topic 与被保留的任务绝不被触碰。

---

## 📝 合并说明

本次合并基于 upstream `v1.34.0`，fork base 为上一发布点 `desktop-v1.33.0+fork`。

- **后端**：`go build ./...` 通过。
- **前端**：`tsc --noEmit` 通过；bundle 预算跟随上游 ratchet（457.5 KiB gzip / 2459.0 KiB raw，fork 额外代码 +1 KiB），locale 软限机制保留。
- **本地安装包构建**：wails build win-amd64 + NSIS 通过。
- **fork 完整性核对**：`scripts/check-fork-integrity.mjs` 39/39 通过（wails 版本号已更新为 1.34.0）。

---

## 🐛 已知问题

- 上游 #9617/#9601 修复为 fork 率先落地，后续随上游跟进。
- GLM-5.3/5.3-flash 的 thinking 恒 enabled（不可关闭），本版按能力暴露 disabled 但实际请求发送时 5.3 会忽略禁用；若需严格区分请以模型文档为准。

---

## 安装

覆盖安装即可，无需卸载、无需迁移数据。已关闭自动更新，未来可随时切回官方版。
