# Reasonix Fork 桌面版 v1.38.3-20261029-1054 — Release Notes

> 基于官方 v1.38.3，包含上一个包 **desktop-v1.38.3-20261029-0803** 以来的全部更新（3 项滚合：tl1232 + tl1193 + tl1241）。
> 数据/会话/记忆目录与官方版完全兼容，覆盖安装即可，无需迁移。

## 概览

本版是 **用户报障清障批**：①autopilot 切换失效根治；②pinned 记忆职责迁移（启动提速）；③聊天贴底随机失效修复。三项均用户实锤报障后定位根治。

## 🔄 autopilot 切换失效根治（tl1232）

- **切不回/切换无效修复**：真凶=465/595 乐观更新残留标签+等值守卫吞点击（1211 嫌疑排除），App.tsx 单点 handler 重写（+16/-3）——autopilot 开关切换即时生效。

## 🧠 pinned 记忆职责迁移（tl1193，实验开关默认关）

- **索引行常驻+body 按需召回**：pinned 记忆从「全文常驻注入」改为「索引行常驻+正文按需召回」，实测 **MemoryLoad -45.7% / FirstRecall -44.1%**——直接治理启动 boot memory_load 859ms 最大嫌疑段。
- **实验开关**（`experimental_pinned_index`）默认关：存量零迁移、行为零变化，开启才生效。
- 宿主休眠殉职前工作已全量 commit，主会话代验收 11/11 绿。

## 📜 聊天贴底随机失效修复（tl1241）

- **restoreLanding 最小修**：贴底漏判 A 形态实测归 tail——聊天窗口在部分操作后不再随机脱离底部；诊断打点扩 ScrollDiagnosticPanel（配合 tl1243 宽度补偿进一步根治）。

## 📥 获取

- 安装包：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（覆盖安装）
- 构成 commits：c6128fc53（1232）/ 1e2b56a91（1193）/ 266184dc8（1241）
