---
description: "本目录存放 `branch`（并行开发）路由使用的模板文件。"
update_time: 2026-07-01
---

# branch 路由资源目录

本目录存放 `branch`（并行开发）路由使用的模板文件。

## 用途

`branch` 路由利用 Git worktree 持久化池（`worktree/dev1`~`dev3`），实现：
- 按需懒创建 worktree slot
- 3 slot 上限 + fallback ephemeral
- 零复制 node_modules（pool slot 内永久驻留）

## 说明

当前 `branch` 路由的操作为纯 Git 命令驱动（`git worktree add`、`git checkout -b` 等），
不依赖模板文件。如需添加：
- 分支命名规范参考
- 合并前检查清单
- worktree pool 可视化报告模板

可将模板文件放入本目录，并在 `config/resource-bundles.yaml` 中注册 `route.branch.assets` 的 includes 路径。
