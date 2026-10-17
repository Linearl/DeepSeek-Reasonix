# 任务 945 交接（wt-945）

- 日期：2026-10-17
- worktree/分支：`github-repo/worktrees/wt-945`（基线 `main-v2-stable @ dc8c677ec`，含 921 全部交付）
- 任务：884 残留①语义减法——陈旧并发头 retire（921 交付时申报、用户已拍板）

## 一、背景对齐

921（空闲期兜底 sweep）handoff 第五节申报：「陈旧并发头 retire（884 残留 #1，
语义减法需拍板）——sweep 只清已 retire 头的字节，不替用户做 retire 决定」。
本件补齐「判定陈旧 + retire」这半边，与 921 sweep 形成闭环：

- 并发头（`HeadKindConcurrent`）由写者竞争 fork 产生（save_dag_plan.go moveHead /
  save_dag_shutdown.go）；写者死亡后头滞留，其独占字节因「仍可达」永不被 rotation 丢弃；
- `RetireSessionHead`（session_head_ops.go，既有）= append retire marker，头转不可达；
- rotation（buildRotatedSessionDAG）只丢不可达节点 → **retire 后 sweep compact 才能清字节**。

## 二、判定设计（保守：四条 AND，缺任一证据即保留）

`internal/agent/session_head_stale.go`，纯函数 `staleConcurrentHeadDecision`：

1. **kind == concurrent**——main 永不自动 retire；用户 fork/rewind 是显式版本资产，不动；
2. **超龄**：`lastActivity` 距今 ≥ `staleConcurrentHeadMinAge`（**72h = 3×recentHeadWindow**，
   可调项）；
3. **无活跃写者**：writer 已注册 + pid>0 + **hostname == 本机** + `pidalive.Alive(pid)==false`
   ——跨主机/pid 缺失/未注册一律「无法证明死活」→ 保留（pid 复用只会延迟 retire，
   不会误 retire 活写者，pidalive 保守方向）；
4. **被 selected 线取代**：selected head 的 lastActivity 严格晚于该头（用户回到主线继续用过）。

另两道结构闸：selected 头不动（RetireSessionHead 自身也拒）；schema-1/damaged 日志不碰。

**retire 语义**：只 append retire marker（复用 RetireSessionHead 的锁/index/meta 全链），
不删任何消息字节；头从 versions 隐藏，独占字节待下一次 rotation 物理清除。

## 三、与 921 sweep 联动

`desktop/events_idle_sweep.go`：

- tick 采集候选时读 sidecar `HeadCount>1 && LogSchema==2` → `MultiHead` 标记
  （廉价预筛，单头占绝对多数，retire 的 replay 成本只花在多头日志上）；
- `runEventsIdleSweepPass`：将被重写的多头候选**先 retire 再 compact**（顺序即语义：
  先 compact 清不到 retire 之后才不可达的字节）；retire 失败仅 WARN 不阻断 compact
  （没有它就是今天的行为，不升级为新失败面）；park 语义不变；
- 921 WARN remedy 已指向 idle sweep 自动出口，无需再改。

## 四、验收对照

1. ✅ 陈旧头判定测试：`TestStaleConcurrentHeadDecisionMatrix`（11 态）——真陈旧 retire、
   活写者不误伤、main/fork/selected/未超龄/未注册/pid 缺失/跨主机/主机空/未取代 全保留；
   `TestRetireStaleConcurrentHeadsRetiresOnlyStale`（真日志）——hdead retire、
   写者仍活的 hlive 与 main 完好。
2. ✅ 与 921 sweep 联动端到端：`TestRetireStaleConcurrentHeadsThenSweepCompactsAndSessionOpens`
   ——并发头 6MB 独占尾巴，判定 retire（产品行为）→ CompactSessionFile 清字节 →
   `LoadSession` 正常打开、6 条消息末条逐字保留、live heads 只剩 main、generation=2；
   pass 级：`TestEventsIdleSweepPassRetiresStaleHeadsBeforeCompact`（单头不问 retire/
   retire 失败不阻断/park 不变）+ `...RetireRunsBeforeCompactPerSession`（顺序）。
3. ✅ 数据保守性：`TestRetireStaleConcurrentHeadsRetiresOnlyStale` 内断言——retire 后
   日志字节数不缩、旧头用户消息字节仍在文件中（物理删除只发生在后续 rotation，
   且那时头已 retired、用户已在主线）。

## 五、测试与预存红申报（2026-10-17）

- **新增 5 测全绿**：agent `TestStaleConcurrentHeadDecisionMatrix`（11 子测）、
  `TestRetireStaleConcurrentHeadsRetiresOnlyStale`（0.23s）、
  `TestRetireStaleConcurrentHeadsThenSweepCompactsAndSessionOpens`（19.8s，6MB 夹具）；
  desktop `TestEventsIdleSweep` 族（7 存量 + 2 新增）。
- **定向族全绿**（本单直接影响面）：agent
  `TestDAG|TestWarnRotate|TestFold|TestRotate|TestEventsRotation|TestSessionEvent|TestHead|
  TestBranchMeta|TestCompactSessionFile|TestRetireStale|TestStaleConcurrent`（54.9s）；
  desktop `TestEventsIdleSweep|TestSessionEvents|TestEventsRepair`（3.9s）；
  `go build ./...`、`go vet ./internal/agent`、desktop build+vet 净。
- **repolint**：新文件三处注释块曾超位置预算（essay +3），已压缩收口；
  repo total 与基线 `dc8c677ec` **逐字一致**（基线预存 9 项超限：banner/commented-code/
  complexity/essay/file-size/function-size/narrative/struct-state/test-file-size，均非本单引入）。
- agent/desktop 全量未跑（921 已申报同基线 13 条负载敏感预存红+desktop 全量挂死基线）；
  本单改动面（判定纯函数+sweep pass 内联）为定向族直接覆盖。

## 六、commit 列表

1. `5666d548b` agent 侧判定+retire（session_head_stale.go + 11 态矩阵/真日志/端到端 3 测）
2. `630e93967` 与 921 sweep 联动（HeadCount 预筛→retire→compact 顺序，2 新测+9 存量调用点）
3. `5275604a3` repolint 注释收口（repo total 回基线逐字一致）

## 七、残留与可调项

- **判定阈值**：`staleConcurrentHeadMinAge = 72h`（session_head_stale.go 顶部常量）——
  现为固定值，未接配置面板；若现场发现误判节奏问题可改此处。
- **触发面**：retire 只发生在 sweep 将要重写的多头候选上（auto+cap 门内，333 设计不越权）。
  manual/off 模式与 under-limit 多头会话的陈旧头**不会**被自动 retire（其独占字节也不构成
  超限压力）；需要时走版本 UI 手动 retire。
- **跨主机写者**：hostname != 本机一律保留（无法验证 pid 死活）——单机场景不受影响；
  多机同步场景若出现跨机陈旧头，需另立判定（如 lease 过期链）。
- **未接 WARN remedy**：fold-no-shrink WARN 的 remedy 已由 921 指向 idle sweep，本单语义
  （retire 清尾巴）未在文案中单独体现——如需可在后续批次补一句。
