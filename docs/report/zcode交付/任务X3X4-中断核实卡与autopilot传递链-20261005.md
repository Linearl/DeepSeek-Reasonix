# 任务 X3/X4 交付报告：中断核实卡状态机 + autopilot 传递链

- 分支：`wt-zcode-x3`（基线 `main-v2-stable@64b6fdebf`，含 P16/P7/435/450）
- 日期：2026-10-05
- 提交：`262a46776`（X3）、`f5c254c7f`（X4）
- 未 push、未 merge、未出包、未动 tasklist/docs/collab（本报告按交付纪律落在 worktree 内 `docs/report/zcode交付/`）

---

## 一、X4 核查结论：autopilot 传递链与丢失点

### 1.1 完整传递链（逐跳）

| 跳 | 路径 | 证据（file:line 均为修复前基线） | 结论 |
|---|---|---|---|
| 1 | 设置面板 → `SetDesktopAutopilot` → `cfg.Desktop.Autopilot`（config 持久化） | `desktop/settings_app.go:2867-2877` | 正常 |
| 2 | Composer 菜单 → `SetCollaborationModeForTab("autopilot")` → `tab.autopilot=true` | `desktop/app.go:2269-2293`；前置门：偏好关→静默回退 normal；审批≠yolo→拒绝+一次性 notice（325 门） | tab 元数据层正常 |
| 3 | `tab.autopilot` → live controller `c.autopilot` | **只在 `boot.Build` 时传入**（`internal/agent/agent.go:1396` 消费 `opts.Autopilot`）；agent 层无任何运行时 setter | **断点 A** |
| 4 | `tab.autopilot` → 前端视图 | `desktop/tabs.go` `collaborationMode()` 只产 plan/goal/normal；`Meta`（`desktop/app.go:7073`）无 autopilot 字段；前端 `normalizeCollaborationMode`（`types.ts:1180`）把 "autopilot" 剥成 normal | **断点 B（双半）** |
| 5 | `tab.autopilot` → tab 记录持久化 | `desktop/tabs_persistence_types.go:3-22` `desktopTabEntry` 无 autopilot 字段 | **断点 C**（重启丢失，见 1.3） |
| 6 | 提交前 profile 重同步 | `SetComposerProfileForTab`（`desktop/app.go:2172`）不含 autopilot 轴，不能置也不能传 | **断点 D**（不丢但也不补） |

### 1.2 断点 A 的最强形态（本次修复的根因）

`desktop/tabs.go:4052` `buildTabControllerWithContextCore` 的 `boot.Options` 字面量是**唯一**不带 `Autopilot/MaxRuntime/AutopilotApprovalGrace` 的 tab 构建路径——clear-session（app.go:2556）、rebind（4593）、set-model（10352）、set-effort（10615）四处重建都带，唯独**初始构建（新会话 + 重启恢复）不带**。

后果：`c.autopilot` 从进程启动起恒为 false，直到用户换模型/换 effort/清空会话触发重建。在此期间：
- ask 高风险题无 `autopilotAskWait` 超时 → **无限等人工**（`internal/control/controller.go:2907-2929`，`c.autopilot==false` 时 `askTimeout` 为 nil）；
- ask 可逆题不自答（`controller.go:2825` 的 `c.autopilot &&` 短路）。

即用户症状「**autopilot 模式下 ask 阻塞**」的直接根因：用户以为开了 autopilot（偏好开了、tab 旗子也可能置上了），但运行时从未收到。

与 P16 已排除项的关系：P16 排除的是「Yolo/auto 被误并进 unattended 分支」（H2，方向相反）；本核查证明的是**显式开启的 autopilot 没有被应用到运行时**——两个结论不冲突，且互为补充。

### 1.3 断点 C（持久化，本片未修，立项建议）

`desktopTabEntry` 无 autopilot 字段 → `tab.autopilot` 不落 tabs 文件。重启后唯一恢复源是 goal-state sidecar（`tabSessionAutopilot`，`desktop/tabs.go:8453-8466`），条件 `Autopilot==true && Status==running`。**无 running goal 的裸 autopilot tab 重启即丢**。修复建议：`desktopTabEntry` 增列 + restore 回填（小切片，可与断点 A 的验证一起装机观察后再做）。

### 1.4 「确已开启」的可验证判据（修复后可用）

desktop.log 单文件三问三答：
1. **开关落没落**：`desktop: autopilot toggle tab=… preference_on=… approval_mode=… applied=true/false refused_requires_yolo=… max_runtime=…`（app.go:2284，新增）。`applied=false` + `preference_on=false` = 偏好没开；`refused_requires_yolo=true` = 审批不在 yolo 被拒。
2. **构建带没带**：`desktop: runtime build begin … autopilot=true/false`（四处重建 + 初始构建均带，新增字段）。toggle 之后没有任何一条 `autopilot=true` 的 build begin = 运行时还是旧值。
3. **运行时生效没有**：`[ask-panel] ask request emitted` 之后，可逆题被自答（transcript 出现「decide for yourself」型答案）或高风险题超时出现 `autopilot · ask — refused` Notice（controller.go:2920）= 生效；两者皆无且 ask 挂起 = `c.autopilot=false` 实证。

### 1.5 X3 修复方向受 X4 结论的影响

X3 的「三按钮无效」与 X4 的僵尸 turn 同源：ask 阻塞的 turn（无 P7 的旧包里无法终止）使 `c.running` 恒真 → 核实卡 guard 恒拦。故 X3 修复必须在 guard 语义上给出不依赖 turn 终止的出口（见二节的 dismiss 设计），P7 装机后僵尸 turn 消失是另一层缓解。

---

## 二、X3 核查结论与修复：中断核实卡

### 2.1 状态机定位

- 卡片数据源：`Controller.ToolRecoverySnapshot()`（`internal/control/tool_recovery.go:34`）→ `executor.PendingToolRecovery()`（session transcript 中 `unresolvedToolRecord` 的 Recovery 元数据，「bash · 结果尚未确认」= state unknown 的残留记录）。
- 卡片显示/隐藏：`ToolRecoveryPanel` 仅在 `running=false` 时探测（`useEffect`，running 变 true 即清快照隐藏）。
- **`Error: turn already running` 的产生点**：`Controller.ResolveToolRecovery` 的 guard `c.running || c.finishing || c.rotating || c.closed` → `ErrTurnRunning`（`internal/control/tool_recovery.go:86-89` 修复前）。前端事件**有发**、后端**有处理**（guard 拒绝经 Wails reject 透传到面板 error 槽）——路径是通的，被拦的是后端状态门。
- **三按钮点击为何不通**：guard 四态中 `closed` 永不自愈、僵尸 turn 使 `running` 恒真（ask 阻塞 + 旧包无 P7），而面板在前端 `running=false` 时照样可见可点 → 点一次报一次。
- **切走切回为何无效**：切 tab 只触发重探（重读同一批未决记录）；记录只有三条清除路径——(a) 面板 resolve（被 guard 拦）、(b) 新 turn 的 `beginToolRecovery` 自动清算（`resolveSideEffectFreeInterruptedCalls`/`resolveHostVerifiableEffects`，`internal/agent/tool_recovery_records.go:203-233`——bash 只读命令属白名单，这正是「发起新输入开新 turn 卡片消失」的机制）、(c) 435 名册结算（仅计划内重启，真 crash 不适用）。切走切回三条都不走。
- 435/450 已覆盖计划内重启的自动结算；本片补的是**真 crash/强退 + 人工核实路径被卡**的场景，不与之重叠。

### 2.2 修复（commit 262a46776）

1. **guard 分报**（`internal/control/tool_recovery.go`）：closed → "session is closed…"（不再谎报 turn running）；rotating → "session is switching…"；running/finishing → 保留 `ErrTurnRunning`（`fmt.Errorf("%w — stop the running turn or wait…")` 包装，`errors.Is` 兼容，文案给出下一步）。
2. **显式清除入口 dismiss**：
   - agent 层 `ResolveToolRecoveryDismissed`（`internal/agent/tool_recovery_actions.go`）：一步结算（`state=not_started`、`resolution=dismissed_by_user`、`source=user`），无需先检查；沿用 confirm 的回滚铁律（结算检查点写失败即回滚并报错，未落盘的解除不得放行屏障——同 `TestToolRecoveryConfirmationRollsBackOnStorageFailure` 的语义）。
   - control 层新增 `case "dismiss"`，且是**唯一在 turn 运行中放行的动作**：结算只是 Recovery 元数据变更，与 run loop 自己在 turn 内做的 `resolveSideEffectFreeInterruptedCalls` 同型同锁（`setToolRecoveryRecord` 写时复制，并发安全注释在先）；rotating/closed 仍拒（executor 会话可能被换）。
   - 前端 `ToolRecoveryPanel` 加「忽略并不再提示」按钮（`disabled={busy}`，不因 running 禁用——卡死场景恰恰是前端认为空闲而后端 guard 为真）+ zh/zh-TW/en 三语文案。
3. **不做的事**：未改 `ErrTurnRunning` 本身文案（多面共用）；未放开 turn 中 retry（并发执行风险）；未动 435 名册语义。

---

## 三、X4 修复（commit f5c254c7f）

1. **断点 A**：`desktop/tabs.go:4052` 初始构建补传 `Autopilot/MaxRuntime/AutopilotApprovalGrace` 三元组（两处写入点——新会话偏好默认 `app.go:1132-1148`、重启 sidecar 恢复 `app.go:1008-1013`——均已经 325 yolo 门过滤，直传即可）。
2. **断点 B**：后端 `collaborationMode()` 增加 autopilot 分支（order=plan>goal>autopilot>normal；运行中 goal 仍赢，裸开关态如实上报）；前端 `normalizeCollaborationMode` 放行 "autopilot"。
3. **判据锚**：toggle 分支一行 slog + 四处 `runtime build begin` 加 autopilot 字段（判据见 1.4）。
4. **断点 C/D 未在本片修**（1.3 立项建议；D 属设计现状——pre-submit 重同步不丢 autopilot，只是不能置位）。

---

## 四、测试证据

| 套件 | 命令/范围 | 结果 |
|---|---|---|
| 新增 agent 测试 | `go test ./internal/agent -run "TestToolRecoveryDismiss" -count=1` | ok（3 例：一步结算+标签断言/回滚/陈旧报错） |
| 新增 control 测试 | `go test ./internal/control -run "TestResolveToolRecovery" -count=1` | ok（3 例：closed 不误报/rotating 分报/turn 中四拒 dismiss 过+rotating 旗不泄漏） |
| control 包全量 | `go test ./internal/control -count=1` | ok（247s，无回归） |
| agent recovery 广谱 | `-run "Recovery|Interrupted|Fence|SideEffect"` | ok（35s） |
| desktop 相关切片 | `-run "TestAutopilot|TestInitialTabBuild|ToolRecovery|TestGoalDelivery|TestTabMode|TestSetCollaborationMode|TestComposerProfile|CollaborationMode"` | ok（含新 3 例：视图矩阵扩展/goal 让位回归/初始构建三元组源级锚） |
| 前端新测试 | `tsx src/__tests__/x3x4-autopilot-view-and-recovery-dismiss.test.ts` | 全过（normalize 放行+旧三态回归护栏/三语 dismiss 键/面板按钮源级） |
| 前端既有对照 | task326、settings-preapprove-managed（51 passed） | 过 |
| fork integrity | `node scripts/check-fork-integrity.mjs` | **450/450**（443 基线 + 7 新锚） |
| 编译 | 主模块 `go build ./...` + desktop 子模块 `go build ./...` | 过 |

**预存红对照（未触碰，非本次引入）**：
- agent 包全量 600s 超时（P13 torn 族同源）——本次只跑 recovery 广谱切片；
- desktop 主包全量 rebind 族挂起（P16 报告已记录同形态）——本次只跑目标切片；
- serve attachments/projects 族 3 红、agent TestAppendForShutdown 挂死——未触碰 serve/append 路径；
- 前端 tsc 与 task325 测试：worktree 未装 node_modules（pnpm 依赖缺失），tsc 无法运行；task325 因 import react 失败（环境限制非回归）；新前端测试不依赖 react，已过。locale 键齐性由三语同键断言兜底。

---

## 五、遗留与建议

1. **断点 C（tab 记录持久化 autopilot 字段）**：建议小切片补 `desktopTabEntry` 列 + restore 回填；当前重启后裸 autopilot tab 仍会丢旗（有 running goal 的不受影响，sidecar 兜底）。
2. **初始构建修复的深度验证**：源级锚测试已锁字面量；如需端到端断言（恢复后 `c.autopilot==true`），需给 `buildTabControllerBoot` 留 boot seam，建议与断点 C 同片做。
3. **装机后观测**：P7（三级终止）+ 本片 dismiss + 判据日志一起，主对话可用 1.4 判据在用户现场直接三分：开关没落 / 构建没带 / 运行时被僵尸 turn 卡。
4. gofmt 说明：`desktop/app.go`、`desktop/tabs.go` 基线本身非 gofmt 规范（已验证 HEAD 版本同样被 gofmt 标记）；本片保持基线格式，未引入新漂移。
