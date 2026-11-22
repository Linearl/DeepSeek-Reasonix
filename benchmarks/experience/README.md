# benchmarks/experience —— EBDD 体验评测集（任务 414 · 阶段 1 体验评测集 + 阶段 4 聚合巡检）

把已完成任务的**人工验收步骤**脚本化成可一键重跑的 bench：只读
`logs/desktop/desktop.log`（轮转文件一并解析）与 `sessions/*.turns.jsonl`
turn ledger，对照 tasklist 各任务验收阈值输出 P50/P95 读数与
PASS/FAIL/N/A/OBSERVE。任一门越线以**退出码 1** 结束，可直接挂批次阶段门。

```bash
go run ./benchmarks/experience                      # 默认窗口 72h，全部 5 项
go run ./benchmarks/experience -bench open-topic    # boot|open-topic|tab-switch|switch-cost|first-response|warn-patrol
go run ./benchmarks/experience -window 240h -json out.json
go run ./benchmarks/experience -window 168h -json weekly.json -trend prev.json   # 阶段 4：周趋势（慢化浮出）
```

判定语义：`PASS/FAIL`= 有样本且对阈值判定；`N/A`= 窗口内无样本（明示，
不算过也不算挂）；`OBSERVE`= tasklist 无数值验收，只报告读数不设门。

## 指标清单（每项带阈值与 tasklist 出处）

| # | 指标（bench） | 阈值 | tasklist 出处 | 数据源 |
|---|---|---|---|---|
| M1 | boot total · 热路径（`boot`） | P95 < 3500ms（tl1683 按可达门重标定，原 1000ms） | 任务 363 验收①「单开新对话（热路径）boot total <1s」（P50 口径仍成立；P95 口径按修复后各相 P95 合计重标定，见下「门值重标定」） | 主口径：`boot: assembly reuse`（boot 自报本次真的复用了 prompt/memory/shell，落在本次 boot 的构建窗口内）；兜底：「prompt+mcp+agent:tools ≤1500ms」启发式。`desktop: runtime assembly hit` 只作标注，不再单独构成热路径 |
| M1c | boot total · 冷启动（`boot`，OBSERVE） | 无（363① 对照基线 3.4s） | tasklist 无冷启动数值验收 | `boot: stage timings` |
| M2 | 打开历史对话→可发消息（`open-topic`） | P95 < 2000ms | 任务 363 追加验收⑥「打开历史对话到可发消息 <2s（open-topic total + ancillary effort 合成体感）」 | `frontend open-topic summary` total + `tab switch timing` 的 `open-topic:ancillary effort` 合成配对（同 tab，60s 窗） |
| M3 | 打开会话失败（`open-topic`，census） | 0 条，任何一条即 FAIL | 任务 298「失败必须留痕（无日志≠未发生）」 | `desktop: open session failed` 计数 |
| M4 | 打开会话 ≥2s 慢开（`open-topic`，census） | 0 条（越线才落日志） | 363⑥ 的实锤形态 / 298 慢路径面 | `desktop: open session slow`（`slowThresholdMs=2000` 截断样本，单列不并入 P95 门） |
| M5 | 发消息→首响（`first-response`） | P95 ≤ 180s（可配；tl1684 R7 按可达形态重标定，原 60s） | 任务 370（无验收数值；原值取遥测分桶上沿；tl1684 归因证明 60s 在修正口径下恒 FAIL——provider 等待主导、重建三期 P95=61.7/171.1/160.1s——门改回归检测语义） | `sessions/*.turns.jsonl`：`turn_started` → 首个 `reasoning/text/tool_dispatch/tool_started`（与 telemetry sink、doctor responsiveness 同口径）；checkpoint 截断样本经留存池跨 checkpoint 聚合（tl1684 R7，按 TurnID 去重） |
| M5b | ≥60s 首响长尾普查（`first-response`，OBSERVE） | 无（占比升高=长尾变重，tl1684 C7：三期 25.9%→45.9%→58.3%） | tl1684 归因 C7（三期唯一真实恶化量；P95 持平周期里它已翻倍） | 与 M5 同源样本：≥60s 条数、长尾时长占全部首响时长比、Max |
| M6 | 切 tab 渲染 render（`tab-switch`） | P95 < 150ms | 任务 196 判定线（`tab_timing.go` `slowTabSwitchLogMs=150`，越线才落日志则取同值作门） | `frontend tab switch render` |
| M7 | transcript 首帧 / switch-tab summary（`tab-switch`，OBSERVE） | 无 | tasklist 无数值验收 | `frontend transcript first frame`、`frontend switch-tab summary` |
| M8 | 后端 stage 越线普查（`tab-switch`，OBSERVE） | 普通 stage 150ms / switch-out 50ms（越线才落行） | 任务 196 | `desktop: tab switch timing` 按 stage 归并计数 |
| M9 | WARN 巡检（`warn-patrol`） | 每模式 ≤0 条（`-warn-max-count` 可配） | 任务 414 阶段 4（heartbeat WARN 巡检；默认模式 = 任务书点名 `slow switch stage`=196 慢线面、`cold cache compact failed`=压缩失败面） | 窗口内 `level=WARN` 且命中 `-warn-patterns` 的行（**采集期按模式过滤入池**；池截断追加显式 FAIL 行，绝不静默假 PASS） |
| M10 | 切换模型耗时（`switch-cost`） | P95 < 1500ms（`-switch-fast-max-ms` 可配） | 任务 909（读数接线）；锚 task 602 验收「跨供应商热切 <2s」留 500ms 余量 | `desktop: model switch` 快路径行的 `elapsed_ms`（fast-same-model / fast-per-request 分桶；**打点随 909 出包**，老日志无此字段=无样本 N/A） |
| M11 | 切换 effort 耗时（`switch-cost`） | P95 < 1500ms（同上门） | 任务 909 门化；锚 task 334/148「快路径不重建」 | `desktop: effort switch` 的 `elapsed_ms`（fast-same-level / fast-per-request；task 148 起有读数） |
| M12 | 切换落重建 fallback（`switch-cost`，OBSERVE 计数） | 无（占比升高需归因） | 任务 909（快路径失效早期信号） | `desktop: model switch path=fallback-*` 计数 + `desktop: runtime build end trigger=set-effort` 计数 |
| M13 | effort 读取 ≥150ms 慢读（`switch-cost`，OBSERVE census） | 无（越线才落行，条数本身即信号） | 任务 639/889（读取分段取证） | `desktop: effort read breakdown` 的 `total_ms`（`effortBreakdownLogMs=150` 截断样本，单列不并入 P95 门） |
| M14 | 发消息→首响按 provider 分层（`first-response`，OBSERVE） | 无（各 provider 形态差异大，不设统一门） | tl1685 ①（P1 首响归因 tl1684 口径的前置：provider 等待主导长尾 88.7%，分层先持续可测） | `sessions/*.turns.jsonl` + 会话 meta 侧车 `<session>.jsonl.meta` 的 `model` 字段（canonical "provider/model" 前缀；**会话级粒度**：按会话当前 model 归窗口内全部 turn，ledger 本身不带 model 信息；缺 meta 归 `unknown`） |
| M15 | boot 各相独立门 prompt / agent:tools / controller（`boot`，热路径 P95） | P95 < 60000 / 8000 / 4000ms（`-boot-phase-prompt-max-ms` / `-boot-phase-tools-max-ms` / `-boot-phase-ctl-max-ms` 可配） | task 1223 相分析法的持续门禁化（tl1685 ②）；**首期基线=20261122 现读数上浮**（prompt P95=37366/Max=47017、agent:tools P95=2677/Max=4707、controller P95=754/Max=2668，n=46），现读数本身是存量劣化面（自报复用 boot 的 prompt 相 P95=37s），门锚回归检测语义，P1 修复落地后收紧重定 | `boot: stage timings` 分相，样本池与 M1 热路径同池；相缺省不计样本不冒 0；冷启动相归因仍由 M1c「最慢相」注记承载 |
| M16 | WARN slow switch stage 按 stage 细分（`warn-patrol`，OBSERVE） | 无（门=M9 巡检行本身，本行只回答「涨在哪些 stage」） | tl1685 ③（第6期候选③：83 条=上期 45+38，观测归因辅助） | M9 巡检池内 `desktop: frontend slow switch stage` 行 detail 的 `stage=` 字段（stage 值可含空格，采集期专用解析）；解析失败归 `(no-stage)` 不静默丢；注记列 top8 stage×n(P50,Max) |
| M17 | effort 慢读条数÷切 tab 次数（`switch-cost`，比值观测） | 无（比值跨期对比替代条数单指标） | tl1685 ④（第6期实测教训：慢读 125→237 条升但 P95 2503→1855ms 反降——条数升/单条快=切换次数增多非单读劣化） | M13 慢读条数 ÷ M7 `frontend switch-tab summary` 行数；周趋势对比走**比值百分比**（比值平而条数升=切换频次增多非劣化，比值升=单读/单切劣化；上期 JSON 无 ratio 字段时退计数对比） |

阈值均可 CLI 覆盖：`-boot-hot-max-ms`、`-open-topic-max-ms`、
`-render-max-ms`、`-switch-fast-max-ms`、`-first-response-max-s`、
`-boot-hot-budget-ms`、`-boot-phase-prompt-max-ms` /
`-boot-phase-tools-max-ms` / `-boot-phase-ctl-max-ms`（tl1685 ② 三相门）、
`-warn-patterns`（巡检模式）、`-warn-max-count`
（巡检门）、`-trend-worse-pct`（趋势慢化线）。`boot > Ns` 类告警即 M1 门
（`-boot-hot-max-ms`），不重复实现。

## 口径与已知边界

- **首响数据源是 turn ledger，不是 events 的 `turn_begin`**：后者骑在下一次
  save 上（文件顺序与时间戳错位）、仅 schema-2 会话写入，2026-10-09 实测
  16 个事件文件 0 样本。ledger 会随 checkpoint 截断旧事件——tl1684 归因
  证实这曾让历史最坏样本对周读数不可见（公布 Max 160s，真实 Max ≥180s），
  **tl1684 R7 起写侧在 checkpoint 行留存有界首响样本池（≤1024 条，
  content-free），读侧跨 checkpoint 聚合、按 TurnID 与 live 配对去重**；
  留存自写侧落地起生效，此前已被截断删除的样本不可恢复。截断的 ledger
  停在截断处（不修补），计数进 `Torn`。
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
  P95 百分比，恶化 ≥ `-trend-worse-pct`（默认 10%）标 `↑慢化`；比值行
  （M17，`ratio` 字段两侧俱备时）按比值百分比对比——条数单指标会跨期误判；
  计数行（census/WARN）按 n 对比，增多即标 `↑增多`。任一侧无样本不出行。
  趋势段**只报告、不改退出码**；上期文件缺失/损坏降级为 stderr 提示。
- **`warn-patrol`**（M9）：窗口内命中 `-warn-patterns` 的 `level=WARN` 行计数
  （**采集期按模式过滤**——2026-10-15 实测教训：全量留档时 168h 窗 6.1 万条
  `config: unknown key` 噪音会先吃满采集上限，把慢 switch 行挤出池子造成
  假 PASS），超 `-warn-max-count`（默认 0）即 FAIL；池截断追加 FAIL 行
  显式「读数不完整」，日志缺失明示 N/A。
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
