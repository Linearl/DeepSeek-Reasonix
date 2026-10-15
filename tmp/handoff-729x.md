# handoff-729x —— 三小件（729/780/859）核验留档（2026-10-14）

结论先行：**三件均已在本 wt（基于 main-v2-stable @ 6280772ea）创建之前完成交付并合入 main**，无源码需要新改；本 wt 仅做核验+留档，唯一 commit 即本文档（tmp/handoff-877.md 同例）。三件各自的原始 commit、验收证据、归属判定如下。

---

## 729（P3 判定留档件）预存红归属

- **commit：f960d96f4**（经 8fc3f70fb batch 22 merge 入 main，2026-10-10）
- **归属判定：①测试过时**（三选一里的第一项），行为无回归。
  - 断点实证：干净基线 b446ba02f 同红（723 交付记录）；断点在 bc055ba45（task 441，2026-10-01）——该提交有意把 ↑/↓ 箭头改成六点拖拽手柄（用户报的交互改进）；旧测试正则 `/onMove?\.\(item, index - 1\)/` 钉的是被移除的箭头形态。
  - 不弱化的修法：回归钉由 2 条断言变 3 条——batch send 调用形态（onBatchSend(batchSendable)）+ movable 共享闸门（const movable = Boolean(onMove)）+ 拖拽落下 onMove(items[from], index)；注释引用 task 441 说明箭头→手柄来源。
- **验收（本次复跑）**：`npx tsx src/__tests__/task258-message-presentation.test.ts` → 62 checks passed, 0 failed（修前 57/1；后续 796 又增 4 条断言，现 62 条全绿）。

## 780（P2 双层 bug）

- **commit：3e9447f32**（经 d4ca1ab0e batch24.4 merge 入 main，2026-10-13）
- **Bug A（617 一键分析改写发起方会话标题）**：命名目标原经 activeSessionLocation() 读全局 active tab 间接寻址，openCrashAnalysisSession 切 activeTabID 后有最长 60s controller 等待窗，窗内 active tab 是可变状态，实测被切回发起方 tab →「崩溃分析-<时间>」写进发起会话 sidecar CustomTitle。修复：新增 sessionLocationForTab(tabID)（desktop/crash_analysis.go:217），命名目标钉死在分析 tab 上；hang 面同步（crash_analysis_hang.go:182）。后续 858（baa31e085）再加 guard 拦截改写用户既有会话的第二道门，已在 HEAD。
- **Bug B（rename_session 声称 listingSynced 但 directory 不变）**：renameCollabSession 走 RenameTopic 只写 TopicTitle 不动 CustomTitle，而 contact directory（agent.scanAddressable）CustomTitle 优先，手动命名过的会话永远改不掉。修复：选写穿案——topic 面成功后对 sessionPath 同步 a.RenameSession 写穿 CustomTitle，失败如实报错不谎报 synced（desktop/session_manage_collab.go:54）；collabSessionTitle 改 CustomTitle 优先与 directory 同源（:86）。
- **验收（本次复跑）**：
  - `go test ./desktop -run "TestStartCrashAnalysisNamesAnalysisSessionNotInitiator|TestRenameCollabSessionWritesThroughManualCustomTitle"` → ok（4.9s）。前者用 publishControllerAndStealActivation 在 controller 发布同一锁段切回发起方 tab 确定性重放竞态（修复前红）；后者钉写穿+同源读（修复前红：previousTitle 误报 Auto title）。
  - 扩面 `go test ./desktop -run "CrashAnalysis|RenameCollab|HangAnalysis"` → ok（9.9s）。

## 859（P3 小改）证据门熔断

- **commit：49a12a034**（经 c983aa0ae merge 入 main，2026-10-14 00:01）
- **任务书冲突说明**（任务书要求注明）：早前 commit_ref 记「本体不动、补件并入 877」，877 已交付 capability 面；本 commit 即按主对话 16:09 新令做的**证据门面本体**（internal/evidence/operation.go 状态机 + internal/agent/operation_lifecycle.go + internal/tool/operation_diagnostic.go），与 877 capability 面无重叠，以本件任务书为准。
- ①阈值：operationRecoveryBudget 2→3（internal/evidence/operation.go:44），第二次同因失败仍给带指引自动恢复，第三次同因失败判循环进 needs_user 兜底。
- ②回执语义：熔断=「带修正指引的强提醒」而非转人工——WRITE_EVIDENCE_STALE 回执补失败原因（文件已被你自己的编辑更新）+合规路径（重读目标行后基于新内容重试）；ModelFacing JSON 带 recovery 文案；未填建议的入口由 failureGuidance 按 code 兜底（operation_lifecycle.go:133）。
- **验收（本次复跑）**：
  - `go test ./internal/evidence -count=1` → ok（全包）；TestOperationSameFailureThreeTimesStopAutomaticRecovery 钉 3 次同因→needs_user（operation_test.go:79）。
  - `go test ./internal/agent -run "TestFailureGuidance|TestOperationRecovery|TestEvidence"` → ok；TestFailureGuidanceNamesReasonAndCompliantPath 钉 STALE 实例同时含原因+合规路径（operation_lifecycle_test.go:163）。

---

## 残留与注意

- 本 wt（wt-729x）无源码改动，唯一 commit 为本文档；分支保留。
- main-v2-stable 上另有一支 780 同内容 commit 8643c8824 在 wt-780 未合入（与 3e9447f32 内容重复，非本件范围，未动）。
- 780 Bug A/B 的用户侧端到端验收（点一键分析看标题）无法在无头环境执行，Go 确定性重放测试已覆盖竞态与写穿路径。
