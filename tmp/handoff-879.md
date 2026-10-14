# 任务 879 交接（wt-879）

- 分支：`wt-879`（基于 main-v2-stable @ 1747f222d）
- 工作区：`github-repo/worktrees/wt-879`
- 状态：代码 + 测试完成，2 个切片已提交；预存红对照完成（baseline worktree 双对照：单测 + 全量）

## 问题

用户 2026-10-14 反馈：调用 wait 工具等后台任务超 6 分钟，前端弹「会话疑似卡死」（hangPrompt 三按钮）；wait 返回即恢复正常。

根因链（已定位非推测）：
1. `internal/doctor/responsiveness.go` `ResponsivenessSilenceAfter = 6min`；
2. `assignVerdict` 判 silent 只看 ledger 静默时长——wait 阻塞期不写 ledger ⇒ 静默 6min ⇒ silent；
3. `desktop/crash_analysis_hang.go` `responsivenessVerdictIsHang` `case "silent" → Hung=true` → 前端 `shouldSurfaceHangPrompt` 弹窗。

## 分叉读码结论

选 **doctor 侧补活性信号**，不选「wait 自身打心跳」：
- ledger 已持久记录 `tool_dispatch`/`tool_started`（带工具名与 id）与 `tool_result`（同 id 关闭），doctor 只读文件即可复原在飞工具——与 turnevent 自身 replay 的 `pendingTools`（`internal/turnevent/ledger.go:265`）同一构造；
- 心跳方案改动面大（wait 执行路径 + ledger 节奏 + compaction 假设），且非修复所必需。

## 修法（2 commit）

1. `503e2befe` **879 fix(doctor)**：新增 verdict `waiting_tool`
   - 阻塞集合 `responsivenessBlockingTools = {wait, event_wait}`（proxy 匹配 `Name` 与 `ResolvedName` 双面）；
   - 静默 ≥6min 且阻塞工具在飞 → `waiting_tool`（detail 带工具名 + 已运行时长）；无在飞 → `silent` 原样（真死回归零变化，detail 改口为「无长阻塞工具在飞」）；
   - `ResponsivenessTurn.BlockingTool`（json `blockingTool`）+ render 补「blocking tool in flight」行与 `--watch` 提示。
2. `9194f8c2c` **879 fix(desktop)**：`responsivenessVerdictIsHang` 显式 `case "waiting_tool": return false`；头注/门注更新为两态语义。

刻意排除：bash/task（有 tool_progress 心跳，静默即可疑）、ask/approval（走 waiting_user，且判序在前）、talk_to_session(wait)（上限 120s < 6min）。

## 验收对照

| # | 验收项 | 结果 |
|---|---|---|
| ① | wait>6min 不判 silent-hang | `TestResponsivenessWaitToolSilenceIsWaitingToolNotSilent`（doctor 判 waiting_tool）+ `TestWaitSilenceLedgerDoesNotGateAsHang`（端到端：真实 ledger → CollectResponsiveness → 门不放行） |
| ② | 真死（无 wait+静默）仍判 hang | 同两测试的负对照：wait 返回后/去掉 dispatch → `silent` → `Hung=true`；既有 gate 表 `silent is hung` 不变 |
| ③ | 三判据正交 | `TestResponsivenessLivenessCriteriaAreOrthogonal`：running→working、pendingPrompt→waiting_user、阻塞工具→waiting_tool、全无→silent；优先级：waiting_user > 阻塞工具、working（窗口内）> 阻塞工具 |
| ④ | 既有族全绿 + 预存红对照 | 见下 |

## 测试记录

绿：
- `go test ./internal/doctor/` 全包 ✓（含 render 新测试）
- `go test ./internal/cli/` 全包 ✓（209s，doctor CLI 族含内）
- `go test ./internal/control/ -run "Cascade|Wait"` ✓（task 732 关联族）
- `go test ./desktop -run "Hang|Crash|Responsiveness"` ✓（18.5s）

**预存红对照**（baseline worktree `wt-879-baseline` @ 1747f222d，无本任务改动，双层对照）：

单测层（baseline 隔离复跑同样 FAIL）：
- `TestPumpDerivedHopExhaustionIsAHopRefusal`、`TestHopExhaustedIsReportedAndSettled`（collab hop 族）、`TestSettingsExposesEffectiveSandboxWriteRoots`（HOME 展开）——**预存红，与 879 无关**。

全量 desktop 包（两支各跑一轮 900s timeout，日志：`wt-879/tmp/desktop_full_879.log`、`wt-879-baseline/tmp/desktop_full_baseline.log`）：
- **两支同 3 红 + 同 `panic: test timed out after 15m0s`**（超时打断点不同：baseline 停在 TestSaveProviderPreservesExplicitEmptyVisionModels 0s、分支停在 TestBuildTabControllerMigratesPersistedBareMimoModel 1s ⇒ 无固定挂死测试，纯套件体量超 15m）；
- 我分支全量多出 `TestSetActiveTabDoesNotRekickFailedLazyTab` FAIL：失败点全在 `testing.go:1464 TempDir RemoveAll cleanup: directory not empty`（autosave 重试 goroutine 与 Windows 目录删除竞态，断言本身 PASS）。交错对照 6 轮 × count=5（每支 30 次）：**分支 2/6 轮失败、baseline 2/6 轮失败，同一失败形态** ⇒ 预存 flake，与 879 无关。

## 未决 / 残留风险

1. **无阻塞项**：baseline 全量对照已完成（见上），预存红全部归因完毕。
2. 残留风险（低）：
   - ledger compaction 若恰在 wait 执行中段丢弃 dispatch 记录（旧事件先删，结果记录仍在）→ 退回旧行为 silent（不会误升 waiting_tool）；反向（留 dispatch 丢 result）因 compaction 先删旧记录而不可达；
   - `event_wait`/`wait` 真的死等（工具自身卡死）也会被 waiting_tool 抑制 hang 弹窗——接受：任务书明示「有→降级」；用户可 `doctor responsiveness --watch` 自查。
3. 前端零改动：`hung=false` 即不弹窗（前端既有测试 `a not-hung verdict never surfaces` 覆盖）。
4. 未跑 `wails` 构建/前端测试（未改前端资源）。

## 清理

- `wt-879/tmp/desktop_full_879.log`（本任务全量日志）：结论已录入本文，已删；
- `wt-879-baseline` 对照 worktree：对照结束已 `git worktree remove`。
