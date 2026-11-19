---
description: "本目录存放 `merge`（合并清理）路由使用的模板文件。"
update_time: 2026-07-01
---

# merge 路由资源目录

本目录存放 `merge`（合并清理）路由使用的模板文件。

## 用途

`merge` 路由负责任务分支合并回主线，包含：
- 自动定位分支（pool slot / ephemeral）
- `--no-ff` 合并 + 链式验证（tsc → lint → unit → int）
- 双场景清理（pool slot 重置 / ephemeral 删除）

## 说明

当前 `merge` 路由的操作为纯 Git 命令驱动，不依赖模板文件。如需添加：
- 合并报告模板
- 验证失败分析模板
- 清理确认清单

可将模板文件放入本目录，并在 `config/resource-bundles.yaml` 中注册 `route.merge.assets` 的 includes 路径。
