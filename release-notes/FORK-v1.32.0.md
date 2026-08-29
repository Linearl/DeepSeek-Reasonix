# Reasonix Fork 桌面版 v1.32.0 — Release Notes

> 本版本基于官方 `v1.32.0`，完整吸收上游远程 SSH 工作区、持久有序回合、对话导航改进等新特性，同时保留全部 fork 增强。数据/会话/记忆目录与官方版完全兼容，**覆盖安装即可，无需迁移**。
> v1.31.4 的实时引导、子代理三档、写目录管理、项目分组、压缩刷新记忆、MiMo 推理档位等增强已包含在内（详见 [v1.31.4 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.4)）。

## 使用攻略

- 所有 fork 功能均可在 **设置 → 权限 / 通用** 或**会话输入框右上角**找到入口；CLI/TUI 用斜杠命令。
- 安装**覆盖安装即可**，无需卸载、无需迁移数据；已关闭自动更新，未来可随时切回官方版。

## 概览

**Reasonix Fork v1.32.0 — 吸收上游远程工作区 + 持久回合 + 对话导航改进**

本版本合并上游 v1.32.0 全部内容（远程 SSH 工作区、持久有序回合、对话导航改进、Windows/状态恢复可靠性增强），同时完整保留 fork 的全部魔改功能（详见 v1.31.4 Release Notes）。

上游 v1.32.0 已吸收我们的 #9158（压缩阈值）、#9374（会话归档）、#9376（记忆能力）、#9155（损坏 meta 重建）、#9032（分组标题字号）5 个已合并 PR。

发布日期：2026-08-29

---

## ✨ 上游 v1.32.0 新特性

### 远程 SSH 工作区

连接远程 SSH 工作区，精确恢复会话，繁忙回合在后台继续运行。支持远程会话管理、文件上传、跨网络协作。（#9369 #9317 #9319 #9368 #9367）

### 持久有序回合

回合生命周期管理改进，确保对话顺序一致性和持久化可靠性。（#9409）

### 对话导航改进

更流畅的对话滚动、选择和历史加载体验。

### Windows 与状态恢复可靠性增强

跨平台 Shell 支持改进（#9519）、状态恢复流程优化。

---

## 🔧 Fork 增强

所有 fork 增强功能（实时引导、子代理三档、写目录管理、项目分组、搜索面板、压缩刷新记忆、MiMo 推理档位、serve 端点扩展等）均已包含，详见 [v1.31.4 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.4)。

---

## 📝 合并说明

本次合并基于 upstream/main-v2 `bba8f8eb6`（2026-08-29），fork base `e182e9ff3`（v1.31.4）。

- **后端**：上游为基，逐处补回 fork 魔改（SubagentPolicy/MemorySystemReload/写目录方法等）。`go build ./...` 通过。
- **前端**：上游为基，补回 fork 独有功能（bridge mock 桩/compactionActive/setSubagentPolicyFromUi/hasLocalTranscriptForTab）。`tsc --noEmit` 通过。
- **本地安装包构建**：wails build win-amd64 + NSIS 安装器通过。

**已确认上游吸收的 fork PR**：#9158 compact_ratio、#9374 单会话归档、#9376 记忆能力、#9155 损坏 meta 重建、#9032 分组标题字号。

---

## 🐛 已知问题

- `kill_shell` 被误判为 workspace writer，后台写子代理持锁时无法用 `kill_shell` 终止（已提上游 issue [#9564](https://github.com/esengine/DeepSeek-Reasonix/issues/9564)）

---

## 安装

覆盖安装即可，无需卸载、无需迁移数据。已关闭自动更新，未来可随时切回官方版。
