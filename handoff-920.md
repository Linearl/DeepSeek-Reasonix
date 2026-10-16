# handoff-920 —— 压缩单飞化：压缩触发窗口内新轮排队，治「压前反升/越压越大」家族（912 续作）wt-920

- 日期：2026-10-15
- worktree：`github-repo/worktrees/wt-920`（分支 `wt-920`，基 `main-v2-stable`，前任零 commit 零 dirty）
- 任务书：任务 920（P1，执行引擎与上下文；批 34）
- 语义权威：20260926 定论「在途注入窗口」（`docs/report/诊断报告/上下文溢出复发-549-根因-20261006.md` §三、`513-压缩后仍超窗诊断-20261006.md` 同族③）+ 912 handoff（`docs/handoff/handoff-912.md`，失败韧性已交付，本件管「成功仍膨胀」）
- 纪律：切片 commit、显式路径 add、禁 stash/merge/rebase、status 净；中文 message「920 」开头；源码改动走 Edit/Write；前端改动禁 wails generate

## 一、勘察结论（缺陷机理）

### 1.1 压缩触发点全景（全部落在 `ContextManager.Prepare` 单飞 RunMu 上）

| 触发方 | 路径 | 有无 admission 门护 |
|---|---|---|
| 每轮采样 preflight（pressure） | `sampling_request.go:127` → Prepare | ✅ turn 内 `c.running=true` 本来就关闸 |
| overflow 恢复 | `sampling_request.go:98`、`context_recovery.go:76` | ✅ 同上 |
| 后台投影重建（549/813） | `preflight.go:218` kickProjectionRebuild goroutine（≤4 轮 × 5min 超时） | ❌ **无 running turn，admission 全开** |
| 手动 `/compact` | `controller.go:3973` Compact → beginRotation | ✅ rotating 门（drop+resend 让路语义） |
| guardian 周期 CompactNow | `guardian.go:281`（独立 agent/session） | 不经本 controller admission，出界 |
| cold-cache 压缩（297） | 实验开关，走 Prepare | ❌ 同后台重建，无门 |
| 显式 compress 工具 | `compact_projection.go:236` compressVisibleRange | ✅ turn 内 |

长窗口在 `foldContext`（`context_manager.go:250`）→ `compactToProjectionLocked`（`compact_projection.go:924`）→ `summarizeFold`——chunked 摘要实测 ~9 分钟（867）。

### 1.2 洞（在途注入窗口的 admission 半）

**无 running turn 的压缩窗口（后台重建/cold-cache）期间，`admitGuardedTurn`（`admission_guard.go:156`）的 closed/draining/rotating/running/finishing 五门全部放行** → 新用户轮立即 admit → 用户消息在摘要在途时注入 canonical：

1. 注入落在「snapshot（`compact_projection.go:926`）→ install CAS（`maintenance_commit.go:62`）」之间 → `errCompressStaleContext` → **整趟摘要作废**（9 分钟 + 全量 cache miss 白烧），`summaryFailed` 记 blocked 本代不再重试 → 视图维持全量 → 下轮再胀；
2. 注入落在 install 之后 → 新内容不在本次折叠里，压完立刻回抬（「压完又胀」）。

steer 半已天然排队（`steerQueue`，`consumeSteer` 在 run loop 工具轮间隙消费——压缩在途时 run loop 阻塞在摘要上，steer 只会入队不会注入）；**洞只在 turn admission**。

### 1.3 既有排队语义（验收③ 对齐基准）

- 581：用户手发提交在 running/finishing 门上 `runGuardedOrPark` 入 `parkedTurns` FIFO，`finishGuardedTurn`（`controller.go:1457`）在关闸临界区内 drain 最老一个；parking-behind-running 附带 interrupt 请求（Claude Code 式让位）。
- 579：inbox durable 项不进 volatile 车道（`runGuardedInbox`），admission 被拒 → SetState 回 queued → `maybeDispatchInbox` 电平重踢。
- steer：mid-turn 注入队列，turn 间隙消费；不适用本门（已有语义）。
- rotating（手动 /compact/换会话）：drop+「please resend」——**保留不动**（换会话时跨 session 排队不安全）。

## 二、单飞门设计（S2 骨架）

### 2.1 信号源：`Agent.CompactionInFlight()`

读 `compactionLive` meter（`compact_live.go`）的 `snapshot().ok`——**恰好 = CompactionStarted→Done 真实压缩窗口**（两处 begin：`compact_projection.go:263`、`:948`；defer stop 覆盖全部出口）。不选 `compactionRunMu.TryLock` 的理由：

- RunMu 每轮采样 fast-path 也持有（µs 级），TryLock 会误报；
- 更要命的 ABBA：CompactionDone 事件在**持有 RunMu 时**经 sink 发出 → 若 controller 持 `c.mu` 再 TryLock RunMu，与 sink 侧路径构成锁序反向。meter.mu 自封闭（emit 在 mutex 释放后），持 c.mu 读 meter 安全。

### 2.2 admission 门（`admitGuardedTurn`，插在 selfHeal 之后、running 检查之前）

```
compacting := c.executor != nil && c.executor.CompactionInFlight()
```

1. `compacting` 时：park-capable 路径（parkWhileRunning=true，即用户手发族）→ 入 `parkedTurns`，**不请求 interrupt**（让压缩落地），`interruptDeferred=true`；非 park 路径（runGuarded 合成轮/runGuardedInbox）→ 复用 `turnDroppedRunning` 结果（不新增枚举，保持各 switch 不动）。
2. 门开但 `parkedTurns>0`（新态：running=false 而队列非空）→ 当前 body 追加队尾，drain 最老一个——**FIFO 自愈**，任何后来 submit 顺带补漏（也兜 Done 事件万一丢失）。
3. running=true 且 compacting → 同样 park 且压制 interrupt（原来会 cancel 掉在途摘要 = 白烧）。

### 2.3 放行（`onCompactionSettled`）

controller sink 链观察 `event.CompactionDone`（succeeded/failed/aborted 三出口都发，`context_receipt.go:181-201`）→ `onCompactionSettled()`：

- `interruptDeferred && running` → 补发 interrupt（压缩落地后才让位）；
- `!running && !finishing && !rotating && parked>0 && !CompactionInFlight()`（防另一个 pass 已起）→ drain 最老（同 `finishGuardedTurn` 临界区模式）；
- `parked==0` → `maybeDispatchInbox()`（579 durable 项重踢，对齐 endRotation 语义）。

`finishGuardedTurn` 的 drain 加一条：`CompactionInFlight()` 时**不 spawn**（留队，等 settle drain）。

### 2.4 验收映射

- ① 压缩 in-flight 提交不二次膨胀：后台重建压缩在途 + Submit → parked，压完才 admit（测试钉死：压缩 in-flight 时提交新轮→排队，压完才执行）。
- ② 正常轮零劣化：无压缩时 `CompactionInFlight()=false`，admission 路径只多一次 atomic/mutex 读，无等待。
- ③ 语义一致：复用 parkedTurns FIFO + 579 电平重踢 + steer 不动。

### 2.5 已知残留（记录在案，不扩大战线）

- snapshot→Started 发出间的 µs 级窗口仍可注入（无网络请求，CAS 兜底作废重试）；
- 压缩失败/超时（912 阶梯耗尽、rebuild 5min/round ctx）都会走 Done 事件 → settle 放行，不死锁（S3 测试钉死）；
- 手动 `/compact` 保持 rotating drop+resend（让路语义），不改排队。

## 三、切片

1. **S1**（本 commit）：勘察 + 设计 handoff。
2. **S2**：`Agent.CompactionInFlight()` + admission 门（park 不 interrupt、FIFO 自愈）+ `onCompactionSettled`（sink 包装观察 Done、finishGuardedTurn drain 补检）+ 测试：
   - `TestTask920SubmitParksDuringBackgroundCompaction`（①：阻塞摘要 provider，压缩 in-flight 提交→不 admit；放行→落盘执行）；
   - `TestTask920NoCompactionNoDelay`（②：无压缩提交即时 admit）；
   - `TestTask920FIFOAcrossCompaction`（③：两个 parked 按序 drain）。
3. **S3**：边界——压缩失败/中止/超时后 settle 放行不死锁 + running 期间压缩的 interrupt 延迟补发，测试：
   - `TestTask920FailedCompactionReleasesParked`；
   - `TestTask920InterruptDeferredUntilCompactionSettles`。

## 四、验收对照

（S2/S3 完成后逐条填写）

## 五、Commits

| commit | 内容 |
|---|---|
| （S1） | 勘察 + 设计 handoff |
