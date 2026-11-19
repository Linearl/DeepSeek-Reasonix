---
description: "[专题名称] — 分阶段需求追踪清单"
date: YYYY-MM-DD
update_time: 2026-07-01
---
# [专题名称] — 分阶段需求追踪清单

> 基线：[审计/诊断报告链接]
> 当前进度：0/N 已完成，N 项待开发（M 个 Block）
> 总计：N 个任务包（X P0 + Y P1 + Z P2）

---

## 零、阶段总览

```text
Block 1（阶段名，并行/串行）          Block 2（阶段名）              Block N（收尾）
┌─ REQ-XXXX 任务A   ─┐                                    ┌─ REQ-XXXX 任务X  ─┐
├─ REQ-XXXX 任务B   ─┼──→  REQ-XXXX             ──→    └─ REQ-XXXX 任务Y  ─┘
└─ REQ-XXXX 任务C   ─┘      任务D                         🔒 全量测试闸门
  🔒 测试闸门                  🔒 测试闸门
```

**闸门规则**：每个 Block 结束后必须运行测试验证，全部通过方可进入下一 Block。任一测试失败须修复后重新验证，不得携带已知问题进入下一阶段。每个 Block 通过后提交作为下一阶段的基线。

---

## 一、Block 1：[阶段名]（并行/串行，预计 X 天）

> 前置条件：无 / 依赖 Block X。
> 目标：[阶段目标]
> 并行说明：[如可并行] 三个任务无互相依赖，可并行执行。

### 1.1 REQ-XXXX [任务名]

| 属性 | 值 |
| --- | --- |
| 目录 | `v09-rXXXX-pX-xxx` |
| 优先级 | P0/P1/P2/P3 |
| 工时 | X 天（N tasks） |
| 核心改动 | [一句话描述] |

**变更清单：**

- [ ] T1 [子任务描述]
- [ ] T2 [子任务描述]
- [ ] T3 [子任务描述]

**验收标准：**

- [ ] [可验证的条件 1]
- [ ] [可验证的条件 2]

### 1.2 REQ-XXXX [任务名]

（同上结构）

### 1.3 复核实际完成度

- [ ] 逐项核对变更清单完成情况
- [ ] 核对验收标准全部达成
- [ ] 更新任务包 `tasks.md` 标记完成
- [ ] 更新任务包 `README.md` 状态

### 1.4 Block 1 测试闸门

```bash
npx tsc --noEmit                         # 类型检查
npm run test:unit                        # 单元测试
npm run test:integration                 # 集成测试
```

- [ ] 类型检查零错误
- [ ] 单元测试全部通过
- [ ] 集成测试全部通过
- [ ] git commit：`chore(Block1): [描述]`

### 1.5 归档本阶段任务包

- [ ] 任务包从 `B.todo` 迁移至 `B.2.done`
- [ ] 同步更新 `docs/1.task/requirements.md`（`pnpm llwf:req-sync -- --apply`）
- [ ] 同步更新 `docs/1.task/README.md`
- [ ] 同步更新 `docs/INDEX.md`
- [ ] git commit：`chore(Block1-archive): 归档 Block 1 任务包 [REQ-XXXX, REQ-XXXX]`

---

## 二、Block 2：[阶段名]（预计 X 天）

> 前置：Block 1 完成。
> 目标：[阶段目标]

### 2.1 REQ-XXXX [任务名]

（同 Block 1 结构）

### 2.3 复核实际完成度

- [ ] 逐项核对变更清单完成情况
- [ ] 核对验收标准全部达成
- [ ] 更新任务包 `tasks.md` 标记完成
- [ ] 更新任务包 `README.md` 状态

### 2.4 Block 2 测试闸门

```bash
npx tsc --noEmit                         # 类型检查
npm run test:unit                        # 单元测试
npm run test:integration                 # 集成测试
npx playwright test                       # E2E 测试（如影响前端）
```

- [ ] 类型检查零错误
- [ ] 单元测试全部通过
- [ ] 集成测试全部通过
- [ ] E2E 全部通过（N tests）
- [ ] git commit：`chore(Block2): [描述]`

### 2.5 归档本阶段任务包

- [ ] 任务包从 `B.todo` 迁移至 `B.2.done`
- [ ] 同步更新 `docs/1.task/requirements.md`（`pnpm llwf:req-sync -- --apply`）
- [ ] 同步更新 `docs/1.task/README.md`
- [ ] 同步更新 `docs/INDEX.md`
- [ ] git commit：`chore(Block2-archive): 归档 Block 2 任务包 [REQ-XXXX, REQ-XXXX]`

---

## 三、Block N：收尾（预计 X 天）

> 前置：所有前置 Block 完成。
> 目标：最终验证 + 全量回归

### N.1 复核实际完成度

- [ ] 逐项核对变更清单完成情况
- [ ] 核对验收标准全部达成
- [ ] 更新任务包 `tasks.md` 标记完成
- [ ] 更新任务包 `README.md` 状态

### N.2 全量测试闸门

```bash
npm run test:all                            # 后端全量测试
npx playwright test                         # E2E 全量
```

- [ ] 后端全量测试通过
- [ ] E2E 全量通过
- [ ] git commit：`chore(BlockN): 收尾验证`

### N.3 归档本阶段任务包

- [ ] 任务包从 `B.todo` 迁移至 `B.2.done`
- [ ] 同步更新 `docs/1.task/requirements.md`（`pnpm llwf:req-sync -- --apply`）
- [ ] 同步更新 `docs/1.task/README.md`
- [ ] 同步更新 `docs/INDEX.md`
- [ ] git commit：`chore(BlockN-archive): 归档收尾阶段任务包`

---

## 四、任务依赖矩阵

```text
任务       Block   依赖        可并行组
──────     ─────   ────────    ──────
REQ-A      1       无          A
REQ-B      1       无          A
REQ-C      1       无          A
REQ-D      2       A,B,C       独立
REQ-E      3       D           B
REQ-F      3       D           B
REQ-G      4       E,F         独立
```

---

## 五、审计项/需求追踪

| ID | 等级 | 描述 | 关联 REQ | Block | 状态 |
| --- | --- | --- | --- | --- | --- |
| H1 | HIGH | [问题描述] | REQ-XXXX | 1 | ⬜ |
| M1 | MEDIUM | [问题描述] | REQ-XXXX | 2 | ⬜ |
| L1 | LOW | [问题描述] | — | P2 积压 | ⬜ |

---

## 六、低优先级积压（择机处理）

以下审计项已识别但尚未创建独立任务包：

| 审计 ID | 等级 | 描述 | 备注 |
| --- | --- | --- | --- |
| — | LOW | [描述] | [备注] |

---

## 七、确认安全的领域（无需处理）

| 领域 | 验证结果 |
| --- | --- |
| [领域名] | [已验证的原因] |

---

## 八、进度追踪

| Block | 任务数 | 状态 | 通过测试？ | 已提交？ |
| --- | --- | --- | --- | --- |
| Block 1 | N | ⬜ | ⬜ | ⬜ |
| Block 2 | N | ⬜ | ⬜ | ⬜ |
| Block 3 | N | ⬜ | ⬜ | ⬜ |
| Block N | N | ⬜ | ⬜ | ⬜ |
