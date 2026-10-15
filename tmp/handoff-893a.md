# 任务 893 一期 A+C handoff（2026-10-15）

worktree：`github-repo/worktrees/wt-893a`（基线 `1c632752f` = main-v2-stable tip）。本件做一期 **A（投影层语义修正）+ C（已完成项并入展示）**；B 已落地（wt-893，0548 装机）勿重做；D'（canonical 抗 compaction 锚）二期，**触发条件经样本核对判定为未满足**（见下）。

## 任务一句话

todo 列表跨回合被静默清空（面板误显「暂无任务」+ AI todo_read/ops 报错）。A = 投影层 `projectTurnStart` 不再清掉可见列表；C = 已完成列表签核后同回合内不回收（前端面板保持可见）。

## 步骤 1：样本核对结论（必做前置，两例定案）

生产配置事实：用户 `config.toml` `session_storage = "v3_only"` → **v4 session 事件存储（projection.go 所属层）在生产未启用**（`SessionV4WritesEnabledForMode("v3_only")=false`，`%APPDATA%\reasonix\sessions-v4` 目录不存在）。空态样本只能也只需在 v3 transcript（`projects/<slug>/sessions/*.jsonl`）上核对。

**S1 = 20260804-063617…deepseek-v4-flash.jsonl（任务书证据链 r_e0f86887/r_a02681b4 的原始会话）**，10-08 窗口（ULID 解时间戳）：

- 13:06 msg60：todo_write ops 成功（r_a02681b4，2 项 1 完成），同进程同会话；
- 13:17:03 msg66：用户新回合（context-state 注入，occupancy 11% 无 compaction、无 reload、无换线）；
- 13:17:19 msg67：todo_write ops → `step_id "step_2" is not in the current list`；
- 13:17:45 msg70：todo_read → `{"todos":[],"note":"no todo list is active"}`。
- **判定：同进程回合间 canonical 丢失；候选 1（compaction 丢失）不成立**（无折叠无重载）。

**S2 = 20261013-170004-session.jsonl（迭代巡检会话，10-13）**：

- msg53：todo_write 成功（1 项，r_8a2c7502）→ msg55：write gate 仍拦「establish a concrete todo…」（读空）→ msg57：ops 报 `step_id … is not in the current list` → msg59：todo_read 空。**同回合内秒级丢失**，模型当场记录「C-20261014-01 ops 假成功现场复现」（任务候选池已有同款 feedback 2026-10-10 两份）。该会话另有 2 处空读属「从未 write」（候选 2，模型探测性 todo_read）。
- 注意：该 transcript 疑为迁移/导入会话（消息 ULID 时间戳全同），live 时序不可完全复原，但「成功回执后基线即空」与 C-20261014-01 描述一致。

**结论**：两例主路径均为**同进程内存 canonical 非预期丢失**（具体清空/未生效点未定位到代码级；goal_run_boundary stall 清空 / cancel-strip 后 rebuild / 迁移导入时序都是候选），**均不指向 compaction 丢失 → D' 二期触发条件不满足，不启动**。一期验收①的「空态→非空」由 C（前端展示保留）兑现；A 是语义对齐 + v4 启用时的前置正确性。现象 1 的根因建议单独立项诊断（候选池 C-20261014-01 同族）。

## Q4 检查点 3：projection.Todos 全量消费方普查（实施前置，已完成）

`git grep "Projection.Todos\|projection.Todos" -- "*.go"` + 全仓 `.Todos` 扫描：

- **写入/清除**：仅 `internal/session/projection.go` 三处（322 turn/start 清、441 todo/write 写、550 clone 拷贝）；
- **读取方**：**零外部消费方**——无 history/checkpoint/前端 API/serve/acp 读它（`Snapshot().Projection` 的消费点只用 Messages/Turns/ActiveTools/Interactions/Title/ModelRef）；JSON 序列化面也无（结构体无 json tag、无 Marshal 调用）；
- 前端任务面板的真实数据通路：**transcript 工具项**（`state.items` 里最新成功顶层 todo_write，args 由 `desktop/app_history_tools.go` 特意保留）+ **`meta.canonicalTodos`**（`ctrl.Todos()` = agent 内存 canonical）——与 projection 层无关；
- **推论**：①评审报告 Q1「现象 2 根因 100% 在 projection.go:322、UI 读的就是它」**不成立**（该普查正是报告标注的实施前缺口）；②A 改动在当前生产（v3_only）与 v4 均**零行为回归**（无消费方依赖 turn 清空）；③A 的价值 = 消除注释与实现矛盾（todo_state.go:3「outlives a turn」）+ v4 启用前的语义就位。

## 改动清单（2 commit）

- `d4973acd8` **A（backend）**：`internal/session/projection.go` projectTurnStart 只清 `TodoWritten`，保留 `Todos`（字段/事件处补契约注释）；新增 `internal/session/projection_todo_contract_test.go` 两条契约测试：①turn/start 跨回合保留（live Snapshot + Flush+Replay 双断言，TodoWritten 复位）②显式空 `todo/write` 照常清空（真回收路径不变）。
- `545108ed6` **C（frontend）**：`TodoPanel.tsx` 全完成态不再 1.14s 自动退场（删 COMPLETION_HOLD/FADE 计时器与 `todo-exit` 类），签核后保持可见（3/3 徽标 +「关闭」）；`styles.css` 删 `.todo-exit` 规则；`todo-panel-lifecycle.test.tsx` 改钉新契约（完成不排退场计时器 / 60s 后仍挂载 / 手动关闭触发 onDismiss 记账 1 次）；**重载恢复的全完成批次行为不变**（mount 即隐藏 → 封存态接管）。

## 验收对照

1. ✅ 跨回合列表存活成常态：C 保证签核后同面板持续可见（跨回合不退场）；A 保证投影层列表 turn 不清。用户装机实测路径：写 3 项 → 全签核 → 面板保持「3/3 + 关闭」→ 次回合仍在；点关闭后 →「任务列表已按回合封存（上一回合 N 项）」。
2. ✅ 真回收封存文案照常：关闭（sidecar dismissedTodoBatches）/ 显式空 todo_write / goal stall 清空路径均未动；`todo-dock-empty-state` 35 断言全绿。
3. ✅ 负面清单机制行为不变：`goal_run_boundary.go:91 ReplaceTodoState(nil)` 一字未动；agent 包全量跑过，`TestTodoStallClearsCanonicalListInOrdinaryChat` 等 stall 族绿（预存红见下，均与 todo 无关）。
4. ✅ 普查报告：见上节。
5. ✅ 样本核对结论：见上节。
6. ✅ 既有 todo/投影族测试全绿：Go `internal/session` 全量 63s ok（含新契约测试）、`internal/tool/builtin` ok、`internal/control` 全量 ok（首跑与 agent 包并行时曾红，隔离重跑全绿）；前端 `test:todo-visibility`（200k 纯函数 + lifecycle）、`todo-dock-empty-state` 35、`todo-sidebar-dock` 63、`task152-todo-tree` 15 全绿；`tsc --noEmit` 0 诊断；check:css / check:comment-leak(0 leak) / check-app-layers 绿。

## 预存红申报（clean-HEAD probe 对照）

- `internal/agent` 全量：`TestToolCheckpointReloadRefreshesProjection`（restart left stale projection）、`TestInterruptedRecoveryKeepsRedactedFailureSummary`（no scripted turn 1）——**clean-HEAD probe worktree（1c632752f 同 node_modules 无关）复跑同红 → 预存**；`TestCompactSessionFileRewritesAndIsIdempotent`（second compact grew the log）全量并行负载下偶发、**双树隔离跑均绿 → 判负载敏感非回归**。
- 前端 `tsc-test-gate`：53 条基线外诊断全在 collab-*/composer-*/project-tree-* 等未触碰测试文件（B handoff 已报同族，依赖安装年代差）；**本件两个文件 0 诊断**。`check-duplicate-definitions` 5 处重复声明（VersionEntry 等）预存同 B。
- 未跑 wails generate（纪律）；bundle 检查未跑（本件 TS 删码 + CSS 删规则，只会降不会升）。

## 残留风险

1. **A 对用户当面痛点无直接可见效果**（零消费方 + v3_only）——面板改善由 C 承担；若二期决定让某消费方改读 projection.Todos，需另立件并重跑本普查。
2. **现象 1 根因未收口**：同进程 canonical 丢失的真实清空点仍未代码级定位（两例样本排除 compaction 主因）；候选池 C-20261014-01 同族，建议独立诊断件（怀疑面：goal 边界 / cancel-strip rebuild / 迁移导入会话 rebuild 时序）。
3. C 后 composer 货架常驻已完成清单直至手动关闭——与 mimo/zcode「完成后自动收起」观感不同，属任务书明示方向（签核后仍可见）；若用户嫌占位可加「折叠但不删除」中间态（未做）。
4. `todo-panel-lifecycle.test.tsx` 含 2 个字面 NUL 字节（历史遗留，Read 工具会拒读该文件）——本件用字节级替换改写，未触碰 NUL 位置；后续动该文件建议走脚本改写。

## 恢复方式

```bash
cd github-repo/worktrees/wt-893a
go test ./internal/session/ ./internal/tool/builtin/ ./internal/control/ -count=1   # agent 包有 2 预存红，见申报
cd desktop/frontend   # node_modules 为主仓 junction
node scripts/test-todo-visibility.mjs && npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/todo-panel-lifecycle.test.tsx \
  && npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/todo-dock-empty-state.test.tsx \
  && npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/todo-sidebar-dock.test.tsx && npx tsc --noEmit
```

落地纪律：保留分支 wt-893a，**不合 main-v2-stable**（合并由用户指定会话执行）。与 wt-908（ProjectTree.tsx 分组守卫）不同文件无冲突面。
