# Reasonix Fork 桌面版 v1.38.3-20261015-1528 — Release Notes

> 基于官方 v1.38.3，包含上一个包 **desktop-v1.38.3-20261015-0641** 以来的全部更新（批 30+31 四件）。
> 数据/会话/记忆目录与官方版完全兼容，覆盖安装即可，无需迁移。⚠️ 本版修了一个 data-loss 级回归，详见下文。

## 概览

本版是 **data-loss 返修 + todo 持久化 + 两项基建落地**：①项目分组归属被静默清空的回归根治（P0）；②会话 todo 跨回合不再被宿主清空；③EBDD 评测驱动研发框架四阶段与 dynamic-workflow 流水线编排 S0-S3a 落地。

## 🔴 data-loss 返修

- **项目分组归属不再被静默清空**（tl908）：`catalog pull` 返回空 roster 时 `pruneProjectGroupAssign` 无守卫，导致全部项目掉出分组（用户现场：工作项目组 0 项、8 项目全掉）。这是 892B 引入的回归——三态测试钉 62/62，空 roster 时不再清理归属。

## ✅ todo 持久化

- **todo 跨回合保留**（tl893 一期 A+C）：投影层 todo 跨回合保留、全完成态不再自动退场（负面清单 `goal_run_boundary` 未动）——任务面板不再「回合一结束就空」。

## 🏗️ 基建

- **EBDD 评测驱动研发框架**（tl414，阶段 1~4）：bench 引擎 E 柱、WARN 巡检、周趋势与心跳周任务四阶段落地。
- **dynamic-workflow 流水线编排**（tl419，S0-S3a）：单会话多 agent 流水线——SQLite 四表 + fleet 写路径 + 读桥（S3b 前端分期）。

## 📥 获取

- 安装包：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（覆盖安装）
- 构成：四支合并（908 / 893 / 414 / 419），收口于 `6280772ea`
