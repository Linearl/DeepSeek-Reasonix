# benchmarks/experience —— EBDD 体验评测集（任务 414 · 阶段 1 体验评测集 + 阶段 4 聚合巡检）

把已完成任务的**人工验收步骤**脚本化成可一键重跑的 bench：只读
`logs/desktop/desktop.log`（轮转文件一并解析）与 `sessions/*.turns.jsonl`
turn ledger，对照 tasklist 各任务验收阈值输出 P50/P95 读数与
PASS/FAIL/N/A/OBSERVE。任一门越线以**退出码 1** 结束，可直接挂批次阶段门。

```bash
go run ./benchmarks/experience                      # 默认窗口 72h，全部 5 项
go run ./benchmarks/experience -bench open-topic    # boot|open-topic|tab-switch|first-response|warn-patrol
go run ./benchmarks/experience -window 240h -json out.json
go run ./benchmarks/experience -window 168h -json weekly.json -trend prev.json   # 阶段 4：周趋势（慢化浮出）
```

判定语义：`PASS/FAIL`= 有样本且对阈值判定；`N/A`= 窗口内无样本（明示，
不算过也不算挂）；`OBSERVE`= tasklist 无数值验收，只报告读数不设门。

## 指标清单（每项带阈值与 tasklist 出处）

| # | 指标（bench） | 阈值 | tasklist 出处 | 数据源 |
|---|---|---|---|---|
| M1 | boot total · 热路径（`boot`） | P95 < 1000ms | 任务 363 验收①「单开新对话（热路径）boot total <1s」 | `boot: stage timings` + `desktop: runtime assembly hit` 配对（构建起点 ±2s）；无 hit 行时退化到「prompt+mcp+agent:tools ≤300ms」启发式 |
| M1c | boot total · 冷启动（`boot`，OBSERVE） | 无（363① 对照基线 3.4s） | tasklist 无冷启动数值验收 | `boot: stage timings` |
| M2 | 打开历史对话→可发消息（`open-topic`） | P95 < 2000ms | 任务 363 追加验收⑥「打开历史对话到可发消息 <2s（open-topic total + ancillary effort 合成体感）」 | `frontend open-topic summary` total + `tab switch timing` 的 `open-topic:ancillary effort` 合成配对（同 tab，60s 窗） |
| M3 | 打开会话失败（`open-topic`，census） | 0 条，任何一条即 FAIL | 任务 298「失败必须留痕（无日志≠未发生）」 | `desktop: open session failed` 计数 |
| M4 | 打开会话 ≥2s 慢开（`open-topic`，census） | 0 条（越线才落日志） | 363⑥ 的实锤形态 / 298 慢路径面 | `desktop: open session slow`（`slowThresholdMs=2000` 截断样本，单列不并入 P95 门） |
| M5 | 发消息→首响（`first-response`） | P95 ≤ 60s（可配） | 任务 370（无验收数值；默认取遥测 `cli_first_response_latency` 分桶 `s_15_60` 上沿） | `sessions/*.turns.jsonl`：`turn_started` → 首个 `reasoning/text/tool_dispatch/tool_started`（与 telemetry sink、doctor responsiveness 同口径） |
| M6 | 切 tab 渲染 render（`tab-switch`） | P95 < 150ms | 任务 196 判定线（`tab_timing.go` `slowTabSwitchLogMs=150`，越线才落日志则取同值作门） | `frontend tab switch render` |
| M7 | transcript 首帧 / switch-tab summary（`tab-switch`，OBSERVE） | 无 | tasklist 无数值验收 | `frontend transcript first frame`、`frontend switch-tab summary` |
| M8 | 后端 stage 越线普查（`tab-switch`，OBSERVE） | 普通 stage 150ms / switch-out 50ms（越线才落行） | 任务 196 | `desktop: tab switch timing` 按 stage 归并计数 |
| M9 | WARN 巡检（`warn-patrol`） | 每模式 ≤0 条（`-warn-max-count` 可配） | 任务 414 阶段 4（heartbeat WARN 巡检；默认模式 = 任务书点名 `slow switch stage`=196 慢线面、`cold cache compact failed`=压缩失败面） | `desktop.log` 全部 `level=WARN` 行按 `-warn-patterns` 子串匹配计数 |

阈值均可 CLI 覆盖：`-boot-hot-max-ms`、`-open-topic-max-ms`、
`-render-max-ms`、`-first-response-max-s`、`-boot-hot-budget-ms`、
`-warn-patterns`（巡检模式）、`-warn-max-count`（巡检门）、
`-trend-worse-pct`（趋势慢化线）。`boot > Ns` 类告警即 M1 门
（`-boot-hot-max-ms`），不重复实现。

## 口径与已知边界

- **首响数据源是 turn ledger，不是 events 的 `turn_begin`**：后者骑在下一次
  save 上（文件顺序与时间戳错位）、仅 schema-2 会话写入，2026-10-09 实测
  16 个事件文件 0 样本。ledger 会随 checkpoint 截断旧事件——样本只覆盖窗口
  内仍保留记录的 turn，这正是 `-window` 的语义；截断的 ledger 停在截断处
  （不修补），计数进 `Torn`。
- **热路径判定主口径是 `runtime assembly hit` 配对**（363A 复用的直接证据，
  acquire 发生在 BuildWithAssembly 前，与 `boot summary` 的构建起点对齐
  ±2s，实测最大偏差 1.7s）；复用功能上线前的老日志退化到分相预算启发式。
- **open session slow 是截断样本**（≥2s 才落日志）且与 M2 合成样本同源不同层
  （后端 openSession vs 前端 hydrate，同一次慢开常被两边各记一次）：并入
  P95 门会重复计数并抬高读数，故单列 census 行。
- guardian 会话（`*.guardian.turns.jsonl`）不是「用户发消息」，不入首响样本。
- 默认会话根 = `<home>/sessions` + `<home>/projects/*/sessions`（项目级会话
  是日常主力，漏掉会砍掉大半样本）；`-sessions-dir` 显式指定时只扫该目录。

## 阶段 4：周趋势与 WARN 巡检（heartbeat 引擎）

- **`-trend <prev.json>`**：与上期读数逐指标对比（键=bench+metric）：延迟行按
  P95 百分比，恶化 ≥ `-trend-worse-pct`（默认 10%）标 `↑慢化`；计数行
  （census/WARN）按 n 对比，增多即标 `↑增多`。任一侧无样本不出行。趋势段
  **只报告、不改退出码**；上期文件缺失/损坏降级为 stderr 提示。
- **`warn-patrol`**（M9）：窗口内全部 `level=WARN` 行按可配模式计数，
  超 `-warn-max-count`（默认 0）即 FAIL，日志缺失明示 N/A。
- **heartbeat 周任务**：`%APPDATA%\reasonix\heartbeat-tasks.json` 的
  `ebdd-weekly-experience`（168h）按「跑 `-window 168h -json` + `-trend 上期`
  → 贴读数/趋势/WARN 巡检」执行，报告落 `docs/report/`；改该文件必须
  `revision++`。首期报告为基线期（无上期，`-trend` 输出「无可对比项」属正常）。

## 阶段边界

本目录覆盖**任务 414 阶段 1（体验评测集 · E 柱）与阶段 4（heartbeat 周趋势
聚合 + WARN 巡检）**；阶段 2（badcase 回归留存入阶段门）与阶段 3（装机清单
二分机器化）为文档纪律件，分别落
`docs/handoff/批次整体规划/批次开发计划-汇总/批次阶段门清单-20261015.md` 与
`docs/drafts/装机验证清单-可自动与必须人工二分-20261015.md`。
