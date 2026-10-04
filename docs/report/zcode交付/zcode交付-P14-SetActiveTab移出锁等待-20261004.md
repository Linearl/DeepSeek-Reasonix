<!-- Branch: wt-zcode-p14 (main-v2-stable @ 0849f7d46) -->
<!-- 交付会话: zcode P14 开发代理 | 2026-10-04 -->

# P14 交付：SetActiveTab 移出锁等待（try-lock + 后台降级刷新）

**TL;DR（结论先行）**

1. `SetActiveTab`（本地 tab 点击路径）不再同步等待会话 save-path 互斥锁：锁空闲时快照内联完成（语义与改前完全一致）；锁被占时立即切换、落盘交给既有的 tab 后台快照循环（`scheduleTabSnapshot`/`tabSnapshotLoop`），锁释放后数据最终刷新。DAG 调研测得的 96 次等锁（p50=28.1s / max=69.4s）从 tab 点击路径上消失。
2. 三层落地：agent 加 `SaveSnapshotIfPathFree`（try-lock 贯穿保存临界区，避免"预检-再拿锁"竞态窗口）、control 加 `Controller.SnapshotIfSavePathFree`（desktop 侧类型断言，不动 `SessionAPI` 接口，零波及测试 fake）、desktop 换 `snapshotTabForSwitch` 调用点（仅此一处；关闭/关机/远端接管路径保留同步快照）。
3. 新增 9 个测试全部通过（agent 4 + control 2 + desktop 3），占锁不阻塞、降级最终落盘、内联不回归、无能力回退四类断言齐备。
4. **预存红（与本切片无关，基线复现已写明）**：main-v2-stable @ 0849f7d46 上 `internal/agent` 的 torn-line 重放家族有挂死（已基线复现 2 个：`TestAppendForShutdownWithoutLockAfterTornTail`、`TestReplaySkipsTornLineBetweenEntries`；自锁死锁：`resumePastTornLine → replayFrom` 重入取同一互斥锁）与 3 个断言失败（context budget / runaway guard ×2）；`internal/control` 有 2 个墙钟敏感测试（round 预算、TurnDone 5s）在多套件并行时偶发；desktop 全量套件在 `TestImageInputSaveDefersAllTabsUntilNextTurn` 起跑后进程静默终止（分支与零改动基线**同测试、同形态**，该测试单跑通过）。零改动基线复现证据见第 4 节。

---

## 1. 改动内容

### 1.1 agent 层（internal/agent/save.go、session_persist_observer.go）

- **`SaveSnapshotIfPathFree(path) (attempted bool, err error)`**：`SaveSnapshot` 的非阻塞形态。
  - `attempted=false` **当且仅当** save-path 互斥锁被同进程其他 goroutine 持有，此时什么都不写、会话内存不动、err 必为 nil（干净拒绝）；
  - 锁空闲则与 `SaveSnapshot` 完全同语义：锁外先做截断转写升级（原有设计），再在锁内走 `saveLocked` + 观测日志 + `notifyPersisted`；失败照常返回错误。
- **`tryWithSessionSaveLocks`**：`withSessionSaveLocks` 的 try-lock 形态。用既有的 `tryLockSessionSavePath`（Task 196fix2 时代已有，listing repair 在用）拿锁并**持锁贯穿保存临界区**——不是"预检后释放再重拿"，所以没有 TOCTOU 窗口：拒绝即真拒绝，放行即全程持锁。跨进程文件锁保持原有有界等待（同进程排队才是本次测得的病灶）。语义规则：锁没拿到=干净拒绝；一旦提交尝试（已持锁），MkdirAll/文件锁/保存失败一律 `attempted=true` + 错误，与阻塞形态一致。
- **`savePathLockFree`**： advisory 探针，用于在昂贵的截断转写升级**之前**提前退出（锁明显被占时不做无谓的全量读回）；注释明确它只能用来跳过投机工作，不能作为写授权。
- **`saveObserved` 抽出 `saveObservedAfterUpgrade`**：转写完整性检查之后的观测保存体（begin/end 日志对、display-index 窥探、保存、persisted 通知）抽成函数，阻塞与非阻塞两条路径共用，消除双份维护。
- **`HoldSessionSavePathForTest`**：跨包测试复现锁竞争的测试专用钩子（互斥锁本体不可导出）；control/desktop 的占锁测试依赖它。

### 1.2 control 层（internal/control/controller.go、snapshot_same_revision.go）

- **`Controller.SnapshotIfSavePathFree() (attempted, err)`**：走 `snapshotWithDurability(..., nonBlocking=true)`。persist 阶段改走 `persistSessionSnapshotIfPathFree`；被拒时以哨兵 `errSavePathBusy` 短路，映射为 `(false, nil)`——**跳过恢复与投影是有意的**：什么都没写，调用方的降级路径稍后跑一次全语义保存。pre-persist 的静默 no-op（executor 空/无内容）仍算 done（`attempted=true`）。
- **`persistSessionSnapshotIfPathFree`**：只尝试纯快照式保存；任何升级形态（forceRewrite、mid-turn reshape、冲突重试）一律按"锁忙"拒绝降级，保证非阻塞写严格保持 append 形态、绝不在 UI 路径上加深 rewrite。

### 1.3 desktop 层（desktop/tab_selection.go）

- `SetActiveTab` 本地切换的快照点从 `snapshotTabForAction(active, "switching tabs")` 换为 **`snapshotTabForSwitch(active)`**：
  - controller 无 try-lock 能力（类型断言失败）→ 回退原阻塞形态（行为不变）；
  - 锁空闲 → 内联快照，与改前一致；
  - 锁被占 → **立即切换**，`scheduleTabSnapshot(tab.ID)` 把落盘排进既有后台循环。该循环单飞（single-flight）per tab、带失败重试、`closing` 标志 + `quiesceTabAutosave` 防关闭竞态（#4384 已修的"删会话复活"问题不受影响）；
  - 快照**报错**（区别于锁忙）→ 沿用原语义：`reportTabSnapshotError` + 中止切换。
- 关闭、关机、远端接管（`snapshotActiveLocalBeforeRemote`）、换会话（rebind）等路径的同步快照**全部保留**——只动 tab 点击这一处，即调研报告低风险节指定的范围。

### 1.4 为什么数据安全

- 切换时刻的快照绝大多数是 no-op（`snapshotUpToDate` 快路径，调研 2.1-2：locked_total 多数为 0ms）——真正变化的落盘由每 turn 末的 autosave 负责。跳过一次被占锁的 no-op 快照不丢任何字节。
- 有未落盘内容（turn 进行中）时，降级路径的 `tabSnapshotLoop` 会阻塞等锁并在锁释放后写出；append-only 语义下晚几秒落盘无损（调研 3.1-1 同结论）。
- 非阻塞分支不碰 rewrite/检查点/冲突恢复，不产生新的写形态。

## 2. 测试（新增 9 个，全部通过）

| 层 | 文件 | 测试 | 断言 |
|---|---|---|---|
| agent | `save_if_path_free_test.go` | `TestSaveSnapshotIfPathFreeUncontended` | 锁空闲=完整落盘、重载内容一致；二次调用仍 (true, nil) |
| | | `TestSaveSnapshotIfPathFreeBusyDeclinesWithoutWriting` | **占锁 2s 内返回 (false, nil)**、零字节落盘；释放后同会话正常保存 |
| | | `TestTryWithSessionSaveLocksDeclinesWhenBusy` | 权威闸门（try-lock 本身）拒绝且保存体不执行；释放后放行 |
| | | `TestSavePathLockFreeReflectsHeldMutex` | advisory 探针三态（未锁/持锁/释放后） |
| control | `controller_snapshot_if_path_free_test.go` | `TestSnapshotIfSavePathFreePersistsWhenUncontended` | 空闲路径快照完整落盘 |
| | | `TestSnapshotIfSavePathFreeDeclinesWhenSavePathHeld` | **真实持锁下 2s 内 (false, nil)**、文件字节不变；释放后欠账刷新落盘 |
| desktop | `tab_selection_lock_test.go` | `TestSetActiveTabReturnsWhileSavePathBusy` | **占锁下 SetActiveTab 3s 内返回且切换成功**；后台刷新被排上（Snapshot 在后台 goroutine 阻塞证明不在调用栈内）；放行后 `waitForFile` 证明数据最终落盘 |
| | | `TestSetActiveTabSnapshotsInlineWhenPathFree` | 空闲路径内联完成、无后台重复刷新 |
| | | `TestSetActiveTabLegacyControllerStillSnapshotsInline` | 无 try-lock 能力的 controller 保持原同步快照 |

既有回归：`TestSetActiveTabBlocksWhenCurrentSessionCannotPersist`（快照报错中止切换）原样通过；desktop 内 `blockingSnapshotCtrl` 等既有 fake 无一经过 `SetActiveTab`，语义不受影响。

## 3. 验证记录

| 项 | 结果 |
|---|---|
| `go build ./...`（主模块 + desktop 子模块） | 通过 |
| `go vet`（agent / control / desktop） | 通过 |
| 新增测试（三层，`-count=1`） | 9/9 通过 |
| `go test ./internal/control -count=1 -timeout 30m -v` | 866 中 864 过；2 个墙钟敏感测试（`TestOrdinaryChatTurnRunsPastTheOldRoundCeiling` / `TestRunShell_EmitsEvents`）在多套件并行抢 CPU 时失败，**单跑通过**（102s / 2.7s） |
| `go test ./internal/agent -count=1 -timeout 25m -v` | 1282 过 + 第 4 节预存红（3 断言失败 + 2 挂死；skip 挂死仍在另一挂死点超时，均基线复现） |
| `go test .`（desktop 全量） | 与零改动基线同模式：879 过后在 `TestImageInputSaveDefersAllTabsUntilNextTurn` 起跑后进程静默终止（分支 602s / 基线 571s，同测试同形态，无任何 `--- FAIL` 行）；该测试单跑 15s 通过。判定预存，非本切片引入 |
| 收尾 `git status` | 干净（仅本切片 5 改 + 3 新测试 + 1 报告，全部显式 add 提交） |

## 4. 预存红（基线复现证据，非本切片引入）

工作分支与**零改动基线**（`git stash -u` 后的 main-v2-stable @ 0849f7d46，以及临时 detached worktree @ 0849f7d46）上，以下问题**完全一致复现**：

1. `internal/agent` torn-line 重放家族挂死（基线各单独复现）：
   - `TestAppendForShutdownWithoutLockAfterTornTail`（30s/90s 超时转储一致）：`AppendForShutdownWithoutLock → appendUnlockedLocked → replayFrom → resumePastTornLine → replayFrom` 链上对同一互斥锁重入 `Lock`，自锁死锁；
   - `TestReplaySkipsTornLineBetweenEntries`（分支套件内曾跑 22m5s 未归，基线 60s 超时复现）。
   两者全在 DAG 重放代码（本切片未触碰），并使 `go test ./internal/agent` 全量无法在 25m 内完成。
2. `internal/agent` `TestWithContextBudgetPrefixesAndSkips`（"budget block missing from turn"）、`TestReadOnlyWanderingTripsTheProgressGuard` / `TestRepeatedReadTripsTheProgressGuard`（41 rounds 未触发 guard）在分支与基线上同样孤立复现失败。
3. `internal/control` `TestOrdinaryChatTurnRunsPastTheOldRoundCeiling`（147s 超总预算）与 `TestRunShell_EmitsEvents`（5s 内未等到 TurnDone）仅在多套件并行时偶发，单跑稳定通过（102.3s / 2.7s）——记录为负载敏感，不判预存红。
4. `desktop` 全量套件在 `TestImageInputSaveDefersAllTabsUntilNextTurn` 起跑后进程静默终止（无 panic、无 `--- FAIL`，前面 879 个测试全过）：分支 602.7s / 基线 571.3s，**同一测试、同一形态**；该测试单跑 15.5s 通过。疑似套件位置相关的进程级问题（os.Exit / CGO 崩溃类），本切片未触碰其路径。

处理建议：1/2/4 建议另开任务修（挂死测试拖累 agent 包测试时长，desktop 静默终止遮蔽套件尾部结果）；3 可在套件串行化或放宽预算后复评。按「分支上预存红须在 handoff 写明」纪律在此登记，P14 不越界修。

## 5. 兼容性说明（P7 stop 升级链 / P9 工具间隙注入）

- 本切片对 `Snapshot()`、`SaveSnapshot`、保存临界区、stop 链、turn 准入**零语义改动**：`snapshotWithDurability` 既有调用方只加了显式 `nonBlocking=false`；非阻塞分支是新增的独立入口。
- `SetSessionPath`/rebind/关闭/关机路径未动；`scheduleTabSnapshot` 是 autosave 同一套机制，P9 注入若依赖 turn 末落盘，降级刷新与其共用单飞循环，互不抢写（`c.snapshotMu` 仍串行化同 controller 的所有快照）。

## 6. 复现命令

```bash
cd github-repo/worktrees/wt-zcode-p14
go test ./internal/agent -run 'TestSaveSnapshotIfPathFree|TestTryWithSessionSaveLocks|TestSavePathLockFree' -v -count=1
go test ./internal/control -run 'TestSnapshotIfSavePathFree' -v -count=1
cd desktop && go test . -run 'TestSetActiveTabReturnsWhileSavePathBusy|TestSetActiveTabSnapshotsInlineWhenPathFree|TestSetActiveTabLegacyControllerStillSnapshotsInline' -v -count=1
```
