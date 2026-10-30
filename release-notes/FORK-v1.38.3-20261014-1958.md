# Reasonix Fork 桌面版 v1.38.3-20261014-1958 — Release Notes

> 基于官方 v1.38.3，包含上一个包 **desktop-v1.38.3-20261014-1900** 以来的全部更新（批 26 三件）。
> 数据/会话/记忆目录与官方版完全兼容，覆盖安装即可，无需迁移。

## 概览

本版是 **压缩可见性与用量面板修复批（三件）**：①压缩失败不再静默，三态显性化；②模型热切换时旧 provider 用量卡即时下线；③bundle 预算棘轮预抬。

## 🗜️ 压缩可见性

- **压缩失败不再静默**（tl867）：`CompactionDone` 补 outcome / attempt / reason 三字段，新增 failureStreak 计数与前端失败卡——压缩为什么失败、失败了几次，用户直接可见，不再「压了但没压动」无从判断。

## 📊 用量面板

- **模型热切换即时下线旧卡**（tl872）：切换模型瞬间 GLM Plan 用量卡与 OpenCode Go 卡并存闪烁的问题修复——新增 `activeModelChanged` 事件、双卡 epoch 校验与骨架屏过渡。

## 📥 获取

- 安装包：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（覆盖安装）
- 构成：批 26 三支，收口于 `1747f222d`
