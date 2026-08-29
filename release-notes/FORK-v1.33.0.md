# Reasonix Fork 桌面版 v1.33.0 — Release Notes

> 本版本基于官方 `v1.33.0`（含其后的 main-v2 提交，截至 `bba8f8eb6`），完整吸收上游 Shell 配置、远程凭据安全、界面稳定性、通知音量控制等改进，并保留全部 fork 增强。数据/会话/记忆目录与官方版完全兼容，**覆盖安装即可，无需迁移**。
> v1.31.4 的实时引导、子代理三档、写目录管理、项目分组、压缩刷新记忆、MiMo 推理档位等增强已包含在内（详见 [v1.31.4 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.4)）。

## 使用攻略

- 所有 fork 功能均可在 **设置 → 权限 / 通用** 或**会话输入框右上角**找到入口；CLI/TUI 用斜杠命令。
- 安装**覆盖安装即可**，无需卸载、无需迁移数据；已关闭自动更新，未来可随时切回官方版。

## 概览

**Reasonix Fork v1.33.0 — 吸收上游 Shell 支持 + 通知音量 + 对话渲染事务化，修复历史加载回归**

本版本合并上游 `v1.32.1 → v1.33.0 → main-v2 (bba8f8eb6)` 全部内容，同时修复了一处合并引入的历史加载回归（见下文 #9468）。

**版本对应说明**：上一版 fork v1.32.0 实际包含的上游内容为 v1.32.1；本版 fork v1.33.0 对应上游 v1.33.0 及其后的 main-v2 提交（#9513 系列），版本号与内容精确对齐。

发布日期：2026-08-29

---

## ✨ 上游 v1.33.0 新特性

### 安全的跨平台 Shell 支持

进程级缓存的 Shell 清单与确定性的 Git for Windows 发现；macOS 支持 Bash / 原生 zsh / POSIX sh。设置面板新增 Shell 能力展示、手动修复指引与安全下载主机白名单。（#9519）

### 稳定的运行回合切换

任务执行期间保持推理与过程披露展开，配合单调高度下限稳定 live footer，消除界面抖动。

### 安全的远程凭据代理路由

远程本地代理路由保持稳定的 scoped 虚拟 token，桌面端持有真实凭据；保存/清除/删除凭据时同步重建或吊销。（安全加固）

### 可调通知音量

声音设置新增 0–100% 通知音量滑块（默认 70%），所有通知音效经统一 master gain 输出，内置 WAV 响度归一化。

### 对话渲染事务化（#9513 系列）

几何 revision、generation-fenced 写请求、手势行程证明、稳定收缩高度接受、绘制前空白回弹修正——根治对话滚动中的空白帧与闪烁。

### 其他上游改进

- 会话索引重建改为有界可等待事务，侧栏不再卡"Organizing history"（#9528）
- 完成的 Todo 面板自动淡出收起（#9529）
- 内存历史索引隔离化，flush 完成所有注册 root（#9557）
- `/status` 过时快照不再崩溃（#9541）
- Topic 元数据迁移 SQLite（#9466）、回合持久有序化（#9409）、远程会话恢复完结（#9319）、MCP capability 精简化（#9499）

---

## 🔧 Fork 增强

所有 fork 增强功能（实时引导、子代理三档、写目录管理、项目分组、搜索面板、压缩刷新记忆、MiMo 推理档位、serve 端点扩展等）均已包含，详见 [v1.31.4 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.4)。

---

## 🐛 Fork 修复

### 历史加载 reload fallback（#9468，本版重新应用）

上滑加载更早历史时，若后端已重写会话（快照恢复路径），上游的身份检查会误拒 reload 结果，导致"earlier conversation could not be loaded"且重试无效。本版恢复 v1.31.4 的修复顺序：reload 结果优先于 fingerprint 拒绝执行。该修复曾在 v1.32 合并时被上游同名区域改动覆盖。

---

## 📝 合并说明

本次合并基于 upstream/main-v2 `bba8f8eb6`（= v1.33.0 `ba86d2f8d` + 20 个后续提交），fork base 为上一发布点。

- **后端**：`go build ./...` 通过。上游将 SandboxView 重构至 `shell_support.go` 并封装 `sandboxViewFor`；fork 的 `OptimisticWrite` 字段（#9213）已补回新结构。
- **前端**：`tsc --noEmit` 通过。bundle 预算跟随上游 ratchet（453.8 KiB gzip / 2438.0 KiB raw），locale 软限机制（59.0 warn / 70.0 硬上限）保留。
- **本地安装包构建**：wails build win-amd64 + NSIS 通过。

**已确认上游吸收的 fork PR**：#9158 compact_ratio、#9374 单会话归档、#9376 记忆能力、#9155 损坏 meta 重建、#9032 分组标题字号。

---

## 🐛 已知问题

- `kill_shell` 被误判为 workspace writer，后台写子代理持锁时无法用 `kill_shell` 终止（已提上游 issue [#9564](https://github.com/esengine/DeepSeek-Reasonix/issues/9564)）

---

## 安装

覆盖安装即可，无需卸载、无需迁移数据。已关闭自动更新，未来可随时切回官方版。
