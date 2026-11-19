---
description: "ll-workflow-core 贡献指南，说明 route、bundle、profile 与文档的协同维护规则"
update_time: 2026-07-01
---
# Contributing

欢迎继续扩展 `ll-workflow-core`。这个仓库的重点不是“把所有逻辑都塞进 SKILL.md”，而是让 route、bundle、profile 与模板体系一起演进。

## 提交前先判断你在改哪一层

| 变更类型 | 优先修改位置 |
| --- | --- |
| 新增 workflow 入口 | `config/route-registry.yaml` + `templates/workflows/` |
| 适配新的目录布局 | `config/directory-layout.yaml` |
| 提升共享模板 / 脚本 | `config/resource-bundles.yaml` |
| 新增 route 专属资源 | `routes/<short_name>/` |
| 补人类可读说明 | `docs/` |
| 调整 skill 触发策略 | `SKILL.md` |

## 贡献原则

- 先改抽象层，再改具体 workflow；不要一上来就在 YAML 里硬编码路径。
- 两条及以上 route 会复用的内容，优先提升为 `shared.*` bundle。
- workflow 中的路径优先通过 layout slot / semantic name 解析，而不是直接写死目录。
- 面向公开仓库的描述应保持中性，避免残留特定公司、特定仓库或本机路径。
- 如果改动了 route、bundle、profile 或模板结构，对应文档也要一起更新。

## 推荐的变更流程

1. 明确这次改动属于 route / bundle / profile / docs / metadata 哪一层。
2. 修改相关文件，并检查相对路径是否仍然成立。
3. 如果改动影响公开使用方式，同步更新：
   - `README.md`
   - `docs/README.md`
   - `docs/PUBLISHING.md`
   - `CHANGELOG.md`
4. 至少做一轮最小验证：
   - markdown / yaml / script 基础校验
   - 关键相对路径抽查
   - 若涉及脚本，执行一次 smoke test

## 针对 route 扩展的最低要求

新增 route 时，至少补齐以下内容：

- `config/route-registry.yaml` 中的 route 定义
- `templates/workflows/<short_name>.workflow.yaml`
- 如有专属资源，补 `routes/<short_name>/`
- 如有共享资源升级，补 `config/resource-bundles.yaml`
- 对应说明文档（通常至少更新 `docs/workflows.md` 或 `docs/EXTENDING.md`）

## 针对 composite 扩展的最低要求（v0.2.0 起）

新增或修改 `templates/composite-actions.yaml` 中的任一 composite 时，**必须**先运行 `npm run test:composite` 通过 CI 后再提交。

最低要求：

- `id` 唯一且与 `flow.*` 命名空间对齐
- `uses` 非空，且每个 action id 在 `templates/action-registry.yaml` 中存在
- `entry_conditions` 与 `exit_conditions` 都已声明
- `default_on_fail` 取值合法
- **不**在同一 composite 内同时调用互斥的 action 对（参见 `action-registry.yaml` 中各 action 的 `usage_boundary` 字段）
- **`notes` 字段**（条件必填）：若 `uses` 中存在带 `usage_boundary` 且包含"内部调用/取代/合并/替代/互斥/顺序约束"关键词的 action，必须填写非空 `notes` 说明边界

完整自检清单参见 [`docs/composite-actions.md`](./docs/composite-actions.md) 第 5 节。

## 触发契约测试

在仓库根目录运行任一脚本：

```bash
# 运行所有契约测试 + 三副本一致性校验
npm run test:workflow-core

# 仅校验副本 MD5 一致性（快速）
npm run test:workflow-core:copies

# 仅运行 action-registry 测试
npm run test:workflow-core:registry

# 仅运行 composite-actions 测试
npm run test:workflow-core:composite

# 详细 spec 报告
npm run test:workflow-core:verbose
```

测试基于 Node.js v18+ 内置 `node:test`，仅需 `js-yaml` 一个依赖。

## 提交信息建议

建议使用语义化提交信息，例如：

- `feat(skill): add publishing guide for standalone repo release`
- `fix(dev-route): align docs init script with numbered docs layout`
- `docs(skill): generalize repository-facing wording`
- `chore(workflow-core): 合并动作注册表冗余（v0.2.0）`
- `test(workflow-core): 添加 mode 切换契约与 notes_required_when 验证`

## 发布相关改动

如果你的改动会影响独立 GitHub 仓库发布，请额外检查：

- 根目录文件是否仍然完整（`SKILL.md` / `README.md` / `LICENSE.txt` / `config/` 等）
- 新增文件是否也适合放在仓库根目录，而不是只适合 monorepo 子目录
- 是否引入了新的组织内向表述或不可公开的路径信息

必要时，请把发布影响同步记录到 `CHANGELOG.md`。
