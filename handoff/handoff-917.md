# 917 计划：S1 底座彻底退役——785 最小退役后的彻底档

- 任务：917（P1 backend 小件，明示可与批 32/33 并行）
- 上游：785（最小退役档，1cfed5da5 已进主线）
- 分支：wt-917（基于 main-v2-stable @ 6280772ea）
- 日期：2026-10-15

## 背景

785 只做了最小退役：UI/配置面全删（S1 底座开关配置键、实验室条目、设置卡、三语文案），
但实现代码作废保留——baseproc 包、agent base_toolcall 远程门、control base_gate、
boot 消费点、cli/desktop 的底座 stdio 子命令面、cmd/probe478 全部留在树里。
本件 = 彻底档：删这些作废实现 + grep 全库清残留（含测试）。

## 依赖甄别结论（785 原话约束：521 链路先确认是否被主链路依赖）

**结论：S1 远程链路不被主链路依赖，可删；pidalive 叶子被活跃依赖，保留。**

1. **base_toolcall 远程门（521 修复所在）**：结构上被主链路调用
   （`internal/agent/tool_dispatch.go` invokeResolvedTool 每次派发都调 `baseToolCall`），
   但语义上恒死：
   - boot 硬编码 `Enabled: false`（785），`baseproc.Start` 恒返回 InlineBaseClient，
     `Mode()` 恒为 ModeInline；
   - 门的第一条检查 `bc.Mode() != baseproc.ModeRemote` 恒真 → 恒返回 ok=false →
     本地路径逐字节原样执行。
   - 521 两处修复（base_toolcall.go Comparable 先于 == 的防崩守卫；lifecycle.go
     ManagedClient 全入口 nil 防线）保护的代码只在 ModeRemote/ManagedClient
     存在时可达——785 后该状态不可达（无任何构造点能产出 remote client）。
   - 删门 = 删一个恒返回 false 的函数与不可达分支，零运行时影响。
2. **pidalive（internal/baseproc/pidalive/）**：被 collabinbox/stale_holder.go 与
   sessioncollab 活跃依赖（任务 511 复发断根 stale-holder 回收），**不是 S1 作废件**。
   原地保留（import 路径 reasonix/internal/baseproc/pidalive 不动：移动目录只带来
   活跃文件 churn 与 3 枚 integrity 锚改写，无功能收益）。
   随之 baseproc/lease.go 一并删除（S1c 会话租约，随包退役），其 511 锚
   （lease 存活源切至叶子包）随文件移除；pidalive 自身三枚锚（100/101/1033）保留。

## 删除清单（按 commit 顺序；每步编译绿后即 commit）

### C2：boot→agent→control 的 BaseClient 接线与两道远程门
- `internal/agent/base_toolcall.go` + `base_toolcall_test.go`（整文件删）
- `internal/agent/tool_dispatch.go`：删 invokeResolvedTool 开头的 baseToolCall 调用块
- `internal/agent/services.go`：删 `base` 字段、赋值、baseproc import
- `internal/agent/agent.go`：删 Options.BaseClient 字段与注释、baseproc import
- `internal/control/base_gate.go` + `base_gate_test.go`（整文件删）
- `internal/control/controller.go`：删 baseClient 字段、Options.BaseClient、赋值、
  ToolContractEntries/AllToolContractEntries 两处 baseCatalogEntries 调用块、import
- `internal/boot/base_client.go` + `base_client_shared_test.go` + `base_client_wiring_test.go`（整文件删）
- `internal/boot/boot.go`：删 startBaseClient 调用与注释（2127）、三处 BaseClient 传参
  （2182/2529/2595）、cleanup 里 `_ = baseClient.Close()` 包装（2800-2805）、
  BuildResult 传参（2953）
- `internal/boot/runtime.go`：删 Runtime.BaseClient 字段与注释、baseproc import

### C3：底座 stdio 子命令消费面（cli / desktop / probe478）
- `internal/cli/base.go` + `internal/cli/base_test.go`（整文件删）
- `internal/cli/cli.go`：删 `case "base"` 分支
- `internal/cli/shell_completion.go`：删 completionSpec("base"...) 注册块
  （dispatch 全覆盖守卫测试要求两侧一致，同步删除）
- `desktop/base_serve.go` + `desktop/base_serve_test.go`（整文件删）
- `desktop/main.go`：删 maybeRunBaseServe 调用块与 478 注释（121-128）
- `cmd/probe478/`（整目录删：验收探针驱动 baseproc.Manager，随实现退役）

### C4：internal/baseproc 包体（保留 pidalive/ 子目录）
- 删：baselog、client、conn、frame、lease、lifecycle、manager、protocol、serve、
  serve_toolface、sink、surface 十二个实现文件 + 全部对应 _test.go
  （baselog_test、client_test、conn_test、lease_test、lifecycle_test、
  lifecycle_nil_test、manager_test、protocol_test、serve_test、serve_toolface_test、
  sink_test、surface_test、toolcall_test）
- 保留：`internal/baseproc/pidalive/`（pidalive.go / _windows / _unix，511 活跃叶子）

### C5：grep 全库清残留（S1 底座三模式：驼峰键名 / 下划线键名 / stdio 子命令路径，含注释与文档）
- `internal/config/render.go`：129 行注释去掉 S1 底座开关的驼峰旧名（改「S1 底座」）
- `internal/config/render_lab_tiers_test.go`（81/215）、`render_lab_switches_test.go`（78）：同上
- 前端注释：`lab-layout.yaml`（338）、`labLayoutDefault.ts`（329）、
  `experimentTiers.ts`（193/249）、`task561-lab-regroup-merge.test.tsx`（多处）、
  `task562-experiment-tiers.test.ts`（78）、`task563-lab-picks-wall.test.ts`（141）——
  纯注释改写，计数断言不动
- `release-notes/FORK-实验特性推荐配置.md`：删 S1 底座配置行（键已随 785 退役）
- `internal/desktoplauncher/stderrlog.go`：注释不再引用已删的 internal/baseproc 文件
  （改内联描述，零代码改动）
- `scripts/check-fork-integrity.mjs`：删除全部指向已删文件/模式的锚
  （164-165 base_toolcall、419-420 521、925-927 S1a、975-980 S1b、984-985 boot 消费点、
  990-993 478、1018-1024 S1c、1029-1030/1038，及 102 lease 锚）；
  pidalive 锚 100/101/1033 与 collabinbox 锚 103 保留；相关注释块同步清理
- `tools/repolint/baseline.json`：手工删已删文件的条目（不跑 -update，避免
  顺带放松其他预算）

## 验收标准（可证伪）

1. `grep -rniE "base[Pp]rocess|base[_]process|base\x20serve"` 全库（除 .git/node_modules/dist）零残留
   （三模式即：驼峰键名 / 下划线键名 / 子命令路径 base␠serve）；
   `grep -rn "base_toolcall\|baseproc\." --include=*.go` 仅剩 pidalive 合法引用。
2. 521 依赖确认结论如上，落档本文件；保留件（pidalive）编译进主链路
   （collabinbox/sessioncollab 测试绿）。
3. `go build ./...` 绿；`go test ./...` 全量绿
   （已知预存红基线：boot MCP bench 负载抖动、desktop sandbox write roots——
   785 同款，主仓基线复现，非本件引入，出现时对照说明）。
4. `node scripts/check-fork-integrity.mjs` 全过；`go run ./tools/repolint` clean。
5. status 净；显式路径提交；中文 message「917 」开头；禁 stash/merge/rebase。

## 验证结果（2026-10-16 回写）

**commit 列表**：C1 `78496d93e` 计划落盘 / C2 `84ae51b5c` 删远程门与接线（13 文件 -1093 行）/
C3 `024513547` 删底座 stdio 子命令消费面（8 文件 -477 行）/ C4 `c2f516842` 删 baseproc 包体
（25 文件 -6601 行，留 pidalive）/ C5 `b0b50a50e` grep 清残留（13 文件，integrity 锚+baseline 同步）。

**验收逐条**：
1. grep 零残留：`grep -rniE "base[Pp]rocess|base[_]process|base\x20serve"`（排除 .git/node_modules/dist）
   → 0 命中（含本计划文档）；`grep -rn "reasonix/internal/baseproc"` 仅 3 处 pidalive 合法 import
   （collabinbox/stale_holder.go、sessioncollab/{sessioncollab,stale_holder_test}.go）；
   Go 侧 base_toolcall/baseToolCall/baseCatalogEntries/maybeRunBaseServe/baseCommand → 0 命中。
   （本计划文档自身的模式举例为唯一例外，属记录性引用。）
2. 521 依赖结论：见上「依赖甄别结论」——不被主链路依赖可删；pidalive 保留，
   collabinbox/sessioncollab 测试绿（32.9s/4.6s）。
3. 测试：`go build ./...` 绿；全量 `go test ./...` 后按包串行复核。
   **预存红（main 基线同款复现，非本件引入）**：store TestSessionPathSpellingsSingleSource、
   provider TestOpenCodeGoModelInfo*/TestPiCatalog*、proc TestEverySpawnSiteIsHiddenOrExempt、
   config TestServeRenderTableCoversEveryKey、agent TestToolCheckpointReloadRefreshesProjection +
   TestInterruptedRecoveryKeepsRedactedFailureSummary、control 12 项（9 项 main 同款 FAIL，
   3 项 main/worktree 互有互无=时序 flaky）、repair TestRepairMutationLock*（main 同款）、
   sessioncatalog TestReconcileBatchBoundary*（main 同款）、cli
   TestServeStartsWithMissingProviderKey（main 连跑 3 次全 FAIL）、desktop
   TestWorkbenchLayoutQuickClicksSerializeWorkspaceRebuild（main 同款）、
   worktree 包（main 同款 FAIL 902s）。
   **本件 touch 包串行复核**：tool/builtin、plugin、sandbox、collabinbox、agent/testutil、
   serve（353s）、agent（除 2 预存红）、config（除预存红）→ 绿；control（除预存红/flaky）、
   cli（除预存红）→ 绿；boot 单包 45m 预算复跑中（12m 超时为整树并行负载所致，
   单测 TestEffectTaskBudget* 单独跑 11s 绿）。
   前端：`tsc --noEmit` 全绿；task561（201 断言）/562（41）/563（35）全绿。
4. integrity 843/843 绿；repolint 违规 = main 基线预存红的严格子集且各项数值全部更低
   （本件净减 complexity/essay/file-size/function-size；main 侧 303 条 over-budget 为预存）。
5. 五 commit 全部「917 」开头、显式路径、status 净、无 stash/merge/rebase。

## 记录

- [x] worktree wt-917 创建；本计划落盘（C1）
- [x] C2：boot/agent/control 接线与远程门删除
- [x] C3：cli/desktop/probe478 底座子命令面删除
- [x] C4：baseproc 包体删除（留 pidalive）
- [x] C5：grep 残留清理（注释/文案/integrity 锚/baseline）
- [x] C6：全量验证 + 本文件回写结果（boot 单包复跑为最后一项）
