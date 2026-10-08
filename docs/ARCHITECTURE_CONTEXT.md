# 结构治理门禁与「改码前拉上下文」约定（任务 392）

> **这是什么**：fork 的结构治理面——模块注册表（`architecture-policy.json`）+
> 增量架构检查器（`scripts/check-architecture.mjs`）+ 存量基线
> （`architecture-baseline.json`）+ 本约定。2026-10-08 由任务 392 落地。
>
> **上游核查结论（2026-10-08，架构型取上游拍板的执行）**：esengine 上游
> 1.38.x→1.39.x **无同构实现**（无模块注册表/架构策略文件/增量结构检查；仅有
> 点检守卫，如 `internal/session/architecture_guard_test.go` 与
> `desktop/frontend/scripts/check-desktop-host-boundary.mjs`，均为单域点检而非
> 注册表型治理）。故裁定**自研**，不移植。`check-desktop-host-boundary.mjs`
> 上游独有，fork 暂缺，留作后续单点吸收候选（与本门禁不重叠）。

## 改码前拉上下文（评审可核对）

**约定**：改动任何已注册模块（`architecture-policy.json` 的 `modules`）之前，
先拉该模块的受控上下文，再动手：

```bash
node scripts/check-architecture.mjs --context ts/lib
node scripts/check-architecture.mjs --context go/control
```

输出包含：模块归属与 managed 状态 / roots / 方向红线（本模块不得 import 谁、
谁不得 import 本模块）/ spec-first 五步与设计八问骨架。**评审时核对**：改动
描述里应能回答五步（唯一所有者/单一路径/显式边界/显式时序/有界上下文）；答
不上来说明上下文没拉，退回先拉。

改完自跑（本地开发环，只查改动文件 + 反向依赖闭包；环检测恒为全图）：

```bash
node scripts/check-architecture.mjs --changed          # 对照 HEAD（含未跟踪新文件）
node scripts/check-architecture.mjs --changed --base main-v2-stable   # 分支对照
```

## 红线（v1，全部按 fork 实测定）

| 规则 | 内容 | 存量 |
|------|------|------|
| `go-host-import` | 生产 Go 包不得 import 宿主（`reasonix/desktop*`、`reasonix/cmd*`）；宿主只能被装配 | 0，硬门禁 |
| `go-testonly` | `internal/**/testutil` 只许被 `_test.go` 导入 | 0，硬门禁 |
| `go-cycles` | `internal/**` 生产包导入图禁环（SCC） | 0，硬门禁 |
| `ts-kernel-no-presentation` | `ts/lib`、`ts/store` 不得 import `ts/components`、`ts/app-shell`、`ts/custom` | 15 条存量登记基线，新增即报错 |
| `ts-components-no-app` | `ts/components` 不得上探 `ts/app-runtime`、`ts/app-shell` | 0，硬门禁 |
| `deep-import:<模块>` | 模块声明 `publicEntrypoints` 后外部只许经入口包进入（渐进启用，机制由单测把守） | 当前无模块声明 |

行数量级说明：zcode 参考值（单文件 400 行等）未照搬——fork 实测存量远超
（`controller.go` 6653 行等），且**行数红线已由 `tools/repolint` 持有**（800 行
天花板 + 棘轮基线），本门禁不重复。

## 与既有防线的分工（互不重叠）

| 防线 | 管什么 | 不管什么 |
|------|--------|----------|
| `tools/repolint` | 单文件行数/函数大小/复杂度/注释/Go 粗粒度分层（leaves/frontends/control），棘轮基线 | 模块级依赖方向、环、深导入 |
| `scripts/check-fork-integrity.mjs` | 上游合并后 fork 特有锚点防静默丢失 | 结构规则 |
| `desktop/frontend/scripts/check-app-layers.mjs` | app-runtime/app-shell 层内 AST 契约（domain 不可达 DOM/展示等） | 模块注册与方向红线 |
| `desktop/frontend/scripts/check-bundle-budget.mjs` | 打包体积预算 | 源码结构 |
| `scripts/check-architecture.mjs`（本件） | 模块归属、依赖方向、testutil 隔离、禁环、深导入 | 以上全部不碰 |

## 基线纪律（防静默污染）

- 存量违规以指纹 `sha256(rule\0file\0target)` 登记 `architecture-baseline.json`，
  检查器**只报基线外的新增违规**；同类违规换个落点就是新指纹，躲不过。
- **基线没有自动刷新通道**：CLI 不提供 update 参数。登记/删除只能人工编辑该
  文件并随 commit 送审（故意 carry-forward 时：修不动的先修，真要背的写明理由）。
- 基线条目**只减不增**：违规还清后检查器会提示对应指纹「已无对应违规」，
  请在同一 PR 删除（欠账还清必须销账）。
- `--strict` 忽略基线全量报，供审计，不进门禁。

## 接线

- CI：`ci.yml` lint job「architecture registry check」步（repolint 同位）；
  单测随 workflows job 的 `node --test` 批跑。
- 本地：`make lint`（repolint 同位）。pre-push 钩子保持 Go-only（`go vet` +
  repolint），不引入 node 依赖；结构门禁由 CI 与 `make lint` 把守。

## 渐进扩面

- 新模块：往 `architecture-policy.json` 的 `modules` 加条目（id/kind/roots/
  owner/managed/notes），`managed: false` 可先只登记不强制。
- 新方向红线：`directionRules` 加一对 from/forbid（先跑 `--strict` 数一数存量，
  有存量就随本变更登记基线，评审可见代价）。
- 禁深导入：给模块声明 `publicEntrypoints` 即启用（先实测存量直连，为 0 再开）。
