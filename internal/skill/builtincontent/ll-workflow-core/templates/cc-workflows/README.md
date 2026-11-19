---
description: CC Workflow 脚本副本目录（ll-workflow-core 内部副本，用于 release 与跨项目分发）
update_time: 2026-07-01
---
# CC Workflow 脚本副本

本目录存放 CC Workflow 脚本的**副本**，与 `.claude/workflows/` 下原始脚本保持同步。

## 副本脚本

| 文件 | 来源 | 用途 |
|------|------|------|
| [audit-parallel-v2.js](audit-parallel-v2.js) | `.claude/workflows/audit-parallel-v2.js` | aud 路由 CC Workflow 版本 |
| [test-generation-parallel.js](test-generation-parallel.js) | `.claude/workflows/test-generation-parallel.js` | gt 路由 CC Workflow 版本 |

## 为什么需要副本？

1. **Release 打包**：ll-workflow-core skill 发布时，连同 CC Workflow 脚本一起打包
2. **跨项目分发**：其他项目引入 ll-workflow-core 时，可直接使用副本，无需单独复制
3. **版本对齐**：避免项目级 `.claude/workflows/` 与 skill 内部脚本版本漂移

## 同步策略

**单向同步**：项目级 `.claude/workflows/` → skill 副本

### 同步命令

```bash
# 从项目级 workflows 同步到 skill 副本
cp .claude/workflows/audit-parallel-v2.js .claude/skills/ll-workflow-core/templates/cc-workflows/
cp .claude/workflows/test-generation-parallel.js .claude/skills/ll-workflow-core/templates/cc-workflows/
```

### 同步时机

| 时机 | 是否同步 |
|------|----------|
| ll-workflow-core release 前 | **必须同步** |
| 日常脚本优化 | 仅更新项目级，下个 release 前批量同步 |
| 紧急修复 | 同步到副本，避免 release 后引入不稳定脚本 |

## 引用方式

route-registry.yaml 中通过相对路径引用：

```yaml
- id: route.aud.cc-workflow
  workflow: ../templates/cc-workflows/audit-parallel-v2.js
```

执行时引擎会解析相对路径（相对于 route-registry.yaml 所在目录）。