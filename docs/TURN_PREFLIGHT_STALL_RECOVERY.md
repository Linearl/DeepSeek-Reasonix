# Turn Preflight Stall Recovery (tl-1213)

## 现象与根因

2026-10-28 实测：turn 永久挂 running 约 40 分钟。工具参数生成层把 bash 的
`command` 字段拆成两个同名 parameter（第二个为空串），JSON 解析取后值 ⇒
bash preflight 校验 `command is required` 拒绝（`state=not_run`，命令根本没
执行）。此后 turn 不终结、不报用户、steer/followup 全部注入不进——无界等待
= 会话失联，唯一解是用户手动停 turn。

关键区分：**preflight 失败本身不是病**——它把错误结果回灌给模型，模型重试
换路即可自愈（历史常态）。病的是「失败之后 turn 生命周期失去了所有出路」：
无论后续卡在哪个环节（provider 流、锁、投递），turn 都没有超时、没有降级、
没有终态。本件修的是 turn 生命周期这层兜底，不修生成层坏参数（同名重复字
段拒绝时机前移属另一同族问题，未在本件范围）。

## 修复设计（三层）

### ① 看门自动终止（核心，`internal/control/turn_liveness.go`）

控制器已有的 stall 看门（`warnIfTurnStalled`，autosave tick 驱动，10 分钟
静默 WARN）只警告不终结。本件在其上加自动终止：

- **preflight 失败标记**：`turnLiveness.observePreflightMarker` 从 ToolResult
  事件识别 `Execution.FailurePhase == "preflight" && State == "not_run"` 且
  有错误文本的调用，置标记。任何「失败之后的新工作」清标记：新的非 partial
  ToolDispatch、兄弟调用的 ToolProgress、下一个（非 preflight 失败的）
  ToolResult、prompt/steer。**StreamAttempt / Retrying 帧不清标记**——那是
  provider 侧空转，「preflight 失败持续而别无他事」正是要抓的卡死形态；
- **终止条件**（全部同时满足）：running、非 canceling、非压缩中（任务1147：
  执行器在跑摘要器时绝不强制解缠）、标记在、全事件静默 ≥ 阈值；
- **终止动作**：复用用户 Stop 的同一条 cancel 链（`cancelTurnLocked` +
  `finishCancel`），先发用户可见 Warn notice（code
  `turn_preflight_stall_abort`），所以 A4 语义与用户停止共享同一机制。

**N = 15 分钟**（`turnPreflightStallAbortThreshold`，atomic var，测试可缩）：
- 任何活动（流增量/重试帧/工具进度）都会刷新静默钟——provider 还在循环就
  永远不「静默」，阈值只审判什么都不产的 turn；
- provider 层自己的 ResponseHeaderTimeout = 300s（三家 provider 一致），
  15 分钟 = 3× 该守卫，到点时说明 provider 侧恢复机器自身也卡死了，而不是
  慢；
- WARN 在 10 分钟先响（既有阈值），给用户留 5 分钟手动停止窗口（A4）；
- 实测事故挂了 40 分钟，15 分钟把最坏失联窗口砍到约 1/3。

### ② abort 终态（`internal/control/controller.go` finishGuardedTurn）

看门经 cancel 链收束，Run 返回 context.Canceled，默认落 `interrupted`
（像用户停的）——不诚实。`takePreflightStallAbort`（按 turn generation 绑
定）把终态改写为 `TurnPreflightStallAbortError`（不 wrap context.Canceled）
+ `Cancelled=false` ⇒ ledger 终态 = **failed（带原因）**，不是静默
finish，不是永久 running，也不冒充用户中断。代数不匹配（任务303 自愈换轮）
则丢弃标记不改写。

已知边界：synchronous turn 路径（headless/serve 阻塞传输）没有 completion
对象，走不到终态改写——同样会终止并收到 notice，只是 ledger 记
interrupted 而非 failed。desktop 流不受影响。

### ③ 积压消息不丢（`internal/control/inbox.go` onInboxTurnDone）

投递泵既有语义：mid-turn 被拒的 steer 回置 `queued + intent=followup`，由
泵在下一轮派单时开轮注入。abort 收束时，turn 的 steer 队列里可能还有已接
收未消费的积压（`steer_accepted`，loader 未 fire ⇒ 正文从未进过模型上下文
）。看门在 cancel 前快照这批 item（`armSteerRequeue`，按代数绑定），
`onInboxTurnDone` 对它们**回置 queued+followup 而不是 ack 出队**——下一轮
泵开轮注入，unreadInbox 可清零，消息不随卡死 turn 陪葬。回置失败则退回既
有 ack 语义（不比现状差）。

注：正常收束/用户手动停的 steer 处置**不变**（`flushSteerQueue` 的
unapplied 留痕契约，#7045 防陈旧指引重放）——只有看门 abort 路径回置。

## 恢复路径（用户视角）

1. turn 卡死后 ≤10 分钟：收到「No progress…」警告（既有）；
2. 10–15 分钟内用户可点 Stop 立即终止（A4，与从前一致）；
3. 15 分钟仍零事件且最后一步是 preflight 失败：看门自动 abort，用户看到
   `turn_preflight_stall_abort` 警告，turn 落 failed 终态；
4. 收束时未消费的 steer/followup 自动回置队列，泵随即开轮执行——无需人工
   补发；
5. 模型看到 preflight 错误结果（一直在 transcript 里），重发/续轮即正常
   重试。**没有任何数据损坏**：not_run = 命令从未执行。

## 测试

`internal/control/turn_preflight_stall_test.go`（7 例）：
- A1+A2 端到端：`TestPreflightStallWatchdogAbortsTurnTerminalFailed`
- A5 面：`TestPreflightStallWithoutMarkerKeepsWarnOnly`、
  `TestPreflightMarkerClearedByLaterWork`
- A4：`TestUserStopDuringWatchdogWaitStaysInterrupted`
- A3：`TestStallAbortRequeuesUnconsumedSteer` +
  对照 `TestNormalTurnDoneStillAcksAcceptedSteer`
- 单元：`TestTakePreflightStallAbortGenerationGuard`、
  `TestPreflightStallAbortDeferredWhileCompacting`
