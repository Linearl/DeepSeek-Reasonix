# task 890 handoff — turn 结束后后台任务不应阻止切模型

## 状态
- worktree: wt-890（基于 main-v2-stable 73ff458bd）
- 阶段：实现完成，四包测试推进中（jobs/control 绿、boot 二绿一 flaky、desktop 后台跑）。

## 设计要点①：swap 后后台任务存活吗？——**现状：不存活**（修复前）

证据链：
1. `internal/boot/boot.go:714`：每次 Build 创建全新 `jobs.NewManager`（Options 原本无 Jobs 字段）；job 的 ctx 派生自 manager root（`internal/jobs/start.go:26`），独立于 controller。
2. rebuild/swap 后旧 controller `Close()` → `close(true, closeJobsWithGrace)` → `c.jobs.Close()` → `m.cancel()` → 全部 job ctx 取消 → `proc.CommandContext` 杀进程树（`desktop/app_model.go:544` swapModelController；`desktop/settings_app.go` rebuildSettingTurnLocked 同样 oldCtrl.Close()）。
3. 佐证：上游 #12507 原文 "the switch builds a new controller and closing the old one terminates its jobs"（见 docs/issues/reports/891-上游同款核查-切模型背景任务拦截-20261014.md）。
4. 例外：`SetModelForTab` 快路径（tryModelSessionOverride，app_model.go:301）不换 controller、且在 guard（:310）**之前**——任务存活且不被拦。真正杀任务的是 rebuild 路径（persona boundary decline / model settings apply 走 refreshTabModelSettings→rebuildSettingTurnLocked 必 rebuild）。

## 设计要点②：上游 issue —— **不动**
891 报告结论：上游同款 #12507 已由 PR #12622 修复（CLOSED/MERGED 2026-10-11），修法 = handoff 路线（CloseHandingOffJobs + boot.Options.Jobs/WorkspaceLease 交接 + Manager.AfterUnfinished），已验证可行；按硬前置不提 issue，本任务只做 fork 修复。

## 拦截链与半提交成因
- 判据：`desktop/app_lease.go:34-36 active() = running || pendingPrompt || backgroundJobs>0`。
- 截图错误 `Send failed:` 来自 turn admission：`desktop/turn_admission.go:142` modelSettingsNeedApply → `model_settings.go:103 refreshTabModelSettings` → `rebuildSettingTurnLocked("saved model settings")` → `rebuildControllerActiveWorkErrorFor` 拦 → 「设置已保存 apply 失败」半提交 + 每次 Send 重试再拦。
- SetModelForTab rebuild fallback 同 guard（app_model.go:310/:322，setting="model"）。
- running=true 时 turn admission 先被 Running 检查拦（ErrTurnRunning），到不了 apply——所以被 background_jobs 单项拦 = turn 已结束的场景（与用户定调一致）。

## 修法（已实现）
**handoff（保住后台任务）+ 切模型场景 backgroundJobs 降权**：

1. `internal/jobs`：`Manager.AfterUnfinished(fn)`——对「调用时刻未完成的 job」快照等它们 done 后执行 fn；无未完成 job 则同步执行；调用后新起的 job 不拖住 fn。
2. `internal/control`：
   - `Options.JobsHandoff bool` → Controller `jobsBorrowed`：借来的 manager，任何 close 模式都不关它（保护 swap 失败路径）；
   - `closeJobsKeep` 模式 + `CloseHandingOffJobs()`：其余清理照常，仅不关 jm；build 级 cleanup（LSP/sessionV4/base）经 `jobs.AfterUnfinished(c.cleanup)` 延后到当下在跑的 job 退出后执行；
   - `AdoptJobs()`（borrowed→owner）、`JobsManager()`/`WorkspaceLeaseOwner()` 访问器。
3. `internal/boot`：`Options.Jobs`/`Options.WorkspaceLease` 成对复用（单腿传入直接报错「handoff requires … together」）；`JobsHandoff: opts.Jobs != nil` 传给 control。
   - **与初版 handoff 文档的修正**：AfterUnfinished **需要做**——fork 的 cleanup 链（sessionV4.CloseAll、lspMgr.Close、baseClient.Close）对 background **task** 子代理有依赖（transcript 保存/LSP 工具/远程 base），cleanup 若在 job 中跑时立刻执行会打断它们；上游 #12622 同样做了延迟。bash 类 job 不受影响，延迟是保守正确的统一解。
4. `desktop`：`jobs_handoff.go`——`jobsHandoffFrom(oldCtrl)`（非 *control.Controller 或缺 manager/lease → nil/nil 回退旧行为）、`closeReplacedControllerAfterHandoff(new, old)`（指针相等证明交接发生 → `new.AdoptJobs()` + `old.CloseHandingOffJobs()`，否则 plain Close）。接线点：buildModelSwitchController、swapModelController、buildSettingReplacementController（reload/legacy 两分支共用 opts）、rebuildSettingTurnLockedWithModel。
5. guard 降权：`activeForModelSwitch() = running || pendingPrompt`；`isModelSwitchSetting` 仅 "model"/"saved model settings"；其他 setting（effort/sandbox/MCP/plugins/subagents/language/runtime reload/settings）行为不变。

## 验收映射（测试证据）
① `desktop.TestSetModelForTabBackgroundJobsSurviveRebuild`：bg=1+running=false 切模型成功、jm/lease 交接（指针相等）、job 存活、替换侧 BackgroundJobs==1、旧 cleanup 延迟到 job 退出后执行。**PASS**
② `desktop.TestRebuildModelSwitchGuardOnlyIgnoresBackgroundJobs` 矩阵 + 既有 persona 测试（running=true 仍拦）。**PASS**
③ `desktop.TestRefreshTabModelSettingsAppliesDespiteBackgroundJobs`：stale model settings + bg job → apply 成功、failure 无残留、ModelSettingsState 收敛（admission 不再重拦）、job 存活。**PASS**
④ 两要点结论写入本文档与交付说明。✓
单元：`internal/jobs/after_unfinished_test.go`（2）、`internal/control/jobs_handoff_test.go`（3：handoff 关不杀 job+延迟 cleanup / borrowed close 不关 jm / AdoptJobs 后 owner close 收编）、`internal/boot/jobs_handoff_test.go`（2：单腿拒绝 / 复用身份+borrowed close 保 jm）。

## 包级测试
- internal/jobs：ok（15s）
- internal/control：ok（252s）
- internal/boot：第 1 跑 FAIL（未留失败名）→ 第 2、3 跑 ok；新测试单跑 5 连绿。疑既有 flaky，未复现。
- desktop：进行中（见最终汇报）。

## 残留风险 / 边界（预登记）
- deferred-rebuild 重试循环（deferred_rebuild.go:258/:310）仍按全量 active() 门控——只影响「先被 running 拦下而入队」的 model 重建：turn 结束后若还有 bg job，队列重试会等到 job 结束才跑。直接路径（用户此刻切换）已畅通；入队路径属既有行为，未改动。
- 非 *control.Controller 的宿主（测试 stub）自动回退旧语义，无交接。
- 旧 controller 的 sessionEnd/ledger/sessionTemp 释放仍在 swap 时立即执行（与修复前一致）；仅 build 级 cleanup 链延迟。
- 上游 #12622 未来合并时的对齐点：同名 CloseHandingOffJobs/Options.Jobs/AfterUnfinished，合并冲突预计集中在 control close 分支与 boot lease 创建段。

## 待办
- [x] control：CloseHandingOffJobs/AdoptJobs/borrowed 关闭语义 + 测试
- [x] boot：Options.Jobs/WorkspaceLease 复用接线 + 测试
- [x] desktop：交接接线 + guard 按 setting 降权 + 测试 4 组
- [x] jobs：AfterUnfinished + 测试
- [ ] desktop 包级测试收尾 + commit（中文「890 」开头，显式路径）+ 最终汇报
