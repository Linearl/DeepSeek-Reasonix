# 886 handoff（中断协议）

分支：wt-886，基线 main-v2-stable @ 1747f222d（status 净，尚无 commit）。
日期：2026-10-14。

## 一、884 甄别（已完成）

- `0394c9649`（wt-884）是 **1747f222d 的直接子提交**（FF 关系），`git format-patch -1 0394c9649 --stdout | git apply --check` 在本分支**干净通过**；wt-884 内 `go test ./internal/agent -run TestDAGLogOversizedUnionDedupsSharedPrefix` **ok**（5.2s）。
- 覆盖面：仅改 `internal/agent/save.go`（sessionMessagesEncodedSize）+ `session_dag_rotate.go`（sessionDAGLogOversized 的 live 估算 Σ整链→union 去重）+ 新测试。**只覆盖 events 旋转门（238MB 面）**。
- 不覆盖：①jsonl 86.5MB 本体（canonical storage keeps every byte，compact 只写投影）②注入链（steer 准入/空闲开轮桥/关 tab 后 runtime 拆除）。
- ⇒ 按任务书转 focus：jsonl 本体 + 注入链 + 体量哨兵。884 不入本分支（wt-884 独立交付，避免重复 patch）。

## 二、新6 不可注入直接机制（日志+代码实证）

现场：`projects/C--...-global-workspace/sessions/20261010-023554.643066900-collab.{jsonl=86.5MB, events.jsonl=238MB}`，turns=514 revision=8326。
日志：`%APPDATA%/reasonix/logs/desktop/desktop.log.4`（覆盖 08:39–13:43）。

时间线（2026-10-14）：

1. **12:14:32 强退重启**（launcher: desktop exited abnormally，对应 885 锁等待 791→11973ms 现场）。
2. 重启后 12:14:49–12:15:20 同一联系人连续 8 次 `opened session ... background=true`（stand-up park deferred×8 → 12:15:18 才 park 成 detached）。
3. **12:16:11 空闲桥开轮成功** `decision=opened`（idle 51s ≥ N=45s，先解 709 自动暂停），12:16:15 `skip:running`，**12:21:14 turn completed（消费队首 2e4f7df0）**——证明 runloop/桥在重启后是活的。
4. **12:22:46 新信 msg_e05deed 入队 c7e8cf49 → 12:22:47 steer 降级 queued_followup**（此刻无活跃 turn，SteerRunActive=false；这是**设计内降级**，非故障）。
5. 故障面在降级之后：**12:23:04 / 12:24:33 / 13:05:13 / 13:06:45 反复出现同一形状**——
   - `desktop: frontend submit degraded to guidance queue ... disposition=queued_followup`（turn_submission_app 的 461-P6 路径：Submit 返回 **ErrTurnRunning** 才会走此分支）；
   - 紧随 ~100ms `inbox merge: drained group as one item carrier=c7e8cf49 members=2`（dispatchInboxOnce 已过 busy 门、NextQueued+merge 成功）；
   - **但 transcript 零增长**：`session: save begin messages=6454` 恒定、12:21 后无 mode!=0 保存、turns.jsonl 仅 1 条 checkpoint（lastStatus=completed, transcriptRevision=416 ≪ meta revision 8326）。
6. 全窗口 **0 条** `controller: dispatch inbox item` / `inbox dispatch hook` / `runtime unpublished` WARN/ERROR。
7. 13:22:01 用户关 tab（dag cache freed=238215768 = events 文件大小），13:22:20 inbox manifest `paused=true`，此后该联系人在全部日志中消失（无 sweep 行、无 delivered 行）。

**直接机制定性（代码+日志交叉）**：

- 同一时刻（12:23:04.540 用户 Submit 得 ErrTurnRunning vs 12:23:04.639 dispatch 通过 busy 门走到 merge）证明 **controller 侧 c.running/c.finishing/c.rotating/c.closed 全 false，唯一还能抛 ErrTurnRunning 的是 `tab.sink.tryBeginTurn()` 返回 false ⇒ `tabEventSink.turn.inFlight` 残留为 true**（tabs.go:1645 tryBeginTurn / :1596 仅 TurnDone 清理 / :1656 cancelTurnStart）。
- dispatch 侧同因：TrySubmitInboxItem → beforeInboxDispatch → beginRuntimeTurn → 同一 tryBeginTurn false → ErrTurnRunning → `dispatchInboxOnce` **按设计静默 idle**（inbox_dispatch.go:168 注释 "staying silent here is by design"，零 WARN）→ 队首永远不被认领，4 次 drain 全部空转。
- inFlight 泄漏窗口（推断，次级成因）：12:16:11 桥 turn 运行中 **12:17:43 用户 open-topic rebind**（日志 session binding apply×16、open-topic seq=3），`admission.finish(ctrl)` 在 `RuntimeStatus().Running==true` 时**跳过 cancelTurnStart**（turn_admission.go finish: started → 不清 inFlight，指望 TurnDone），epoch/rebind 窗口内 TurnDone 未回到该 sink ⇒ 残留。**待独立定案**（需 runtimeEpoch 路由取证）。
- 结构性放大器：**空闲桥对可见 tab 全盲**——`sessionCollabTargets()`（session_collab.go:1905）构造 target **不带 ctrl 字段**，`sweepIdleInboxTurns` 对 `t.ctrl == nil` **静默 continue**（session_collab_idle_turn.go:548，不打日志）⇒ 关键窗口（12:17:43 reattach 后）桥完全看不见该会话。佐证：全量生产日志 `decision=skip:active_tab` **0 条**（该分支不可达）；本会话 12:16:15 后再无任何 sweep 行。796 的 finishing 自愈也因此不可达。
- 与 885/889 的关系：885 的 save 锁等待是**强退诱因**（前置），非注入链阻塞点；重启后锁等待回到 ms 级。889 effort 超时同窗拥堵，非本因。
- **不是**：runloop 死（12:16 桥 turn 正常跑完 5min）、park 卡（park deferred 属 738 正常有界等待）、save 锁永久持有（重启后 save phases lock_wait≤5ms）。

## 三、体量哨兵方案（待写码）

落点：`desktop/perf_monitor.go`（已有 warnGate 限频 + eventsMb 水位/增长率告警，但都是**全库总量**，单会话 jsonl 无任何预警——新6 jsonl 86.5MB 全程静默）。

1. `topTranscriptFileUnder(memoryRoot)`：镜像 `topEventsFileUnder`（.trash 跳过、*-recovery-* 排除、`store.IsSessionTranscriptName` 分类），同一次 walk 同时返回 top transcript（path,bytes）与 top events（已有函数）。
2. 新阈值常量（带 886 现场依据注释）：
   - `perfMonitorWarnSessionTranscriptMB = 64`（新6=86.5MB，正常会话 ≤26MB）
   - `perfMonitorWarnSessionEventsFileMB = 100`（884 实测健康 union ≤102MB、cap 50MiB×2）
3. `warnThresholdsAt` 增加两项检查，复用 `warnGate.allow`（metric 名 `sessionTranscriptMb` / `sessionEventsFileMb` 独立记账），WARN 字段含 `topPath`/`topMB`/`limit` + **remedy=建议转线/归档（转录 canonical 不可缩，唯一出路是转新会话）**。
4. 扫描仅在告警判断需要时走（或并入 collectTotals 已有 walk，零额外遍历——优先并入）。
5. 单测：`desktop/perf_monitor_session_size_test.go`
   - 阈值下不告警 / 阈值上触发且字段含 topPath+remedy；
   - .trash 与 *-recovery-* 不计入（沿用 walk 测试夹具风格）；
   - 同 metric 15min 内重复超限被 warnGate 抑制（suppressed 计数）。

## 四、剩余工序

1. 哨兵写码 + 单测（本文件方案）。
2. 包级测试：`go test ./desktop -run 'PerfMonitor|Walk|Warn' -count=1` + collab/注入族（session_collab/inbox/turn_admission 定向）+ `go build ./... && go vet ./desktop`。
3. 预存红对照申报（884 申报的 agent 两条红在 wt-884 已复现过，本分支跑不到 internal/agent 改动则不需要）。
4. 切片 commit：中文 message 以「886 」开头，显式路径 add（desktop/perf_monitor.go + 新测试文件 + 本 handoff 可不提交）。
5. 汇报：①状态 ②commit 列表 ③验收逐条 ④残留风险（机制修复=inFlight 泄漏+桥 ctrl 盲区，属后续任务，不在 886 验收内；886 验收①是「定性」，已完成如上）。

## 五、环境备忘

- grep 一律 `git grep`（Windows FS 慢）；大文件用 Read 带 offset。
- 日志在 `%APPDATA%\reasonix\logs\desktop\desktop.log*`（按时间倒序轮转，log.4 覆盖 10-14 08:39–13:43）。
- 会话实体在 `%APPDATA%\reasonix\projects\C--Users-yinji-AppData-Roaming-reasonix-global-workspace\sessions\`。
