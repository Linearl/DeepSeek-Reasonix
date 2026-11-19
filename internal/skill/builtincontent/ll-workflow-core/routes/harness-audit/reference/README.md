---
description: "参考实现脚本——Diff-Size Gate、CAPABILITY_MAP 重复检测、重复定义扫描器"
update_time: 2026-07-01
---
# Harness 参考实现

本目录包含从实际项目中提炼的 harness 脚本参考实现。

> **重要**：这些脚本是**参考模板**，不是开箱即用的工具。实际使用时通常需要根据项目结构调整路径、阈值、匹配规则等配置。

## 脚本清单

| 脚本 | 类型 | 用途 |
|------|------|------|
| `diff-gate.mjs` | PostToolUse hook | 累计代码变更行数，超阈值触发审计轮次 |
| `capability-gate.mjs` | PreToolUse hook | 写入前检测新标识符是否与已有能力重复 |
| `scan-duplicate-definitions.mjs` | 诊断脚本 | 扫描仓库中同名不同定义的函数/类/常量 |
| `validate-capability-map.mjs` | 校验脚本 | 检查 CAPABILITY_MAP 中的文件路径和函数名是否有效 |
| `config/diff-gate-config.json` | 配置文件 | diff-gate 和 capability-gate 的共享配置 |

## 适配步骤

1. **路径适配**：搜索脚本中的 `PROJECT_ROOT`、`BACKEND_SRC`、`CAPABILITY_MAP` 等路径常量，替换为你的项目结构
2. **阈值调优**：根据项目规模和团队习惯调整 `diff-gate-config.json` 中的 `threshold`
3. **扩展名适配**：修改 `codeExtensions` 匹配你的技术栈（如 Python 项目加 `.py`）
4. **排除模式**：根据项目目录结构调整 `excludePatterns`
5. **注册 hook**：将脚本注册到 `.claude/settings.local.json` 的 hooks 配置中

## 已知限制

- diff-gate 仅统计行数差异，不感知语义变更（删除 vs 新增）
- capability-gate 依赖 CAPABILITY_MAP 手动维护，不自动发现新能力
- scan-duplicate-definitions 使用正则提取声明，不支持 TypeScript 泛型、重载等复杂语法
- validate-capability-map 需要 Node.js 环境，不支持 Windows 原生 `find` 命令
