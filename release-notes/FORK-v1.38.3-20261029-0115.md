# Reasonix Fork 桌面版 v1.38.3-20261029-0115 — Release Notes

> 基于官方 v1.38.3，包含上一个包 **desktop-v1.38.3-20261028-1557**（包42）以来的全部更新（5 项滚合：1218 三波 + 1224a）。
> 数据/会话/记忆目录与官方版完全兼容，覆盖安装即可，无需迁移。
> **Fork 与上游差异全览**：见 [FORK-vs-upstream.md](./FORK-vs-upstream.md)。

## 概览

本版是 **multi_edit 增强与能力可见性批**：①multi_edit 三条增强全部实验开关化（默认关、行为零变化）；②能力目录预算扩容临时缓解——修复「多数能力对模型不可见」的截断缺陷。

## 🔧 multi_edit 三条增强（实验开关，默认关）

- **错误详情增强**（`experimental_multi_edit_error_detail`）：证据拦截信息附带缺失/失配行的实际内容（≤5 行/行≤200 字符/总量≤2KB），失败时能直接看到盘上是什么，不用再手动打开文件核对。
- **证据门收窄**（`experimental_multi_edit_evidence_narrow`）：write 证据拦截从「全工作区写操作连坐」收窄为「只拦涉事文件」，其他文件的编辑不再被无关文件的过期证据挡住。
- **单条失败跳过**（`experimental_multi_edit_skip_failed`）：批量编辑中单条失败可选择跳过继续+逐条报告（关=原子语义逐字不变，默认保守）。

## 👁️ 能力可见性修复（tl1224 阶段一）

- **能力目录预算 20KB→64KB**：此前 20KB 截断导致 98/236 条能力（技能/MCP 工具等）对模型不可见（rows=168/dropped=233），本次扩容+撞顶预警，模型能看到的能力列表大幅补全。

## 📥 获取

- 安装包：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（覆盖安装）
- 构成 commits：ffe7a78e3（1218 三开关+tier 面并集解）/ dcdbe246d（1224a 预算扩容）+ 前三波（10-28 晚批）
