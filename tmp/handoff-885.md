# 任务 885 交接（wt-885）—— 跨会话 save 锁互等恶化：锁粒度定性 + 等待有界根治

生成：2026-10-14/15 | 分支 wt-885（基 main-v2-stable tip 73ff458bd）| 状态：代码+测试已落地，待全量包测绿后分片 commit

## 一句话结论

「两会话 save 互等锁」是**观测错觉**：锁键是 canonical 会话文件路径，每文件一把，两会话**不共享**锁键（并发测试实锤）；现场791ms→11973ms 的单调恶化是**同键排队**——同一会话的多个 saver/loader 排在同一把 mutex 后面，持有者做 O(文件) 重 IO（全量 load / digest+rewrite），队列随文件增长恶化。根治：阻塞 save 路径的 in-process 锁等待加上界（sessionSavePathLockWait=30s），超时返回 ErrSessionFileLockHeld 走既有降级/恢复机制。

## 机制定性（代码级证据）

1. **锁键 = 每会话文件一把**：`internal/agent/save.go:1456 lockSessionSavePath` → `sessionSaveLocks.LoadOrStore(canonicalSessionSavePath(path))`；键来自 `pathidentity.Canonical`（clean+abs+物理 resolve+Windows 小写，`internal/pathidentity/path.go:12`）。不同文件 → 不同键。另有 per-path 跨进程 `store.SessionLockFile`（`<path>.lock`，save.go:1503 `lockSessionFile`，5s bounded）与 per-path meta 锁。**无任何跨会话共享锁键**。
2. **粒度 = 一把 in-process 排他锁罩住整个 save 临界区**：`withSessionSaveLocks`（save.go:282）持 path mutex → 跨进程 .lock → `saveLocked`（digest 全量、classify 全量 loadSessionUnlocked、事件日志 rewrite、display index 重建、meta 写）。同一把锁也被 `LoadSession/LoadSessionTail`（全量 replay）、`LoadSessionDisplayMessages`（history 冷读）、listing projection、head ops 复用——读写不分，是「同键排队」的根源。
3. **恶化曲线成因**：桌面日志 desktop.log.4 实证——`save phases locked_total_ms` 4482~8321ms（023554，238MB events / 6434 msgs）；11973ms 等待窗口（12:14:09.5→12:14:21.5）内**无任何 save begin/end**，说明持锁者是非 save 的静默操作（LoadSession 家族全量 replay 或 display 冷读）。同键多个排队者 → 等待 = 前面所有临界区之和 → 文件越大临界区越长 → 等待单调恶化。两会话相互拖慢的通道是磁盘带宽与 canonical walk（250ms 预算被拖爆，883 已证），不是锁键。
4. **无跨路径嵌套**：仅 migrate/rename（`save.go:2164`、`migrate_native.go`）会 old→new 两把锁，且同会话有序，无 ABBA 风险。

## 根治落地（本分支改动）

- `internal/agent/save.go`
  - 新增 `sessionSavePathLockWait = 30s`、`sessionSavePathLockPoll = 25ms`（包变量，测试可缩短）。
  - 新增 `lockSessionSavePathWithin(path, budget)`：TryLock 轮询，预算耗尽返回 **wrapped ErrSessionFileLockHeld**（复用既有 sentinel：shutdown 恢复、degraded-read、desktop friendly-error 全部自动生效）+ WARN 日志（wait_ms/budget_ms）。
  - `withSessionSaveLocks`（阻塞 save 路径）改用有界获取；`os.MkdirAll` 移到锁外（快慢分离第一步，IO 出锁）。
  - 读路径（LoadSession 家族、display 读、projection、head ops 除 save 外、migrate、reparent）**保持无界** `lockSessionSavePath`——慢持有者可以拖慢 save 但不能 fail 掉 hydrate/repair。
  - `ErrSessionFileLockHeld` 注释扩展为覆盖 in-process 持锁超时语义。
- `internal/agent/save_dag_shutdown.go`：`AppendForShutdownWithoutLock`（shutdown 逃生舱）同样改有界——避免「file lock 超时后改走无锁 append，却被卡死的 in-process 持有者永久挂住」。卡死时返回错误 → `recoverShutdownSnapshot` 走恢复分支。
- `internal/agent/save_lock_wait_885_test.go`（新增4测试）：
  - `TestCrossSessionSavesDoNotShareLockKey`：A 持锁时 B save <2s 完成（断言 wait 上限，锁键独立实证）。
  - `TestSameSessionSaveWaitChainBounded`：复现同键排队链，释放后完成，wait 有界。
  - `TestBlockingSavePathWaitBounded`：预算内 ErrSessionFileLockHeld 失败、不落盘、释放后恢复成功。
  - `TestConcurrentSessionSavesBothBounded`：两会话并发，A 有界失败、B 不受拖累。

## 预算取值依据

30s > 现场最大健康临界区（023554 ~14s，desktop.log 实测 locked_total_max≈8.3s + 读锁尾部），只有真正卡死的持有者才触发超时；与 task 357 的 sessionFileLockWait=5s（跨进程）同族分层。

## 511/472 先例对照

- **511（收件箱锁挂起）**：拿锁失败不静默 → Warn 留痕 + Degraded 贯穿。本件同构：超时打 WARN（wait_ms/budget_ms）+ 返回 typed error，desktop friendly-error 已映射 ErrSessionFileLockHeld → errSessionBusyElsewhere（session_errors.go:47）。
- **472（set_target 慢）**：无预算无日志的等待 = 根因。本件正补上 in-process 锁等待的预算与日志（此前只有 250ms 的 WARN 提示、无上界）。

## 与883/889 的边界

- 883（canonical 探针）：本件未动 canonical；其「projects 前缀跳过物理 resolve」建议留给 639 根治面。
- 889（App.mu 持锁段 IO）：tabLock:3891ms 是 **desktop a.mu 锁面**，与本件 save 锁面是两把锁，互不混同；889 继续治 a.mu。

## 残留风险 / 未做

1. 读路径仍无界：卡死的持有者依然会 fail 掉 save（30s 后 busy）但不会挂死进程；hydrate/repair 仍会等——保守选择，改读路径超时语义影响面大，未动。
2. 30s 预算下，023554 级别的重会话在极端拥堵下可能出现 save busy 失败→ 下次 autosave 重试；数据不丢（内存 transcript 保留 + 事件日志 WAL），但需观测。
3. `AppendForShutdownWithoutLock` 超时后走 recoverShutdownSnapshot，行为变化已在注释说明。
4. 快慢分离只做了 MkdirAll 一步；classify 全量 loadSessionUnlocked、display index 重建仍在锁内——结构性改造（锁内只做关键段）需另立项。

## 中断恢复

```bash
cd github-repo/worktrees/wt-885
go test ./internal/agent/ -run "TestCrossSessionSavesDoNotShareLockKey|TestSameSessionSaveWaitChainBounded|TestBlockingSavePathWaitBounded|TestConcurrentSessionSavesBothBounded" -count=1
go test ./internal/agent/ ./internal/control/ -count=1 -timeout 20m
```
