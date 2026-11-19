---
description: "gt(gen-test) 路由目录：并行生成后端服务层单元测试"
update_time: 2026-07-01
---

# gt 路由

`gt`（gen-test）路由负责**并行生成后端服务层单元测试**。

## 变体

| 变体 | short_name | executor | workflow | 推荐度 |
|------|-----------|----------|----------|--------|
| CC Workflow | `gt-cc` | cc_workflow | `templates/cc-workflows/test-generation-parallel.js` | 推荐 |
| 传统 YAML | `gt-trad` | traditional_yaml | `templates/workflows/gt.workflow.yaml` | 占位实现 |

## 目录说明

- `config/` — gt 路由专属配置（执行参数、门控阈值等）
- `docs/` — gt 路由文档（扩展点说明、调用示例）
- `scripts/` — gt 路由附带的辅助脚本（如 `mock_strategy_validate.py`）
- `templates/` — gt 路由产物模板（生成的测试文件模板、覆盖率报告模板）

## 状态

`status: experimental` —— `gt-cc` 已可用，`gt-trad` 为占位实现，待实质化。

## 与其他路由的关系

- `gt` 不依赖 `req` 路由生成的产物（不需要 requirements.md 即可运行）
- `gt` 输出物可被 `dev` 路由的 `gate.e2e_smoke` 门控消费
- `gt` 不调用 `fix` 路由（修复由 dev 流程负责，gt 只生成测试）

## 触发词

```
生成测试 / 并行测试生成 / 测试生成 / 并行测试 / 服务层测试 / 单元测试生成
```
