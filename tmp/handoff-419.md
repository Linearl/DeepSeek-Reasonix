# Handoff — 任务 419 dynamic-workflow 单会话流水线（wt-419）

- 分支：`wt-419`（基 `main-v2-stable@1c632752f`）；创建于 2026-10-15
- 中断协议位置：本文件（`wt-419/tmp/handoff-419.md`）
- 设计文档（权威规格）：`global-workspace/docs/report/zcode交付/zcode交付-419-dynamic-workflow设计-20261015.md`

## 1. 开工前置结论（已回板主对话，此处存档）

### 349 依赖：已满足，全速推进档
- `tl show 349`：done（2026-10-09）；`4efc8b235` 主件等 audit-2 PASS 合入 main-v2-stable；`merge-base --is-ancestor 4efc8b235 HEAD` = YES。
- 落地面：`internal/collabchannel`（1151 行 SQLite channel + pub/sub + per-recipient）、`internal/agent/channel_tools.go`、`desktop/collab_view_app.go`、前端 CollabGroupPanel/ChannelChatPanel（802）。
- ⇒ 按任务书「349 已落地则依赖满足可全速推进」→ 阶段推进：roster→journal→投影→时间线 UI。

### 685/686 接口边界（均未落地：pending，2026-10-09 范围冻结排 20 批，代码面 grep 0）
| 件 | 管 | 不管 |
|---|---|---|
| **419** | 单会话已发生运行的数据面：journal 记录、roster/产物投影、**时间序**时间线 | 声明式 DAG、编排引擎 |
| **685** | 声明式 DAG 引擎（YAML schema、确定性门、独立确认、report 落袋；事件走 internal/event） | 记录/投影/时间线 |
| **686** | DAG **结构序**流程图面板（消费 685 schema） | 时间序事件轨 |

防撞车：419 的 `events.kind` 是稳定契约可扩展容纳 685 节点事件；685 落地时可消费 419 的 journal 表做持久层；686/419 时间线是两个不同视图（结构 vs 时间），不合并。

### zcode 借鉴七条（存档于设计文档 §1.3）
纯派生 roster / 事件=时间唯一来源 / 端口能力探测可见降级 / 产物只收 completed+版本归并 / launch-anchor 零 SQL / 终态限额+关闭主动 abort / timeline 色族（rule/trace + 语义灯 + avatar 九色不编码状态）。

## 2. 已完成切片

| 切片 | commit | 内容 |
|---|---|---|
| S0 | （workspace docs，不在本仓） | 设计文档 `zcode交付-419-dynamic-workflow设计-20261015.md` |
| S1 | `419 切片1：runjournal 后端包…` | `internal/runjournal/`：Store（runs/members/events/artifacts 四表 SQLite WAL，collabchannel 先例）+ 投影纯函数（BuildRoster/BuildTimeline/ArtifactsOf）+ 16 测试 |
| S2 | `419 切片2：…`（见 git log） | `internal/agent/run_journal.go`（runRecorder 薄适配：nil 全 no-op、open/begin 失败 slog `runjournal` 短语后降级）、fleet/parallel_tasks 接线（begin=校验后/runFleet 入口，dispatched=startOne/startTask，settled=worker 内+defer 收口，end=终态 defer）、boot 注入 `agent.RunJournalDBPath(sessionDir)`、6 集成测试 |

## 3. 关键设计决定（改代码前必读）

1. **run 定义**：一次 fleet 或 parallel_tasks 组执行 = 一个 run；单发 task 不记录（v1）；预检失败不建 run 行（没发生的运行不记录）。
2. **时间唯一来源**：members 行无时间列；一切时刻从 events 取（seq 单调，锁内 MAX+1）。事件 kind 五种：run_created/member_dispatched/member_settled/artifact_published/run_ended（**稳定契约**）。
3. **降级链**：sessionDir 空→路径空→recorder nil→全 no-op（419 前行为逐字节不变）；Open 失败→slog 一行+继续运行；begin 失败→关库自禁用；写失败→slog+运行不受影响。**记录层永不破坏运行。**
4. **first-write-wins**：Settled 只改非终态行（取消 drain 不覆盖真结果）；End 只改 `status='running'` 行（二次 End 保留首终态，事件轨照记两次）。
5. **产物**：completed 成员 settle 时发布（id=ref）；组聚合在 End 时发布（id=run_id+":result"，primary）。ArtifactsOf 只收 completed、latest 提顶、primary 跨版本粘滞。
6. **子代理 ref 前提**：成员 ref 只在**持久子代理**存在（需 `WithParentSession(ctx, ...)`；ephemeral run 无 ref → 无成员产物）。测试必须带 ParentSession。
7. **并发**：Store 单连接（SetMaxOpenConns(1)）+ mu；worker 多 goroutine settle 安全。
8. **开关**：v1 未加实验开关——journal 是纯观测写路径（不改行为、无 UI），设计上开关留给 S3 时间线 UI（铁律 2 由 UI 侧兑现）。若评审要求后端也挂开关，加 `experimental_run_journal`（config+render+lab 测试三处，参照 677 先例）。

## 4. 已完成 S3a / 未完成 S3b

### S3a 已 commit（`419 切片3a：run journal 桥接读口…`）
- `desktop/run_journal_app.go`：ListRunJournalRuns / GetRunJournalRunDetail（自包含 open→read→close；降级=空结果+error 带原因）；`desktop/run_journal_app_test.go` 5 用例全绿。
- `bridge.ts`：AppBindings 两方法 + 七个 RunJournal*View 类型 + dev mock 空目录；tsc 全绿。

### S3b 未做：前端时间线面板 + 实验开关（完整一块，参照 677 接线模板）
`git show 61c94ffd5`（677 群聊入口开关）是完整的开关接线模板，涉及：
- Go：desktop config key（`experimental_run_timeline` 建议）+ Config/App 两层 setter + SettingsView/DesktopStartupSettingsView 双视图回读 + render.go 固定键集 + labFeatureTiers 登记（render_lab_tiers_test 钉值同步）。
- 前端：labFlags 旗 + applyLabFlags + App.tsx 挂载点 + **RunJournalPanel 组件**（条目按 at 分组、成员 pill 语义状态灯、DESIGN.md 色纪律：状态用语义 token、avatar 色永不编码状态）+ SettingsPanel 提效组条目 + experimentTiers/task562 测试同步 + settingsViewTypes/types。
- 三语 locale 4 键×3；bundle 预算棘轮按实测抬；class-contract（新引用类必须有定义）；`pnpm build` 全绿。
- 面板数据就是 S3a 两方法：ListRunJournalRuns 选 run → GetRunJournalRunDetail 渲染 roster/health/timeline/artifacts 四段；error 非空时显示「运行日志不可用」而非空历史。

## 5. 测试基线（2026-10-15 实测，S3a 收尾）

- 包级全绿：runjournal ok / collabchannel ok / sessioncollab ok / boot ok（复跑）；desktop ok（journal 5 用例）；tsc --noEmit 全绿。
- 预存红（非 419 引入，主仓基线同红）：`internal/agent TestToolCheckpointReloadRefreshesProjection`（"restart left stale projection"）；`TestCompactSessionFileRewritesAndIsIdempotent` 全量下负载抖动、隔离复跑绿；boot `TestMCPReuseAcrossRebuildsBench` 抖动复跑绿。
- 新增测试：runjournal 16 + agent 6 + desktop 5 = 27 用例。

## 6. 工作纪律提醒（本分支）

- 切片 commit，中文 message「419 」开头；显式路径 add；禁 add -A/commit -a/stash/merge/rebase。
- high risk 拦截先例：被拒时源码改动走 Edit/Write。
- 被拒/中断 → 更新本文件；继续者先读设计文档 §9（边界：不做 685/686/349 的事）。
